package searchcore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	MaxTraversalEntries = 100_000
	MaxTraversalDepth   = 128
	MaxFallbackFileSize = 8 << 20
	binaryProbeBytes    = 8 << 10
	maxIgnoreFileBytes  = 256 << 10
	MaxOutputBytes      = 256 << 10
	MaxLineBytes        = 1 << 20
)

var errWalkStopped = errors.New("search walk stopped")

type GrepOptions struct {
	Pattern    string
	Path       string
	Glob       string
	IgnoreCase bool
	Literal    bool
	Context    int
	Limit      int
}

type GrepLine struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
	Text   string `json:"text"`
	Match  bool   `json:"match"`
}

type GrepResult struct {
	Lines      []GrepLine `json:"lines"`
	MatchCount int        `json:"match_count"`
	Truncated  bool       `json:"truncated"`
	Engine     string     `json:"engine"`
	Warning    string     `json:"warning,omitempty"`
}

type FindOptions struct {
	Pattern string
	Path    string
	Limit   int
}

type FindResult struct {
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
	Engine    string   `json:"engine"`
	Warning   string   `json:"warning,omitempty"`
}

type FS interface {
	Getwd() (string, error)
	Lstat(string) (os.FileInfo, error)
	ReadDir(string) ([]os.FileInfo, error)
	Open(string) (io.ReadCloser, error)
}

type ignoreRule struct {
	base         string
	match        *regexp.Regexp
	basenameOnly bool
	directory    bool
	negated      bool
}

type PathMatcher struct {
	match        *regexp.Regexp
	basenameOnly bool
	absolute     bool
	negated      bool
}

type searchWalker struct {
	fs         FS
	root       string
	maxEntries int
	entries    int
	truncated  bool
}

func CompileGrepPattern(opt GrepOptions) (*regexp.Regexp, error) {
	pattern := opt.Pattern
	if opt.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	if opt.IgnoreCase {
		pattern = "(?i:" + pattern + ")"
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("Go 正则无效: %w", err)
	}
	return compiled, nil
}

func Grep(ctx context.Context, fs FS, opt GrepOptions) (GrepResult, error) {
	searchPattern, err := CompileGrepPattern(opt)
	if err != nil {
		return GrepResult{}, err
	}
	glob, err := CompilePathMatcher(opt.Glob, false)
	if err != nil {
		return GrepResult{}, fmt.Errorf("glob 无效: %w", err)
	}
	root, err := resolveSearchRoot(fs, opt.Path)
	if err != nil {
		return GrepResult{}, err
	}

	out := GrepResult{Lines: make([]GrepLine, 0, min(opt.Limit, 128))}
	outputBytes := 64
	walker := searchWalker{fs: fs, root: root, maxEntries: MaxTraversalEntries}
	err = walker.walk(ctx, func(fullPath, relativePath string, info os.FileInfo) (bool, error) {
		if !info.Mode().IsRegular() || (glob != nil && !glob.matches(relativePath, fullPath)) {
			return true, nil
		}
		stopped, incomplete, err := grepFallbackFile(ctx, fs, fullPath, relativePath, info, searchPattern, opt, &out, &outputBytes)
		if incomplete {
			out.Truncated = true
		}
		return !stopped, err
	})
	if err != nil {
		return GrepResult{}, err
	}
	out.Truncated = out.Truncated || walker.truncated
	return out, nil
}

