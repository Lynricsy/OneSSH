package searchx

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
)

const (
	defaultGrepLimit = 100
	maxGrepLimit     = 2000
	defaultFindLimit = 1000
	maxFindLimit     = 5000
	searchTimeout    = 30 * time.Second
	maxJSONLine      = 1 << 20
	maxStderr        = 64 << 10
	maxOutputBytes   = 256 << 10
)

type Manager struct {
	Pool *sshpool.Pool
}

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

type rgText struct {
	Text  *string `json:"text"`
	Bytes *string `json:"bytes"`
}

type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       rgText `json:"path"`
		Lines      rgText `json:"lines"`
		LineNumber *int   `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
		} `json:"submatches"`
	} `json:"data"`
}

func New(pool *sshpool.Pool) *Manager {
	return &Manager{Pool: pool}
}

func (m *Manager) Grep(ctx context.Context, host string, opt GrepOptions) (GrepResult, error) {
	if opt.Pattern == "" {
		return GrepResult{}, errors.New("pattern 不能为空")
	}
	if strings.IndexByte(opt.Pattern, 0) >= 0 || strings.IndexByte(opt.Path, 0) >= 0 || strings.IndexByte(opt.Glob, 0) >= 0 {
		return GrepResult{}, errors.New("搜索参数不能包含 NUL")
	}
	if opt.Path == "" {
		opt.Path = "."
	}
	if opt.Limit <= 0 {
		opt.Limit = defaultGrepLimit
	}
	if opt.Limit > maxGrepLimit {
		opt.Limit = maxGrepLimit
	}
	if opt.Context < 0 {
		opt.Context = 0
	}
	if opt.Context > 20 {
		opt.Context = 20
	}

	args := []string{"--json", "--line-number", "--color=never", "--hidden", "--max-columns=4000", "--max-columns-preview"}
	if opt.IgnoreCase {
		args = append(args, "--ignore-case")
	}
	if opt.Literal {
		args = append(args, "--fixed-strings")
	}
	if opt.Glob != "" {
		args = append(args, "--glob", opt.Glob)
	}
	if opt.Context > 0 {
		args = append(args, "--context", fmt.Sprint(opt.Context))
	}
	args = append(args, "--", opt.Pattern, opt.Path)
	command := "command -v rg >/dev/null 2>&1 || { printf '%s\\n' 'ripgrep (rg) 未安装' >&2; exit 127; }; exec rg " + shellArgs(args)

	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	out := GrepResult{Lines: make([]GrepLine, 0, min(opt.Limit, 128)), Engine: "rg"}
	outputBytes := len(out.Engine) + 64
	stderr, exitCode, stopped, err := m.stream(ctx, host, command, func(line string) bool {
		event, ok := decodeRGLine(line)
		if !ok || (event.Type != "match" && event.Type != "context") {
			return true
		}
		item, ok := grepLine(event)
		if !ok {
			return true
		}
		item.Path = relativeResultPath(item.Path, opt.Path)
		encoded, _ := json.Marshal(item)
		lineBytes := len(encoded) + 1
		if outputBytes+lineBytes > maxOutputBytes {
			return false
		}
		outputBytes += lineBytes
		if item.Match {
			out.MatchCount++
			if out.MatchCount > opt.Limit {
				out.MatchCount = opt.Limit
				return false
			}
		}
		out.Lines = append(out.Lines, item)
		return out.MatchCount < opt.Limit
	})
	if err != nil {
		return GrepResult{}, err
	}
	out.Truncated = stopped || out.MatchCount >= opt.Limit
	if exitCode == 127 && !stopped {
		return m.grepSFTP(ctx, host, opt)
	}
	if exitCode != 0 && exitCode != 1 && !stopped {
		return GrepResult{}, commandError("rg", exitCode, stderr)
	}
	return out, nil
}

func (m *Manager) Find(ctx context.Context, host string, opt FindOptions) (FindResult, error) {
	if opt.Pattern == "" {
		return FindResult{}, errors.New("pattern 不能为空")
	}
	if strings.IndexByte(opt.Pattern, 0) >= 0 || strings.IndexByte(opt.Path, 0) >= 0 {
		return FindResult{}, errors.New("搜索参数不能包含 NUL")
	}
	if opt.Path == "" {
		opt.Path = "."
	}
	if opt.Limit <= 0 {
		opt.Limit = defaultFindLimit
	}
	if opt.Limit > maxFindLimit {
		opt.Limit = maxFindLimit
	}
	pattern := opt.Pattern
	args := []string{"--glob", "--color=never", "--hidden", "--max-results", fmt.Sprint(opt.Limit)}
	if strings.Contains(pattern, "/") {
		args = append(args, "--full-path")
		if !strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "**/") && pattern != "**" {
			pattern = "**/" + pattern
		}
	}
	args = append(args, "--", pattern, opt.Path)
	command := "if command -v fd >/dev/null 2>&1; then _onessh_fd=fd; elif command -v fdfind >/dev/null 2>&1; then _onessh_fd=fdfind; else printf '%s\\n' 'fd 未安装' >&2; exit 127; fi; exec \"$_onessh_fd\" " + shellArgs(args)

	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	out := FindResult{Paths: make([]string, 0, min(opt.Limit, 256)), Engine: "fd"}
	outputBytes := len(out.Engine) + 64
	stderr, exitCode, stopped, err := m.stream(ctx, host, command, func(line string) bool {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			return true
		}
		resultPath := relativeResultPath(line, opt.Path)
		encoded, _ := json.Marshal(resultPath)
		pathBytes := len(encoded) + 1
		if outputBytes+pathBytes > maxOutputBytes {
			return false
		}
		outputBytes += pathBytes
		out.Paths = append(out.Paths, resultPath)
		return len(out.Paths) < opt.Limit
	})
	if err != nil {
		return FindResult{}, err
	}
	out.Truncated = stopped || len(out.Paths) >= opt.Limit
	if exitCode == 127 && !stopped {
		return m.findSFTP(ctx, host, opt)
	}
	if exitCode != 0 && !stopped {
		return FindResult{}, commandError("fd", exitCode, stderr)
	}
	return out, nil
}

func shellArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = execx.SHQ(arg)
	}
	return strings.Join(quoted, " ")
}

func relativeResultPath(result, root string) string {
	result = path.Clean(result)
	root = path.Clean(root)
	if root == "." {
		return strings.TrimPrefix(result, "./")
	}
	if result == root {
		return path.Base(result)
	}
	prefix := strings.TrimSuffix(root, "/") + "/"
	if strings.HasPrefix(result, prefix) {
		return strings.TrimPrefix(result, prefix)
	}
	return result
}

func commandError(command string, exitCode int, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "无错误输出"
	}
	return fmt.Errorf("%s 退出码 %d: %s", command, exitCode, stderr)
}

func decodeRGLine(line string) (rgEvent, bool) {
	var event rgEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return rgEvent{}, false
	}
	return event, true
}

func grepLine(event rgEvent) (GrepLine, bool) {
	path, ok := rgString(event.Data.Path)
	if !ok || event.Data.LineNumber == nil {
		return GrepLine{}, false
	}
	text, ok := rgString(event.Data.Lines)
	if !ok {
		return GrepLine{}, false
	}
	text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	item := GrepLine{Path: path, Line: *event.Data.LineNumber, Text: text, Match: event.Type == "match"}
	if item.Match && len(event.Data.Submatches) > 0 {
		item.Column = event.Data.Submatches[0].Start + 1
	}
	return item, true
}

func rgString(value rgText) (string, bool) {
	if value.Text != nil {
		return *value.Text, true
	}
	if value.Bytes == nil {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(*value.Bytes)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

func (m *Manager) stream(ctx context.Context, host, command string, consume func(string) bool) (stderr string, exitCode int, stopped bool, err error) {
	client, err := m.Pool.Get(ctx, host)
	if err != nil {
		return "", 0, false, err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", 0, false, err
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", 0, false, err
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		return "", 0, false, err
	}
	if err = session.Start("cd \"$HOME\" 2>/dev/null || exit 1; " + command); err != nil {
		return "", 0, false, err
	}

	stderrCh := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(stderrPipe, maxStderr+1))
		if len(data) > maxStderr {
			data = append(data[:maxStderr], []byte("\n[stderr 已截断]")...)
		}
		stderrCh <- string(data)
	}()
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-closed:
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxJSONLine)
	for scanner.Scan() {
		if !consume(scanner.Text()) {
			stopped = true
			_ = session.Close()
			break
		}
	}
	waitErr := session.Wait()
	close(closed)
	stderr = <-stderrCh
	if ctx.Err() != nil {
		return stderr, 0, stopped, ctx.Err()
	}
	if scanErr := scanner.Err(); scanErr != nil && !stopped {
		return stderr, 0, false, scanErr
	}
	if waitErr == nil {
		return stderr, 0, stopped, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(waitErr, &exitErr) {
		return stderr, exitErr.ExitStatus(), stopped, nil
	}
	if stopped {
		return stderr, 0, true, nil
	}
	return stderr, 0, false, waitErr
}
