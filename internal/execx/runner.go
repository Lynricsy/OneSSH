package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

const captureLimit = 256 << 10

type Options struct {
	Timeout   time.Duration
	MaxLines  int
	Tail      bool
	CaptureID string
	OnOutput  func(stream string, chunk []byte)
}
type Result struct {
	Stdout             string `json:"stdout"`
	Stderr             string `json:"stderr"`
	Output             string `json:"output"`
	ExitCode           int    `json:"exit_code"`
	ExitCodeKnown      bool   `json:"-"`
	Cwd                string `json:"cwd"`
	Timeout            bool   `json:"timeout"`
	Truncated          bool   `json:"truncated"`
	ArtifactID         string `json:"artifact_id,omitempty"`
	TotalLines         int    `json:"total_lines"`
	TotalBytes         int    `json:"total_bytes"`
	StdoutBytes        int    `json:"stdout_bytes"`
	StderrBytes        int    `json:"stderr_bytes"`
	OutputRecorded     bool   `json:"output_recorded"`
	OutputCaptureError string `json:"output_capture_error,omitempty"`
}
type Runner struct{ dataDir string }

func New(dataDir string) *Runner { return &Runner{dataDir: dataDir} }

func (r *Runner) CleanupArtifacts(cutoff time.Time) (int, error) {
	dir := filepath.Join(r.dataDir, "artifacts")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取 artifact 目录: %w", err)
	}
	removed := 0
	var cleanupErr error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		if _, err := uuid.Parse(strings.TrimSuffix(entry.Name(), ".log")); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("读取 artifact %s: %w", entry.Name(), err))
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("删除 artifact %s: %w", entry.Name(), err))
			continue
		}
		removed++
	}
	return removed, cleanupErr
}

// CleanupCommandOutputs 只删除数据库明确允许清理的托管 UUID.stdout/stderr.log
// 文件。使用删除白名单，避免数据库快照之后新启动的命令文件被误当成垃圾。
func (r *Runner) CleanupCommandOutputs(deleteIDs map[string]struct{}) (int, error) {
	dir := filepath.Join(r.dataDir, "command-runs")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取命令输出目录: %w", err)
	}
	removed := 0
	var cleanupErr error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		parts := strings.Split(entry.Name(), ".")
		if len(parts) != 3 || (parts[1] != "stdout" && parts[1] != "stderr") || parts[2] != "log" {
			continue
		}
		if _, err := uuid.Parse(parts[0]); err != nil {
			continue
		}
		if _, remove := deleteIDs[parts[0]]; !remove {
			continue
		}
		if err = os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("删除命令输出 %s: %w", entry.Name(), err))
			continue
		}
		removed++
	}
	return removed, cleanupErr
}

func SHQ(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func Script(command, cwd string, env map[string]string) string {
	return commandScript(command, cwd, env, "")
}

func commandScript(command, cwd string, env map[string]string, trailerID string) string {
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(SHQ(cwd))
	b.WriteString(" 2>/dev/null || cd \"$HOME\"\n")
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("export ")
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(SHQ(env[k]))
		b.WriteByte('\n')
	}
	b.WriteString("{ ")
	b.WriteString(command)
	b.WriteString("\n}\n__ec=$?\nprintf '\\n\\001ONESSH:")
	if trailerID != "" {
		b.WriteString(trailerID)
		b.WriteByte(':')
	}
	b.WriteString("%d:%s\\001' \"$__ec\" \"$PWD\"\n")
	return b.String()
}

type limitedWriter struct {
	buf             bytes.Buffer
	tail            []byte
	limit           int
	truncated       bool
	written         int
	newlines        int
	stream          string
	callback        func(string, []byte)
	full            io.Writer
	fullErr         error
	stripTrailer    bool
	trailerMarker   []byte
	callbackPending []byte
	callbackStopped bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	original := len(p)
	if w.full != nil && w.fullErr == nil {
		if _, err := w.full.Write(p); err != nil {
			// 记录失败不能反过来打断已经在远端执行的命令；最终结果会明确携带捕获错误。
			w.fullErr = err
		}
	}
	w.emitCallback(p)
	w.written += original
	w.newlines += bytes.Count(p, []byte{'\n'})
	headLimit := (w.limit + 1) / 2
	remain := max(0, headLimit-w.buf.Len())
	headLen := min(remain, len(p))
	_, _ = w.buf.Write(p[:headLen])
	if headLen < len(p) {
		tailLimit := w.limit - headLimit
		w.tail = append(w.tail, p[headLen:]...)
		if len(w.tail) > tailLimit {
			w.tail = append([]byte(nil), w.tail[len(w.tail)-tailLimit:]...)
		}
	}
	w.truncated = w.written > w.limit
	return original, nil
}

