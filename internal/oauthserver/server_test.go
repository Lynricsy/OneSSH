package oauthserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"onessh/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	server, err := New(st, "http://localhost:8866")
	if err != nil {
		t.Fatal(err)
	}
	return server, st
}

func TestOAuthAuthorizationCodeFlowPreservesPermissionsAndAudience(t *testing.T) {
	server, st := newTestServer(t)
	ctx := context.Background()
	host, err := st.CreateHost(ctx, store.Host{Name: "build", Addr: "127.0.0.1", Port: 22, Username: "runner", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}

	registration := `{"client_name":"Claude Code","redirect_uris":["http://127.0.0.1:39123/callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code"],"response_types":["code"]}`
	registerRequest := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(registration))
	registerResponse := httptest.NewRecorder()
	server.RegisterClient(registerResponse, registerRequest)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("注册状态码 %d: %s", registerResponse.Code, registerResponse.Body.String())
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if err = json.NewDecoder(registerResponse.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}

	verifier := strings.Repeat("v", 64)
	challengeHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {registered.ClientID},
		"redirect_uri":          {"http://127.0.0.1:39123/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {"http://localhost:8866/mcp"},
		"scope":                 {"mcp"},
		"state":                 {"state-123"},
	}
	infoRequest := httptest.NewRequest(http.MethodGet, "/api/v1/oauth/authorization?"+params.Encode(), nil)
	infoResponse := httptest.NewRecorder()
	server.AuthorizationInfo(infoResponse, infoRequest)
	if infoResponse.Code != http.StatusOK {
		t.Fatalf("授权信息状态码 %d: %s", infoResponse.Code, infoResponse.Body.String())
	}

	decisionBody := `{"query":` + quoted("?"+params.Encode()) + `,"decision":"approve","all_hosts":false,"manage_hosts":true,"host_ids":[` + fmtInt(host.ID) + `]}`
	decisionRequest := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/authorization", strings.NewReader(decisionBody))
	decisionResponse := httptest.NewRecorder()
	server.AuthorizationDecision(decisionResponse, decisionRequest)
	if decisionResponse.Code != http.StatusOK {
		t.Fatalf("授权状态码 %d: %s", decisionResponse.Code, decisionResponse.Body.String())
	}
	var decision struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if err = json.NewDecoder(decisionResponse.Body).Decode(&decision); err != nil {
		t.Fatal(err)
	}
	callback, err := url.Parse(decision.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	if callback.Query().Get("state") != "state-123" || callback.Query().Get("code") == "" {
		t.Fatalf("回调参数异常: %s", decision.RedirectURI)
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {registered.ClientID},
		"redirect_uri":  {"http://127.0.0.1:39123/callback"},
		"code":          {callback.Query().Get("code")},
		"code_verifier": {verifier},
		"resource":      {"http://localhost:8866/mcp"},
	}
	tokenRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResponse := httptest.NewRecorder()
	server.Token(tokenResponse, tokenRequest)
	if tokenResponse.Code != http.StatusOK {
		t.Fatalf("令牌状态码 %d: %s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var tokenBody struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err = json.NewDecoder(tokenResponse.Body).Decode(&tokenBody); err != nil {
		t.Fatal(err)
	}
	if tokenBody.ExpiresIn != int64(time.Hour.Seconds()) || tokenBody.Scope != "mcp" || tokenBody.RefreshToken == "" {
		t.Fatalf("令牌响应异常: %#v", tokenBody)
	}
	stored, hosts, err := st.FindTokenForResource(ctx, store.TokenHash(tokenBody.AccessToken), "http://localhost:8866/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Source != "oauth" || !stored.ManageHosts || stored.AllHosts || len(hosts) != 1 || hosts[0].Name != "build" {
		t.Fatalf("OAuth 权限未保留: token=%#v hosts=%#v", stored, hosts)
	}
	if _, _, err = st.FindTokenForResource(ctx, store.TokenHash(tokenBody.AccessToken), "http://localhost:8866/other"); err == nil {
		t.Fatal("OAuth 令牌被错误资源接受")
	}

	refreshForm := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {registered.ClientID},
		"refresh_token": {tokenBody.RefreshToken},
		"resource":      {"http://localhost:8866/mcp"},
	}
	refreshRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(refreshForm.Encode()))
	refreshRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshResponse := httptest.NewRecorder()
	server.Token(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("刷新状态码 %d: %s", refreshResponse.Code, refreshResponse.Body.String())
	}
	var refreshed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err = json.NewDecoder(refreshResponse.Body).Decode(&refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken == "" || refreshed.RefreshToken == "" || refreshed.RefreshToken == tokenBody.RefreshToken {
		t.Fatalf("刷新令牌未轮换: %#v", refreshed)
	}
	replayRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(refreshForm.Encode()))
	replayRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	replayResponse := httptest.NewRecorder()
	server.Token(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusBadRequest {
		t.Fatalf("旧刷新令牌被重复使用: %d %s", replayResponse.Code, replayResponse.Body.String())
	}
	if _, _, err = st.FindTokenForResource(ctx, store.TokenHash(refreshed.AccessToken), "http://localhost:8866/mcp"); err == nil {
		t.Fatal("刷新令牌重放后活动 access token 仍有效")
	}
	revokedForm := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {registered.ClientID},
		"refresh_token": {refreshed.RefreshToken},
		"resource":      {"http://localhost:8866/mcp"},
	}
	revokedRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(revokedForm.Encode()))
	revokedRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	revokedResponse := httptest.NewRecorder()
	server.Token(revokedResponse, revokedRequest)
	if revokedResponse.Code != http.StatusBadRequest {
		t.Fatalf("重放后活动 refresh token 仍有效: %d %s", revokedResponse.Code, revokedResponse.Body.String())
	}
}

