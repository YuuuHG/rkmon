package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type failRec struct {
	n     int
	until time.Time
}

// Auth 提供 Basic Auth、常量时间比较、失败限速/锁定与成功凭据短期缓存。
// 凭据缓存的原因与 Python 版一致：2 万轮派生不应在每个 /api/data 请求上重算。
type Auth struct {
	mu      sync.Mutex
	user    string
	hash    string
	salt    string
	failed  map[string]*failRec
	okCache map[string]time.Time
}

func NewAuth(c *Config) *Auth {
	return &Auth{
		user:    c.User,
		hash:    c.PassHash,
		salt:    c.Salt,
		failed:  make(map[string]*failRec),
		okCache: make(map[string]time.Time),
	}
}

func clientHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *Auth) deny(host string) {
	a.mu.Lock()
	rec := a.failed[host]
	if rec == nil {
		rec = &failRec{}
		a.failed[host] = rec
	}
	rec.n++
	if rec.n >= 5 {
		shift := rec.n - 5
		if shift > 4 {
			shift = 4
		}
		rec.until = time.Now().Add(time.Duration(60*(1<<shift)) * time.Second)
		rec.n = 0
	}
	if len(a.failed) > 1024 {
		a.failed = make(map[string]*failRec)
	}
	a.mu.Unlock()
}

func (a *Auth) allow(host, cacheKey string) {
	a.mu.Lock()
	delete(a.failed, host)
	a.okCache[cacheKey] = time.Now().Add(60 * time.Second)
	if len(a.okCache) > 256 {
		a.okCache = make(map[string]time.Time)
	}
	a.mu.Unlock()
}

func (a *Auth) locked(host string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec := a.failed[host]
	return rec != nil && time.Now().Before(rec.until)
}

func (a *Auth) cached(cacheKey string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.okCache[cacheKey]
	return ok && time.Now().Before(t)
}

func (a *Auth) check(r *http.Request) bool {
	host := clientHost(r)
	if a.locked(host) {
		return false
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Basic ") {
		a.deny(host)
		return false
	}
	sum := sha256.Sum256([]byte(h))
	key := hex.EncodeToString(sum[:])
	if a.cached(key) {
		return true
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, "Basic "))
	if err != nil {
		a.deny(host)
		return false
	}
	pair := strings.SplitN(string(raw), ":", 2)
	if len(pair) != 2 {
		a.deny(host)
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(pair[0]), []byte(a.user)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(derive(pair[1], a.salt)), []byte(a.hash)) == 1
	if !userOK || !passOK {
		a.deny(host)
		return false
	}
	a.allow(host, key)
	return true
}

func (a *Auth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.check(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="rkmon", charset="UTF-8"`)
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
