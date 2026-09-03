package oauthserver

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"onessh/internal/store"
)

type clientRegistrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri"`
}

type authorizationDecision struct {
	Query       string  `json:"query"`
	Decision    string  `json:"decision"`
	AllHosts    bool    `json:"all_hosts"`
	ManageHosts bool    `json:"manage_hosts"`
	HostIDs     []int64 `json:"host_ids"`
}

func (s *Server) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.BaseURL(r)
	metadataJSON(w, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"scopes_supported":                      []string{supportedScope},
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (s *Server) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	resource, _ := s.ResourceURLs(r)
	metadataJSON(w, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{s.BaseURL(r)},
		"scopes_supported":         []string{supportedScope},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "OneSSH MCP",
	})
}

func metadataJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) RegisterClient(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input clientRegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		oauthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "注册信息不是有效 JSON")
		return
	}
	if len(input.RedirectURIs) == 0 || len(input.RedirectURIs) > 10 {
		oauthJSONError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris 必须包含 1 到 10 个地址")
		return
	}
	seen := make(map[string]struct{}, len(input.RedirectURIs))
	redirectURIs := make([]string, 0, len(input.RedirectURIs))
	for _, redirectURI := range input.RedirectURIs {
		if err := validateRedirectURI(redirectURI); err != nil {
			oauthJSONError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
		if _, ok := seen[redirectURI]; ok {
			continue
		}
		seen[redirectURI] = struct{}{}
		redirectURIs = append(redirectURIs, redirectURI)
	}
	if input.TokenEndpointAuthMethod != "" && input.TokenEndpointAuthMethod != "none" {
		oauthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "仅支持公共客户端 token_endpoint_auth_method=none")
		return
	}
	if len(input.GrantTypes) > 0 && !contains(input.GrantTypes, "authorization_code") {
		oauthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_types 必须包含 authorization_code")
		return
	}
	if len(input.ResponseTypes) > 0 && !contains(input.ResponseTypes, "code") {
		oauthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "response_types 必须包含 code")
		return
	}
	input.ClientName = strings.TrimSpace(input.ClientName)
	if input.ClientName == "" {
		input.ClientName = "未命名 MCP 客户端"
	}
	if len(input.ClientName) > 200 {
		oauthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "client_name 过长")
		return
	}
	if err := validateOptionalURI(input.ClientURI); err != nil {
		oauthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "client_uri 无效")
		return
	}
	clientID, err := randomValue("osc_", 24)
	if err != nil {
		log.Printf("OAuth 动态注册失败: 无法生成客户端标识: %v", err)
		oauthJSONError(w, http.StatusInternalServerError, "server_error", "无法生成客户端标识")
		return
	}
	client, err := s.Store.CreateOAuthClient(r.Context(), store.OAuthClient{ClientID: clientID, ClientName: input.ClientName, ClientURI: input.ClientURI, RedirectURIs: redirectURIs})
	if err != nil {
		log.Printf("OAuth 动态注册失败: 无法保存客户端: %v", err)
		oauthJSONError(w, http.StatusInternalServerError, "server_error", "无法保存客户端")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_id":                  client.ClientID,
		"client_id_issued_at":        client.CreatedAt,
		"client_name":                client.ClientName,
		"client_uri":                 client.ClientURI,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

func (s *Server) AuthorizationInfo(w http.ResponseWriter, r *http.Request) {
	request, err := s.parseAuthorizationRequest(r, r.URL.Query())
	if err != nil {
		writeRequestError(w, err)
		return
	}
	hosts, err := s.Store.ListHosts(r.Context())
	if err != nil {
		oauthAPIError(w, http.StatusInternalServerError, "无法读取主机列表")
		return
	}
	hostViews := make([]store.HostView, 0, len(hosts))
	for _, host := range hosts {
		hostViews = append(hostViews, host.View())
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"client_name":      request.Client.ClientName,
		"client_uri":       request.Client.ClientURI,
		"redirect_uri":     request.RedirectURI,
		"requested_scopes": request.Scopes,
		"hosts":            hostViews,
	})
}

