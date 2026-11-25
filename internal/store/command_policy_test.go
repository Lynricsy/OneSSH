package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"onessh/internal/cmdpolicy"
)

func assertCommandPolicy(t *testing.T, got cmdpolicy.TokenPolicy, mode string, deny, allow string) {
	t.Helper()
	if got.Mode != mode || strings.Join(got.Deny, ",") != deny || strings.Join(got.Allow, ",") != allow {
		t.Fatalf("命令策略 = %#v，期望 mode=%s deny=%q allow=%q", got, mode, deny, allow)
	}
}

func TestTokenCommandPolicyRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	token, err := st.CreateToken(ctx, TokenCreate{
		Name: "agent", Hash: "hash-1", AllHosts: true,
		CommandPolicy: cmdpolicy.TokenPolicy{Mode: "deny", Deny: []string{"rm\\s+-rf", "docker\\s+system\\s+prune"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, token.CommandPolicy, "deny", "rm\\s+-rf,docker\\s+system\\s+prune", "")

	found, _, err := st.FindToken(ctx, "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, found.CommandPolicy, "deny", "rm\\s+-rf,docker\\s+system\\s+prune", "")

	list, err := st.ListTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, list[0].CommandPolicy, "deny", "rm\\s+-rf,docker\\s+system\\s+prune", "")

	updated, err := st.UpdateToken(ctx, token.ID, TokenUpdate{
		Name: "agent", AllHosts: true,
		CommandPolicy: &cmdpolicy.TokenPolicy{Mode: "allow", Deny: []string{"shutdown"}, Allow: []string{"^uptime$"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, updated.CommandPolicy, "allow", "shutdown", "^uptime$")

	if _, err = st.UpdateToken(ctx, token.ID, TokenUpdate{
		Name: "agent", AllHosts: true,
		CommandPolicy: &cmdpolicy.TokenPolicy{Mode: "deny", Deny: []string{"("}},
	}); err == nil || !strings.Contains(err.Error(), "不是合法正则") {
		t.Fatalf("UpdateToken 应拒绝非法正则，err=%v", err)
	}
	got, err := st.GetToken(ctx, token.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, got.CommandPolicy, "allow", "shutdown", "^uptime$")

	// 旧客户端/旧前端 PUT 时不带 command_policy：必须保留，不能静默清空。
	if _, err = st.UpdateToken(ctx, token.ID, TokenUpdate{Name: "agent-renamed", AllHosts: true}); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetToken(ctx, token.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, got.CommandPolicy, "allow", "shutdown", "^uptime$")

	// 显式 off 才清除。
	if _, err = st.UpdateToken(ctx, token.ID, TokenUpdate{
		Name: "agent-renamed", AllHosts: true,
		CommandPolicy: &cmdpolicy.TokenPolicy{Mode: "off"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetToken(ctx, token.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, got.CommandPolicy, "off", "", "")
}

// 库里策略损坏时不能退化成 off：按全拒处理，但令牌本身仍可读取。
func TestTokenCommandPolicyCorruptionFailsClosed(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	token, err := st.CreateToken(ctx, TokenCreate{Name: "agent", Hash: "hash-1", AllHosts: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(ctx, `UPDATE tokens SET command_policy_json=? WHERE id=?`, `{"mode":"deny","deny":["("]}`, token.ID); err != nil {
		t.Fatal(err)
	}
	found, _, err := st.FindToken(ctx, "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := found.CommandPolicy.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.Active() || !cmdpolicy.Check([]cmdpolicy.Layer{{Scope: "令牌", Policy: compiled}}, "uptime").Denied {
		t.Fatalf("损坏策略未 fail-closed: %#v", found.CommandPolicy)
	}
}

func TestOAuthCommandPolicyPreservedAcrossRefresh(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	const resource = "http://localhost/mcp"
	const redirectURI = "http://localhost/callback"
	client, err := st.CreateOAuthClient(ctx, OAuthClient{
		ClientID: "client", ClientName: "client", RedirectURIs: []string{redirectURI},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	expiresAt := now + 3600
	if err = st.CreateOAuthAuthorizationCode(ctx, OAuthAuthorizationCode{
		CodeHash: "code", ClientID: client.ClientID, RedirectURI: redirectURI,
		Resource: resource, CodeChallenge: "challenge", Scope: "mcp",
		AllHosts: true, CreatedAt: now, ExpiresAt: expiresAt,
		CommandPolicy: cmdpolicy.TokenPolicy{Mode: "deny", Deny: []string{"rm\\s+-rf"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err = st.CreateOAuthAuthorizationCode(ctx, OAuthAuthorizationCode{
		CodeHash: "bad-code", ClientID: client.ClientID, RedirectURI: redirectURI,
		Resource: resource, CodeChallenge: "challenge", Scope: "mcp",
		AllHosts: true, ExpiresAt: expiresAt,
		CommandPolicy: cmdpolicy.TokenPolicy{Mode: "deny", Deny: []string{"("}},
	}); err == nil || !strings.Contains(err.Error(), "不是合法正则") {
		t.Fatalf("授权码应拒绝非法正则，err=%v", err)
	}
	if _, err = st.ExchangeOAuthAuthorizationCode(ctx, OAuthAuthorizationCodeExchange{
		CodeHash: "code", ClientID: client.ClientID, RedirectURI: redirectURI,
		Resource: resource, CodeChallenge: "challenge", GrantID: "grant",
		AccessTokenName: "oauth", AccessTokenHash: "access-0", RefreshTokenHash: "refresh-0",
		Now: now, AccessExpiresAt: expiresAt, RefreshExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	access, _, err := st.FindTokenForResource(ctx, "access-0", resource)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, access.CommandPolicy, "deny", "rm\\s+-rf", "")

	if _, err = st.RotateOAuthRefreshToken(ctx, OAuthRefreshTokenRotation{
		TokenHash: "refresh-0", ClientID: client.ClientID, Resource: resource,
		AccessTokenName: "access-1", AccessTokenHash: "access-1", RefreshTokenHash: "refresh-1",
		Now: now + 1, AccessExpiresAt: expiresAt, RefreshExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	access, _, err = st.FindTokenForResource(ctx, "access-1", resource)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, access.CommandPolicy, "deny", "rm\\s+-rf", "")

	// 管理面收紧令牌策略后，尚未使用的刷新令牌也要继承，不能只改当前 access token。
	if _, err = st.UpdateToken(ctx, access.ID, TokenUpdate{
		Name: "oauth-restricted", AllHosts: true,
		CommandPolicy: &cmdpolicy.TokenPolicy{Mode: "allow", Allow: []string{"^uptime$"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.RotateOAuthRefreshToken(ctx, OAuthRefreshTokenRotation{
		TokenHash: "refresh-1", ClientID: client.ClientID, Resource: resource,
		AccessTokenName: "access-2", AccessTokenHash: "access-2", RefreshTokenHash: "refresh-2",
		Now: now + 2, AccessExpiresAt: expiresAt, RefreshExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	access, _, err = st.FindTokenForResource(ctx, "access-2", resource)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandPolicy(t, access.CommandPolicy, "allow", "", "^uptime$")
}
