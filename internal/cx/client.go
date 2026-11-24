// Package cx 是超星学习通 PC 网页看课协议的最小实现（参考 Pybsama/Caochaoxing 的 ChaoxingCore）。
package cx

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// AppUA 用于登录和 mooc1-api 移动端接口。
	AppUA = "Dalvik/2.1.0 (Linux; U; Android 12; SM-N9006 Build/8aba9e4.0) (schild:2d97f7b9439f21333c946878fc4d6ccb) (device:SM-N9006) Language/zh_CN com.chaoxing.mobile/ChaoXingStudy_3_6.7.5_android_phone_10941_314 (@Kalimdor)_68f184fd763546c1a04ab3a09b3deebb"
	// PCUA 用于 mooc1-2 / mooc2-ans 等 PC 网页学习接口。
	PCUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

	loginURL    = "https://passport2.chaoxing.com/fanyalogin"
	userInfoURL = "https://sso.chaoxing.com/apis/login/userLogin4Uname.do"
	transferKey = "u2oh6Vu^HWe4_AES"
)

type Client struct {
	http    *http.Client
	user    string
	encPass string
	Name    string

	reloginMu sync.Mutex
	debug     io.Writer
	debugMu   sync.Mutex
}

func New(debug io.Writer) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		http:  &http.Client{Jar: jar, Timeout: 30 * time.Second},
		debug: debug,
	}
}

func (c *Client) logf(format string, args ...any) {
	if c.debug == nil {
		return
	}
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	fmt.Fprintf(c.debug, time.Now().Format("15:04:05.000 ")+format+"\n", args...)
}

// encryptField: AES-128-CBC，key = iv = transferKey，PKCS7，Base64。
func encryptField(plain string) string {
	key := []byte(transferKey)
	block, _ := aes.NewCipher(key)
	data := []byte(plain)
	pad := aes.BlockSize - len(data)%aes.BlockSize
	data = append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, key).CryptBlocks(out, data)
	return base64.StdEncoding.EncodeToString(out)
}

func (c *Client) Login(ctx context.Context, user, pass string) error {
	c.user = strings.TrimSpace(user)
	c.encPass = encryptField(pass)
	if err := c.login(ctx); err != nil {
		return err
	}
	return c.fetchProfile(ctx)
}

