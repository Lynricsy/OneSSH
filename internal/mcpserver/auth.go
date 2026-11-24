package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"onessh/internal/cmdpolicy"
	"onessh/internal/store"
)

type principalKey struct{}
type Principal struct {
	Token store.Token
	Hosts map[string]store.Host
	Store *store.Store
	// commandPolicy 是令牌策略在本次请求内的编译缓存：Bearer 中间件装载时编译一次，
	// policyActive / commandDenial / envDenial 直接复用，不再各自重复编译同一批正则。
	// nil 表示未经中间件装载（测试手工构造的 Principal），调用方退回现场编译。
	commandPolicy *cmdpolicy.Policy
}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

type ResourceResolver func(*http.Request) (resource, metadataURL string)

func Bearer(st *store.Store, resolve ResourceResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resource, metadataURL := resolve(r)
		authorization := strings.Fields(r.Header.Get("Authorization"))
		if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") {
			bearerUnauthorized(w, metadataURL, "")
			return
		}
		token, hosts, err := st.FindTokenForResource(r.Context(), store.TokenHash(authorization[1]), resource)
		if err != nil {
			bearerUnauthorized(w, metadataURL, "invalid_token")
			return
		}
		allowed := make(map[string]store.Host, len(hosts))
		for _, h := range hosts {
			allowed[h.Name] = h
		}
		// 令牌策略在装载时编译一次，整次请求复用；库里解析不出来按 fail-closed 全拒，
		// 与 policyLayers 的语义一致。
		policy, err := token.CommandPolicy.Compile()
		if err != nil {
			policy = cmdpolicy.DenyAll()
		}
		ctx := context.WithValue(r.Context(), principalKey{}, Principal{Token: token, Hosts: allowed, Store: st, commandPolicy: &policy})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerUnauthorized(w http.ResponseWriter, metadataURL, errorCode string) {
	challenge := fmt.Sprintf(`Bearer resource_metadata=%q, scope="mcp"`, metadataURL)
	if errorCode != "" {
		challenge += fmt.Sprintf(`, error=%q`, errorCode)
	}
	w.Header().Set("WWW-Authenticate", challenge)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
func AuthorizedHost(ctx context.Context, name string) (store.Host, error) {
	p, ok := FromContext(ctx)
	if !ok {
		return store.Host{}, toolError("unauthorized")
	}
	if h, ok := p.Hosts[name]; ok {
		return h, nil
	}
	if p.Store != nil {
		h, err := p.Store.GetHostByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			return store.Host{}, toolError("unknown host: " + name)
		}
		if err == nil && h.ArchivedAt.Valid {
			return store.Host{}, toolError("host archived: " + name + "（主机已归档，需管理员在控制台取消归档）")
		}
	}
	return store.Host{}, toolError("host not authorized: " + name)
}

func AuthorizedHostManagement(ctx context.Context) (Principal, error) {
	p, ok := FromContext(ctx)
	if !ok {
		return Principal{}, toolError("unauthorized")
	}
	if !p.Token.ManageHosts {
		return Principal{}, toolError("host management not authorized")
	}
	return p, nil
}

type toolError string

func (e toolError) Error() string { return string(e) }