func Find(ctx context.Context, fs FS, opt FindOptions) (FindResult, error) {
	matcher, err := CompilePathMatcher(opt.Pattern, true)
	if err != nil {
		return FindResult{}, fmt.Errorf("glob 无效: %w", err)
	}
	root, err := resolveSearchRoot(fs, opt.Path)
	if err != nil {
		return FindResult{}, err
	}

	out := FindResult{Paths: make([]string, 0, min(opt.Limit, 256))}
	outputBytes := 64
	walker := searchWalker{fs: fs, root: root, maxEntries: MaxTraversalEntries}
	err = walker.walk(ctx, func(fullPath, relativePath string, _ os.FileInfo) (bool, error) {
		if !matcher.matches(relativePath, fullPath) {
			return true, nil
		}
		encoded, _ := json.Marshal(relativePath)
		encodedBytes := len(encoded) + 1
		if outputBytes+encodedBytes > MaxOutputBytes {
			out.Truncated = true
			return false, nil
		}
		outputBytes += encodedBytes
		out.Paths = append(out.Paths, relativePath)
		if len(out.Paths) >= opt.Limit {
			out.Truncated = true
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return FindResult{}, err
	}
	out.Truncated = out.Truncated || walker.truncated
	return out, nil
}

func resolveSearchRoot(fs FS, name string) (string, error) {
	cwd, err := fs.Getwd()
	if err != nil {
		return "", fmt.Errorf("读取远端主目录: %w", err)
	}
	switch {
	case name == "~":
		name = cwd
	case strings.HasPrefix(name, "~/"):
		name = path.Join(cwd, strings.TrimPrefix(name, "~/"))
	case !path.IsAbs(name):
		name = path.Join(cwd, name)
	}
	return path.Clean(name), nil
}

func (w *searchWalker) walk(ctx context.Context, visit func(string, string, os.FileInfo) (bool, error)) error {
	info, err := w.fs.Lstat(w.root)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if info.Mode().IsRegular() {
		keepGoing, err := visit(w.root, path.Base(w.root), info)
		if !keepGoing {
			w.truncated = true
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	if err = w.walkDir(ctx, w.root, "", nil, 0, visit); errors.Is(err, errWalkStopped) {
		return nil
	}
	return err
}

func (w *searchWalker) walkDir(ctx context.Context, directory, relativeDir string, inherited []ignoreRule, depth int, visit func(string, string, os.FileInfo) (bool, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth >= MaxTraversalDepth {
		w.truncated = true
		return nil
	}
	local, err := loadIgnoreRules(w.fs, directory, relativeDir)
	if err != nil {
		return err
	}
	rules := inherited
	if len(local) > 0 {
		rules = make([]ignoreRule, 0, len(inherited)+len(local))
		rules = append(rules, inherited...)
		rules = append(rules, local...)
	}
	entries, err := w.fs.ReadDir(directory)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, info := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		w.entries++
		if w.entries > w.maxEntries {
			w.truncated = true
			return errWalkStopped
		}
		name := info.Name()
		relativePath := path.Join(relativeDir, name)
		fullPath := path.Join(directory, name)
		if info.Mode()&os.ModeSymlink != 0 || ignoredPath(rules, relativePath, info.IsDir()) {
			continue
		}
		if info.IsDir() {
			if name == ".git" || name == ".hg" || name == ".svn" {
				continue
			}
			keepGoing, visitErr := visit(fullPath, relativePath, info)
			if visitErr != nil {
				return visitErr
			}
			if !keepGoing {
				w.truncated = true
				return errWalkStopped
			}
			if err = w.walkDir(ctx, fullPath, relativePath, rules, depth+1, visit); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		keepGoing, visitErr := visit(fullPath, relativePath, info)
		if visitErr != nil {
			return visitErr
		}
		if !keepGoing {
			w.truncated = true
			return errWalkStopped
		}
	}
	return nil
}

func loadIgnoreRules(fs FS, directory, relativeDir string) ([]ignoreRule, error) {
	var rules []ignoreRule
	for _, name := range []string{".gitignore", ".ignore", ".rgignore"} {
		file, err := fs.Open(path.Join(directory, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxIgnoreFileBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > maxIgnoreFileBytes {
			return nil, fmt.Errorf("忽略文件 %s 超过 %d 字节", path.Join(directory, name), maxIgnoreFileBytes)
		}
		parsed, err := parseIgnoreRules(string(data), relativeDir)
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", path.Join(directory, name), err)
		}
		rules = append(rules, parsed...)
	}
	return rules, nil
}

func parseIgnoreRules(contents, base string) ([]ignoreRule, error) {
	var rules []ignoreRule
	for _, raw := range strings.Split(contents, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule := ignoreRule{base: base}
		if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		} else if strings.HasPrefix(line, "!") {
			rule.negated = true
			line = line[1:]
		}
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "/") {
			rule.directory = true
			line = strings.TrimSuffix(line, "/")
		}
		line = strings.TrimPrefix(line, "/")
		rule.basenameOnly = !strings.Contains(line, "/")
		compiled, err := compileGlob(line)
		if err != nil {
			return nil, err
		}
		rule.match = compiled
		rules = append(rules, rule)
	}
	return rules, nil
}

func ignoredPath(rules []ignoreRule, relativePath string, directory bool) bool {
	ignored := false
	for _, rule := range rules {
		if rule.directory && !directory {
			continue
		}
		candidate := relativePath
		if rule.base != "" {
			prefix := rule.base + "/"
			if !strings.HasPrefix(candidate, prefix) {
				continue
			}
			candidate = strings.TrimPrefix(candidate, prefix)
		}
		if rule.basenameOnly {
			candidate = path.Base(candidate)
		}
		if rule.match.MatchString(candidate) {
			ignored = !rule.negated
		}
	}
	return ignored
}

func CompilePathMatcher(pattern string, findPattern bool) (*PathMatcher, error) {
	if pattern == "" {
		return nil, nil
	}
	matcher := &PathMatcher{}
	if !findPattern && strings.HasPrefix(pattern, "!") {
		matcher.negated = true
		pattern = strings.TrimPrefix(pattern, "!")
	}
	if pattern == "" {
		return nil, errors.New("pattern 不能为空")
	}
	matcher.absolute = path.IsAbs(pattern)
	matcher.basenameOnly = !strings.Contains(pattern, "/")
	if matcher.absolute {
		pattern = path.Clean(pattern)
	} else if !matcher.basenameOnly && findPattern && !strings.HasPrefix(pattern, "**/") && pattern != "**" {
		pattern = "**/" + pattern
	} else {
		pattern = strings.TrimPrefix(pattern, "/")
	}
	compiled, err := compileGlob(pattern)
	if err != nil {
		return nil, err
	}
	matcher.match = compiled
	return matcher, nil
}

func (m *PathMatcher) matches(relativePath, fullPath string) bool {
	if m == nil {
		return true
	}
	candidate := relativePath
	if m.absolute {
		candidate = fullPath
	} else if m.basenameOnly {
		candidate = path.Base(relativePath)
	}
	matched := m.match.MatchString(candidate)
	if m.negated {
		return !matched
	}
	return matched
}

func compileGlob(pattern string) (*regexp.Regexp, error) {
	var expression strings.Builder
	expression.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				for i+1 < len(pattern) && pattern[i+1] == '*' {
					i++
				}
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					expression.WriteString("(?:.*/)?")
				} else {
					expression.WriteString(".*")
				}
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		case '[':
			end := i + 1
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end == len(pattern) {
				return nil, errors.New("未闭合的字符组")
			}
			class := pattern[i+1 : end]
			if class == "" || strings.Contains(class, "/") {
				return nil, errors.New("无效的字符组")
			}
			expression.WriteByte('[')
			if class[0] == '!' {
				expression.WriteByte('^')
				class = class[1:]
			}
			if class == "" {
				return nil, errors.New("无效的字符组")
			}
			expression.WriteString(strings.ReplaceAll(class, `\`, `\\`))
			expression.WriteByte(']')
			i = end
		case '\\':
			if i+1 >= len(pattern) {
				return nil, errors.New("末尾反斜杠")
			}
			i++
			expression.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		default:
			expression.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	expression.WriteByte('$')
	return regexp.Compile(expression.String())
}

func grepFallbackFile(ctx context.Context, fs FS, fullPath, relativePath string, info os.FileInfo, pattern *regexp.Regexp, opt GrepOptions, out *GrepResult, outputBytes *int) (bool, bool, error) {
	if info.Size() > MaxFallbackFileSize {
		return false, true, nil
	}
	file, err := fs.Open(fullPath)
	if err != nil {
		return false, false, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, binaryProbeBytes)
	probe, peekErr := reader.Peek(binaryProbeBytes)
	if bytes.IndexByte(probe, 0) >= 0 {
		return false, false, nil
	}
	if peekErr != nil && !errors.Is(peekErr, io.EOF) && !errors.Is(peekErr, bufio.ErrBufferFull) {
		return false, false, peekErr
	}

	type bufferedLine struct {
		number int
		text   string
	}
	previous := make([]bufferedLine, 0, opt.Context)
	after := 0
	lastEmitted := 0
	lineNumber := 0
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), MaxLineBytes)
	for scanner.Scan() {
		if err = ctx.Err(); err != nil {
			return false, false, err
		}
		lineNumber++
		text := strings.TrimSuffix(scanner.Text(), "\r")
		location := pattern.FindStringIndex(text)
		if location != nil {
			for _, prior := range previous {
				if prior.number > lastEmitted {
					if appendFallbackGrepLine(out, outputBytes, opt.Limit, GrepLine{Path: relativePath, Line: prior.number, Text: prior.text}) {
						return true, false, nil
					}
					lastEmitted = prior.number
				}
			}
			if appendFallbackGrepLine(out, outputBytes, opt.Limit, GrepLine{Path: relativePath, Line: lineNumber, Column: location[0] + 1, Text: text, Match: true}) {
				return true, false, nil
			}
			lastEmitted = lineNumber
			after = opt.Context
		} else if after > 0 {
			if appendFallbackGrepLine(out, outputBytes, opt.Limit, GrepLine{Path: relativePath, Line: lineNumber, Text: text}) {
				return true, false, nil
			}
			lastEmitted = lineNumber
			after--
		}
		if opt.Context > 0 {
			if len(previous) == opt.Context {
				copy(previous, previous[1:])
				previous = previous[:opt.Context-1]
			}
			previous = append(previous, bufferedLine{number: lineNumber, text: text})
		}
	}
	if scanner.Err() != nil {
		return false, true, nil
	}
	return false, false, nil
}

func appendFallbackGrepLine(out *GrepResult, outputBytes *int, limit int, line GrepLine) bool {
	encoded, _ := json.Marshal(line)
	encodedBytes := len(encoded) + 1
	if *outputBytes+encodedBytes > MaxOutputBytes {
		out.Truncated = true
		return true
	}
	*outputBytes += encodedBytes
	out.Lines = append(out.Lines, line)
	if line.Match {
		out.MatchCount++
		if out.MatchCount >= limit {
			out.Truncated = true
			return true
		}
	}
	return false
}