var legacyTrailerMarker = []byte("\n\x01ONESSH:")

// stdout 最后附有 Runner 自己的退出码/cwd trailer。回调只暂存 marker 长度的尾巴，
// 既能处理 marker 横跨两个 SSH 数据块，又不会让正常输出产生可感知的延迟。
func (w *limitedWriter) emitCallback(p []byte) {
	if w.callback == nil || w.callbackStopped {
		return
	}
	if !w.stripTrailer {
		w.callbackChunks(p)
		return
	}
	w.callbackPending = append(w.callbackPending, p...)
	marker := w.trailerMarker
	if len(marker) == 0 {
		marker = legacyTrailerMarker
	}
	if index := bytes.Index(w.callbackPending, marker); index >= 0 {
		w.callbackChunks(w.callbackPending[:index])
		w.callbackPending = nil
		w.callbackStopped = true
		return
	}
	keep := len(marker) - 1
	if len(w.callbackPending) > keep {
		emit := len(w.callbackPending) - keep
		w.callbackChunks(w.callbackPending[:emit])
		w.callbackPending = append([]byte(nil), w.callbackPending[emit:]...)
	}
}

func (w *limitedWriter) callbackChunks(data []byte) {
	for len(data) > 0 {
		n := min(len(data), 4096)
		w.callback(w.stream, append([]byte(nil), data[:n]...))
		data = data[n:]
	}
}

func (w *limitedWriter) finishCallback() {
	if !w.callbackStopped {
		w.callbackChunks(w.callbackPending)
	}
	w.callbackPending = nil
}

func (w *limitedWriter) captured() []byte {
	out := append([]byte(nil), w.buf.Bytes()...)
	out = append(out, w.tail...)
	return out
}

func (r *Runner) Run(ctx context.Context, client *ssh.Client, command, cwd string, env map[string]string, opt Options) (Result, error) {
	if opt.Timeout <= 0 {
		opt.Timeout = 60 * time.Second
	}
	if opt.MaxLines <= 0 {
		opt.MaxLines = 200
	}
	session, err := client.NewSession()
	if err != nil {
		return Result{}, err
	}
	defer session.Close()
	var capture *commandCapture
	var captureErr error
	if opt.CaptureID != "" {
		capture, captureErr = r.openCommandCapture(opt.CaptureID)
	}
	trailerID := uuid.NewString()
	trailerMarker := []byte("\n\x01ONESSH:" + trailerID + ":")
	stdout := &limitedWriter{limit: captureLimit, stream: "stdout", callback: opt.OnOutput, stripTrailer: true, trailerMarker: trailerMarker}
	stderr := &limitedWriter{limit: captureLimit, stream: "stderr", callback: opt.OnOutput}
	if capture != nil {
		stdout.full = capture.stdout
		stderr.full = capture.stderr
	}
	session.Stdout = stdout
	session.Stderr = stderr
	if err = session.Start(commandScript(command, cwd, env, trailerID)); err != nil {
		if capture != nil {
			capture.close()
			capture.remove()
		}
		return Result{}, err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	var waitErr error
	timedOut := false
	timer := time.NewTimer(opt.Timeout)
	defer timer.Stop()
	// 超时或取消时必须先发 SSH signal；只关闭 channel 不会终止 OpenSSH 的远端进程组。
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		timedOut = true
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		waitErr = ctx.Err()
	case <-timer.C:
		timedOut = true
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		waitErr = errors.New("command timeout")
	}
	stdoutBytes := stdout.captured()
	exitCode, newCwd, clean, parsed := parseTrailerWithMarker(stdoutBytes, trailerMarker)
	trailerBytes := 0
	if parsed {
		trailerBytes = len(stdoutBytes) - len(clean)
	}
	stdoutBytes = clean
	if !parsed {
		exitCode = exitCodeFromError(waitErr)
		newCwd = cwd
	}
	exitCodeKnown := parsed
	if !parsed {
		var exitErr *ssh.ExitError
		exitCodeKnown = errors.As(waitErr, &exitErr)
	}
	stderrBytes := stderr.captured()
	stdout.finishCallback()
	stderr.finishCallback()
	if capture != nil {
		captureErr = errors.Join(captureErr, capture.close())
		if parsed {
			captureErr = errors.Join(captureErr, stripCaptureTrailer(capture.stdoutPath, trailerMarker))
		}
	}
	captureErr = errors.Join(captureErr, stdout.fullErr, stderr.fullErr)
	combined := append(append([]byte(nil), stdoutBytes...), stderrBytes...)
	selected, capturedLines, lineCut := selectLines(combined, opt.MaxLines, opt.Tail)
	totalLines := capturedLines
	if stdout.truncated || stderr.truncated {
		totalLines = stdout.newlines + stderr.newlines
		if parsed {
			totalLines--
		}
	}
	stdoutTotal := max(0, stdout.written-trailerBytes)
	res := Result{Stdout: string(stdoutBytes), Stderr: string(stderrBytes), Output: string(selected), ExitCode: exitCode, ExitCodeKnown: exitCodeKnown, Cwd: newCwd, Timeout: timedOut, Truncated: stdout.truncated || stderr.truncated || lineCut, TotalLines: totalLines, TotalBytes: stdoutTotal + stderr.written, StdoutBytes: stdoutTotal, StderrBytes: stderr.written, OutputRecorded: opt.CaptureID != "" && capture != nil && captureErr == nil}
	if captureErr != nil {
		res.OutputCaptureError = captureErr.Error()
	}
	if res.Truncated {
		if err := os.MkdirAll(filepath.Join(r.dataDir, "artifacts"), 0o700); err != nil {
			return res, err
		}
		id := uuid.NewString()
		artifactPath := filepath.Join(r.dataDir, "artifacts", id+".log")
		if res.OutputRecorded {
			err = concatenateFiles(artifactPath, capture.stdoutPath, capture.stderrPath)
		} else {
			err = os.WriteFile(artifactPath, combined, 0o600)
		}
		if err != nil {
			return res, err
		}
		res.ArtifactID = id
	}
	if waitErr != nil && !parsed && !timedOut {
		if _, ok := waitErr.(*ssh.ExitError); !ok {
			return res, waitErr
		}
	}
	return res, nil
}

