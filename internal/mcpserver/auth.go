package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"onessh/internal/store"
)

type principalKey struct{}
type Principal struct {
	Token store.Token
	Hosts map[string]store.Host
	Store *store.Store
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
		ctx := context.WithValue(r.Context(), principalKey{}, Principal{Token: token, Hosts: allowed, Store: st})
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
		if _, err := p.Store.GetHostByName(ctx, name); errors.Is(err, sql.ErrNoRows) {
			return store.Host{}, toolError("unknown host: " + name)
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
