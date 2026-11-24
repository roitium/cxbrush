package server

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- 登录限流 ----------

const (
	rlFreeAttempts = 3                // 前 3 次失败不惩罚，避免手滑打错就锁
	rlBaseDelay    = time.Second      // 第 4 次失败后的冷却时间
	rlMaxDelay     = 15 * time.Minute // 单账号最长冷却
	rlIPFree       = 30               // 同一 IP 的失败阈值（多账号共享出口时留足余量）
	rlIPBlock      = 10 * time.Minute // 单 IP 超阈值后的冷却
	rlEntryTTL     = 6 * time.Hour    // 长期无失败的记录会被回收
	rlMaxEntries   = 4096
)

type rateEntry struct {
	failures int
	until    time.Time
	last     time.Time
}

// loginLimiter 是进程内的失败计数限流器：按「IP + 账号」做指数退避，
// 另外按 IP 做一个粗粒度的总量上限。多实例部署时应换成共享存储。
type loginLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	byKey map[string]*rateEntry
	byIP  map[string]*rateEntry
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		now:   time.Now,
		byKey: map[string]*rateEntry{},
		byIP:  map[string]*rateEntry{},
	}
}

func backoff(failures int, free int, base, max time.Duration) time.Duration {
	if failures <= free {
		return 0
	}
	d := base
	for i := 0; i < failures-free-1 && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

// retry 返回还需要等待的时间，0 表示允许尝试。
func (l *loginLimiter) retry(key, ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wait time.Duration
	for _, e := range []*rateEntry{l.byKey[key], l.byIP[ip]} {
		if e == nil || e.until.IsZero() {
			continue
		}
		if d := e.until.Sub(now); d > wait {
			wait = d
		}
	}
	return wait
}

func (l *loginLimiter) fail(key, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.byKey[key] == nil {
		l.gcLocked(now)
	}
	k := l.byKey[key]
	if k == nil {
		k = &rateEntry{}
		l.byKey[key] = k
	}
	if d := backoff(k.failures+1, rlFreeAttempts, rlBaseDelay, rlMaxDelay); d > 0 {
		k.until = now.Add(d)
	}
	k.failures++
	k.last = now

	ipEntry := l.byIP[ip]
	if ipEntry == nil {
		ipEntry = &rateEntry{}
		l.byIP[ip] = ipEntry
	}
	ipEntry.failures++
	ipEntry.last = now
	if ipEntry.failures > rlIPFree {
		ipEntry.until = now.Add(rlIPBlock)
	}
}

// success 只清掉该账号的失败记录，不动 IP 计数：
// 否则攻击者可以用一个自己的账号把 IP 限流刷掉。
func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byKey, key)
}

func (l *loginLimiter) gcLocked(now time.Time) {
	if len(l.byKey) < rlMaxEntries && len(l.byIP) < rlMaxEntries {
		return
	}
	for k, e := range l.byKey {
		if now.Sub(e.last) > rlEntryTTL {
			delete(l.byKey, k)
		}
	}
	for k, e := range l.byIP {
		if now.Sub(e.last) > rlEntryTTL {
			delete(l.byIP, k)
		}
	}
}

// clientIP 只在显式信任反向代理时才读 X-Forwarded-For，否则直连对端地址更可靠。
func (s *Server) clientIP(r *http.Request) string {
	if s.opt.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				xff = xff[:i]
			}
			xff = strings.TrimSpace(xff)
			if net.ParseIP(xff) != nil {
				return xff
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// loginKey 返回限流用的维度键；acc 为账号（用户名/手机号），空则只按 IP 计。
func (s *Server) loginKey(r *http.Request, scope, acc string) (key, ip string) {
	ip = s.clientIP(r)
	acc = strings.ToLower(strings.TrimSpace(acc))
	if acc == "" {
		return scope + "|ip:" + ip, ip
	}
	return scope + "|" + ip + "|" + acc, ip
}

// tooMany 返回 true 表示已经写出 429，调用方应立即返回。
func (s *Server) tooMany(w http.ResponseWriter, key, ip string) bool {
	wait := s.limiter.retry(key, ip)
	if wait <= 0 {
		return false
	}
	secs := int(wait.Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	httpError(w, http.StatusTooManyRequests, "尝试过于频繁，请 "+strconv.Itoa(secs)+" 秒后再试")
	return true
}

// ---------- CSRF 与安全响应头 ----------

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// sameOrigin 比较 Origin/Referer 的 host 与请求 Host。
// 反代后面 r.Host 是公网域名，浏览器给的 Origin 也是同一个，所以只比 host 即可。
func sameOrigin(r *http.Request, raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// guard 是全局中间件：安全响应头 + 状态变更请求的同源校验。
// 会话 Cookie 是 SameSite=Lax，这里再加一层，防止反代/老浏览器放宽 Cookie 策略。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")

		if strings.HasPrefix(r.URL.Path, "/api/") {
			// 接口返回里有 Cookie 状态、任务详情等，别让中间层缓存。
			h.Set("Cache-Control", "no-store")
		}

		if strings.HasPrefix(r.URL.Path, "/api/") && !isSafeMethod(r.Method) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				origin = r.Header.Get("Referer")
			}
			// 没有 Origin/Referer 的是非浏览器客户端（curl、脚本），不拦；
			// 有但不同源的一律拒绝，浏览器发出的跨站请求必带 Origin。
			if origin != "" && !sameOrigin(r, origin) {
				httpError(w, http.StatusForbidden, "跨站请求被拒绝")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// cookieSecure 判断会话 Cookie 是否应带 Secure。
func (s *Server) cookieSecure(r *http.Request) bool {
	if s.opt.SecureCookie {
		return true
	}
	if s.opt.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return r.TLS != nil
}
