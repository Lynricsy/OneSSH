package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/cmdpolicy"
	"onessh/internal/store"
	"onessh/internal/toolgroups"
)

type policyHarness struct {
	st   *store.Store
	http *httptest.Server
}

func newPolicyHarness(t *testing.T, opts Options) *policyHarness {
	t.Helper()
	server := newTestServer(t, opts)
	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(server.Store, server, resolve))
	t.Cleanup(httpServer.Close)
	return &policyHarness{st: server.Store, http: httpServer}
}

func (h *policyHarness) createHost(t *testing.T, name string) store.Host {
	t.Helper()
	host, err := h.st.CreateHost(t.Context(), store.Host{
		Name: name, Addr: "127.0.0.1", Port: 22, Username: "root", AuthType: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	return host
}

// connectBearer 建一个真实经过 Bearer 鉴权的 MCP 会话，确保令牌级策略随请求生效。
func (h *policyHarness) connectBearer(t *testing.T, ctx context.Context, secret string) *mcp.ClientSession {
	t.Helper()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "policy-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   h.http.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: secret}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func (h *policyHarness) createToken(t *testing.T, name, secret string, policy cmdpolicy.TokenPolicy) store.Token {
	t.Helper()
	token, err := h.st.CreateToken(t.Context(), store.TokenCreate{
		Name: name, Hash: store.TokenHash(secret), AllHosts: true, CommandPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func callTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s 调用失败: %v", name, err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("%s 应被拒绝，实际 %#v", name, result)
	}
	return result
}

func countRows(t *testing.T, h *policyHarness, query string, args ...any) int {
	t.Helper()
	var count int
	if err := h.st.DB.QueryRowContext(t.Context(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestCommandPolicyRejectsAllEntrypointsBeforeCreatingRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	instance, err := cmdpolicy.Parse("deny", []string{`rm\s+-rf`, `docker\s+system\s+prune`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := newPolicyHarness(t, Options{CommandPolicy: instance})

	hostA := h.createHost(t, "web-01")
	h.createHost(t, "web-02")
	secret := "secret-deny-rule"
	token := h.createToken(t, "deny-rule", secret, cmdpolicy.TokenPolicy{})
	session := h.connectBearer(t, ctx, secret)

	dangerous := "sudo rm -rf /var/lib/data"
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{name: "exec", args: map[string]any{"host": "web-01", "command": dangerous}},
		{name: "exec_many", args: map[string]any{"hosts": []string{"web-01", "web-02"}, "command": dangerous}},
		{name: "job_start", args: map[string]any{"host": "web-01", "command": dangerous}},
	} {
		result := callTool(t, ctx, session, tc.name, tc.args)
		text := toolText(result)
		if !strings.Contains(text, "网关命令策略拒绝了本次调用") || !strings.Contains(text, "不是主机连不上") || !strings.Contains(text, "rm\\s+-rf") {
			t.Fatalf("%s 拒绝文案不完整: %s", tc.name, text)
		}
	}

	if runs := countRows(t, h, `SELECT count(*) FROM command_runs WHERE token_id=?`, token.ID); runs != 0 {
		t.Fatalf("被策略拒绝的调用不应创建命令执行记录，实际 %d 条", runs)
	}
	if jobs := countRows(t, h, `SELECT count(*) FROM jobs WHERE token_id=?`, token.ID); jobs != 0 {
		t.Fatalf("被策略拒绝的 job_start 不应创建任务，实际 %d 条", jobs)
	}

	// 环境变量是同一执行面的绕过入口：设置与 job_start.env 都必须拒绝。
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{name: "session_env", args: map[string]any{"host": "web-01", "session": "default", "set": map[string]string{"PATH": "/tmp/evil"}}},
		{name: "job_start", args: map[string]any{"host": "web-01", "command": "uptime", "env": map[string]string{"LD_PRELOAD": "/tmp/evil.so"}}},
	} {
		result := callTool(t, ctx, session, tc.name, tc.args)
		if !strings.Contains(toolText(result), "敏感环境变量") {
			t.Fatalf("%s 环境变量拒绝文案 = %s", tc.name, toolText(result))
		}
	}
	// env 键名本身也是 shell 语法的一部分：`PATH; export EVIL=1; #` 不能靠“不等于 PATH”混过去。
	invalidEnv := callTool(t, ctx, session, "job_start", map[string]any{
		"host": "web-01", "command": "uptime",
		"env": map[string]string{"PATH; export EVIL=1; #": "x"},
	})
	if !strings.Contains(toolText(invalidEnv), "环境变量名无效") {
		t.Fatalf("非法 env 键名文案 = %s", toolText(invalidEnv))
	}
	if runs := countRows(t, h, `SELECT count(*) FROM command_runs`); runs != 0 {
		t.Fatalf("环境变量拒绝后不应产生执行记录，实际 %d 条", runs)
	}
	if sessions := countRows(t, h, `SELECT count(*) FROM sessions WHERE token_id=?`, token.ID); sessions != 0 {
		t.Fatalf("被拒绝的 session_env 不应写库，实际 %d 条", sessions)
	}
	state, err := h.st.GetSession(ctx, token.ID, hostA.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Env["PATH"]; ok {
		t.Fatalf("被拒绝的 PATH 被写入了会话: %#v", state.Env)
	}

	// 拒绝必须落成 outcome=denied 的持久审计，而不是普通执行失败。
	audit, err := h.st.ListAudit(ctx, []int64{token.ID}, nil, nil, nil, store.AuditOutcomeDenied, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 5 {
		t.Fatalf("拒绝审计条数 = %d，期望 5: %#v", len(audit), audit)
	}
	for _, row := range audit {
		if row.OK || row.Outcome != store.AuditOutcomeDenied || row.DenyReason == "" {
			t.Fatalf("拒绝审计字段异常: %#v", row)
		}
	}
}

func TestCommandPolicyInstanceAllowAndTokenTightening(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	instance, err := cmdpolicy.Parse("allow", nil, []string{`^uptime$`, `^df -h$`})
	if err != nil {
		t.Fatal(err)
	}
	h := newPolicyHarness(t, Options{CommandPolicy: instance})
	h.createHost(t, "web-01")

	// 令牌 off 不能放宽实例 allow：reboot 连实例白名单都没命中。
	offSecret := "secret-off"
	h.createToken(t, "off-token", offSecret, cmdpolicy.TokenPolicy{})
	offSession := h.connectBearer(t, ctx, offSecret)
	result := callTool(t, ctx, offSession, "exec", map[string]any{"host": "web-01", "command": "reboot"})
	if !strings.Contains(toolText(result), "实例白名单未命中") {
		t.Fatalf("实例 allow 拒绝文案 = %s", toolText(result))
	}

	// 令牌 allow 只能在实例白名单上再缩小：df -h 同时命中两层，uptime 只命中实例。
	tokenSecret := "secret-narrow"
	h.createToken(t, "narrow-token", tokenSecret, cmdpolicy.TokenPolicy{Mode: "allow", Allow: []string{`^df -h$`}})
	tokenSession := h.connectBearer(t, ctx, tokenSecret)
	result = callTool(t, ctx, tokenSession, "exec", map[string]any{"host": "web-01", "command": "uptime"})
	if !strings.Contains(toolText(result), "令牌白名单未命中") {
		t.Fatalf("令牌白名单收窄文案 = %s", toolText(result))
	}

	// 令牌 deny 可以追加拒绝，即使实例白名单放行。
	denySecret := "secret-extra-deny"
	h.createToken(t, "extra-deny", denySecret, cmdpolicy.TokenPolicy{Mode: "deny", Deny: []string{`df`}})
	denySession := h.connectBearer(t, ctx, denySecret)
	result = callTool(t, ctx, denySession, "exec", map[string]any{"host": "web-01", "command": "df -h"})
	if !strings.Contains(toolText(result), "令牌拒绝规则命中") {
		t.Fatalf("令牌 deny 文案 = %s", toolText(result))
	}
}

func TestCommandPolicyTokenOnlyAndFailClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h := newPolicyHarness(t, Options{})
	host := h.createHost(t, "web-01")

	allowSecret := "secret-token-allow"
	allowToken := h.createToken(t, "token-allow", allowSecret, cmdpolicy.TokenPolicy{Mode: "allow", Allow: []string{`^uptime$`}})
	allowSession := h.connectBearer(t, ctx, allowSecret)
	// 令牌级策略也要进入 initialize instructions，不能只覆盖实例级静态提示词。
	if instructions := allowSession.InitializeResult().Instructions; !strings.Contains(instructions, "【命令策略】") {
		t.Fatalf("令牌级策略未出现在 instructions: %s", instructions)
	}
	for _, tc := range []struct {
		name     string
		args     map[string]any
		contains string
	}{
		{name: "exec", args: map[string]any{"host": "web-01", "command": "cat /etc/shadow"}, contains: "令牌白名单未命中"},
		{name: "exec_many", args: map[string]any{"hosts": []string{"web-01"}, "command": "reboot"}, contains: "令牌白名单未命中"},
		{name: "job_start", args: map[string]any{"host": "web-01", "command": "reboot"}, contains: "令牌白名单未命中"},
		{name: "session_env", args: map[string]any{"host": "web-01", "session": "default", "set": map[string]string{"PATH": "/tmp/evil"}}, contains: "敏感环境变量"},
	} {
		result := callTool(t, ctx, allowSession, tc.name, tc.args)
		if !strings.Contains(toolText(result), tc.contains) {
			t.Fatalf("%s 令牌级拒绝文案 = %s", tc.name, toolText(result))
		}
	}
	if sessions := countRows(t, h, `SELECT count(*) FROM sessions WHERE token_id=?`, allowToken.ID); sessions != 0 {
		t.Fatalf("被拒绝的调用不应创建会话，实际 %d 条", sessions)
	}

	// 令牌 off 时不应拦住 PATH（保持默认行为），这是“只能收紧”的基线。
	offSecret := "secret-token-off"
	offToken := h.createToken(t, "token-off", offSecret, cmdpolicy.TokenPolicy{})
	offSession := h.connectBearer(t, ctx, offSecret)
	if result, err := offSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "session_env", Arguments: map[string]any{"host": "web-01", "session": "default", "set": map[string]string{"PATH": "/opt/bin"}},
	}); err != nil || (result != nil && result.IsError) {
		t.Fatalf("令牌 off 时 session_env 不应被策略拒绝: %#v, %v", result, err)
	}
	state, err := h.st.GetSession(ctx, offToken.ID, host.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	if state.Env["PATH"] != "/opt/bin" {
		t.Fatalf("令牌 off 时 PATH 未落库: %#v", state.Env)
	}

	// 损坏的令牌策略必须 fail-closed，不能退回 off。
	corruptSecret := "secret-token-corrupt"
	corrupt := h.createToken(t, "token-corrupt", corruptSecret, cmdpolicy.TokenPolicy{})
	if _, err := h.st.DB.ExecContext(ctx, `UPDATE tokens SET command_policy_json=? WHERE id=?`, `{"mode":"deny","deny":["("]}`, corrupt.ID); err != nil {
		t.Fatal(err)
	}
	corruptSession := h.connectBearer(t, ctx, corruptSecret)
	result := callTool(t, ctx, corruptSession, "exec", map[string]any{"host": "web-01", "command": "uptime"})
	if !strings.Contains(toolText(result), "白名单为空") {
		t.Fatalf("损坏策略应全拒，实际 = %s", toolText(result))
	}
}

func TestCommandPolicyInstructionMentionsOnlyExposedTools(t *testing.T) {
	instance, err := cmdpolicy.Parse("deny", []string{`rm\s+-rf`}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 只关 exec 组：提示词可以提 job_start.env，但不能提 session_env。
	server := newTestServer(t, Options{
		CommandPolicy: instance,
		DisabledTools: toolgroups.Disabled{toolgroups.Exec: true},
	})
	instructions := serverInstructions(server.disabledTools, true)
	if !strings.Contains(instructions, "【命令策略】") || !strings.Contains(instructions, "job_start.env") {
		t.Fatalf("缺少命令策略或 job_start.env: %s", instructions)
	}
	if strings.Contains(instructions, "session_env") {
		t.Fatalf("exec 组禁用后仍提到 session_env: %s", instructions)
	}

	// exec 与 jobs 都关、只留 fanout：不能出现任何环境变量工具名。
	server = newTestServer(t, Options{
		CommandPolicy: instance,
		DisabledTools: toolgroups.Disabled{toolgroups.Exec: true, toolgroups.Jobs: true},
	})
	instructions = serverInstructions(server.disabledTools, true)
	if !strings.Contains(instructions, "【命令策略】") || strings.Contains(instructions, "session_env") || strings.Contains(instructions, "job_start") {
		t.Fatalf("只留 fanout 时提示词越界: %s", instructions)
	}

	// 零值策略必须与升级前完全一致：默认配置下不出现命令策略段落。
	if zero := serverInstructions(nil, cmdpolicy.Policy{}.Active()); strings.Contains(zero, "【命令策略】") {
		t.Fatalf("默认 off 时不应出现命令策略段落: %s", zero)
	}
}

// 策略可能在会话环境写入之后才启用（令牌级策略无需重启），执行前必须复查残留的敏感变量。
func TestCommandPolicyChecksResidualSessionEnv(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	instance, err := cmdpolicy.Parse("deny", []string{`rm\s+-rf`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := newPolicyHarness(t, Options{CommandPolicy: instance})
	host := h.createHost(t, "web-01")
	secret := "secret-residual-env"
	token := h.createToken(t, "residual-env", secret, cmdpolicy.TokenPolicy{})
	if err = h.st.SaveSession(ctx, store.Session{
		TokenID: token.ID, HostID: host.ID, Label: "default", Cwd: "~",
		Env: map[string]string{"PATH": "/tmp/evil", "LANG": "C"},
	}); err != nil {
		t.Fatal(err)
	}
	session := h.connectBearer(t, ctx, secret)

	result := callTool(t, ctx, session, "exec", map[string]any{"host": "web-01", "command": "uptime"})
	if !strings.Contains(toolText(result), "会话中残留敏感环境变量 PATH") {
		t.Fatalf("残留环境变量拒绝文案 = %s", toolText(result))
	}
	if runs := countRows(t, h, `SELECT count(*) FROM command_runs WHERE token_id=?`, token.ID); runs != 0 {
		t.Fatalf("残留环境变量拒绝不应创建执行记录，实际 %d 条", runs)
	}

	// 删除敏感变量本身不能被拦，否则用户没有恢复路径。
	result, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "session_env", Arguments: map[string]any{"host": "web-01", "session": "default", "unset": []string{"PATH"}},
	})
	if err != nil || (result != nil && result.IsError) {
		t.Fatalf("解除敏感环境变量失败: %#v, %v", result, err)
	}
	state, err := h.st.GetSession(ctx, token.ID, host.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Env["PATH"]; ok || state.Env["LANG"] != "C" {
		t.Fatalf("解除后的会话环境 = %#v", state.Env)
	}
}

// host_test 与 exec 是同一个执行面：uptime 也要过策略，拒绝发生在取连接与固定指纹之前。
func TestCommandPolicyDeniesHostTest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	instance, err := cmdpolicy.Parse("deny", []string{`uptime`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := newPolicyHarness(t, Options{CommandPolicy: instance})
	h.createHost(t, "web-01")
	secret := "secret-host-test"
	if _, err = h.st.CreateToken(t.Context(), store.TokenCreate{
		Name: "host-test", Hash: store.TokenHash(secret), AllHosts: true, ManageHosts: true,
	}); err != nil {
		t.Fatal(err)
	}
	session := h.connectBearer(t, ctx, secret)

	result := callTool(t, ctx, session, "host_test", map[string]any{"host": "web-01"})
	if !strings.Contains(toolText(result), "网关命令策略拒绝了本次调用") || !strings.Contains(toolText(result), "不是主机连不上") {
		t.Fatalf("host_test 拒绝文案 = %s", toolText(result))
	}
	// 拒绝同样落成 outcome=denied 的持久审计，WebUI 可与其他策略拦截一并筛选。
	if denials := countRows(t, h, `SELECT count(*) FROM audit WHERE tool='host_test' AND outcome=?`, store.AuditOutcomeDenied); denials != 1 {
		t.Fatalf("host_test 策略拒绝审计 = %d 条，期望 1", denials)
	}
}

// 手工构造的 Principal（未经 Bearer 中间件装载）走现场编译回退：损坏的令牌策略必须
// fail-closed 全拒；不经 New 构造的 Server（s.command 为 nil）跳过实例层而不是 panic。
func TestPolicyLayersFallbackAndNilCommandGuard(t *testing.T) {
	corruptCtx := context.WithValue(context.Background(), principalKey{}, Principal{
		Token: store.Token{CommandPolicy: cmdpolicy.TokenPolicy{Mode: "deny", Deny: []string{"("}}},
	})
	s := &Server{}
	if reason, denied := s.commandDenial(corruptCtx, "uptime"); !denied || !strings.Contains(reason, "白名单为空") {
		t.Fatalf("损坏策略应 fail-closed 全拒: %q, %v", reason, denied)
	}

	tokenCtx := context.WithValue(context.Background(), principalKey{}, Principal{
		Token: store.Token{CommandPolicy: cmdpolicy.TokenPolicy{Mode: "deny", Deny: []string{`^rm\s+-rf`}}},
	})
	if _, denied := s.commandDenial(tokenCtx, "rm -rf /"); !denied {
		t.Fatal("手工构造 Principal 的合法令牌策略应正常拦截")
	}
	if _, denied := s.commandDenial(tokenCtx, "uptime"); denied {
		t.Fatal("未命中令牌规则不应拒绝")
	}
	// 环境检查由 s.command==nil 短路，与令牌策略是否活跃无关；这里传入活跃策略与
	// 敏感键 PATH，若将来调换 nil 检查与 policyActive 的先后（或删掉 nil 检查），
	// 会在这里 panic 或响亮失败，而不是静默改行为。
	if _, denied := s.envDenial(tokenCtx, []string{"PATH"}); denied {
		t.Fatal("s.command 为 nil 时应跳过环境变量检查，即使令牌策略活跃")
	}
}