func (s *Server) AuthorizationDecision(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input authorizationDecision
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		oauthAPIError(w, http.StatusBadRequest, "授权请求不是有效 JSON")
		return
	}
	values, err := url.ParseQuery(strings.TrimPrefix(input.Query, "?"))
	if err != nil {
		oauthAPIError(w, http.StatusBadRequest, "授权查询参数无效")
		return
	}
	request, err := s.parseAuthorizationRequest(r, values)
	if err != nil {
		writeRequestError(w, err)
		return
	}
	if input.Decision == "deny" {
		jsonResponse(w, http.StatusOK, map[string]string{"redirect_uri": authorizationRedirect(request, "", "access_denied", "管理员拒绝了授权")})
		return
	}
	if input.Decision != "approve" {
		oauthAPIError(w, http.StatusBadRequest, "decision 必须是 approve 或 deny")
		return
	}
	if input.AllHosts {
		input.HostIDs = nil
	} else {
		input.HostIDs, err = s.Store.ValidateHostIDs(r.Context(), input.HostIDs)
		if err != nil {
			oauthAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(input.HostIDs) == 0 && !input.ManageHosts {
			oauthAPIError(w, http.StatusBadRequest, "请至少授权一台主机")
			return
		}
	}
	plainCode, err := randomValue("osc_code_", 32)
	if err != nil {
		oauthAPIError(w, http.StatusInternalServerError, "无法生成授权码")
		return
	}
	if err = s.Store.CreateOAuthAuthorizationCode(r.Context(), store.OAuthAuthorizationCode{
		CodeHash:      store.TokenHash(plainCode),
		ClientID:      request.Client.ClientID,
		RedirectURI:   request.RedirectURI,
		Resource:      request.Resource,
		CodeChallenge: request.CodeChallenge,
		Scope:         request.Scope,
		AllHosts:      input.AllHosts,
		ManageHosts:   input.ManageHosts,
		HostIDs:       input.HostIDs,
		ExpiresAt:     s.now().Add(codeLifetime).Unix(),
	}); err != nil {
		oauthAPIError(w, http.StatusInternalServerError, "无法保存授权码")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"redirect_uri": authorizationRedirect(request, plainCode, "", "")})
}

func authorizationRedirect(request authorizationRequest, code, errorCode, description string) string {
	u, _ := url.Parse(request.RedirectURI)
	query := u.Query()
	if code != "" {
		query.Set("code", code)
	} else {
		query.Set("error", errorCode)
		if description != "" {
			query.Set("error_description", description)
		}
	}
	if request.State != "" {
		query.Set("state", request.State)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func (s *Server) Token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthTokenError(w, "invalid_request", "令牌请求格式无效")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.exchangeAuthorizationCode(w, r)
	case "refresh_token":
		s.refreshAccessToken(w, r)
	default:
		oauthTokenError(w, "unsupported_grant_type", "仅支持 authorization_code 和 refresh_token")
	}
}

