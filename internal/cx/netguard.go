package cx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ValidateAIBaseURL 校验用户配置的模型地址。默认只允许 https + 公网地址，
// 防止把服务端当成 SSRF 跳板；本地模型端点需要显式 allowPrivate。
func ValidateAIBaseURL(ctx context.Context, raw string, allowPrivate bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("接口地址不合法: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("接口地址要以 http:// 或 https:// 开头")
	}
	if u.User != nil {
		return fmt.Errorf("接口地址不能包含用户名或密码")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("接口地址缺少主机名")
	}
	if !allowPrivate && u.Scheme != "https" {
		return fmt.Errorf("线上环境模型地址必须使用 https（本地调试可加 -allow-private-ai）")
	}
	return checkPublicHost(ctx, host, allowPrivate)
}

func checkPublicHost(ctx context.Context, host string, allowPrivate bool) error {
	if allowPrivate {
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("无法解析模型地址 %s", host)
	}
	if len(ips) == 0 {
		return fmt.Errorf("模型地址 %s 没有解析到 IP", host)
	}
	for _, ip := range ips {
		if blockedIP(ip.IP) {
			return fmt.Errorf("模型地址指向内网/保留地址（%s），已拒绝", ip.IP)
		}
	}
	return nil
}

func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 0: // 0.0.0.0/8
			return true
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127: // 100.64.0.0/10
			return true
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0.0/24
			return true
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19): // 198.18.0.0/15
			return true
		case v4[0] >= 240: // 240.0.0.0/4
			return true
		}
		return false
	}
	// IPv6 文档地址 2001:db8::/32
	if len(ip) >= 4 && ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
		return true
	}
	return false
}

// NewGuardedHTTPClient 返回一个在拨号时再次校验目标 IP 的 HTTP 客户端，
// 防止 DNS 重绑定或重定向绕过保存时的检查。
func NewGuardedHTTPClient(allowPrivate bool, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		// 不读环境代理：代理可能把请求转发进内网，绕过这里的检查。
		Proxy:                 nil,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if allowPrivate {
				return dialer.DialContext(ctx, network, addr)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range ips {
				if blockedIP(ip.IP) {
					lastErr = fmt.Errorf("目标地址 %s 属于内网/保留地址", ip.IP)
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("无法解析目标地址 %s", host)
			}
			return nil, lastErr
		},
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}
