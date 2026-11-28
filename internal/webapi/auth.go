package webapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	loginMaxFailures = 10
	loginWindow      = 15 * time.Minute
	loginLockout     = 15 * time.Minute
	loginMaxTracked  = 4096
)

type loginAttempt struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
	lastSeen    time.Time
}

type AdminAuth struct {
	password string
	key      []byte
	now      func() time.Time
	mu       sync.Mutex
	attempts map[string]*loginAttempt
	// db 非空即数据库模式：可修改密码、服务端会话与登录记录，见 account.go
	db *sql.DB
}

func NewAdminAuth(password string, masterKey []byte) *AdminAuth {
	// 把管理员密码摘要纳入会话密钥派生：密码轮换重启后，此前签发的所有管理会话
	// cookie 立即失效，无需额外引入服务端会话表。
	pwHash := sha256.Sum256([]byte(password))
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte("onessh-admin-session:"))
	mac.Write(pwHash[:])
	return &AdminAuth{
		password: password,
		key:      mac.Sum(nil),
		now:      time.Now,
		attempts: make(map[string]*loginAttempt),
	}
}

func clientIP(r *http.Request) string {
	// 不信任未经配置校验的 X-Forwarded-For，避免攻击者伪造请求头绕过登录限流。
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (a *AdminAuth) checkRateLimit(ip string, now time.Time) (time.Duration, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.attempts[ip]
	if !ok {
		return 0, true
	}
	if now.Before(entry.lockedUntil) {
		return entry.lockedUntil.Sub(now), false
	}
	if !entry.lockedUntil.IsZero() || now.Sub(entry.windowStart) >= loginWindow {
		delete(a.attempts, ip)
	}
	return 0, true
}

func (a *AdminAuth) recordLoginFailure(ip string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.attempts[ip]
	if !ok {
		if len(a.attempts) >= loginMaxTracked {
			a.evictAttemptsLocked(now)
		}
		entry = &loginAttempt{windowStart: now}
		a.attempts[ip] = entry
	} else if now.Sub(entry.windowStart) >= loginWindow {
		entry.failures = 0
		entry.windowStart = now
		entry.lockedUntil = time.Time{}
	}
	entry.failures++
	entry.lastSeen = now
	if entry.failures >= loginMaxFailures {
		entry.lockedUntil = now.Add(loginLockout)
	}
}

func (a *AdminAuth) clearLoginFailures(ip string) {
	a.mu.Lock()
	delete(a.attempts, ip)
	a.mu.Unlock()
}

func (a *AdminAuth) evictAttemptsLocked(now time.Time) {
	for ip, entry := range a.attempts {
		if now.Before(entry.lockedUntil) {
			continue
		}
		if !entry.lockedUntil.IsZero() || now.Sub(entry.windowStart) >= loginWindow {
			delete(a.attempts, ip)
		}
	}
	if len(a.attempts) < loginMaxTracked {
		return
	}
	var oldestIP string
	var oldestTime time.Time
	for ip, entry := range a.attempts {
		if oldestIP == "" || entry.lastSeen.Before(oldestTime) {
			oldestIP = ip
			oldestTime = entry.lastSeen
		}
	}
	if oldestIP != "" {
		delete(a.attempts, oldestIP)
	}
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwardedProto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwardedProto, "https")
}