func (s *Server) exchangeAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	plainCode := r.PostForm.Get("code")
	verifier := r.PostForm.Get("code_verifier")
	if plainCode == "" || !validCodeVerifier(verifier) {
		oauthTokenError(w, "invalid_grant", "授权码或 PKCE verifier 无效")
		return
	}
	verifierHash := sha256.Sum256([]byte(verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(verifierHash[:])
	resource, err := normalizeResource(r.PostForm.Get("resource"))
	if err != nil {
		oauthTokenError(w, "invalid_grant", "授权码校验失败")
		return
	}
	plainToken, err := randomValue("osh_oauth_", 32)
	if err != nil {
		oauthServerError(w, "authorization_code", "无法生成访问令牌", err)
		return
	}
	plainRefreshToken, err := randomValue("osh_refresh_", 48)
	if err != nil {
		oauthServerError(w, "authorization_code", "无法生成刷新令牌", err)
		return
	}
	grantID, err := randomValue("osg_", 24)
	if err != nil {
		oauthServerError(w, "authorization_code", "无法生成授权标识", err)
		return
	}
	now := s.now()
	code, err := s.Store.ExchangeOAuthAuthorizationCode(r.Context(), store.OAuthAuthorizationCodeExchange{
		CodeHash:         store.TokenHash(plainCode),
		ClientID:         r.PostForm.Get("client_id"),
		RedirectURI:      r.PostForm.Get("redirect_uri"),
		Resource:         resource,
		CodeChallenge:    expectedChallenge,
		GrantID:          grantID,
		AccessTokenName:  fmt.Sprintf("OAuth · %s · %s", r.PostForm.Get("client_id"), plainToken[len(plainToken)-8:]),
		AccessTokenHash:  store.TokenHash(plainToken),
		RefreshTokenHash: store.TokenHash(plainRefreshToken),
		Now:              now.Unix(),
		AccessExpiresAt:  now.Add(tokenLifetime).Unix(),
		RefreshExpiresAt: now.Add(refreshLifetime).Unix(),
	})
	if err != nil {
		writeGrantError(w, "authorization_code", err, "授权码无效、已使用、被撤销或已过期")
		return
	}
	writeTokenResponse(w, plainToken, plainRefreshToken, code.Scope)
}

func (s *Server) refreshAccessToken(w http.ResponseWriter, r *http.Request) {
	plainRefreshToken := r.PostForm.Get("refresh_token")
	resource, err := normalizeResource(r.PostForm.Get("resource"))
	if plainRefreshToken == "" || err != nil {
		oauthTokenError(w, "invalid_grant", "refresh_token 或 resource 无效")
		return
	}
	plainToken, err := randomValue("osh_oauth_", 32)
	if err != nil {
		oauthServerError(w, "refresh_token", "无法生成访问令牌", err)
		return
	}
	plainRotatedRefreshToken, err := randomValue("osh_refresh_", 48)
	if err != nil {
		oauthServerError(w, "refresh_token", "无法生成刷新令牌", err)
		return
	}
	now := s.now()
	refreshToken, err := s.Store.RotateOAuthRefreshToken(r.Context(), store.OAuthRefreshTokenRotation{
		TokenHash:        store.TokenHash(plainRefreshToken),
		ClientID:         r.PostForm.Get("client_id"),
		Resource:         resource,
		AccessTokenName:  fmt.Sprintf("OAuth · %s · %s", r.PostForm.Get("client_id"), plainToken[len(plainToken)-8:]),
		AccessTokenHash:  store.TokenHash(plainToken),
		RefreshTokenHash: store.TokenHash(plainRotatedRefreshToken),
		Now:              now.Unix(),
		AccessExpiresAt:  now.Add(tokenLifetime).Unix(),
		RefreshExpiresAt: now.Add(refreshLifetime).Unix(),
	})
	if err != nil {
		writeGrantError(w, "refresh_token", err, "refresh_token 无效、已使用、被撤销或已过期")
		return
	}
	writeTokenResponse(w, plainToken, plainRotatedRefreshToken, refreshToken.Scope)
}

func writeTokenResponse(w http.ResponseWriter, plainToken, plainRefreshToken, scope string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  plainToken,
		"refresh_token": plainRefreshToken,
		"token_type":    "Bearer",
		"expires_in":    int64(tokenLifetime.Seconds()),
		"scope":         scope,
	})
}

func oauthJSONError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
}

func oauthTokenError(w http.ResponseWriter, code, description string) {
	status := http.StatusBadRequest
	if code == "server_error" {
		status = http.StatusInternalServerError
	}
	oauthJSONError(w, status, code, description)
}

// oauthServerError 记录服务端内部错误并以 500 server_error 返回，
// 让客户端退避重试而不是把瞬时故障判定为凭据永久失效。
func oauthServerError(w http.ResponseWriter, grantType, description string, err error) {
	log.Printf("OAuth 令牌端点内部错误 (grant_type=%s): %s: %v", grantType, description, err)
	oauthTokenError(w, "server_error", description)
}

// writeGrantError 把 store 错误映射为令牌端点响应：
// 协议级拒绝返回 400 invalid_grant，其余按内部错误返回 500 并记录日志。
func writeGrantError(w http.ResponseWriter, grantType string, err error, invalidGrantDescription string) {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrOAuthRefreshReuse) || errors.Is(err, store.ErrOAuthAuthorizationCodeReuse) {
		oauthTokenError(w, "invalid_grant", invalidGrantDescription)
		return
	}
	oauthServerError(w, grantType, "令牌签发失败，请稍后重试", err)
}

func oauthAPIError(w http.ResponseWriter, status int, description string) {
	jsonResponse(w, status, map[string]string{"error": description})
}

func writeRequestError(w http.ResponseWriter, err error) {
	var requestErr requestError
	if errors.As(err, &requestErr) {
		oauthJSONError(w, http.StatusBadRequest, requestErr.Code, requestErr.Description)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		oauthJSONError(w, http.StatusBadRequest, "invalid_request", "客户端未注册")
		return
	}
	oauthJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
