package cx

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

var (
	typeLabels      = map[string]string{"0": "单选题", "1": "多选题", "2": "填空题", "3": "判断题", "4": "简答题"}
	typeCodeByLabel = map[string]string{"单选题": "0", "多选题": "1", "填空题": "2", "判断题": "3", "简答题": "4"}
	reStemType      = regexp.MustCompile(`^\s*【([^】]{2,6})】`)
)

type textSeg struct {
	text   string
	secret bool
}

// Option.Letter 是页面显示字母，Value 是真正的提交值（num_option 的 data）。
// randomOptions=true 时两者顺序不一致，提交必须用 Value。
type Option struct {
	Letter  string `json:"letter"`
	Value   string `json:"value"`
	TextRaw string `json:"textRaw,omitempty"`
	Text    string `json:"text"`

	segs []textSeg
}

type Uncertain struct {
	Raw          string   `json:"raw"`
	Char         string   `json:"char"`
	Score        float64  `json:"score"`
	Margin       float64  `json:"margin"`
	Alternatives []string `json:"alternatives,omitempty"`
}

type Question struct {
	ID       string `json:"id"`
	Number   string `json:"number"`
	Section  string `json:"section,omitempty"`
	Type     string `json:"type"`
	TypeCode string `json:"typeCode"`

	StemRaw string `json:"stemRaw,omitempty"`
	Stem    string `json:"stem"`

	// Degraded 表示结构没解析干净，Raw 是解密后的整块明文，供模型兜底。
	Degraded bool   `json:"degraded,omitempty"`
	Raw      string `json:"raw,omitempty"`

	Options   []Option    `json:"options"`
	Uncertain []Uncertain `json:"uncertain,omitempty"`

	stemSegs []textSeg
	rawSegs  []textSeg
}

// Decode 用字体映射还原题干和选项。只有带 font-cxsecret 的节点才会被替换，
// 避免把页面上本来就正常显示的同名载体字误伤。
func (q *Question) Decode(f *SecretFont) {
	var dec func(string) string
	if f != nil {
		dec = f.Decode
	}
	q.Stem = renderSegs(q.stemSegs, dec)
	if raw := renderSegs(q.stemSegs, nil); raw != q.Stem {
		q.StemRaw = raw
	}
	for i := range q.Options {
		o := &q.Options[i]
		if len(o.segs) == 0 {
			continue
		}
		o.Text = renderSegs(o.segs, dec)
		if raw := renderSegs(o.segs, nil); raw != o.Text {
			o.TextRaw = raw
		}
	}
	if len(q.rawSegs) > 0 {
		q.Raw = renderSegs(q.rawSegs, dec)
	}
	if f == nil {
		return
	}
	q.Uncertain = q.uncertain(f)
}

func (q *Question) uncertain(f *SecretFont) []Uncertain {
	var out []Uncertain
	seen := map[rune]bool{}
	add := func(segs []textSeg) {
		for _, s := range segs {
			if !s.secret {
				continue
			}
			for _, r := range s.text {
				if seen[r] {
					continue
				}
				d, ok := f.Detail[r]
				if !ok || d.Confidence != "low" {
					continue
				}
				seen[r] = true
				u := Uncertain{Raw: string(r), Char: string(d.Char), Score: d.Score, Margin: d.Margin}
				for _, a := range d.Alternatives {
					u.Alternatives = append(u.Alternatives, string(a))
				}
				out = append(out, u)
			}
		}
	}
	add(q.stemSegs)
	add(q.rawSegs)
	for _, o := range q.Options {
		add(o.segs)
	}
	return out
}

// NormalizeAnswer 把 AI/用户给出的答案转换成页面真正提交的 data 值。
// 优先把输入当成 value；如果匹配不上，再按显示字母映射。
func (q *Question) NormalizeAnswer(ans string) (string, error) {
	ans = strings.TrimSpace(ans)
	if ans == "" {
		return "", fmt.Errorf("题目 %s 的答案为空", q.ID)
	}
	switch q.TypeCode {
	case "3":
		tok := judgmentValue(ans)
		if tok == "" {
			tok = ans
		}
		if v, ok := q.matchValue(tok); ok {
			return v, nil
		}
		if v, ok := q.mapToken(ans); ok {
			return v, nil
		}
		return "", fmt.Errorf("题目 %s 的判断题答案 %q 无法匹配选项", q.ID, ans)
	case "1":
		parts := splitMulti(ans)
		if len(parts) == 0 {
			return "", fmt.Errorf("题目 %s 的多选答案为空", q.ID)
		}
		seen := map[string]bool{}
		var vals []string
		for _, p := range parts {
			v, ok := q.mapToken(p)
			if !ok {
				return "", fmt.Errorf("题目 %s 的多选项 %q 无法匹配选项", q.ID, p)
			}
			if !seen[v] {
				seen[v] = true
				vals = append(vals, v)
			}
		}
		sort.Strings(vals)
		return strings.Join(vals, ""), nil
	case "0":
		if v, ok := q.mapToken(ans); ok {
			return v, nil
		}
		return "", fmt.Errorf("题目 %s 的答案 %q 无法匹配选项", q.ID, ans)
	default:
		// 填空/简答等直接使用原文。
		return ans, nil
	}
}

