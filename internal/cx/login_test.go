package cx

import (
	"strings"
	"testing"
)

func TestCookieHeaderRoundTrip(t *testing.T) {
	c := New(nil)
	if err := c.SetCookieHeader(`_uid=42; fid=7; uf="abc"; empty=`, "chaoxing.com", "mooc1.chaoxing.com"); err != nil {
		t.Fatal(err)
	}
	h := c.CookieHeader("chaoxing.com", "mooc1.chaoxing.com", "mooc1-gray.chaoxing.com")
	for _, want := range []string{"_uid=42", "fid=7", "uf=abc"} {
		if !strings.Contains(h, want) {
			t.Errorf("CookieHeader 缺少 %q：%s", want, h)
		}
	}
	if strings.Contains(h, "empty=") {
		t.Errorf("空值 Cookie 不应导出：%s", h)
	}
	// 名字去重：父域和子域都能取到 _uid，不能出现两次。
	if strings.Count(h, "_uid=") != 1 {
		t.Errorf("Cookie 未去重：%s", h)
	}
}

func TestLoginFailMessage(t *testing.T) {
	cases := []struct {
		weak, two bool
		msg, want string
	}{
		{true, false, "", "弱密码"},
		{false, true, "", "二次验证"},
		{false, false, "用户名或密码错误", "用户名或密码错误"},
		{false, false, "", "登录失败"},
	}
	for _, c := range cases {
		if got := loginFailMessage(c.weak, c.two, c.msg); !strings.Contains(got, c.want) {
			t.Errorf("loginFailMessage(%v,%v,%q) = %q, want contains %q", c.weak, c.two, c.msg, got, c.want)
		}
	}
}
