package cx

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	reReviewScore   = regexp.MustCompile(`本次成绩[：:]\s*<span[^>]*>\s*([0-9]+(?:\.[0-9]+)?)`)
	reReviewJSScore = regexp.MustCompile(`stuScore\s*=\s*["']([0-9]+(?:\.[0-9]+)?)["']`)
	reReviewSection = regexp.MustCompile(`（共\s*\d+\s*题\s*[,，]\s*([0-9]+(?:\.[0-9]+)?)\s*分）`)
	reReviewMark    = regexp.MustCompile(`aria-label="(答案[^"]*)"`)
	reReviewBlock   = regexp.MustCompile(`<div[^>]*class="[^"]*\bTiMu\b[^"]*"`)
	reReviewStatus  = regexp.MustCompile(`>(已完成|待批阅|批阅中|未批阅)<`)
)

// WorkReview 是提交后批阅页的核对结果：用来确认这次提交真的生效、答对了几题。
// 批阅页的「我的答案」显示的是页面字母，value 顺序会被 randomOptions 打乱，
// 所以这里只取与字母无关的聚合信息（成绩、对错标记）。
type WorkReview struct {
	Status    string  `json:"status,omitempty"`
	Score     float64 `json:"score"`
	Total     float64 `json:"total,omitempty"`
	Questions int     `json:"questions"`
	Correct   int     `json:"correct"`
	Wrong     int     `json:"wrong"`
	Partial   int     `json:"partial,omitempty"`
	Unmarked  int     `json:"unmarked,omitempty"`
}

func (r *WorkReview) String() string {
	if r == nil {
		return "无批阅结果"
	}
	var b strings.Builder
	if r.Status != "" {
		b.WriteString(r.Status + "，")
	}
	fmt.Fprintf(&b, "%g 分", r.Score)
	if r.Total > 0 {
		fmt.Fprintf(&b, "/%g", r.Total)
	}
	fmt.Fprintf(&b, "（对 %d / 错 %d", r.Correct, r.Wrong)
	if r.Partial > 0 {
		fmt.Fprintf(&b, " / 部分对 %d", r.Partial)
	}
	if r.Unmarked > 0 {
		fmt.Fprintf(&b, " / 未判 %d", r.Unmarked)
	}
	b.WriteString("）")
	return b.String()
}

// ParseWorkReview 解析「查看已批阅作业」页面。不是批阅页时返回 nil。
func ParseWorkReview(html string) *WorkReview {
	marks := reReviewMark.FindAllStringSubmatch(html, -1)
	score, hasScore := reviewScore(html)
	if !hasScore && len(marks) == 0 {
		return nil
	}
	r := &WorkReview{Score: score}
	if m := reReviewStatus.FindStringSubmatch(html); m != nil {
		r.Status = m[1]
	}
	for _, m := range reReviewSection.FindAllStringSubmatch(html, -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			r.Total += v
		}
	}
	r.Questions = len(reReviewBlock.FindAllString(html, -1))
	for _, m := range marks {
		switch m[1] {
		case "答案正确":
			r.Correct++
		case "答案错误":
			r.Wrong++
		default:
			r.Partial++
		}
	}
	if n := r.Questions - r.Correct - r.Wrong - r.Partial; n > 0 {
		r.Unmarked = n
	}
	return r
}

func reviewScore(html string) (float64, bool) {
	for _, re := range []*regexp.Regexp{reReviewScore, reReviewJSScore} {
		if m := re.FindStringSubmatch(html); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				return v, true
			}
		}
	}
	// 兜底：把每题「得分：xx分」加起来。
	sum := 0.0
	for _, m := range reReviewPerScore.FindAllStringSubmatch(html, -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			sum += v
		}
	}
	return sum, sum > 0
}

var reReviewPerScore = regexp.MustCompile(`得分[：:]\s*<span[^>]*>\s*([0-9]+(?:\.[0-9]+)?)\s*分`)

// FetchWorkReview 重新请求该测验；已提交的测验会被 302 到批阅页。
func (c *Client) FetchWorkReview(ctx context.Context, w *Work) (*WorkReview, error) {
	if w == nil {
		return nil, fmt.Errorf("空的测验任务")
	}
	headers := map[string]string{"Referer": studyBase + "/mycourse/studentstudy"}
	data, resp, err := c.getAuthResp(ctx, BuildWorkURL(w, "s"), headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("批阅页 HTTP %d", resp.StatusCode)
	}
	r := ParseWorkReview(string(data))
	if r == nil {
		return nil, fmt.Errorf("没有解析到批阅结果（可能还没批阅）")
	}
	return r, nil
}
