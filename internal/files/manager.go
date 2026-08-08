package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/pmezard/go-difflib/difflib"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
)

const maxFile = 4 << 20

type Manager struct {
	Clients *ClientPool
	Pool    *sshpool.Pool
	Exec    *execx.Runner
}
type ReadResult struct {
	Content    string `json:"content"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	TotalLines int    `json:"total_lines"`
}
type WriteResult struct {
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	NonAtomic bool   `json:"non_atomic,omitempty"`
	Warning   string `json:"warning,omitempty"`
}
type Edit struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}
type EditResult struct {
	WriteResult
	Diff string `json:"diff"`
}
type Entry struct {
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	Mode          string `json:"mode"`
	Mtime         int64  `json:"mtime"`
	Directory     bool   `json:"directory"`
	SymlinkTarget string `json:"symlink_target,omitempty"`
}
type TransferResult struct {
	Bytes             int64  `json:"bytes"`
	SourceSHA256      string `json:"source_sha256"`
	DestinationSHA256 string `json:"destination_sha256,omitempty"`
	Verified          bool   `json:"verified"`
	Warning           string `json:"warning,omitempty"`
}

func New(pool *sshpool.Pool, runner *execx.Runner) *Manager {
	return &Manager{Clients: NewClientPool(pool), Pool: pool, Exec: runner}
}
func (m *Manager) RawRead(ctx context.Context, host, file string, max int64) ([]byte, error) {
	c, err := m.Clients.Get(ctx, host)
	if err != nil {
		return nil, err
	}
	file, err = resolvePath(c, file)
	if err != nil {
		return nil, err
	}
	info, err := c.Stat(file)
	if err != nil {
		return nil, err
	}
	if info.Size() > max {
		return nil, fmt.Errorf("文件大小 %d 超过限制 %d", info.Size(), max)
	}
	f, err := c.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max+1))
}
func (m *Manager) Read(ctx context.Context, host, file string, offset, limit int) (ReadResult, error) {
	data, err := m.RawRead(ctx, host, file, maxFile)
	if err != nil {
		return ReadResult{}, err
	}
	sniff := data
	if len(sniff) > 8192 {
		sniff = sniff[:8192]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return ReadResult{}, fmt.Errorf("binary file；请使用 image_view 或 WebUI 下载")
	}
	sum := sha256.Sum256(data)
	lines := strings.Split(string(data), "\n")
	total := len(lines)
	if len(data) > 0 && data[len(data)-1] == '\n' {
		total--
		lines = lines[:len(lines)-1]
	}
	if offset <= 0 {
		offset = 1
	}
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	start := min(offset-1, len(lines))
	end := min(start+limit, len(lines))
	var b strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%d:%s", i+1, lines[i])
		if i+1 < end {
			b.WriteByte('\n')
		}
	}
	return ReadResult{Content: b.String(), SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data)), TotalLines: total}, nil
}
func randomSuffix() string { var b [6]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func (m *Manager) Write(ctx context.Context, host, file string, data []byte, mode os.FileMode) (WriteResult, error) {
	c, err := m.Clients.Get(ctx, host)
	if err != nil {
		return WriteResult{}, err
	}
	file, err = resolvePath(c, file)
	if err != nil {
		return WriteResult{}, err
	}
	if err = c.MkdirAll(path.Dir(file)); err != nil {
		return WriteResult{}, err
	}
	tmp := file + ".onessh-tmp-" + randomSuffix()
	f, err := c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return WriteResult{}, err
	}
	n, copyErr := io.Copy(f, bytes.NewReader(data))
	closeErr := f.Close()
	if copyErr != nil {
		c.Remove(tmp)
		return WriteResult{}, copyErr
	}
	if closeErr != nil {
		c.Remove(tmp)
		return WriteResult{}, closeErr
	}
	if err = c.Chmod(tmp, mode); err != nil {
		c.Remove(tmp)
		return WriteResult{}, err
	}
	nonAtomic := false
	if err = c.Rename(tmp, file); err != nil {
		nonAtomic = true
		_ = c.Remove(file)
		if err = c.Rename(tmp, file); err != nil {
			c.Remove(tmp)
			return WriteResult{}, err
		}
	}
	sum := sha256.Sum256(data)
	out := WriteResult{SHA256: hex.EncodeToString(sum[:]), Bytes: n, NonAtomic: nonAtomic}
	if nonAtomic {
		out.Warning = "目标服务器不支持覆盖 rename，已使用非原子回退"
	}
	return out, nil
}
func (m *Manager) Edit(ctx context.Context, host, file string, edits []Edit, expected string) (EditResult, error) {
	data, err := m.RawRead(ctx, host, file, maxFile)
	if err != nil {
		return EditResult{}, err
	}
	sum := sha256.Sum256(data)
	current := hex.EncodeToString(sum[:])
	if expected != "" && expected != current {
		return EditResult{}, fmt.Errorf("conflict: expected_sha256=%s actual_sha256=%s；请重读文件", expected, current)
	}
	updated := string(data)
	for i, e := range edits {
		count := strings.Count(updated, e.OldText)
		if count != 1 {
			return EditResult{}, fmt.Errorf("edit %d: old_text 必须唯一匹配，实际 %d 次", i+1, count)
		}
		updated = strings.Replace(updated, e.OldText, e.NewText, 1)
	}
	diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(string(data)), B: difflib.SplitLines(updated), FromFile: file, ToFile: file, Context: 3})
	wr, err := m.Write(ctx, host, file, []byte(updated), 0o644)
	if err != nil {
		return EditResult{}, err
	}
	return EditResult{WriteResult: wr, Diff: diff}, nil
}
func (m *Manager) List(ctx context.Context, host, dir string) ([]Entry, error) {
	c, err := m.Clients.Get(ctx, host)
	if err != nil {
		return nil, err
	}
	dir, err = resolvePath(c, dir)
	if err != nil {
		return nil, err
	}
	items, err := c.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(items) > 500 {
		return nil, fmt.Errorf("目录超过 500 项，请缩小范围")
	}
	out := make([]Entry, 0, len(items))
	for _, info := range items {
		e := Entry{Name: info.Name(), Size: info.Size(), Mode: info.Mode().String(), Mtime: info.ModTime().Unix(), Directory: info.IsDir()}
		if info.Mode()&os.ModeSymlink != 0 {
			e.SymlinkTarget, _ = c.ReadLink(path.Join(dir, info.Name()))
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Directory != out[j].Directory {
			return out[i].Directory
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
func (m *Manager) Transfer(ctx context.Context, srcHost, srcPath, dstHost, dstPath string) (TransferResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	src, err := m.Clients.Get(ctx, srcHost)
	if err != nil {
		return TransferResult{}, err
	}
	dst, err := m.Clients.Get(ctx, dstHost)
	if err != nil {
		return TransferResult{}, err
	}
	srcPath, err = resolvePath(src, srcPath)
	if err != nil {
		return TransferResult{}, err
	}
	dstPath, err = resolvePath(dst, dstPath)
	if err != nil {
		return TransferResult{}, err
	}
	in, err := src.Open(srcPath)
	if err != nil {
		return TransferResult{}, err
	}
	defer in.Close()
	if err = dst.MkdirAll(path.Dir(dstPath)); err != nil {
		return TransferResult{}, err
	}
	tmp := dstPath + ".onessh-tmp-" + randomSuffix()
	out, err := dst.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return TransferResult{}, err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, hash), in)
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		dst.Remove(tmp)
		if err != nil {
			return TransferResult{}, err
		}
		return TransferResult{}, closeErr
	}
	if err = dst.Rename(tmp, dstPath); err != nil {
		_ = dst.Remove(dstPath)
		if err = dst.Rename(tmp, dstPath); err != nil {
			dst.Remove(tmp)
			return TransferResult{}, err
		}
	}
	sourceHash := hex.EncodeToString(hash.Sum(nil))
	result := TransferResult{Bytes: n, SourceSHA256: sourceHash}
	sshClient, err := m.Pool.Get(ctx, dstHost)
	if err == nil {
		res, e := m.Exec.Run(ctx, sshClient, `if command -v sha256sum >/dev/null 2>&1; then sha256sum -- `+execx.SHQ(dstPath)+` | cut -d' ' -f1; else exit 127; fi`, "~", nil, execx.Options{Timeout: 30 * time.Second, MaxLines: 2})
		if e == nil && res.ExitCode == 0 {
			result.DestinationSHA256 = strings.TrimSpace(res.Output)
			result.Verified = result.DestinationSHA256 == sourceHash
		} else {
			result.Warning = "目标缺少 sha256sum，已跳过落盘校验"
		}
	}
	return result, nil
}

var _ = sftp.ErrSSHFxEOF
var _ = strconv.IntSize