func (q *Question) matchValue(v string) (string, bool) {
	for _, o := range q.Options {
		if o.Value == v || strings.EqualFold(o.Value, v) {
			return o.Value, true
		}
	}
	return "", false
}

func (q *Question) mapToken(tok string) (string, bool) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "", false
	}
	// 1) 已经是 value（用户要求强制使用 value）。
	for _, o := range q.Options {
		if o.Value == tok {
			return o.Value, true
		}
	}
	// 2) 显示字母 -> value。
	for _, o := range q.Options {
		if strings.EqualFold(o.Letter, tok) {
			return o.Value, true
		}
	}
	// 3) 大小写不敏感的 value。
	for _, o := range q.Options {
		if strings.EqualFold(o.Value, tok) {
			return o.Value, true
		}
	}
	return "", false
}

func judgmentValue(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "对", "正确", "true", "t", "√", "yes", "y":
		return "true"
	case "错", "错误", "false", "f", "×", "x", "no", "n":
		return "false"
	}
	return ""
}

func splitMulti(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == '、' || unicode.IsSpace(r)
	})
	var out []string
	for _, f := range fields {
		// "ABCD" 这种没有分隔符的写法按单个字符拆开。
		rs := []rune(f)
		if len(rs) > 1 && allSingleTokens(rs) {
			for _, r := range rs {
				out = append(out, string(r))
			}
			continue
		}
		out = append(out, f)
	}
	return out
}

func allSingleTokens(rs []rune) bool {
	for _, r := range rs {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// WorkForm 是答题页里的 form#form1。
type WorkForm struct {
	Action        string
	ActionURL     *url.URL
	Fields        url.Values
	RandomOptions bool
}

// SubmitURL 对应前端最后追加的参数。
func (f *WorkForm) SubmitURL() *url.URL {
	u := *f.ActionURL
	q := u.Query()
	q.Set("ua", "pc")
	q.Set("formType", "post")
	q.Set("saveStatus", "1")
	q.Set("version", "1")
	u.RawQuery = q.Encode()
	return &u
}

func (f *WorkForm) value(names ...string) string {
	for _, n := range names {
		if v := f.Fields.Get(n); v != "" {
			return v
		}
	}
	if f.ActionURL != nil {
		q := f.ActionURL.Query()
		for _, n := range names {
			if v := q.Get(n); v != "" {
				return v
			}
		}
	}
	return ""
}

// BuildAnswers 生成提交 body。questions 的顺序就是 answerwqbid 的顺序。
func (f *WorkForm) BuildAnswers(questions []*Question, answers map[string]string, enc string) (url.Values, error) {
	fields := url.Values{}
	for k, vs := range f.Fields {
		fields[k] = append([]string(nil), vs...)
	}
	var qids []string
	for _, q := range questions {
		ans, ok := answers[q.ID]
		if !ok {
			return nil, fmt.Errorf("缺少题目 %s 的答案", q.ID)
		}
		v, err := q.NormalizeAnswer(ans)
		if err != nil {
			return nil, err
		}
		fields.Set("answer"+q.ID, v)
		fields.Set("answertype"+q.ID, q.TypeCode)
		qids = append(qids, q.ID)
	}
	if len(qids) == 0 {
		return nil, fmt.Errorf("没有题目，拒绝提交")
	}
	fields.Set("answerwqbid", strings.Join(qids, ",")+",")
	fields.Set("pyFlag", "")
	if enc != "" {
		fields.Set("enc", enc)
	}
	return fields, nil
}

// ---------- HTML 解析 ----------

func parseQuestions(htmlStr string) ([]*Question, error) {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("HTML 解析失败: %w", err)
	}
	return buildQuestions(doc), nil
}

func lastASCIILetter(s string) string {
	s = strings.TrimSpace(s)
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return string(c)
		}
	}
	return ""
}

