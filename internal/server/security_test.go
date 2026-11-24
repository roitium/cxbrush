package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cxbrush/internal/store"
)

func newTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	opts.DataDir = ""
	opts.Workers = 1
	return New(st, opts)
}

func TestLoginLimiterBackoff(t *testing.T) {
	l := newLoginLimiter()
	now := time.Unix(1700000000, 0)
	l.now = func() time.Time { return now }

	for i := 0; i < rlFreeAttempts; i++ {
		l.fail("k", "ip")
		if d := l.retry("k", "ip"); d != 0 {
			t.Fatalf("第 %d 次失败后不应冷却，got %v", i+1, d)
		}
	}
	l.fail("k", "ip")
	if d := l.retry("k", "ip"); d != rlBaseDelay {
		t.Fatalf("第 4 次失败后冷却 %v，want %v", d, rlBaseDelay)
	}
	now = now.Add(rlBaseDelay)
	if d := l.retry("k", "ip"); d > 0 {
		t.Fatalf("冷却结束后应放行，got %v", d)
	}

	l.fail("k", "ip")
	if d := l.retry("k", "ip"); d != 2*rlBaseDelay {
		t.Fatalf("第 5 次失败后冷却 %v，want %v", d, 2*rlBaseDelay)
	}

	// 其它账号不受影响
	if d := l.retry("other", "ip"); d > 0 {
		t.Fatalf("其它账号不应被冷却，got %v", d)
	}
	l.success("k")
	if d := l.retry("k", "ip"); d > 0 {
		t.Fatalf("登录成功后应清零，got %v", d)
	}
	// 冷却有上限
	for i := 0; i < 40; i++ {
		l.fail("k", "ip")
	}
	if d := l.retry("k", "ip"); d > rlMaxDelay {
		t.Fatalf("冷却不应超过上限，got %v", d)
	}
}

func TestLoginLimiterPerIP(t *testing.T) {
	l := newLoginLimiter()
	now := time.Unix(1700000000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i <= rlIPFree; i++ {
		l.fail("k"+string(rune('a'+i%26))+string(rune('a'+i/26)), "1.2.3.4")
	}
	if d := l.retry("clean", "1.2.3.4"); d <= 0 {
		t.Fatal("同一 IP 大量失败后应冷却")
	}
	if d := l.retry("clean", "5.6.7.8"); d > 0 {
		t.Fatal("其它 IP 不应受影响")
	}
}

func TestLoginRateLimited(t *testing.T) {
	s := newTestServer(t, Options{})
	if _, err := s.st.CreateUser("alice", "$2a$10$invalid"); err != nil {
		t.Fatal(err)
	}
	body := `{"username":"alice","password":"wrong"}`
	var last *httptest.ResponseRecorder
	for i := 0; i < rlFreeAttempts+2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
		req.RemoteAddr = "9.9.9.9:1234"
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, req)
		last = rec
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("连续失败后应返回 429，got %d", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Fatal("429 应带 Retry-After")
	}
	if !strings.Contains(last.Body.String(), "频繁") {
		t.Fatalf("unexpected body %s", last.Body.String())
	}
}

func TestCSRFBlocksCrossSite(t *testing.T) {
	s := newTestServer(t, Options{})
	req := httptest.NewRequest(http.MethodPost, "https://cx.example.com/api/login", strings.NewReader(`{}`))
	req.Host = "cx.example.com"
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("跨站 POST 应 403，got %d", rec.Code)
	}

	// 同源放行（这里密码错会返回 401/400，但不应是 403）
	req = httptest.NewRequest(http.MethodPost, "https://cx.example.com/api/login", strings.NewReader(`{}`))
	req.Host = "cx.example.com"
	req.Header.Set("Origin", "https://cx.example.com")
	rec = httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatal("同源请求不应被拦截")
	}

	// GET 不做 CSRF 校验
	req = httptest.NewRequest(http.MethodGet, "https://cx.example.com/api/me", nil)
	req.Host = "cx.example.com"
	req.Header.Set("Origin", "https://evil.example.com")
	rec = httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET 应走到 auth 返回 401，got %d", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t, Options{})
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, h := range []string{
		"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy",
	} {
		if rec.Header().Get(h) == "" {
			t.Errorf("缺少安全响应头 %s", h)
		}
	}
}

func TestSessionCookieSecure(t *testing.T) {
	s := newTestServer(t, Options{SecureCookie: true})
	rec := httptest.NewRecorder()
	s.setSession(rec, httptest.NewRequest(http.MethodPost, "/api/login", nil), 7)
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("会话 Cookie 应带 Secure/HttpOnly: %+v", cookies)
	}

	plain := newTestServer(t, Options{})
	rec = httptest.NewRecorder()
	plain.setSession(rec, httptest.NewRequest(http.MethodPost, "/api/login", nil), 7)
	if c := rec.Result().Cookies(); len(c) == 0 || c[0].Secure {
		t.Fatalf("默认不应强制 Secure: %+v", c)
	}
}

func TestClientIPTrustProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")

	no := newTestServer(t, Options{})
	if got := no.clientIP(r); got != "10.0.0.1" {
		t.Fatalf("不信任代理时应取对端地址，got %s", got)
	}
	yes := newTestServer(t, Options{TrustProxy: true})
	if got := yes.clientIP(r); got != "203.0.113.9" {
		t.Fatalf("信任代理时应取 XFF 首个地址，got %s", got)
	}
}
