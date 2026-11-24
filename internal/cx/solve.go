package cx

import (
	"context"
	"fmt"
)

// SolveResult 是一次章节测验自动作答的结果。
type SolveResult struct {
	Work       *Work
	Page       *WorkPage
	Answers    map[string][]string // 已归一化的 value
	Submit     *SubmitResult
	Review     *WorkReview // 提交后重新打开批阅页核对到的成绩
	ReviewErr  error
	Submitted  bool
	Skipped    bool
	SkipReason string
	FontChars  int
	Cached     int // 命中答案缓存的题目数
}

// SolveWork 抓取答题页、还原内联字体、调用模型作答，并在 ai 与页面都可用时提交。
// dec 可以为 nil（页面没有混淆字体时不需要）；cache 命中时不会请求模型。
func (c *Client) SolveWork(ctx context.Context, w *Work, dec *FontDecoder, ai *AIAnswerer, cache *AnswerCache) (*SolveResult, error) {
	res := &SolveResult{Work: w}
	if w == nil {
		return nil, fmt.Errorf("空的测验任务")
	}
	if w.IsPassed {
		res.Skipped, res.SkipReason = true, "已完成"
		return res, nil
	}
	page, err := c.FetchWorkPage(ctx, BuildWorkURL(w, "s"))
	if err != nil {
		return nil, err
	}
	res.Page = page
	page.Work = w
	if page.Review {
		res.Skipped, res.SkipReason = true, "已批阅"
		return res, nil
	}
	if len(page.Questions) == 0 {
		return nil, fmt.Errorf("答题页里没有解析到题目（可能页面结构变化或需要人脸验证）")
	}
	if page.HasFont {
		if dec == nil {
			return nil, fmt.Errorf("答题页有混淆字体，但没有可比对的参考字体")
		}
		family, ttf, err := ExtractSecretFont(page.HTML)
		if err != nil {
			return nil, err
		}
		secret, err := dec.Decode(family, ttf, page.CarrierRunes())
		if err != nil {
			return nil, err
		}
		page.ApplyFont(secret)
		res.FontChars = len(secret.Map)
	}
	var answers map[string][]string
	pending := page.Questions
	if cache != nil {
		answers, pending = cache.Lookup(page.Questions)
		res.Cached = len(page.Questions) - len(pending)
	}
	if len(pending) > 0 {
		if ai == nil {
			return nil, fmt.Errorf("没有配置答题模型")
		}
		got, err := ai.Answer(ctx, pending)
		if err != nil {
			return nil, err
		}
		if answers == nil {
			answers = make(map[string][]string, len(got))
		}
		for qid, vals := range got {
			answers[qid] = vals
		}
		if cache != nil {
			for i := range pending {
				cache.Put(pending[i], got[pending[i].ID])
			}
		}
	}
	res.Answers = answers

	plain := make(map[string]string, len(answers))
	for qid, vals := range answers {
		plain[qid] = joinValues(vals)
	}
	submit, err := c.SubmitWork(ctx, page, plain)
	if err != nil {
		return res, err
	}
	res.Submit = submit
	res.Submitted = true
	// 提交成功后再打开一次，确认批阅结果；失败不影响已提交的事实。
	if review, rerr := c.FetchWorkReview(ctx, w); rerr != nil {
		res.ReviewErr = rerr
	} else {
		res.Review = review
	}
	return res, nil
}

func joinValues(vals []string) string {
	if len(vals) == 1 {
		return vals[0]
	}
	out := ""
	for _, v := range vals {
		out += v
	}
	return out
}
