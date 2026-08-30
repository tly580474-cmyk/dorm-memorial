package httpapi

import (
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Media GETs can prepare missing renditions. Do not allow another origin to
// trigger that work with a member's browser cookies, including sibling sites.
// Address-bar navigation (none) and non-browser clients remain supported.
func crossOriginMediaRead(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "":
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false
		}
		parsed, err := url.Parse(origin)
		return err != nil || parsed.Host != r.Host
	default:
		return true
	}
}

// Bound both password-guessing attempts and bcrypt work. Only the TCP peer is
// trusted; accepting arbitrary forwarded headers would let clients bypass it.
const (
	authAttemptLimit  = 20
	authAttemptWindow = 5 * time.Minute
	authPeerLimit     = 4096
)

type authAttempts struct {
	count   int
	expires time.Time
}

type authLimiter struct {
	mu    sync.Mutex
	peers map[string]authAttempts
	slots chan struct{}
}

func (l *authLimiter) allow(peer string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.peers == nil {
		l.peers = make(map[string]authAttempts)
	}
	entry, exists := l.peers[peer]
	if exists && now.Before(entry.expires) {
		if entry.count >= authAttemptLimit {
			return false
		}
		entry.count++
		l.peers[peer] = entry
		return true
	}
	if !exists && len(l.peers) >= authPeerLimit {
		for key, value := range l.peers {
			if !now.Before(value.expires) {
				delete(l.peers, key)
			}
		}
		if len(l.peers) >= authPeerLimit {
			return false
		}
	}
	l.peers[peer] = authAttempts{count: 1, expires: now.Add(authAttemptWindow)}
	return true
}

func (l *authLimiter) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(15 * time.Second))
		defer controller.SetReadDeadline(time.Time{})
		if !l.allow(remoteIP(r), time.Now()) {
			w.Header().Set("Retry-After", "300")
			writeError(w, http.StatusTooManyRequests, "登录或注册尝试过于频繁，请稍后重试")
			return
		}
		select {
		case l.slots <- struct{}{}:
			defer func() { <-l.slots }()
			next(w, r)
		default:
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusTooManyRequests, "登录服务繁忙，请稍后重试")
		}
	}
}
