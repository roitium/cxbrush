package cx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Work 是 knowledge/cards 里 mArg.attachments 的一个章节测验/作业任务点。
// 页面上的「题」并不在 cards 里，而是由 /mooc-ans/api/work 302 到真正的答题页。
type Work struct {
	CourseID, ClassID, CPI int64
	ChapterID              string
	UserID                 string

	// JobID 是 work-<hex>，OriginJobID 通常与 JobID 相同。
	JobID, OriginJobID string
	// WorkID 是 cards 里的十六进制 workid，提交时对应 oldWorkId。
	WorkID string
	// SchoolID 仅部分学校课程存在，存在时 api/work 的 workId 要写成 <schoolid>-<workid>。
	SchoolID string
	// WorkType 为 workA/workB；workB 会在 api/work 里带 type=b。
	WorkType string
	Title    string
	// Enc 是 cards attachment 的 enc，不是答题页表单里的 enc_work。
	Enc    string
	MID    string
	KToken string
	Mooc2  int

	IsJob, IsPassed bool
}

func (w *Work) ID() string { return w.ChapterID + "_" + w.JobID }

// APIWorkID 按 front-end /ananas/modules/work/index.html 的规则拼接 workId。
func (w *Work) APIWorkID() string {
	if w.SchoolID != "" {
		return w.SchoolID + "-" + w.WorkID
	}
	return w.WorkID
}

func (w *Work) SubmitType() string {
	if w.WorkType == "workB" {
		return "b"
	}
	return ""
}

// BuildWorkURL 完全用课程/章节/cards 的返回数据构造「最初请求题目」的 API。
// ut 为 s（学生）或 t（教师），学生页面固定 s。
func BuildWorkURL(w *Work, ut string) string {
	if ut == "" {
		ut = "s"
	}
	q := url.Values{}
	q.Set("api", "1")
	q.Set("workId", w.APIWorkID())
	q.Set("jobid", w.JobID)
	q.Set("originJobId", w.OriginJobID)
	q.Set("needRedirect", "true")
	q.Set("skipHeader", "true")
	q.Set("knowledgeid", w.ChapterID)
	q.Set("ktoken", w.KToken)
	q.Set("cpi", strconv.FormatInt(w.CPI, 10))
	q.Set("ut", ut)
	q.Set("clazzId", strconv.FormatInt(w.ClassID, 10))
	q.Set("type", w.SubmitType())
	q.Set("enc", w.Enc)
	if w.Mooc2 == 1 {
		q.Set("mooc2", "1")
	}
	q.Set("courseid", strconv.FormatInt(w.CourseID, 10))
	return studyBase + "/mooc-ans/api/work?" + q.Encode()
}

// WorkPage 是 /api/work 跟随 302 后的最终答题/批阅页。
type WorkPage struct {
	URL       *url.URL
	HTML      string
	Review    bool
	HasFont   bool
	Work      *Work
	Form      *WorkForm
	Questions []*Question
	Font      *SecretFont
}

