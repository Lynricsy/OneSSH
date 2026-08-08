package searchx

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecodeRGMatch(t *testing.T) {
	raw := `{"type":"match","data":{"path":{"text":"/tmp/project/main.go"},"lines":{"text":"package main\n"},"line_number":7,"submatches":[{"match":{"text":"main"},"start":8,"end":12}]}}`
	event, ok := decodeRGLine(raw)
	if !ok {
		t.Fatal("未解析 ripgrep JSON")
	}
	line, ok := grepLine(event)
	if !ok {
		t.Fatal("未转换匹配行")
	}
	if line.Path != "/tmp/project/main.go" || line.Line != 7 || line.Column != 9 || line.Text != "package main" || !line.Match {
		t.Fatalf("匹配行错误: %+v", line)
	}
}

func TestDecodeRGBytesAndContext(t *testing.T) {
	encodedPath := base64.StdEncoding.EncodeToString([]byte("src/非UTF8.go"))
	encodedLine := base64.StdEncoding.EncodeToString([]byte("上下文\n"))
	raw := `{"type":"context","data":{"path":{"bytes":"` + encodedPath + `"},"lines":{"bytes":"` + encodedLine + `"},"line_number":3,"submatches":[]}}`
	event, ok := decodeRGLine(raw)
	if !ok {
		t.Fatal("未解析 bytes 事件")
	}
	line, ok := grepLine(event)
	if !ok || line.Path != "src/非UTF8.go" || line.Text != "上下文" || line.Match {
		t.Fatalf("上下文行错误: %+v ok=%v", line, ok)
	}
}

func TestRelativeResultPath(t *testing.T) {
	cases := map[string]struct {
		result string
		root   string
		want   string
	}{
		"dot":      {"./src/main.go", ".", "src/main.go"},
		"relative": {"src/pkg/main.go", "src", "pkg/main.go"},
		"absolute": {"/tmp/project/pkg/main.go", "/tmp/project", "pkg/main.go"},
		"outside":  {"/tmp/other/main.go", "/tmp/project", "/tmp/other/main.go"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if got := relativeResultPath(test.result, test.root); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestShellArgsQuotesEveryArgument(t *testing.T) {
	got := shellArgs([]string{"--", "a'b; touch /tmp/pwn", "dir with spaces"})
	if got != `'--' 'a'\''b; touch /tmp/pwn' 'dir with spaces'` {
		t.Fatalf("引用错误: %q", got)
	}
	if strings.Contains(got, "'a'b") {
		t.Fatalf("单引号未转义: %q", got)
	}
}
