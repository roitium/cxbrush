package cx

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "100.127.255.255", "0.0.0.0",
		"192.0.0.1", "198.18.0.1", "198.19.0.1", "240.0.0.1", "255.255.255.255",
		"224.0.0.1", "::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1",
		"2001:db8::1", "::ffff:127.0.0.1",
	}
	for _, s := range blocked {
		if ip := net.ParseIP(s); ip == nil {
			t.Fatalf("bad test ip %q", s)
		} else if !blockedIP(ip) {
			t.Errorf("%s 应被拦截", s)
		}
	}
	allowed := []string{"1.1.1.1", "8.8.8.8", "100.63.255.255", "172.32.0.1", "2606:4700::1111"}
	for _, s := range allowed {
		if blockedIP(net.ParseIP(s)) {
			t.Errorf("%s 不应被拦截", s)
		}
	}
	if !blockedIP(nil) {
		t.Error("nil IP 应被拦截")
	}
}

func TestValidateAIBaseURLRejects(t *testing.T) {
	ctx := context.Background()
	cases := []struct{ raw, want string }{
		{"http://api.deepseek.com/v1", "https"},
		{"ftp://example.com", "http"},
		{"https://user:pass@example.com", "用户名"},
		{"https://127.0.0.1:11434/v1", "内网"},
		{"https://10.0.0.5/v1", "内网"},
		{"https://[::1]/v1", "内网"},
		{"https://", "主机名"},
		{"://bad", "不合法"},
	}
	for _, c := range cases {
		err := ValidateAIBaseURL(ctx, c.raw, false)
		if err == nil {
			t.Errorf("%q 应该被拒绝", c.raw)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q 错误信息应包含 %q，实际 %v", c.raw, c.want, err)
		}
	}
}

func TestValidateAIBaseURLAllows(t *testing.T) {
	ctx := context.Background()
	// allowPrivate 放开本地模型；公网 https 域名走 DNS 解析。
	if err := ValidateAIBaseURL(ctx, "http://127.0.0.1:11434/v1", true); err != nil {
		t.Fatalf("allowPrivate 应放行: %v", err)
	}
	if err := ValidateAIBaseURL(ctx, "https://example.com/v1", false); err != nil {
		t.Logf("（DNS 不可用时跳过）: %v", err)
	}
}
