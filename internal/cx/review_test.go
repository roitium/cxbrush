package cx

import (
	"os"
	"strings"
	"testing"
)

func TestParseWorkReview(t *testing.T) {
	data, err := os.ReadFile("testdata/review.html")
	if err != nil {
		t.Fatal(err)
	}
	r := ParseWorkReview(string(data))
	if r == nil {
		t.Fatal("没有解析到批阅结果")
	}
	if r.Status != "已完成" || r.Score != 100 || r.Total != 80 {
		t.Errorf("status/score/total = %q %v %v", r.Status, r.Score, r.Total)
	}
	if r.Questions != 4 || r.Correct != 1 || r.Wrong != 1 || r.Partial != 1 || r.Unmarked != 1 {
		t.Errorf("counts = %+v", r)
	}
	if got := r.String(); got == "" || !strings.Contains(got, "100") {
		t.Errorf("String() = %q", got)
	}
}

func TestParseWorkReviewFallbacks(t *testing.T) {
	// 没有「本次成绩」时用脚本里的 stuScore。
	js := `<div class="TiMu"></div><i aria-label="答案正确"></i><script>var stuScore = "85.5";</script>`
	r := ParseWorkReview(js)
	if r == nil || r.Score != 85.5 || r.Questions != 1 || r.Correct != 1 || r.Wrong != 0 {
		t.Fatalf("js fallback = %+v", r)
	}

	// 两者都没有时按每题得分累加。
	per := `<div class="TiMu"></div><div class="TiMu"></div>` +
		`<span>得分：<span>10.0分</span></span><span>得分：<span>5.5分</span></span>`
	r = ParseWorkReview(per)
	if r == nil || r.Score != 15.5 || r.Questions != 2 || r.Correct != 0 {
		t.Fatalf("per-question fallback = %+v", r)
	}

	// 不是批阅页。
	if r := ParseWorkReview(`<form id="form1"></form>`); r != nil {
		t.Fatalf("普通页面不应解析出批阅结果: %+v", r)
	}
}
