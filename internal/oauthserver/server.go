package oauthserver

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"onessh/internal/store"
)

const (
	supportedScope  = "mcp"
	codeLifetime    = 5 * time.Minute
	tokenLifetime   = time.Hour
	refreshLifetime = 30 * 24 * time.Hour
)

type Server struct {
	Store     *store.Store
	PublicURL string
	now       func() time.Time
}

type authorizationRequest struct {
	Client        store.OAuthClient
	RedirectURI   string
	Resource      string
	Scope         string
	Scopes        []string
	State         string
	CodeChallenge string
}

func New(st *store.Store, publicURL string) (*Server, error) {
	publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if publicURL != "" {
		u, err := url.Parse(publicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("ONESSH_PUBLIC_URL 必须是无路径、查询和片段的 http(s) 来源地址")
		}
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.Path = ""
		publicURL = u.String()
	}
	return &Server{Store: st, PublicURL: publicURL, now: time.Now}, nil
}

func (s *Server) BaseURL(r *http.Request) string {
	if s.PublicURL != "" {
		return s.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return scheme + "://" + strings.ToLower(r.Host)
}

func (s *Server) ResourceURLs(r *http.Request) (resource, metadataURL string) {
	base := s.BaseURL(r)
	return base + "/mcp", base + "/.well-known/oauth-protected-resource/mcp"
}

func (s *Server) parseAuthorizationRequest(r *http.Request, values url.Values) (authorizationRequest, error) {
	if values.Get("response_type") != "code" {
		return authorizationRequest{}, oauthRequestError("unsupported_response_type", "仅支持 authorization_code 流程")
	}
	clientID := values.Get("client_id")
	client, err := s.Store.GetOAuthClient(r.Context(), clientID)
	if err != nil {
		return authorizationRequest{}, oauthRequestError("invalid_request", "客户端未注册")
	}
	redirectURI := values.Get("redirect_uri")
	if !contains(client.RedirectURIs, redirectURI) {
		return authorizationRequest{}, oauthRequestError("invalid_request", "redirect_uri 与客户端注册信息不匹配")
	}
	challenge := values.Get("code_challenge")
	decodedChallenge, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil || len(decodedChallenge) != 32 || values.Get("code_challenge_method") != "S256" {
		return authorizationRequest{}, oauthRequestError("invalid_request", "必须使用 S256 PKCE")
	}
	resource, _ := s.ResourceURLs(r)
	requestedResource, err := normalizeResource(values.Get("resource"))
	if err != nil || requestedResource != resource {
		return authorizationRequest{}, oauthRequestError("invalid_target", "resource 必须指向当前 OneSSH MCP 端点")
	}
	scopes, err := parseScopes(values.Get("scope"))
	if err != nil {
		return authorizationRequest{}, err
	}
	return authorizationRequest{
		Client:        client,
		RedirectURI:   redirectURI,
		Resource:      resource,
		Scope:         strings.Join(scopes, " "),
		Scopes:        scopes,
		State:         values.Get("state"),
		CodeChallenge: challenge,
	}, nil
}

func parseScopes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{supportedScope}, nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 1)
	for _, scope := range strings.Fields(raw) {
		if scope != supportedScope {
			return nil, oauthRequestError("invalid_scope", fmt.Sprintf("不支持 scope %q", scope))
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out, nil
}

func normalizeResource(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return "", errors.New("resource 无效")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

func validateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("redirect_uri 必须是绝对 URL 且不能包含片段")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme != "http" {
		return errors.New("redirect_uri 必须使用 HTTPS，localhost 可使用 HTTP")
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("HTTP redirect_uri 仅允许 localhost 或回环地址")
	}
	return nil
}

func validateOptionalURI(raw string) error {
	if raw == "" {
		return nil
	}
	return validateRedirectURI(raw)
}
func validCodeVerifier(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("-._~", char) {
			continue
		}
		return false
	}
	return true
}

func randomValue(prefix string, size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type requestError struct {
	Code        string
	Description string
}

func (e requestError) Error() string { return e.Description }

func oauthRequestError(code, description string) error {
	return requestError{Code: code, Description: description}
}
