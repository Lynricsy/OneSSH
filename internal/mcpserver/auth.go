package mcpserver

import (
	"context"
	"database/sql"
	"errors"
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
func Bearer(st *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if raw == "" || raw == r.Header.Get("Authorization") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		token, hosts, err := st.FindToken(r.Context(), store.TokenHash(raw))
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
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
