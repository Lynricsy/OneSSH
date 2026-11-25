package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/store"
)

func TestBearerAttachesPrincipal(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.CreateToken(context.Background(), store.TokenCreate{Name: "agent", Hash: store.TokenHash("secret"), AllHosts: true}); err != nil {
		t.Fatal(err)
	}
	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok || p.Token.Name != "agent" {
			t.Fatal("缺少 principal")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	Bearer(st, resolve, next).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("状态码 %d", rr.Code)
	}
	bad := httptest.NewRecorder()
	Bearer(st, resolve, next).ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("未授权状态码 %d", bad.Code)
	}
	if challenge := bad.Header().Get("WWW-Authenticate"); challenge != `Bearer resource_metadata="https://onessh.example/.well-known/oauth-protected-resource/mcp", scope="mcp"` {
		t.Fatalf("鉴权挑战异常: %s", challenge)
	}
}

func TestAuthorizedHostPreventsHostEnumeration(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err = st.CreateHost(ctx, store.Host{Name: "known", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password"}); err != nil {
		t.Fatal(err)
	}
	archivedDenied, err := st.CreateHost(ctx, store.Host{Name: "archived-denied", Addr: "127.0.0.2", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	archivedAllowed, err := st.CreateHost(ctx, store.Host{Name: "archived-allowed", Addr: "127.0.0.3", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "scoped", Hash: store.TokenHash("scoped-secret"), AllHosts: false,
		HostIDs: []int64{archivedAllowed.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ArchiveHost(ctx, archivedDenied.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err = st.ArchiveHost(ctx, archivedAllowed.ID, 100); err != nil {
		t.Fatal(err)
	}

	scopedCtx := context.WithValue(ctx, principalKey{}, Principal{Token: tok, Hosts: map[string]store.Host{}, Store: st})
	// 未知主机、未授权活跃主机、未授权已归档主机必须返回同一文案，不能充当主机名枚举 oracle。
	for _, name := range []string{"missing", "known", "archived-denied"} {
		if _, err = AuthorizedHost(scopedCtx, name); err == nil || err.Error() != "host not authorized: "+name {
			t.Fatalf("%s 错误 = %v，期望统一返回 host not authorized", name, err)
		}
	}
	// 本令牌已授权但已归档的主机仍保留明确归档提示。
	if _, err = AuthorizedHost(scopedCtx, "archived-allowed"); err == nil || !strings.HasPrefix(err.Error(), "host archived: archived-allowed") {
		t.Fatalf("已授权归档主机错误 = %v，期望 host archived", err)
	}
}

func TestOutputReadEnforcesTokenOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := newTestServer(t, Options{MCPApps: true, SearchHelper: true})
	st := server.Store
	ownerTok, err := st.CreateToken(ctx, store.TokenCreate{Name: "owner", Hash: store.TokenHash("secret-owner"), AllHosts: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateToken(ctx, store.TokenCreate{Name: "other", Hash: store.TokenHash("secret-other"), AllHosts: true}); err != nil {
		t.Fatal(err)
	}

	id := uuid.NewString()
	logPath, err := server.Exec.ArtifactPath(id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(logPath, []byte("top-secret-output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = server.Exec.WriteArtifactOwner(id, ownerTok.ID); err != nil {
		t.Fatal(err)
	}

	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	t.Cleanup(httpServer.Close)

	connect := func(secret string) *mcp.ClientSession {
		t.Helper()
		session, err := mcp.NewClient(&mcp.Implementation{Name: "artifact-owner-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   httpServer.URL,
			HTTPClient: &http.Client{Transport: bearerTransport{token: secret}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}

	okRes, err := connect("secret-owner").CallTool(ctx, &mcp.CallToolParams{
		Name:      "output_read",
		Arguments: map[string]any{"artifact_id": id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if okRes.IsError || !strings.Contains(toolText(okRes), "top-secret-output") {
		t.Fatalf("归属令牌应可读取自己的 artifact，实际 %#v", okRes)
	}

	deniedRes, err := connect("secret-other").CallTool(ctx, &mcp.CallToolParams{
		Name:      "output_read",
		Arguments: map[string]any{"artifact_id": id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deniedRes.IsError || !strings.Contains(toolText(deniedRes), "artifact 已过期或不存在") {
		t.Fatalf("跨令牌读取 artifact 应被拒绝，实际 %#v", deniedRes)
	}
}

func TestAuthorizedHostManagementDoesNotExpandExecutionHosts(t *testing.T) {
	if _, err := AuthorizedHostManagement(context.Background()); err == nil || err.Error() != "unauthorized" {
		t.Fatalf("缺少身份错误: %v", err)
	}
	denied := context.WithValue(context.Background(), principalKey{}, Principal{Token: store.Token{ManageHosts: false}})
	if _, err := AuthorizedHostManagement(denied); err == nil || err.Error() != "host management not authorized" {
		t.Fatalf("缺少管理权限错误: %v", err)
	}
	ctx := context.WithValue(context.Background(), principalKey{}, Principal{
		Token: store.Token{ManageHosts: true},
		Hosts: map[string]store.Host{},
	})
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		t.Fatalf("管理权限被拒绝: %v", err)
	}
	if _, err := AuthorizedHost(ctx, "any"); err == nil || err.Error() != "host not authorized: any" {
		t.Fatalf("管理权限扩张了执行授权: %v", err)
	}
}
