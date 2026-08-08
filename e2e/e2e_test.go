//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type authTransport struct{ token string }

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}

type hostView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type tokenView struct {
	Token string `json:"token"`
}
type execResult struct {
	Output     string `json:"output"`
	ExitCode   int    `json:"exit_code"`
	Cwd        string `json:"cwd"`
	Timeout    bool   `json:"timeout"`
	Truncated  bool   `json:"truncated"`
	ArtifactID string `json:"artifact_id"`
}
type jobStart struct {
	JobID string `json:"job_id"`
	PID   int64  `json:"pid"`
}
type jobState struct {
	Job struct {
		Status   string `json:"status"`
		ExitCode *int64 `json:"exit_code"`
	} `json:"job"`
	LogBytes int64 `json:"log_bytes"`
}
type fileWrite struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type fileRead struct {
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}
type transfer struct {
	SourceSHA256      string `json:"source_sha256"`
	DestinationSHA256 string `json:"destination_sha256"`
	Verified          bool   `json:"verified"`
}
type artifact struct {
	Content string `json:"content"`
}
type snapshot struct {
	CPUPct     *float64 `json:"cpu_pct"`
	MemTotalKB *int64   `json:"mem_total_kb"`
}

func request[T any](t *testing.T, c *http.Client, method, url string, body any) T {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Fatalf("%s %s: %d %s", method, url, res.StatusCode, data)
	}
	var out T
	if len(data) > 0 {
		if err = json.Unmarshal(data, &out); err != nil {
			t.Fatalf("解析响应: %v: %s", err, data)
		}
	}
	return out
}
func call(t *testing.T, s *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("调用 %s: %v", name, err)
	}
	return result
}
func decoded[T any](t *testing.T, r *mcp.CallToolResult) T {
	t.Helper()
	if r.IsError {
		t.Fatalf("工具错误: %s", toolText(r))
	}
	raw, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解码 structured content: %v: %s", err, raw)
	}
	return out
}
func toolText(r *mcp.CallToolResult) string {
	var out []string
	for _, c := range r.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			out = append(out, text.Text)
		}
	}
	return strings.Join(out, "\n")
}
func connect(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "onessh-e2e", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: authTransport{token: token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestEndToEnd(t *testing.T) {
	url := os.Getenv("ONESSH_URL")
	if url == "" {
		url = "http://localhost:8866/mcp"
	}
	base := strings.TrimSuffix(url, "/mcp")
	jar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	request[map[string]bool](t, admin, http.MethodPost, base+"/api/v1/login", map[string]string{"password": "test123"})
	createHost := func(name string) hostView {
		return request[hostView](t, admin, http.MethodPost, base+"/api/v1/hosts", map[string]any{"name": name, "addr": name, "port": 2222, "username": "test", "auth_type": "password", "password": "pass", "monitor_enabled": true})
	}
	ssh1 := createHost("ssh1")
	ssh2 := createHost("ssh2")
	for _, h := range []hostView{ssh1, ssh2} {
		var ok bool
		for range 20 {
			req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/hosts/%d/test", base, h.ID), strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			res, err := admin.Do(req)
			if err == nil && res.StatusCode == 200 {
				res.Body.Close()
				ok = true
				break
			}
			if res != nil {
				res.Body.Close()
			}
			time.Sleep(time.Second)
		}
		if !ok {
			t.Fatalf("主机 %s 未就绪", h.Name)
		}
	}
	tokenA := request[tokenView](t, admin, http.MethodPost, base+"/api/v1/tokens", map[string]any{"name": "all", "all_hosts": true})
	tokenB := request[tokenView](t, admin, http.MethodPost, base+"/api/v1/tokens", map[string]any{"name": "ssh1-only", "all_hosts": false, "host_ids": []int64{ssh1.ID}})
	a := connect(t, url, tokenA.Token)
	defer a.Close()
	b := connect(t, url, tokenB.Token)
	defer b.Close()
	t.Run("session cwd", func(t *testing.T) {
		first := decoded[execResult](t, call(t, a, "exec", map[string]any{"host": "ssh1", "command": "cd /tmp && pwd", "session": "cwd"}))
		if first.ExitCode != 0 || !strings.Contains(first.Output, "/tmp") {
			t.Fatalf("首次 exec %+v", first)
		}
		second := decoded[execResult](t, call(t, a, "exec", map[string]any{"host": "ssh1", "command": "pwd", "session": "cwd"}))
		if strings.TrimSpace(second.Output) != "/tmp" {
			t.Fatalf("cwd 未持久化 %+v", second)
		}
	})
	t.Run("large output artifact", func(t *testing.T) {
		large := decoded[execResult](t, call(t, a, "exec", map[string]any{"host": "ssh1", "command": "seq 1 100000", "max_lines": 200}))
		if !large.Truncated || large.ArtifactID == "" {
			t.Fatalf("未生成 artifact %+v", large)
		}
		found := decoded[artifact](t, call(t, a, "output_read", map[string]any{"artifact_id": large.ArtifactID, "grep": "^99999$"}))
		if !strings.Contains(found.Content, "99999") {
			t.Fatalf("未命中尾部: %q", found.Content)
		}
	})
	t.Run("jobs", func(t *testing.T) {
		started := decoded[jobStart](t, call(t, a, "job_start", map[string]any{"host": "ssh1", "command": "sleep 2; echo done"}))
		state := decoded[jobState](t, call(t, a, "job_status", map[string]any{"job_id": started.JobID}))
		if state.Job.Status != "running" {
			t.Fatalf("初始状态 %+v", state)
		}
		time.Sleep(3 * time.Second)
		state = decoded[jobState](t, call(t, a, "job_status", map[string]any{"job_id": started.JobID}))
		if state.Job.Status != "exited" || state.Job.ExitCode == nil || *state.Job.ExitCode != 0 {
			t.Fatalf("结束状态 %+v", state)
		}
		logs := decoded[execResult](t, call(t, a, "job_logs", map[string]any{"job_id": started.JobID}))
		if !strings.Contains(logs.Output, "done") {
			t.Fatalf("任务日志缺失 %q", logs.Output)
		}
		long := decoded[jobStart](t, call(t, a, "job_start", map[string]any{"host": "ssh1", "command": "sleep 300"}))
		if call(t, a, "job_kill", map[string]any{"job_id": long.JobID}).IsError {
			t.Fatal("job_kill 失败")
		}
	})
	t.Run("files", func(t *testing.T) {
		written := decoded[fileWrite](t, call(t, a, "file_write", map[string]any{"host": "ssh1", "path": "/tmp/onessh.txt", "content": "one\ntwo\n"}))
		edited := decoded[fileWrite](t, call(t, a, "file_edit", map[string]any{"host": "ssh1", "path": "/tmp/onessh.txt", "expected_sha256": written.SHA256, "edits": []map[string]string{{"old_text": "two", "new_text": "second"}}}))
		read := decoded[fileRead](t, call(t, a, "file_read", map[string]any{"host": "ssh1", "path": "/tmp/onessh.txt"}))
		if !strings.Contains(read.Content, "2:second") {
			t.Fatalf("编辑结果 %q", read.Content)
		}
		conflict := call(t, a, "file_edit", map[string]any{"host": "ssh1", "path": "/tmp/onessh.txt", "expected_sha256": "bad", "edits": []map[string]string{{"old_text": "one", "new_text": "1"}}})
		if !conflict.IsError || !strings.Contains(toolText(conflict), "conflict") {
			t.Fatal("未报告冲突")
		}
		_ = edited
		payload := strings.Repeat("x", 1<<20)
		decoded[fileWrite](t, call(t, a, "file_write", map[string]any{"host": "ssh1", "path": "/tmp/blob", "content": payload}))
		moved := decoded[transfer](t, call(t, a, "file_transfer", map[string]any{"src_host": "ssh1", "src_path": "/tmp/blob", "dst_host": "ssh2", "dst_path": "/tmp/blob"}))
		if !moved.Verified || moved.SourceSHA256 != moved.DestinationSHA256 {
			t.Fatalf("传输 %+v", moved)
		}
	})
	t.Run("image", func(t *testing.T) {
		pngBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=="
		decoded[execResult](t, call(t, a, "exec", map[string]any{"host": "ssh1", "command": fmt.Sprintf("printf %%s %q | base64 -d > /tmp/pixel.png", pngBase64)}))
		result := call(t, a, "image_view", map[string]any{"host": "ssh1", "path": "/tmp/pixel.png", "max_dim": 1})
		if result.IsError {
			t.Fatal(toolText(result))
		}
		var imageSeen bool
		for _, content := range result.Content {
			if img, ok := content.(*mcp.ImageContent); ok {
				if _, err := base64.StdEncoding.DecodeString(base64.StdEncoding.EncodeToString(img.Data)); err == nil && len(img.Data) > 0 {
					imageSeen = true
				}
			}
		}
		if !imageSeen {
			t.Fatal("未返回 ImageContent")
		}
	})
	t.Run("authorization and monitor", func(t *testing.T) {
		denied := call(t, b, "exec", map[string]any{"host": "ssh2", "command": "true"})
		if !denied.IsError || !strings.Contains(toolText(denied), "host not authorized") {
			t.Fatalf("越权未拒绝: %s", toolText(denied))
		}
		snap := decoded[snapshot](t, call(t, a, "host_status", map[string]any{"host": "ssh1", "fresh": true}))
		if snap.CPUPct == nil || snap.MemTotalKB == nil {
			t.Fatalf("指标缺失 %+v", snap)
		}
	})
	t.Run("terminal", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		wsURL := strings.Replace(base, "http://", "ws://", 1) + "/api/v1/ws/terminal?host=ssh1&cols=80&rows=24"
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: admin})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		payload, _ := json.Marshal(map[string]any{"type": "input", "data": "echo TERMINAL_OK\n"})
		if err = conn.Write(ctx, websocket.MessageText, payload); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		for !strings.Contains(output.String(), "TERMINAL_OK") || strings.Count(output.String(), "TERMINAL_OK") < 2 {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			output.Write(data)
		}
		resize, _ := json.Marshal(map[string]any{"type": "resize", "cols": 100, "rows": 40})
		if err = conn.Write(ctx, websocket.MessageText, resize); err != nil {
			t.Fatal(err)
		}
	})
	audit := request[[]map[string]any](t, admin, http.MethodGet, base+"/api/v1/audit?limit=500", nil)
	if len(audit) == 0 {
		t.Fatal("审计为空")
	}
	for _, row := range audit {
		if row["Tool"] == "file_edit" {
			params, _ := row["ParamsJSON"].(string)
			if strings.Contains(params, "old_text") || strings.Contains(params, "second") {
				t.Fatalf("审计泄漏 edits 正文: %s", params)
			}
			return
		}
	}
	t.Fatal("审计缺少 file_edit")
}
