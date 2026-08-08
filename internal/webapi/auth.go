package webapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type AdminAuth struct {
	password string
	key      []byte
}

func NewAdminAuth(password string, masterKey []byte) *AdminAuth {
	sum := sha256.Sum256(append(append([]byte{}, masterKey...), []byte("onessh-admin-session")...))
	return &AdminAuth{password: password, key: sum[:]}
}
func (a *AdminAuth) Login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || subtle.ConstantTimeCompare([]byte(in.Password), []byte(a.password)) != 1 {
		http.Error(w, "密码错误", http.StatusUnauthorized)
		return
	}
	exp := time.Now().Add(24 * time.Hour).Unix()
	msg := strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(msg))
	value := msg + "." + hex.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{Name: "onessh_session", Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 86400})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
func (a *AdminAuth) valid(r *http.Request) bool {
	c, err := r.Cookie("onessh_session")
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	got, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(parts[0]))
	return hmac.Equal(got, mac.Sum(nil))
}
func (a *AdminAuth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.valid(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