type commandCapture struct {
	stdout     *os.File
	stderr     *os.File
	stdoutPath string
	stderrPath string
}

func (r *Runner) openCommandCapture(id string) (*commandCapture, error) {
	stdoutPath, err := r.CommandOutputPath(id, "stdout")
	if err != nil {
		return nil, err
	}
	stderrPath, err := r.CommandOutputPath(id, "stderr")
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(stdoutPath), 0o700); err != nil {
		return nil, err
	}
	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		stdout.Close()
		os.Remove(stdoutPath)
		return nil, err
	}
	return &commandCapture{stdout: stdout, stderr: stderr, stdoutPath: stdoutPath, stderrPath: stderrPath}, nil
}

func (c *commandCapture) close() error {
	return errors.Join(c.stdout.Close(), c.stderr.Close())
}

func (c *commandCapture) remove() {
	_ = os.Remove(c.stdoutPath)
	_ = os.Remove(c.stderrPath)
}

func stripCaptureTrailer(path string, marker []byte) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	const tailLimit = 64 << 10
	start := max(int64(0), info.Size()-tailLimit)
	tail := make([]byte, info.Size()-start)
	if _, err = file.ReadAt(tail, start); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	index := bytes.LastIndex(tail, marker)
	if index < 0 {
		return fmt.Errorf("完整输出缺少命令 trailer")
	}
	return file.Truncate(start + int64(index))
}

func concatenateFiles(destination string, sources ...string) (err error) {
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, out.Close())
	}()
	for _, source := range sources {
		in, openErr := os.Open(source)
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(out, in)
		closeErr := in.Close()
		if err = errors.Join(copyErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}
func parseTrailer(out []byte) (int, string, []byte, bool) {
	return parseTrailerWithMarker(out, legacyTrailerMarker)
}

func parseTrailerWithMarker(out, marker []byte) (int, string, []byte, bool) {
	idx := bytes.LastIndex(out, marker)
	if idx < 0 {
		return 0, "", out, false
	}
	start := idx + len(marker)
	end := bytes.IndexByte(out[start:], 1)
	if end < 0 {
		return 0, "", out, false
	}
	payload := string(out[start : start+end])
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) != 2 {
		return 0, "", out, false
	}
	ec, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", out, false
	}
	return ec, parts[1], out[:idx], true
}
func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee.ExitStatus()
	}
	return -1
}
func selectLines(data []byte, maxLines int, tail bool) ([]byte, int, bool) {
	if len(data) == 0 {
		return nil, 0, false
	}
	lines := bytes.Split(data, []byte("\n"))
	total := len(lines)
	if data[len(data)-1] == '\n' {
		total--
	}
	if total <= maxLines {
		return data, total, false
	}
	if tail {
		return bytes.Join(lines[len(lines)-maxLines:], []byte("\n")), total, true
	}
	return bytes.Join(lines[:maxLines], []byte("\n")), total, true
}
func (r *Runner) ArtifactPath(id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("artifact_id 无效")
	}
	return filepath.Join(r.dataDir, "artifacts", id+".log"), nil
}

