package execx

import (
	"bytes"
	"strings"
	"testing"
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
	if n != 6 || w.buf.String() != "abc" || !w.truncated {
		t.Fatalf("limited writer 错误")
	}
	selected, total, cut := selectLines([]byte("1\n2\n3\n"), 2, false)
	if string(selected) != "1\n2" || total != 3 || !cut {
		t.Fatalf("行选择 %q %d %v", selected, total, cut)
	}
	if !strings.Contains(Script("pwd", "~", map[string]string{"A": "b"}), "export A='b'") {
		t.Fatal("脚本缺少 env")
	}
}
