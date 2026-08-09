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
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	HostKeyFP *string `json:"hostkey_fp"`
}
type managedHosts struct {
	Hosts []hostView `json:"hosts"`
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
type grepResult struct {
	Lines []struct {
		Path   string `json:"path"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
		Text   string `json:"text"`
		Match  bool   `json:"match"`
	} `json:"lines"`
	MatchCount int    `json:"match_count"`
	Truncated  bool   `json:"truncated"`
	Engine     string `json:"engine"`
	Warning    string `json:"warning"`
}
type findResult struct {
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
	Engine    string   `json:"engine"`
	Warning   string   `json:"warning"`
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
	sshNoTools := createHost("ssh-no-tools")
	for _, h := range []hostView{ssh1, ssh2, sshNoTools} {
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
	managerToken := request[tokenView](t, admin, http.MethodPost, base+"/api/v1/tokens", map[string]any{"name": "host-manager", "all_hosts": false, "manage_hosts": true, "host_ids": []int64{ssh1.ID}})
	a := connect(t, url, tokenA.Token)
	defer a.Close()
	b := connect(t, url, tokenB.Token)
	defer b.Close()
	manager := connect(t, url, managerToken.Token)
	defer manager.Close()
	t.Run("host management lifecycle and authorization isolation", func(t *testing.T) {
		deniedList := call(t, b, "hosts_manage_list", map[string]any{})
		if !deniedList.IsError || !strings.Contains(toolText(deniedList), "host management not authorized") {
			t.Fatalf("普通令牌未拒绝管理列表: %s", toolText(deniedList))
		}
		deniedCreate := call(t, b, "host_create", map[string]any{
			"name": "denied", "addr": "ssh1", "port": 2222, "username": "test", "auth_type": "password", "password": "pass",
		})
		if !deniedCreate.IsError || !strings.Contains(toolText(deniedCreate), "host management not authorized") {
			t.Fatalf("普通令牌未拒绝创建主机: %s", toolText(deniedCreate))
		}

		listed := decoded[managedHosts](t, call(t, manager, "hosts_manage_list", map[string]any{}))
		names := make(map[string]bool, len(listed.Hosts))
		for _, host := range listed.Hosts {
			names[host.Name] = true
		}
		if !names["ssh1"] || !names["ssh2"] {
			t.Fatalf("管理列表未覆盖全局主机: %#v", names)
		}

		created := decoded[hostView](t, call(t, manager, "host_create", map[string]any{
			"name": "managed-host", "addr": "ssh1", "port": 2222, "username": "test", "auth_type": "password", "password": "pass", "monitor_enabled": false,
		}))
		managerExec := call(t, manager, "exec", map[string]any{"host": created.Name, "command": "true"})
		if !managerExec.IsError || !strings.Contains(toolText(managerExec), "host not authorized") {
			t.Fatalf("管理权限扩张了新主机执行范围: %s", toolText(managerExec))
		}
		tested := decoded[execResult](t, call(t, manager, "host_test", map[string]any{"host": created.Name}))
		if tested.ExitCode != 0 {
			t.Fatalf("新主管理测试失败: %+v", tested)
		}
		hostsAfterTest := request[[]hostView](t, admin, http.MethodGet, base+"/api/v1/hosts", nil)
		var fingerprinted bool
		for _, host := range hostsAfterTest {
			if host.Name == created.Name && host.HostKeyFP != nil && *host.HostKeyFP != "" {
				fingerprinted = true
			}
		}
		if !fingerprinted {
			t.Fatal("主机测试未固定 TOFU 指纹")
		}

		updated := decoded[hostView](t, call(t, manager, "host_update", map[string]any{
			"host": created.Name, "name": "managed-renamed", "addr": "ssh1", "port": 2222, "username": "test", "auth_type": "password",
		}))
		if updated.Name != "managed-renamed" {
			t.Fatalf("主机改名失败: %+v", updated)
		}
		decoded[execResult](t, call(t, manager, "host_test", map[string]any{"host": updated.Name}))
		if call(t, manager, "host_reset_fingerprint", map[string]any{"host": updated.Name}).IsError {
			t.Fatal("重置指纹失败")
		}
		hostsAfterReset := request[[]hostView](t, admin, http.MethodGet, base+"/api/v1/hosts", nil)
		for _, host := range hostsAfterReset {
			if host.Name == updated.Name && host.HostKeyFP != nil {
				t.Fatalf("重置后指纹仍存在: %q", *host.HostKeyFP)
			}
		}
		decoded[execResult](t, call(t, manager, "host_test", map[string]any{"host": updated.Name}))

		restrictedToken := request[tokenView](t, admin, http.MethodPost, base+"/api/v1/tokens", map[string]any{
			"name": "managed-only", "all_hosts": false, "host_ids": []int64{updated.ID},
		})
		restricted := connect(t, url, restrictedToken.Token)
		defer restricted.Close()
		if call(t, manager, "host_delete", map[string]any{"host": updated.Name}).IsError {
			t.Fatal("删除受管主机失败")
		}
		replacement := decoded[hostView](t, call(t, manager, "host_create", map[string]any{
			"name": "replacement", "addr": "ssh2", "port": 2222, "username": "test", "auth_type": "password", "password": "pass", "monitor_enabled": false,
		}))
		if replacement.ID != updated.ID {
			t.Fatalf("测试前提不成立，SQLite 未复用最大主机 ID: old=%d new=%d", updated.ID, replacement.ID)
		}
		for label, session := range map[string]*mcp.ClientSession{"旧受限令牌": restricted, "管理令牌": manager} {
			result := call(t, session, "exec", map[string]any{"host": replacement.Name, "command": "true"})
			if !result.IsError || !strings.Contains(toolText(result), "host not authorized") {
				t.Fatalf("%s 越权执行替代主机: %s", label, toolText(result))
			}
		}
		if call(t, manager, "host_delete", map[string]any{"host": replacement.Name}).IsError {
			t.Fatal("删除替代主机失败")
		}
		finalList := decoded[managedHosts](t, call(t, manager, "hosts_manage_list", map[string]any{}))
		for _, host := range finalList.Hosts {
			if host.Name == updated.Name || host.Name == replacement.Name {
				t.Fatalf("已删除主机仍在管理列表: %+v", host)
			}
		}
	})
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
	t.Run("search", func(t *testing.T) {
		decoded[execResult](t, call(t, a, "exec", map[string]any{"host": "ssh1", "command": "mkdir -p /tmp/onessh-search/.git"}))
		for path, content := range map[string]string{
			"/tmp/onessh-search/.gitignore":       "ignored.go\n",
			"/tmp/onessh-search/main.go":          "package main\n// NeedleAlpha\n",
			"/tmp/onessh-search/pkg/main_test.go": "package pkg\n// needLEalpha\n",
			"/tmp/onessh-search/ignored.go":       "package ignored\n// NeedleAlpha\n",
		} {
			decoded[fileWrite](t, call(t, a, "file_write", map[string]any{"host": "ssh1", "path": path, "content": content}))
		}
		nativeGrep := call(t, a, "grep", map[string]any{
			"host": "ssh1", "pattern": "needlealpha", "path": "/tmp/onessh-search",
			"ignoreCase": true, "context": 1, "limit": 10,
		})
		rawNativeGrep, _ := json.Marshal(nativeGrep.StructuredContent)
		if bytes.Contains(rawNativeGrep, []byte(`"warning"`)) {
			t.Fatalf("原生 grep 不应返回 warning: %s", rawNativeGrep)
		}
		found := decoded[grepResult](t, nativeGrep)
		if found.Engine != "rg" || found.Warning != "" || found.MatchCount != 2 || found.Truncated {
			t.Fatalf("grep 原生引擎或结果错误: %+v", found)
		}
		matches := map[string]bool{}
		for _, line := range found.Lines {
			if line.Match {
				matches[line.Path] = true
			}
		}
		if len(matches) != 2 || !matches["main.go"] || !matches["pkg/main_test.go"] {
			t.Fatalf("grep 路径或 ignore 规则错误: %v", matches)
		}
		nativeFind := call(t, a, "find", map[string]any{
			"host": "ssh1", "pattern": "**/*_test.go", "path": "/tmp/onessh-search",
		})
		rawNativeFind, _ := json.Marshal(nativeFind.StructuredContent)
		if bytes.Contains(rawNativeFind, []byte(`"warning"`)) {
			t.Fatalf("原生 find 不应返回 warning: %s", rawNativeFind)
		}
		files := decoded[findResult](t, nativeFind)
		if files.Engine != "fd" || files.Warning != "" || len(files.Paths) != 1 || files.Paths[0] != "pkg/main_test.go" || files.Truncated {
			t.Fatalf("find 原生引擎或结果错误: %+v", files)
		}
		limited := decoded[grepResult](t, call(t, a, "grep", map[string]any{
			"host": "ssh1", "pattern": "package|Needle", "path": "/tmp/onessh-search/main.go", "limit": 1,
		}))
		if limited.MatchCount != 1 || !limited.Truncated {
			t.Fatalf("grep 限制未生效: %+v", limited)
		}
		invalid := call(t, a, "grep", map[string]any{"host": "ssh1", "pattern": "[", "path": "/tmp/onessh-search"})
		if !invalid.IsError || !strings.Contains(toolText(invalid), "rg") {
			t.Fatalf("无效正则未返回错误: %s", toolText(invalid))
		}
		longLine := "NeedleAlpha " + strings.Repeat("x", 3000) + "\n"
		decoded[fileWrite](t, call(t, a, "file_write", map[string]any{
			"host": "ssh1", "path": "/tmp/onessh-large.txt", "content": strings.Repeat(longLine, 100),
		}))
		bounded := decoded[grepResult](t, call(t, a, "grep", map[string]any{
			"host": "ssh1", "pattern": "NeedleAlpha", "path": "/tmp/onessh-large.txt", "limit": 1000,
		}))
		if !bounded.Truncated || bounded.MatchCount == 0 || bounded.MatchCount >= 100 {
			t.Fatalf("grep 输出上限未生效: matches=%d truncated=%v", bounded.MatchCount, bounded.Truncated)
		}
	})
	t.Run("search fallback without remote binaries", func(t *testing.T) {
		for path, content := range map[string]string{
			"/tmp/onessh-fallback/.gitignore": "ignored.go\n",
			"/tmp/onessh-fallback/main.go":    "package main\n// FallbackNeedle\n",
			"/tmp/onessh-fallback/ignored.go": "package ignored\n// FallbackNeedle\n",
			"/tmp/onessh-fallback/binary.go":  "FallbackNeedle\x00binary\n",
			"/tmp/onessh-outside.go":          "package outside\n// FallbackNeedle\n",
		} {
			decoded[fileWrite](t, call(t, a, "file_write", map[string]any{"host": "ssh-no-tools", "path": path, "content": content}))
		}
		decoded[execResult](t, call(t, a, "exec", map[string]any{
			"host": "ssh-no-tools", "command": "ln -s /tmp/onessh-outside.go /tmp/onessh-fallback/linked.go",
		}))
		found := decoded[grepResult](t, call(t, a, "grep", map[string]any{
			"host": "ssh-no-tools", "pattern": "FallbackNeedle", "path": "/tmp/onessh-fallback", "limit": 10,
		}))
		if found.Engine != "sftp" || !strings.Contains(found.Warning, "性能") || found.MatchCount != 1 || found.Truncated || len(found.Lines) != 1 || found.Lines[0].Path != "main.go" {
			t.Fatalf("SFTP grep 降级元数据或结果错误: %+v", found)
		}
		files := decoded[findResult](t, call(t, a, "find", map[string]any{
			"host": "ssh-no-tools", "pattern": "*.go", "path": "/tmp/onessh-fallback", "limit": 10,
		}))
		if files.Engine != "sftp" || !strings.Contains(files.Warning, "性能") || files.Truncated || len(files.Paths) != 2 || files.Paths[0] != "binary.go" || files.Paths[1] != "main.go" {
			t.Fatalf("SFTP find 降级元数据或结果错误: %+v", files)
		}
		invalid := call(t, a, "grep", map[string]any{
			"host": "ssh-no-tools", "pattern": "[", "path": "/tmp/onessh-fallback",
		})
		if !invalid.IsError || !strings.Contains(toolText(invalid), "Go 正则无效") {
			t.Fatalf("SFTP grep 无效正则未返回错误: %s", toolText(invalid))
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
	expectedManagement := map[string]bool{
		"hosts_manage_list":      false,
		"host_create":            false,
		"host_update":            false,
		"host_test":              false,
		"host_reset_fingerprint": false,
		"host_delete":            false,
	}
	deniedManagement := map[string]bool{"hosts_manage_list": false, "host_create": false}
	fileEditSeen := false
	hostCreateRedacted := false
	for _, row := range audit {
		tool, _ := row["Tool"].(string)
		ok, _ := row["OK"].(bool)
		params, _ := row["ParamsJSON"].(string)
		if tool == "file_edit" {
			fileEditSeen = true
			if strings.Contains(params, "old_text") || strings.Contains(params, "second") {
				t.Fatalf("审计泄漏 edits 正文: %s", params)
			}
		}
		if _, tracked := expectedManagement[tool]; tracked {
			if strings.Contains(params, `"password":"pass"`) {
				t.Fatalf("主机管理审计泄漏密码: %s", params)
			}
			if ok {
				expectedManagement[tool] = true
			} else if _, trackedDenied := deniedManagement[tool]; trackedDenied {
				deniedManagement[tool] = true
			}
			if tool == "host_create" && strings.Contains(params, `"<redacted>"`) {
				hostCreateRedacted = true
			}
		}
	}
	if !fileEditSeen {
		t.Fatal("审计缺少 file_edit")
	}
	for tool, seen := range expectedManagement {
		if !seen {
			t.Fatalf("审计缺少成功管理调用 %s", tool)
		}
	}
	for tool, seen := range deniedManagement {
		if !seen {
			t.Fatalf("审计缺少失败管理调用 %s", tool)
		}
	}
	if !hostCreateRedacted {
		t.Fatal("host_create 审计未记录脱敏占位符")
	}
}