func (r *Runner) CommandOutputPath(id, stream string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("run_id 无效")
	}
	if stream != "stdout" && stream != "stderr" {
		return "", fmt.Errorf("stream 仅支持 stdout 或 stderr")
	}
	return filepath.Join(r.dataDir, "command-runs", id+"."+stream+".log"), nil
}

type OutputChunk struct {
	Content    string `json:"content"`
	Offset     int64  `json:"offset_bytes"`
	NextOffset int64  `json:"next_offset_bytes"`
	TotalBytes int64  `json:"total_bytes"`
	Complete   bool   `json:"complete"`
}

// CompleteUTF8Prefix 避免文本分页恰好切在 UTF-8 字符中间。被保留的尾部字节会由
// 下一页从相同字节偏移重新读取；无后续数据时原样返回，让 JSON 编码器处理非法字节。
func CompleteUTF8Prefix(data []byte, hasMore bool) []byte {
	if !hasMore || len(data) == 0 {
		return data
	}
	start := len(data) - 1
	for start > 0 && !utf8.RuneStart(data[start]) {
		start--
	}
	if utf8.FullRune(data[start:]) {
		return data
	}
	return data[:start]
}

// UTF8Page 在 limit 内取一个完整字符边界；当 limit 小到连首个字符都放不下时，
// 允许只超出到该字符末尾，保证 next_offset 能继续前进。
func UTF8Page(data []byte, limit int, hasMore bool) []byte {
	if limit <= 0 || len(data) <= limit {
		return CompleteUTF8Prefix(data, hasMore)
	}
	page := CompleteUTF8Prefix(data[:limit], true)
	if len(page) > 0 {
		return page
	}
	_, size := utf8.DecodeRune(data)
	if size <= 0 || size > len(data) {
		size = 1
	}
	return data[:size]
}

// ReadCommandOutput 按原始字节偏移读取输出，避免详情页一次把大日志装进内存。
func (r *Runner) ReadCommandOutput(id, stream string, offset int64, limit int) (OutputChunk, error) {
	path, err := r.CommandOutputPath(id, stream)
	if err != nil {
		return OutputChunk{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return OutputChunk{}, fmt.Errorf("命令输出已过期或不存在")
		}
		return OutputChunk{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return OutputChunk{}, err
	}
	if offset < 0 {
		offset = 0
	}
	if offset > info.Size() {
		offset = info.Size()
	}
	if limit <= 0 {
		limit = 256 << 10
	}
	if limit > 1<<20 {
		limit = 1 << 20
	}
	readLimit := int64(limit) + utf8.UTFMax - 1
	data := make([]byte, min(readLimit, info.Size()-offset))
	read, readErr := file.ReadAt(data, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return OutputChunk{}, readErr
	}
	data = data[:read]
	data = UTF8Page(data, limit, offset+int64(read) < info.Size())
	rawBytes := len(data)
	next := offset + int64(rawBytes)
	// 游标始终按原始字节推进；非法 UTF-8 只在文本响应里替换，不能改变下一页偏移。
	content := strings.ToValidUTF8(string(data), "�")
	return OutputChunk{Content: content, Offset: offset, NextOffset: next, TotalBytes: info.Size(), Complete: next >= info.Size()}, nil
}
func ReadArtifact(path string, offset, limit int, pattern string) (string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, fmt.Errorf("artifact 已过期或不存在")
		}
		return "", 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", 0, err
	}
	lines := strings.Split(string(data), "\n")
	total := len(lines)
	if len(data) > 0 && data[len(data)-1] == '\n' {
		total--
		lines = lines[:len(lines)-1]
	}
	var re *regexp.Regexp
	if pattern != "" {
		re, err = regexp.Compile(pattern)
		if err != nil {
			return "", total, fmt.Errorf("grep 正则无效: %w", err)
		}
	}
	if offset <= 0 {
		offset = 1
	}
	if limit <= 0 || limit > 5000 {
		limit = 200
	}
	var matched []string
	for i, line := range lines {
		if re == nil || re.MatchString(line) {
			matched = append(matched, fmt.Sprintf("%d:%s", i+1, line))
		}
	}
	start := min(offset-1, len(matched))
	end := min(start+limit, len(matched))
	return strings.Join(matched[start:end], "\n"), total, nil
}
