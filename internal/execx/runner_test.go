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
