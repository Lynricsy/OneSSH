package store

import (
	"context"
	"strings"
	"testing"
)

func TestTokenDisabledToolsRoundTripAndNormalize(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	token, err := st.CreateToken(ctx, TokenCreate{
		Name: "agent", Hash: "hash-1", AllHosts: true, DisabledTools: []string{"memory", "files"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// NormalizeList 按 All 顺序去重：files 在 memory 之前。
	if strings.Join(token.DisabledTools, ",") != "files,memory" {
		t.Fatalf("created disabled_tools = %#v", token.DisabledTools)
	}
	found, _, err := st.FindToken(ctx, "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(found.DisabledTools, ",") != "files,memory" {
		t.Fatalf("find disabled_tools = %#v", found.DisabledTools)
	}

	updated, err := st.UpdateToken(ctx, token.ID, TokenUpdate{
		Name: "agent", AllHosts: true, DisabledTools: []string{"exec"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.DisabledTools) != 1 || updated.DisabledTools[0] != "exec" {
		t.Fatalf("updated = %#v", updated.DisabledTools)
	}
}

func TestStoreRejectsUnknownDisabledToolGroups(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	_, err = st.CreateToken(ctx, TokenCreate{
		Name: "bad", Hash: "hash-bad", AllHosts: true, DisabledTools: []string{"not-a-group"},
	})
	if err == nil || !strings.Contains(err.Error(), "未知工具组") {
		t.Fatalf("CreateToken 应拒绝未知分组, err=%v", err)
	}

	token, err := st.CreateToken(ctx, TokenCreate{
		Name: "ok", Hash: "hash-ok", AllHosts: true, DisabledTools: []string{"exec"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpdateToken(ctx, token.ID, TokenUpdate{
		Name: "ok", AllHosts: true, DisabledTools: []string{"nope"},
	})
	if err == nil || !strings.Contains(err.Error(), "未知工具组") {
		t.Fatalf("UpdateToken 应拒绝未知分组, err=%v", err)
	}

	err = st.CreateOAuthAuthorizationCode(ctx, OAuthAuthorizationCode{
		CodeHash: "code-hash", ClientID: "client", RedirectURI: "http://127.0.0.1/cb",
		Resource: "http://localhost/mcp", CodeChallenge: "challenge", Scope: "mcp",
		AllHosts: true, DisabledTools: []string{"bogus"}, ExpiresAt: 9999999999,
	})
	if err == nil || !strings.Contains(err.Error(), "未知工具组") {
		t.Fatalf("CreateOAuthAuthorizationCode 应拒绝未知分组, err=%v", err)
	}
}