// seedRefreshGrant 用调用方给定的刷新令牌明文建立一个受限于单台主机的 OAuth 授权。
// 受限授权（AllHosts=false）会让轮换事务同时写入 tokens 与 token_hosts，覆盖完整的写入路径。
func seedRefreshGrant(t *testing.T, st *store.Store, clientID, resource, plainRefreshToken string) {
	t.Helper()
	ctx := context.Background()
	host, err := st.CreateHost(ctx, store.Host{Name: "rotation-host", Addr: "127.0.0.1", Port: 22, Username: "runner", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateOAuthClient(ctx, store.OAuthClient{
		ClientID:     clientID,
		ClientName:   "Rotation test",
		RedirectURIs: []string{"http://localhost/callback"},
	}); err != nil {
		t.Fatal(err)
	}
	accessToken, err := st.CreateToken(ctx, store.TokenCreate{
		Name:      "OAuth rotation seed",
		Hash:      "seed-access-hash",
		HostIDs:   []int64{host.ID},
		Source:    "oauth",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Resource:  resource,
		ClientID:  clientID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateOAuthRefreshToken(ctx, store.OAuthRefreshToken{
		TokenHash:     store.TokenHash(plainRefreshToken),
		GrantID:       "rotation-grant",
		AccessTokenID: accessToken.ID,
		ClientID:      clientID,
		Resource:      resource,
		Scope:         "mcp",
		HostIDs:       []int64{host.ID},
		ExpiresAt:     time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
}

// postRefresh 用给定刷新令牌调用 /oauth/token 的 refresh_token 授权。
func postRefresh(t *testing.T, server *Server, clientID, resource, plainRefreshToken string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {plainRefreshToken},
		"resource":      {resource},
	}
	request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	server.Token(response, request)
	return response
}

func countRows(t *testing.T, st *store.Store, query string, args ...any) int {
	t.Helper()
	var count int
	if err := st.DB.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestOAuthRefreshFailureRollsBackRotation 覆盖 issue #18 的验收条件 1 与 2：
// 轮换事务中任意一步写入失败都必须整体回滚，旧刷新令牌保持可用、不留下孤儿访问令牌，
// 且故障消失后客户端用同一枚旧令牌重试即可自愈。
func TestOAuthRefreshFailureRollsBackRotation(t *testing.T) {
	const (
		clientID          = "rotation-client"
		resource          = "http://localhost:8866/mcp"
		plainRefreshToken = "osh_refresh_retryable"
	)
	// RAISE(FAIL) 只中止当前语句并保持事务打开，由 Go 侧 defer tx.Rollback() 整体回滚；
	// 换成 RAISE(ROLLBACK) 会让 SQLite 自行结束事务，从而掩盖被测行为。
	cases := []struct {
		name    string
		trigger string
		drop    string
	}{
		{
			name:    "刷新令牌写入失败",
			trigger: `CREATE TRIGGER fail_oauth_refresh_insert BEFORE INSERT ON oauth_refresh_tokens BEGIN SELECT RAISE(FAIL, 'forced refresh failure'); END`,
			drop:    `DROP TRIGGER fail_oauth_refresh_insert`,
		},
		{
			name:    "访问令牌写入失败",
			trigger: `CREATE TRIGGER fail_oauth_access_insert BEFORE INSERT ON tokens WHEN NEW.source='oauth' BEGIN SELECT RAISE(FAIL, 'forced access failure'); END`,
			drop:    `DROP TRIGGER fail_oauth_access_insert`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, st := newTestServer(t)
			ctx := context.Background()
			seedRefreshGrant(t, st, clientID, resource, plainRefreshToken)
			if _, err := st.DB.ExecContext(ctx, tc.trigger); err != nil {
				t.Fatal(err)
			}

			failed := postRefresh(t, server, clientID, resource, plainRefreshToken)
			if failed.Code != http.StatusInternalServerError || !strings.Contains(failed.Body.String(), `"server_error"`) {
				t.Fatalf("轮换写入失败未返回可重试服务端错误: %d %s", failed.Code, failed.Body.String())
			}
			if active := countRows(t, st, `SELECT count(*) FROM oauth_refresh_tokens WHERE token_hash=? AND used_at IS NULL AND revoked_at IS NULL`, store.TokenHash(plainRefreshToken)); active != 1 {
				t.Fatalf("失败轮换烧毁了旧刷新令牌: 可用刷新令牌=%d", active)
			}
			if orphans := countRows(t, st, `SELECT count(*) FROM tokens t WHERE t.source='oauth' AND NOT EXISTS (SELECT 1 FROM oauth_refresh_tokens r WHERE r.access_token_id=t.id)`); orphans != 0 {
				t.Fatalf("失败轮换残留孤儿访问令牌: %d", orphans)
			}
			if issued := countRows(t, st, `SELECT count(*) FROM tokens WHERE source='oauth'`); issued != 1 {
				t.Fatalf("失败轮换未回滚访问令牌写入: oauth 令牌=%d", issued)
			}

			if _, err := st.DB.ExecContext(ctx, tc.drop); err != nil {
				t.Fatal(err)
			}
			retry := postRefresh(t, server, clientID, resource, plainRefreshToken)
			if retry.Code != http.StatusOK {
				t.Fatalf("旧 refresh token 无法重试: %d %s", retry.Code, retry.Body.String())
			}
			var rotated struct {
				RefreshToken string `json:"refresh_token"`
			}
			if err := json.NewDecoder(retry.Body).Decode(&rotated); err != nil {
				t.Fatal(err)
			}
			if rotated.RefreshToken == "" || rotated.RefreshToken == plainRefreshToken {
				t.Fatalf("重试未轮换出新的刷新令牌: %q", rotated.RefreshToken)
			}
			replay := postRefresh(t, server, clientID, resource, plainRefreshToken)
			if replay.Code != http.StatusBadRequest {
				t.Fatalf("成功轮换后旧刷新令牌仍可用: %d %s", replay.Code, replay.Body.String())
			}
		})
	}
}

// TestOAuthRefreshRotationSurvivesConcurrentMetricWrites 覆盖 issue #18 的验收条件 3：
// 监控采样持续并发写入 metrics 时，刷新令牌轮换必须始终能推进。
//
// 这里不断言「每次都返回 200」：SQLite 的 busy handler 并不公平，
// 持续的写入流可能让等待中的写事务耗尽 busy_timeout，这与本次修复无关，
// 且修复前后都存在；断言「零失败」会让本用例在 CI 负载下随机抖动。
// 真正的验收点是轮换的原子性——写事务失败必须整体回滚，旧刷新令牌保持可用，
// 客户端重试即可自愈，而不是像修复前那样被永久烧毁。
//
// BEGIN IMMEDIATE 本身由 store 包的 TestBeginTxTakesWriteLockBeforeFirstRead
// 确定性覆盖，不依赖此处的并发时序。
func TestOAuthRefreshRotationSurvivesConcurrentMetricWrites(t *testing.T) {
	const (
		clientID          = "concurrent-client"
		resource          = "http://localhost:8866/mcp"
		plainRefreshToken = "osh_refresh_concurrent"
		rotations         = 30
		// 仅作为活性上限，防止「永远无法推进」时死循环。
		// 修复后 30 次轮换通常 0~1 次重试，这里留出充裕余量以免 CI 负载下抖动。
		maxRetries = 60
	)
	server, st := newTestServer(t)
	seedRefreshGrant(t, st, clientID, resource, plainRefreshToken)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 4)
	var wg sync.WaitGroup
	// 与 monitor 的 maxConcurrentSamples 同量级：多个采样 goroutine 持续写入 metrics。
	// 每次写入之间留出间隔，真实监控每轮 60s、单轮最多 5 个 worker。
	for worker := 1; worker <= 4; worker++ {
		wg.Add(1)
		go func(hostID int64) {
			defer wg.Done()
			for ts := int64(1); ctx.Err() == nil; ts++ {
				if err := st.AddMetric(ctx, store.Metric{HostID: hostID, Ts: ts}); err != nil && ctx.Err() == nil {
					errCh <- err
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(int64(worker))
	}

	fatal := func(format string, args ...any) {
		t.Helper()
		cancel()
		wg.Wait()
		t.Fatalf(format, args...)
	}

	current := plainRefreshToken
	retries := 0
	for i := 0; i < rotations; i++ {
		response := postRefresh(t, server, clientID, resource, current)
		for response.Code == http.StatusInternalServerError {
			// 写冲突必须整体回滚：旧刷新令牌仍未被消费，因此可以原样重试。
			if active := countRows(t, st, `SELECT count(*) FROM oauth_refresh_tokens WHERE token_hash=? AND used_at IS NULL AND revoked_at IS NULL`, store.TokenHash(current)); active != 1 {
				fatal("第 %d 次轮换失败后旧刷新令牌被烧毁: 可用刷新令牌=%d", i+1, active)
			}
			if retries++; retries > maxRetries {
				fatal("并发写入下轮换重试次数超出上限: %d", retries)
			}
			response = postRefresh(t, server, clientID, resource, current)
		}
		if response.Code != http.StatusOK {
			fatal("第 %d 次轮换在并发写入下失败: %d %s", i+1, response.Code, response.Body.String())
		}
		var rotated struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(response.Body).Decode(&rotated); err != nil {
			fatal("解析轮换响应失败: %v", err)
		}
		if rotated.RefreshToken == "" || rotated.RefreshToken == current {
			fatal("第 %d 次轮换未换出新的刷新令牌", i+1)
		}
		current = rotated.RefreshToken
	}
	if orphans := countRows(t, st, `SELECT count(*) FROM tokens t WHERE t.source='oauth' AND NOT EXISTS (SELECT 1 FROM oauth_refresh_tokens r WHERE r.access_token_id=t.id)`); orphans != 0 {
		fatal("并发轮换残留孤儿访问令牌: %d", orphans)
	}
	cancel()
	wg.Wait()
	close(errCh)
	if err := <-errCh; err != nil {
		t.Fatalf("并发 metrics 写入失败: %v", err)
	}
	if retries > 0 {
		t.Logf("并发写入下有 %d 次轮换遇到写冲突并成功重试", retries)
	}
}

func TestOAuthRejectsUnsafeRedirectAndInvalidPKCE(t *testing.T) {
	server, _ := newTestServer(t)
	unsafe := httptest.NewRecorder()
	server.RegisterClient(unsafe, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{"client_name":"bad","redirect_uris":["http://example.com/callback"],"token_endpoint_auth_method":"none"}`)))
	if unsafe.Code != http.StatusBadRequest {
		t.Fatalf("不安全回调状态码 %d", unsafe.Code)
	}

	client, err := server.Store.CreateOAuthClient(context.Background(), store.OAuthClient{ClientID: "client", ClientName: "client", RedirectURIs: []string{"http://localhost:3000/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = client
	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {"client"},
		"redirect_uri":          {"http://localhost:3000/callback"},
		"code_challenge":        {"not-a-sha256-challenge"},
		"code_challenge_method": {"plain"},
		"resource":              {"http://localhost:8866/mcp"},
	}
	response := httptest.NewRecorder()
	server.AuthorizationInfo(response, httptest.NewRequest(http.MethodGet, "/api/v1/oauth/authorization?"+values.Encode(), nil))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "S256") {
		t.Fatalf("错误 PKCE 未被拒绝: %d %s", response.Code, response.Body.String())
	}
}

func TestOAuthAuthorizationCodeInvalidAttemptDoesNotConsumeAndReplayRevokesGrant(t *testing.T) {
	server, st := newTestServer(t)
	ctx := context.Background()
	const (
		clientID    = "replay-client"
		redirectURI = "http://localhost:3000/callback"
		resource    = "http://localhost:8866/mcp"
		plainCode   = "osh_code_replay_test"
	)
	if _, err := st.CreateOAuthClient(ctx, store.OAuthClient{
		ClientID:     clientID,
		ClientName:   "Replay test",
		RedirectURIs: []string{redirectURI},
	}); err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 64)
	challengeHash := sha256.Sum256([]byte(verifier))
	if err := st.CreateOAuthAuthorizationCode(ctx, store.OAuthAuthorizationCode{
		CodeHash:      store.TokenHash(plainCode),
		ClientID:      clientID,
		RedirectURI:   redirectURI,
		Resource:      resource,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(challengeHash[:]),
		Scope:         "mcp",
		AllHosts:      true,
		ExpiresAt:     time.Now().Add(time.Minute).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"client_id":    {clientID},
		"redirect_uri": {redirectURI},
		"code":         {plainCode},
		"resource":     {resource},
	}
	exchange := func(verifier string) *httptest.ResponseRecorder {
		t.Helper()
		form.Set("code_verifier", verifier)
		request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		server.Token(response, request)
		return response
	}

	invalid := exchange(strings.Repeat("x", 64))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("错误 verifier 状态码 %d: %s", invalid.Code, invalid.Body.String())
	}
	success := exchange(verifier)
	if success.Code != http.StatusOK {
		t.Fatalf("错误请求消耗了授权码: %d %s", success.Code, success.Body.String())
	}
	var issued struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(success.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	replay := exchange(verifier)
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("授权码重放状态码 %d: %s", replay.Code, replay.Body.String())
	}
	if _, _, err := st.FindTokenForResource(ctx, store.TokenHash(issued.AccessToken), resource); err == nil {
		t.Fatal("授权码重放后关联 access token 仍有效")
	}
	refreshForm := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {issued.RefreshToken},
		"resource":      {resource},
	}
	refreshRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(refreshForm.Encode()))
	refreshRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshResponse := httptest.NewRecorder()
	server.Token(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusBadRequest {
		t.Fatalf("授权码重放后关联 refresh token 仍有效: %d %s", refreshResponse.Code, refreshResponse.Body.String())
	}
}

func TestOAuthMetadataAdvertisesMCPDiscovery(t *testing.T) {
	server, _ := newTestServer(t)
	asResponse := httptest.NewRecorder()
	server.AuthorizationServerMetadata(asResponse, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	if !strings.Contains(asResponse.Body.String(), `"code_challenge_methods_supported":["S256"]`) || !strings.Contains(asResponse.Body.String(), `"registration_endpoint"`) {
		t.Fatalf("授权服务器元数据不完整: %s", asResponse.Body.String())
	}
	resourceResponse := httptest.NewRecorder()
	server.ProtectedResourceMetadata(resourceResponse, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil))
	if !strings.Contains(resourceResponse.Body.String(), `"resource":"http://localhost:8866/mcp"`) || !strings.Contains(resourceResponse.Body.String(), `"authorization_servers":["http://localhost:8866"]`) {
		t.Fatalf("资源元数据不完整: %s", resourceResponse.Body.String())
	}
}

func quoted(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func fmtInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