// FetchWorkPage 请求 api/work（自动跟随 302），并解析最终页面。
// 如果该测验已经提交过，最终页会是批阅页（没有 cxSecretStyle 和提交表单）。
func (c *Client) FetchWorkPage(ctx context.Context, rawURL string) (*WorkPage, error) {
	headers := map[string]string{"Referer": studyBase + "/mycourse/studentstudy"}
	data, resp, err := c.getAuthResp(ctx, rawURL, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("答题页 HTTP %d: %s", resp.StatusCode, resp.Request.URL)
	}
	p, err := ParseWorkPage(string(data), resp.Request.URL)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ParseWorkPage 解析已经拿到的答题页 HTML。pageURL 用于解析表单 action。
func ParseWorkPage(html string, pageURL *url.URL) (*WorkPage, error) {
	p := &WorkPage{URL: pageURL, HTML: html, HasFont: reSecretStyle.MatchString(html)}
	var err error
	p.Questions, err = parseQuestions(html)
	if err != nil {
		return nil, err
	}
	p.Form = parseWorkForm(html, pageURL)
	// 已提交过的测验会 302 到批阅页（selectWorkQuestionYiPiYue），没有提交表单。
	// 注意：没有混淆字体并不等于已批阅，有的答题页本来就不加字体。
	reviewURL := pageURL != nil && strings.Contains(pageURL.Path, "selectWorkQuestionYiPiYue")
	p.Review = reviewURL || p.Form == nil
	// 有字体却没有表单，说明页面结构和预期不符，宁可报错也不要静默跳过。
	if p.Form == nil && p.HasFont && !reviewURL {
		return nil, fmt.Errorf("答题页里没有找到提交表单 addStudentWorkNewWeb")
	}
	return p, nil
}

// ApplyFont 用已经建好的解码表还原题目文本。
func (p *WorkPage) ApplyFont(f *SecretFont) {
	p.Font = f
	if f == nil {
		return
	}
	for _, q := range p.Questions {
		q.Decode(f)
	}
}

// WorkMetadata 从表单里反推 work 的基本信息（离线解析 HTML 时使用）。
func (p *WorkPage) WorkMetadata() *Work {
	if p.Work != nil {
		return p.Work
	}
	if p.Form == nil {
		return nil
	}
	get := func(k string) string { return p.Form.Fields.Get(k) }
	parse := func(k string) int64 { v, _ := strconv.ParseInt(get(k), 10, 64); return v }
	w := &Work{
		CourseID:    parse("courseId"),
		ClassID:     parse("classId"),
		CPI:         parse("cpi"),
		ChapterID:   firstNonEmpty(get("knowledgeid"), p.Form.ActionURL.Query().Get("knowledgeid")),
		UserID:      get("userId"),
		JobID:       get("jobid"),
		OriginJobID: get("jobid"),
		WorkID:      get("oldWorkId"),
		Title:       "",
		Enc:         p.Form.ActionURL.Query().Get("enc"),
		KToken:      p.Form.ActionURL.Query().Get("ktoken"),
	}
	if p.Form.ActionURL.Query().Get("testmooc2") == "1" {
		w.Mooc2 = 1
	}
	return w
}

// ValidateWork 是提交前的 GET /mooc-ans/work/validate。
// status=3 才能提交；2 需要验证码；1 账号异常。
func (c *Client) ValidateWork(ctx context.Context, p *WorkPage) (status int, enc string, err error) {
	if p.Form == nil {
		return 0, "", fmt.Errorf("没有提交表单")
	}
	q := url.Values{}
	q.Set("classId", p.Form.value("classId", "classId"))
	q.Set("courseId", p.Form.value("courseId", "courseid"))
	q.Set("knowledgeId", p.Form.value("knowledgeid", "knowledgeid"))
	q.Set("cpi", p.Form.value("cpi", "cpi"))
	q.Set("_", strconv.FormatInt(nowMillis(), 10))
	u := p.baseURL() + "/mooc-ans/work/validate?" + q.Encode()
	data, code, err := c.getAuth(ctx, u, map[string]string{
		"Referer":          p.refURL(),
		"X-Requested-With": "XMLHttpRequest",
	})
	if err != nil {
		return 0, "", err
	}
	if code != http.StatusOK {
		return 0, "", fmt.Errorf("validate HTTP %d: %s", code, truncate(string(data), 120))
	}
	root, err := decodeJSON(data)
	if err != nil {
		return 0, "", fmt.Errorf("validate 响应不是 JSON: %s", truncate(string(data), 120))
	}
	status = intv(root["status"], 0)
	enc = firstNonEmpty(strv(root["enc"]), strv(root["encWork"]))
	switch status {
	case 3:
		return status, enc, nil
	case 2:
		return status, enc, fmt.Errorf("需要验证码（validate status=2）")
	case 1:
		return status, enc, fmt.Errorf("账号异常（validate status=1），请重新登录")
	default:
		return status, enc, fmt.Errorf("validate 未通过（status=%d）", status)
	}
}

type SubmitResult struct {
	Status    bool
	StuStatus int
	Msg       string
	URL       string
	Raw       map[string]any
}

// SubmitWork 校验并提交答案。answers 的 key 是题目 qid，value 是提交值
// （页面 num_option 的 data，而不是显示字母）。多选题可以直接给 "ABCD"。
// 本方法不会自动重试：重复提交可能消耗作答次数。
func (c *Client) SubmitWork(ctx context.Context, p *WorkPage, answers map[string]string) (*SubmitResult, error) {
	if p.Form == nil {
		return nil, fmt.Errorf("没有提交表单")
	}
	status, enc, err := c.ValidateWork(ctx, p)
	if err != nil {
		return nil, err
	}
	if status != 3 {
		return nil, fmt.Errorf("validate status=%d，不能提交", status)
	}
	fields, err := p.Form.BuildAnswers(p.Questions, answers, enc)
	if err != nil {
		return nil, err
	}
	u := p.Form.SubmitURL().String()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(fields.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", PCUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json, text/javascript, */*; 0.01")
	req.Header.Set("Referer", p.refURL())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	c.logf("POST %s -> %d %s", u, resp.StatusCode, truncate(string(body), 300))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("提交 HTTP %d: %s", resp.StatusCode, truncate(string(body), 160))
	}
	root, err := decodeJSON(body)
	if err != nil {
		return nil, fmt.Errorf("提交响应不是 JSON: %s", truncate(string(body), 160))
	}
	r := &SubmitResult{
		Status:    boolValue(root["status"]),
		StuStatus: intv(root["stuStatus"], 0),
		Msg:       strv(root["msg"]),
		URL:       strv(root["url"]),
		Raw:       root,
	}
	if !r.Status {
		return r, fmt.Errorf("提交失败: %s", firstNonEmpty(r.Msg, truncate(string(body), 160)))
	}
	return r, nil
}

func (p *WorkPage) refURL() string {
	if p.URL == nil {
		return ""
	}
	return p.URL.String()
}

func (p *WorkPage) baseURL() string {
	if p.URL != nil && p.URL.Scheme != "" && p.URL.Host != "" {
		return p.URL.Scheme + "://" + p.URL.Host
	}
	return studyBase
}

func boolValue(v any) bool {
	b, _ := boolv(v)
	return b
}

func nowMillis() int64 { return time.Now().UnixMilli() }
