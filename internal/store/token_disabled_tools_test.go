package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
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

func TestDecodeDisabledToolsRejectsJSONNull(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	token, err := st.CreateToken(ctx, TokenCreate{
		Name: "null-deny", Hash: "hash-null", AllHosts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(ctx, `UPDATE tokens SET disabled_tools_json=? WHERE id=?`, `null`, token.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = st.FindToken(ctx, "hash-null")
	if err == nil || !strings.Contains(err.Error(), "disabled_tools_json must be a JSON array") {
		t.Fatalf("null denylist 应 fail-closed 拒绝解码, err=%v", err)
	}
}

func TestUpdateOAuthTokenPermissionsAcrossRefresh(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "更新后连续刷新保留限制"
		if rollback {
			name = "刷新授权更新失败时原子回滚"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })

			var hostIDs []int64
			for _, name := range []string{"allowed", "removed"} {
				host, err := st.CreateHost(ctx, Host{
					Name: name, Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password",
				})
				if err != nil {
					t.Fatal(err)
				}
				hostIDs = append(hostIDs, host.ID)
			}
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
				ManageHosts: true, DisabledTools: []string{"memory"}, HostIDs: hostIDs,
				CreatedAt: now, ExpiresAt: expiresAt,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err = st.ExchangeOAuthAuthorizationCode(ctx, OAuthAuthorizationCodeExchange{
				CodeHash: "code", ClientID: client.ClientID, RedirectURI: redirectURI,
				Resource: resource, CodeChallenge: "challenge", GrantID: "grant",
				AccessTokenName: "oauth", AccessTokenHash: "access-0", RefreshTokenHash: "refresh-0",
				Now: now, AccessExpiresAt: expiresAt, RefreshExpiresAt: expiresAt,
			}); err != nil {
				t.Fatal(err)
			}

			// 从认证调用方观察权限，不读取刷新令牌内部存储字段。
			assertPermissions := func(hash, disabled string, manageHosts bool, wantHostIDs []int64) Token {
				t.Helper()
				token, hosts, err := st.FindTokenForResource(ctx, hash, resource)
				if err != nil {
					t.Fatalf("FindTokenForResource(%q): %v", hash, err)
				}
				if token.AllHosts || token.ManageHosts != manageHosts || strings.Join(token.DisabledTools, ",") != disabled {
					t.Fatalf("%s 权限异常: all_hosts=%v manage_hosts=%v disabled_tools=%v",
						hash, token.AllHosts, token.ManageHosts, token.DisabledTools)
				}
				if len(hosts) != len(wantHostIDs) {
					t.Fatalf("%s 主机范围异常: hosts=%v want IDs=%v", hash, hosts, wantHostIDs)
				}
				for i, host := range hosts {
					if host.ID != wantHostIDs[i] {
						t.Fatalf("%s 主机范围异常: hosts=%v want IDs=%v", hash, hosts, wantHostIDs)
					}
				}
				return token
			}
			current := assertPermissions("access-0", "memory", true, hostIDs)

			if rollback {
				// 在 token 和主机关联已更新后注入失败，必须回滚整个事务。
				if _, err = st.DB.ExecContext(ctx, `CREATE TRIGGER fail_refresh_permissions
					BEFORE UPDATE ON oauth_refresh_tokens
					BEGIN SELECT RAISE(ABORT, 'refresh permission update blocked'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, err = st.UpdateToken(ctx, current.ID, TokenUpdate{
				Name: "restricted", DisabledTools: []string{"files", "memory"}, HostIDs: hostIDs[:1],
			})
			wantDisabled, wantManageHosts, wantHostIDs := "files,memory", false, hostIDs[:1]
			if rollback {
				if err == nil || !strings.Contains(err.Error(), "refresh permission update blocked") {
					t.Fatalf("UpdateToken 应返回触发器错误，实际为 %v", err)
				}
				if _, err = st.DB.ExecContext(ctx, `DROP TRIGGER fail_refresh_permissions`); err != nil {
					t.Fatal(err)
				}
				wantDisabled, wantManageHosts, wantHostIDs = "memory", true, hostIDs
			} else if err != nil {
				t.Fatal(err)
			}
			assertPermissions("access-0", wantDisabled, wantManageHosts, wantHostIDs)

			// 第二次刷新验证限制继续传递，而非仅修正下一代 access token。
			for generation := 1; generation <= 2; generation++ {
				accessHash := fmt.Sprintf("access-%d", generation)
				if _, err = st.RotateOAuthRefreshToken(ctx, OAuthRefreshTokenRotation{
					TokenHash: fmt.Sprintf("refresh-%d", generation-1),
					ClientID:  client.ClientID, Resource: resource,
					AccessTokenName: accessHash, AccessTokenHash: accessHash,
					RefreshTokenHash: fmt.Sprintf("refresh-%d", generation),
					Now:              now + int64(generation), AccessExpiresAt: expiresAt, RefreshExpiresAt: expiresAt,
				}); err != nil {
					t.Fatalf("第 %d 次刷新失败: %v", generation, err)
				}
				assertPermissions(accessHash, wantDisabled, wantManageHosts, wantHostIDs)
			}
		})
	}
}
