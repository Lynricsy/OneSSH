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

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

const captureLimit = 256 << 10

type Options struct {
	Timeout  time.Duration
	MaxLines int
	Tail     bool
	OnOutput func(stream string, chunk []byte)
}
type Result struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exit_code"`
	Cwd        string `json:"cwd"`
	Timeout    bool   `json:"timeout"`
	Truncated  bool   `json:"truncated"`
	ArtifactID string `json:"artifact_id,omitempty"`
	TotalLines int    `json:"total_lines"`
	TotalBytes int    `json:"total_bytes"`
}
type Runner struct{ dataDir string }

func New(dataDir string) *Runner { return &Runner{dataDir: dataDir} }

func SHQ(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func Script(command, cwd string, env map[string]string) string {
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
	b.WriteString("\n}\n__ec=$?\nprintf '\\n\\001ONESSH:%d:%s\\001' \"$__ec\" \"$PWD\"\n")
	return b.String()
}

type limitedWriter struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
	stream    string
	callback  func(string, []byte)
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	original := len(p)
	remain := w.limit - w.buf.Len()
	if remain <= 0 {
		w.truncated = true
		return original, nil
	}
	accepted := p
	if len(accepted) > remain {
		accepted = accepted[:remain]
		w.truncated = true
	}
	_, _ = w.buf.Write(accepted)
	if w.callback != nil {
		for len(accepted) > 0 {
			n := min(len(accepted), 4096)
			chunk := append([]byte(nil), accepted[:n]...)
			w.callback(w.stream, chunk)
			accepted = accepted[n:]
		}
	}
	return original, nil
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
	stdout := &limitedWriter{limit: captureLimit, stream: "stdout", callback: opt.OnOutput}
	stderr := &limitedWriter{limit: captureLimit, stream: "stderr", callback: opt.OnOutput}
	session.Stdout = stdout
	session.Stderr = stderr
	if err = session.Start(Script(command, cwd, env)); err != nil {
		return Result{}, err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	var waitErr error
	timedOut := false
	timer := time.NewTimer(opt.Timeout)
	defer timer.Stop()
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		timedOut = true
		_ = session.Close()
		waitErr = ctx.Err()
	case <-timer.C:
		timedOut = true
		_ = session.Close()
		waitErr = errors.New("command timeout")
	}
	stdoutBytes := stdout.buf.Bytes()
	exitCode, newCwd, clean, parsed := parseTrailer(stdoutBytes)
	stdoutBytes = clean
	if !parsed {
		exitCode = exitCodeFromError(waitErr)
		newCwd = cwd
	}
	combined := append(append([]byte(nil), stdoutBytes...), stderr.buf.Bytes()...)
	selected, totalLines, lineCut := selectLines(combined, opt.MaxLines, opt.Tail)
	res := Result{Stdout: string(stdoutBytes), Stderr: stderr.buf.String(), Output: string(selected), ExitCode: exitCode, Cwd: newCwd, Timeout: timedOut, Truncated: stdout.truncated || stderr.truncated || lineCut, TotalLines: totalLines, TotalBytes: len(combined)}
	if res.Truncated {
		if err := os.MkdirAll(filepath.Join(r.dataDir, "artifacts"), 0o700); err != nil {
			return res, err
		}
		id := uuid.NewString()
		if err := os.WriteFile(filepath.Join(r.dataDir, "artifacts", id+".log"), combined, 0o600); err != nil {
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
func parseTrailer(out []byte) (int, string, []byte, bool) {
	idx := bytes.LastIndex(out, []byte("\n\x01ONESSH:"))
	if idx < 0 {
		return 0, "", out, false
	}
	start := idx + len("\n\x01ONESSH:")
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
