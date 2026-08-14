package execx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSHQAndTrailer(t *testing.T) {
	if got := SHQ("a'b"); got != "'a'\\''b'" {
		t.Fatalf("shq=%q", got)
	}
	out := []byte("hello\n\x01ONESSH:7:/tmp\x01")
	ec, cwd, clean, ok := parseTrailer(out)
	if !ok || ec != 7 || cwd != "/tmp" || !bytes.Equal(clean, []byte("hello")) {
		t.Fatalf("解析失败: %v %d %q %q", ok, ec, cwd, clean)
	}
}
func TestLimitedWriterAndLines(t *testing.T) {
	w := &limitedWriter{limit: 3}
	n, _ := w.Write([]byte("abcdef"))
	if n != 6 || string(w.captured()) != "abf" || !w.truncated || len(w.captured()) > 3 {
		t.Fatalf("limited writer 错误: %q", w.captured())
	}
	stdout := &limitedWriter{limit: captureLimit}
	stderr := &limitedWriter{limit: captureLimit}
	_, _ = stdout.Write(bytes.Repeat([]byte("o"), captureLimit*2))
	_, _ = stderr.Write(bytes.Repeat([]byte("e"), captureLimit*2))
	if len(stdout.captured()) > captureLimit || len(stderr.captured()) > captureLimit || len(stdout.captured())+len(stderr.captured()) > 512<<10 {
		t.Fatal("artifact 捕获预算超限")
	}
	selected, total, cut := selectLines([]byte("1\n2\n3\n"), 2, false)
	if string(selected) != "1\n2" || total != 3 || !cut {
		t.Fatalf("行选择 %q %d %v", selected, total, cut)
	}
	if !strings.Contains(Script("pwd", "~", map[string]string{"A": "b"}), "export A='b'") {
		t.Fatal("脚本缺少 env")
	}
}

func TestLimitedWriterStreamsAllOutputAndStripsTrailer(t *testing.T) {
	var streamed bytes.Buffer
	w := &limitedWriter{
		limit: 3, stream: "stdout", stripTrailer: true, trailerMarker: legacyTrailerMarker,
		callback: func(_ string, chunk []byte) { streamed.Write(chunk) },
	}
	_, _ = w.Write([]byte("abcdef\n\x01ONE"))
	_, _ = w.Write([]byte("SSH:0:/tmp\x01"))
	w.finishCallback()
	if got := streamed.String(); got != "abcdef" {
		t.Fatalf("实时输出包含 trailer 或被捕获上限截断: %q", got)
	}
}

func TestCommandOutputReadAndCleanup(t *testing.T) {
	dataDir := t.TempDir()
	runner := New(dataDir)
	id := uuid.NewString()
	stdoutPath, err := runner.CommandOutputPath(id, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(stdoutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(stdoutPath, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	chunk, err := runner.ReadCommandOutput(id, "stdout", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Content != "cd" || chunk.Offset != 2 || chunk.NextOffset != 4 || chunk.TotalBytes != 6 || chunk.Complete {
		t.Fatalf("分段读取异常: %#v", chunk)
	}
	unmanaged := filepath.Join(filepath.Dir(stdoutPath), "manual.stdout.log")
	if err = os.WriteFile(unmanaged, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	newID := uuid.NewString()
	newPath, err := runner.CommandOutputPath(newID, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(newPath, []byte("running"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := runner.CleanupCommandOutputs(nil)
	if err != nil || removed != 0 {
		t.Fatalf("空删除白名单清理了文件 removed=%d err=%v", removed, err)
	}
	removed, err = runner.CleanupCommandOutputs(map[string]struct{}{id: {}})
	if err != nil || removed != 1 {
		t.Fatalf("命令输出清理结果 removed=%d err=%v", removed, err)
	}
	if _, err = os.Stat(stdoutPath); !os.IsNotExist(err) {
		t.Fatalf("过期输出仍存在: %v", err)
	}
	if _, err = os.Stat(newPath); err != nil {
		t.Fatalf("快照后新建的运行中输出被误删: %v", err)
	}
	if _, err = os.Stat(unmanaged); err != nil {
		t.Fatalf("非托管文件被误删: %v", err)
	}
}

func TestStripCaptureTrailer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdout.log")
	if err := os.WriteFile(path, []byte("hello\n\x01ONESSH:7:/tmp\x01"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stripCaptureTrailer(path, legacyTrailerMarker); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("捕获文件 trailer 未移除: %q", data)
	}
}

func TestCleanupArtifactsHonorsRetention(t *testing.T) {
	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(artifactDir, uuid.NewString()+".log")
	newPath := filepath.Join(artifactDir, uuid.NewString()+".log")
	unmanagedPath := filepath.Join(artifactDir, "manual.log")
	for _, path := range []string{oldPath, newPath, unmanagedPath} {
		if err := os.WriteFile(path, []byte("output"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(oldPath, now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, now.Add(-6*24*time.Hour), now.Add(-6*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unmanagedPath, now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	removed, err := New(dataDir).CleanupArtifacts(now.Add(-7 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("删除数量 %d", removed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("过期 artifact 仍存在: %v", err)
	}
	for _, path := range []string{newPath, unmanagedPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("应保留 %s: %v", path, err)
		}
	}
	if removed, err = New(filepath.Join(dataDir, "missing")).CleanupArtifacts(now); err != nil || removed != 0 {
		t.Fatalf("缺失目录清理结果 removed=%d err=%v", removed, err)
	}
}

func TestReadCommandOutputPreservesUTF8AcrossPages(t *testing.T) {
	if page := UTF8Page([]byte("你a"), 1, true); string(page) != "你" {
		t.Fatalf("极小分页没有推进完整字符: %q", page)
	}
	dataDir := t.TempDir()
	runner := New(dataDir)
	id := uuid.NewString()
	path, err := runner.CommandOutputPath(id, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("abcd你b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = runner.ReadCommandOutput(id, "stdout", 5, 4); err == nil {
		t.Fatal("位于 UTF-8 字符内部的 offset 未被拒绝")
	}
	first, err := runner.ReadCommandOutput(id, "stdout", 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if first.Content != "abcd" || first.NextOffset != 4 || first.Complete {
		t.Fatalf("第一页切分异常: %#v", first)
	}
	second, err := runner.ReadCommandOutput(id, "stdout", first.NextOffset, 4)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "你b" || second.NextOffset != 8 || !second.Complete {
		t.Fatalf("第二页切分异常: %#v", second)
	}
}