func (c *Client) login(ctx context.Context) error {
	body := strings.Join([]string{
		"fid=-1",
		"uname=" + formEscapeBase64(encryptField(c.user)),
		"password=" + formEscapeBase64(c.encPass),
		"refer=https%3A%2F%2Fi.chaoxing.com",
		"t=true",
		"forbidotherlogin=0",
		"validate=",
		"doubleFactorLogin=0",
		"independentId=0",
		"independentNameId=0",
	}, "&")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", AppUA)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("登录请求失败: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	c.logf("POST %s -> %d %s", loginURL, resp.StatusCode, truncate(string(data), 300))
	var r struct {
		Status    bool   `json:"status"`
		Msg       string `json:"msg"`
		Msg2      string `json:"msg2"`
		URL       string `json:"url"`
		Weakpwd   bool   `json:"weakpwd"`
		TwoFactor bool   `json:"containTwoFactorLogin"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("登录响应格式错误: %s", truncate(string(data), 120))
	}
	if !r.Status {
		return errors.New(loginFailMessage(r.Weakpwd, r.TwoFactor, firstNonEmpty(r.Msg2, r.Msg)))
	}
	// fanyalogin 只下发登录票据，真正的会话 Cookie 要靠这次跳转补全。
	if jump := strings.TrimSpace(r.URL); jump != "" {
		if unescaped, err := url.PathUnescape(jump); err == nil {
			jump = unescaped
		}
		if _, _, err := c.get(ctx, jump, map[string]string{"User-Agent": PCUA}); err != nil {
			c.logf("登录跳转失败: %v", err)
		}
	}
	return nil
}

func loginFailMessage(weakpwd, twoFactor bool, msg string) string {
	switch {
	case weakpwd:
		return "该账号是弱密码，请先在网页端修改密码再登录"
	case twoFactor:
		return "该账号需要二次验证，请改用 Cookie 方式导入"
	case msg != "":
		return msg
	}
	return "登录失败，请检查账号或密码"
}

func (c *Client) fetchProfile(ctx context.Context) error {
	data, _, err := c.get(ctx, userInfoURL, map[string]string{"User-Agent": AppUA})
	if err != nil {
		return err
	}
	var r struct {
		Msg struct {
			Name string `json:"name"`
		} `json:"msg"`
	}
	if json.Unmarshal(data, &r) == nil {
		c.Name = r.Msg.Name
	}
	return nil
}

// RefreshSession 调 SSO 的 userLogin4Uname 续期：服务端会重新下发
// _uid / _d / cx_p_token / p_auth_token，把 7 天有效期整体往后推。
// 只依赖当前 Cookie，不需要账号密码；Cookie 已过期时返回错误。
func (c *Client) RefreshSession(ctx context.Context) error {
	data, resp, err := c.get(ctx, userInfoURL, map[string]string{"User-Agent": AppUA})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("续期 HTTP %d", resp.StatusCode)
	}
	var r struct {
		Msg map[string]any `json:"msg"`
	}
	if json.Unmarshal(data, &r) != nil || r.Msg == nil {
		return fmt.Errorf("会话已失效，需要重新登录")
	}
	// msg.uid 是数字，且和 Cookie 里的 _uid 不是同一个值，只用来判断返回是否合法。
	if _, ok := r.Msg["uid"]; !ok {
		return fmt.Errorf("会话已失效，需要重新登录")
	}
	if !strings.Contains(c.CookieHeader("chaoxing.com"), "p_auth_token=") {
		return fmt.Errorf("续期后没有拿到新的登录 Cookie")
	}
	return nil
}

// ErrSessionExpired 表示会话失效且客户端没有可用于自动重登的账号密码
// （例如 Cookie 导入模式），需要用户重新登录。
var ErrSessionExpired = errors.New("学习通登录已过期，请重新登录")

func (c *Client) relogin(ctx context.Context) error {
	c.reloginMu.Lock()
	defer c.reloginMu.Unlock()
	if c.user == "" || c.encPass == "" {
		return ErrSessionExpired
	}
	c.logf("会话失效，重新登录")
	return c.login(ctx)
}

// SetCookieHeader 把浏览器复制的 Cookie 请求头塞进 cookiejar。
// 只用于无浏览器模式：从 DevTools 复制即可，不需要账号密码。
func (c *Client) SetCookieHeader(raw string, hosts ...string) error {
	var cookies []*http.Cookie
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 || kv[0] == "" {
			continue
		}
		name := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		// 浏览器导出的 Cookie 常把值包在双引号里，net/http 会直接丢弃这种值。
		if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
			value = value[1 : len(value)-1]
		}
		cookies = append(cookies, &http.Cookie{Name: name, Value: value})
	}
	if len(cookies) == 0 {
		return fmt.Errorf("Cookie 为空")
	}
	for _, h := range hosts {
		u := &url.URL{Scheme: "https", Host: h}
		// 像 chaoxing.com 这种「注册域」按父域下发，才能覆盖 mooc1-api 等子域；
		// 具体主机仍然按 host-only 处理。
		applied := cookies
		if strings.Count(h, ".") <= 1 {
			applied = make([]*http.Cookie, len(cookies))
			for i, ck := range cookies {
				cp := *ck
				cp.Domain = "." + h
				applied[i] = &cp
			}
		}
		c.http.Jar.SetCookies(u, applied)
	}
	return nil
}

// CookieHeader 把 jar 里这些域下的 Cookie 拼成 Cookie 请求头（按名称去重），
// 用于登录成功后落库，或从账号登录切回无状态复用。
func (c *Client) CookieHeader(hosts ...string) string {
	if len(hosts) == 0 {
		hosts = []string{"chaoxing.com", "i.chaoxing.com", "mooc1.chaoxing.com", "mooc1-gray.chaoxing.com"}
	}
	seen := map[string]string{}
	var order []string
	for _, h := range hosts {
		u := &url.URL{Scheme: "https", Host: h}
		for _, ck := range c.http.Jar.Cookies(u) {
			if ck.Name == "" || strings.TrimSpace(ck.Value) == "" {
				continue
			}
			if _, ok := seen[ck.Name]; ok {
				continue
			}
			seen[ck.Name] = ck.Value
			order = append(order, ck.Name)
		}
	}
	parts := make([]string, 0, len(order))
	for _, n := range order {
		parts = append(parts, n+"="+seen[n])
	}
	return strings.Join(parts, "; ")
}

func (c *Client) get(ctx context.Context, rawURL string, headers map[string]string) ([]byte, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", PCUA)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.logf("GET %s -> ERR %v", rawURL, err)
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	c.logf("GET %s -> %d %s", rawURL, resp.StatusCode, truncate(string(data), 300))
	return data, resp, err
}

// getAuthResp 在被重定向到登录页或 401 时自动重新登录并重试一次。
// 返回的 resp 仍然有效，可用 resp.Request.URL 拿到跟随重定向后的最终地址。
func (c *Client) getAuthResp(ctx context.Context, rawURL string, headers map[string]string) ([]byte, *http.Response, error) {
	data, resp, err := c.get(ctx, rawURL, headers)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || isLoginPage(data, resp) {
		if err := c.relogin(ctx); err != nil {
			return nil, nil, err
		}
		data, resp, err = c.get(ctx, rawURL, headers)
		if err != nil {
			return nil, nil, err
		}
	}
	return data, resp, nil
}

func (c *Client) getAuth(ctx context.Context, rawURL string, headers map[string]string) ([]byte, int, error) {
	data, resp, err := c.getAuthResp(ctx, rawURL, headers)
	if err != nil {
		return nil, 0, err
	}
	return data, resp.StatusCode, nil
}

func (c *Client) getHTML(ctx context.Context, rawURL string, headers map[string]string) (string, error) {
	data, code, err := c.getAuth(ctx, rawURL, headers)
	if err != nil {
		return "", err
	}
	if code < 200 || code >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", code, rawURL)
	}
	return string(data), nil
}

func isLoginPage(data []byte, resp *http.Response) bool {
	host := strings.ToLower(resp.Request.URL.Host)
	if host == "passport2.chaoxing.com" || host == "passport.chaoxing.com" {
		return true
	}
	if len(data) > 80_000 {
		return false
	}
	return reLoginPage.Match(data)
}

func formEscapeBase64(s string) string {
	return strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(s)
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