func parseWorkForm(htmlStr string, pageURL *url.URL) *WorkForm {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil
	}
	form := findFirst(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "form" {
			return false
		}
		return attr(n, "id") == "form1" || strings.Contains(attr(n, "action"), "addStudentWorkNewWeb")
	})
	if form == nil {
		return nil
	}
	f := &WorkForm{Action: attr(form, "action"), Fields: url.Values{}}
	if au, err := url.Parse(f.Action); err == nil {
		if pageURL != nil {
			au = pageURL.ResolveReference(au)
		}
		f.ActionURL = au
	}
	findAll(form, func(n *html.Node) bool {
		return n.Type == html.ElementNode && (n.Data == "input" || n.Data == "textarea") && attr(n, "name") != ""
	}, func(n *html.Node) {
		name := attr(n, "name")
		typ := strings.ToLower(attr(n, "type"))
		if typ == "submit" || typ == "button" || typ == "reset" || typ == "file" {
			return
		}
		v := attr(n, "value")
		if n.Data == "textarea" {
			v = textOf(n)
		}
		f.Fields.Set(name, v)
	})
	f.RandomOptions = strings.EqualFold(f.Fields.Get("randomOptions"), "true")
	return f
}

func findAllNodes(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	findAll(n, pred, func(x *html.Node) { out = append(out, x) })
	return out
}

func findAll(n *html.Node, pred func(*html.Node) bool, fn func(*html.Node)) {
	if pred(n) {
		fn(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		findAll(c, pred, fn)
	}
}

func findFirst(n *html.Node, pred func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if pred(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if r := findFirst(c, pred); r != nil {
			return r
		}
	}
	return nil
}

func hasClass(n *html.Node, class string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			return
		}
		if x.Type == html.ElementNode {
			if x.Data == "script" || x.Data == "style" {
				return
			}
			if isBlockElement(x.Data) {
				b.WriteByte(' ')
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if x.Type == html.ElementNode && isBlockElement(x.Data) {
			b.WriteByte(' ')
		}
	}
	walk(n)
	return b.String()
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func isBlockElement(tag string) bool {
	switch tag {
	case "br", "p", "div", "li", "ul", "ol", "h1", "h2", "h3", "h4", "h5", "h6",
		"tr", "td", "th", "table", "section", "article", "blockquote", "pre":
		return true
	}
	return false
}

func collectText(n *html.Node, inSecret bool, skip func(*html.Node) bool, out *[]textSeg) {
	if n == nil {
		return
	}
	if n.Type == html.TextNode {
		if n.Data != "" {
			*out = append(*out, textSeg{text: n.Data, secret: inSecret})
		}
		return
	}
	if n.Type != html.ElementNode {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collectText(c, inSecret, skip, out)
		}
		return
	}
	if skip != nil && skip(n) {
		return
	}
	if n.Data == "script" || n.Data == "style" {
		return
	}
	secret := inSecret || hasClass(n, "font-cxsecret")
	block := isBlockElement(n.Data)
	if block {
		*out = append(*out, textSeg{text: " "})
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, secret, skip, out)
	}
	if block {
		*out = append(*out, textSeg{text: " "})
	}
}

// cleanSegs 把空白折叠成单个普通空格，并去掉首尾空白。
func cleanSegs(in []textSeg) []textSeg {
	var out []textSeg
	var b strings.Builder
	cur := false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, textSeg{text: b.String(), secret: cur})
			b.Reset()
		}
	}
	pendingSpace := false
	for _, s := range in {
		for _, r := range s.text {
			if unicode.IsSpace(r) {
				flush()
				pendingSpace = true
				continue
			}
			if pendingSpace {
				if len(out) > 0 || b.Len() > 0 {
					flush()
					out = append(out, textSeg{text: " "})
				}
				pendingSpace = false
			}
			if b.Len() > 0 && cur != s.secret {
				flush()
			}
			cur = s.secret
			b.WriteRune(r)
		}
	}
	flush()
	// 去掉最后的空格段
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1].text) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func renderSegs(segs []textSeg, decode func(string) string) string {
	var b strings.Builder
	for _, s := range segs {
		if s.secret && decode != nil {
			b.WriteString(decode(s.text))
		} else {
			b.WriteString(s.text)
		}
	}
	return cleanText(b.String())
}
