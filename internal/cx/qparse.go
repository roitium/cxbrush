package cx

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// 题目解析只依赖「提交时真正用到的字段」：
//
//	name=answer<qid>       隐藏答案位 / 判断题选项
//	name=answercheck<qid>  多选题选项
//	name=answertype<qid>   题型编码
//
// 它们缺了页面就交不上卷，所以改版也很难绕过。CSS class（singleQuesId、
// TiMu、fontLabel…）只用来辅助切分题干和选项文字；一旦结构认不出来，就把
// 整块明文一起交给模型（Question.Degraded / Question.Raw），保证仍然能作答。

// qidAnchor 是某道题在 DOM 里第一次出现的位置，同时决定了题目顺序。
type qidAnchor struct {
	id      string
	node    *html.Node
	section string
}

func buildQuestions(doc *html.Node) []*Question {
	anchors := collectQidAnchors(doc)
	if len(anchors) == 0 {
		return nil
	}
	qs := make([]*Question, 0, len(anchors))
	for _, a := range anchors {
		if q := buildQuestion(doc, a); q != nil {
			// 先按明文渲染一次：没有混淆字体的页面也能把题干交给模型。
			q.Decode(nil)
			qs = append(qs, q)
		}
	}
	return qs
}

func collectQidAnchors(doc *html.Node) []qidAnchor {
	var out []qidAnchor
	seen := map[string]bool{}
	section := ""
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "h3" && hasClass(n, "newTestType") {
				section = cleanText(textOf(n))
			}
			if id := qidFromAnswerName(attr(n, "name")); id != "" && !seen[id] {
				seen[id] = true
				out = append(out, qidAnchor{id: id, node: n, section: section})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// qidFromAnswerName 从 answer<qid> / answercheck<qid> / answertype<qid> 里取 qid。
func qidFromAnswerName(name string) string {
	name = strings.TrimSpace(name)
	for _, prefix := range []string{"answertype", "answercheck", "answer"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := name[len(prefix):]
		if rest != "" && isDigits(rest) {
			return rest
		}
	}
	return ""
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func buildQuestion(doc *html.Node, a qidAnchor) *Question {
	box := questionContainer(a.node, a.id)
	if box == nil {
		return nil
	}
	q := &Question{ID: a.id, Section: a.section}
	q.TypeCode = typeCodeOf(doc, box, a.id)
	q.Number = numberIn(box)
	q.stemSegs = stemSegsOf(box, a.id)
	q.Options = optionsOf(box, a.id)
	q.applyTypeLabel()
	if q.Type == "" {
		q.Type = typeLabels[q.TypeCode]
	}
	if q.TypeCode == "" {
		q.TypeCode = typeCodeByLabel[q.Type]
	}
	if q.TypeCode == "" {
		q.TypeCode = inferTypeCode(q.Options)
		if q.Type == "" {
			q.Type = typeLabels[q.TypeCode]
		}
	}
	// 结构没认全（题干空、该有选项却没有、选项文字全空）时，
	// 把整块明文也交给模型兜底。
	if q.rawStem() == "" || (needsOptions(q.TypeCode) && len(q.Options) == 0) || optionsTextEmpty(q.Options) {
		q.Degraded = true
		q.rawSegs = blockSegs(box, a.id, true)
	}
	return q
}

// questionContainer 从题目字段所在节点往上爬，取「仍然只包含本题」的最大祖先。
// 有 singleQuesId / TiMu 时优先用它，避免单题页面一路爬到整个表单。
func questionContainer(anchor *html.Node, qid string) *html.Node {
	if anchor == nil {
		return nil
	}
	cur := anchor
	var named *html.Node
	if isQuestionBox(anchor) {
		named = anchor
	}
	for p := anchor.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			break
		}
		switch p.Data {
		case "body", "html", "form":
			return pick(cur, named)
		}
		if hasOtherQid(p, qid) || containsNamed(p, "answerwqbid") {
			break
		}
		cur = p
		if isQuestionBox(p) {
			named = p
		}
	}
	return pick(cur, named)
}

func pick(cur, named *html.Node) *html.Node {
	if named != nil {
		return named
	}
	return cur
}

func isQuestionBox(n *html.Node) bool {
	return hasClass(n, "singleQuesId") || hasClass(n, "TiMu")
}

func hasOtherQid(n *html.Node, qid string) bool {
	return findFirst(n, func(x *html.Node) bool {
		if x.Type != html.ElementNode {
			return false
		}
		id := qidFromAnswerName(attr(x, "name"))
		return id != "" && id != qid
	}) != nil
}

func containsNamed(n *html.Node, name string) bool {
	return findFirst(n, func(x *html.Node) bool {
		return x.Type == html.ElementNode && attr(x, "name") == name
	}) != nil
}

func typeCodeOf(doc, box *html.Node, qid string) string {
	find := func(root *html.Node) string {
		n := findFirst(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && attr(n, "name") == "answertype"+qid
		})
		if n == nil {
			return ""
		}
		return cleanText(attr(n, "value"))
	}
	if tc := find(box); tc != "" {
		return tc
	}
	if tc := find(doc); tc != "" {
		return tc
	}
	// 老页面把题型写在 TiMu 的 data 里；新页面 data 是题号，所以限定单字符。
	if ti := findFirst(box, func(n *html.Node) bool {
		return n.Type == html.ElementNode && isQuestionBox(n)
	}); ti != nil {
		if tc := attr(ti, "data"); len(tc) == 1 && tc[0] >= '0' && tc[0] <= '4' {
			return tc
		}
	}
	return ""
}

func numberIn(box *html.Node) string {
	if n := findFirst(box, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "i" && hasClass(n, "fl")
	}); n != nil {
		return cleanText(textOf(n))
	}
	return ""
}

// stemSegsOf 取题干：优先外层 font-cxsecret 容器，排除选项行和表单控件。
func stemSegsOf(box *html.Node, qid string) []textSeg {
	var stems []*html.Node
	findAll(box, func(n *html.Node) bool {
		if n.Type != html.ElementNode || !hasClass(n, "font-cxsecret") {
			return false
		}
		if inOptionRow(n, qid) {
			return false
		}
		for p := n.Parent; p != nil && p != box; p = p.Parent {
			if hasClass(p, "font-cxsecret") {
				return false
			}
		}
		return true
	}, func(n *html.Node) { stems = append(stems, n) })

	var labeled []*html.Node
	for _, n := range stems {
		if hasClass(n, "fontLabel") {
			labeled = append(labeled, n)
		}
	}
	if len(labeled) > 0 {
		stems = labeled
	}
	if len(stems) == 0 {
		return blockSegs(box, qid, false)
	}

	skip := func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return false
		}
		if isFormControl(n.Data) {
			return true
		}
		return inOptionRow(n, qid)
	}
	var segs []textSeg
	for _, n := range stems {
		collectText(n, secretAt(n), skip, &segs)
	}
	return cleanSegs(segs)
}

// blockSegs 是整块明文，用于结构识别失败时的兜底。
// markOptions 为 true 时在每个选项前写入真实提交值（〔B〕），
// 这样即使页面把显示字母和 value 打乱，模型也能对上。
func blockSegs(box *html.Node, qid string, markOptions bool) []textSeg {
	var segs []textSeg
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, secret bool) {
		if n.Type == html.TextNode {
			if n.Data != "" {
				segs = append(segs, textSeg{text: n.Data, secret: secret})
			}
			return
		}
		if n.Type != html.ElementNode {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c, secret)
			}
			return
		}
		if isFormControl(n.Data) {
			return
		}
		secret = secret || hasClass(n, "font-cxsecret")
		if isOptionToken(n, qid) {
			if !markOptions {
				return
			}
			segs = append(segs, textSeg{text: "〔" + optionValue(n) + "〕"})
		} else if !markOptions && inOptionRow(n, qid) {
			return
		}
		block := isBlockElement(n.Data)
		if block {
			segs = append(segs, textSeg{text: " "})
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, secret)
		}
		if block {
			segs = append(segs, textSeg{text: " "})
		}
	}
	walk(box, secretAt(box))
	return cleanSegs(segs)
}

func isFormControl(tag string) bool {
	switch tag {
	case "input", "select", "textarea", "button", "script", "style":
		return true
	}
	return false
}

func optionsOf(box *html.Node, qid string) []Option {
	var opts []Option
	seen := map[string]bool{}
	findAll(box, func(n *html.Node) bool { return isOptionToken(n, qid) }, func(n *html.Node) {
		v := optionValue(n)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		opts = append(opts, newOption(n, v, len(opts), qid))
	})
	return opts
}

// isOptionToken 判断一个节点是不是某题的选项载体。
// 结构 A：<span name="answer<qid>" data="B">A</span>
// 结构 B：<input name="answercheck<qid>" type="checkbox" value="A">
//
//	<input name="answer<qid>" type="radio" value="true">（判断题）
//
// 隐藏答案位（type=hidden / value 为空）会被排除。
func isOptionToken(n *html.Node, qid string) bool {
	if n.Type != html.ElementNode {
		return false
	}
	name := attr(n, "name")
	if name != "answer"+qid && name != "answercheck"+qid {
		return false
	}
	if strings.EqualFold(attr(n, "type"), "hidden") {
		return false
	}
	return optionValue(n) != ""
}

func optionValue(n *html.Node) string {
	if v := strings.TrimSpace(attr(n, "data")); v != "" {
		return v
	}
	return strings.TrimSpace(attr(n, "value"))
}

func newOption(n *html.Node, value string, index int, qid string) Option {
	o := Option{Value: value, Letter: optionLetter(n)}
	if o.Letter == "" {
		o.Letter = letterAt(index)
	}
	row := closestRow(n)
	if row == nil {
		row = n
	}
	secret := secretAt(row)
	target := findFirst(row, func(x *html.Node) bool {
		return x.Type == html.ElementNode && x.Data == "a" && hasClass(x, "after")
	})
	if target == nil {
		target = findFirst(row, func(x *html.Node) bool {
			return x.Type == html.ElementNode && x.Data == "b"
		})
	}
	var segs []textSeg
	if target != nil {
		collectText(target, secret || secretAt(target), nil, &segs)
	} else if row != n {
		collectText(row, secret, func(x *html.Node) bool {
			if x == n || x == row {
				return false
			}
			if x.Type != html.ElementNode {
				return false
			}
			return x.Data == "label" || x.Data == "input"
		}, &segs)
	}
	if len(segs) == 0 {
		// 扁平结构：选项文字紧跟在载体节点后面。
		for s := n.NextSibling; s != nil; s = s.NextSibling {
			if s.Type == html.ElementNode && isOptionToken(s, qid) {
				break
			}
			collectText(s, secretAt(s), nil, &segs)
		}
	}
	o.segs = cleanSegs(segs)
	o.Text = cleanText(renderSegs(o.segs, nil))
	if o.Text == "" {
		switch strings.ToLower(o.Value) {
		case "true":
			o.Text = "对"
		case "false":
			o.Text = "错"
		}
	}
	return o
}

func optionLetter(n *html.Node) string {
	if t := cleanText(textOf(n)); t != "" && utf8.RuneCountInString(t) <= 3 {
		if l := lastASCIILetter(t); l != "" {
			return l
		}
		return t
	}
	lab := closestLabel(n)
	if lab == nil {
		return ""
	}
	t := cleanText(textOf(lab))
	if l := lastASCIILetter(t); l != "" {
		return l
	}
	if t != "" && utf8.RuneCountInString(t) <= 3 {
		return t
	}
	return ""
}

func letterAt(i int) string {
	if i < 0 || i >= 26 {
		return ""
	}
	return string(rune('A' + i))
}

func closestLabel(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if p.Data == "label" {
			return p
		}
		if p.Data == "li" || p.Data == "dd" {
			return nil
		}
	}
	return nil
}

func closestRow(n *html.Node) *html.Node {
	for p := n; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && (p.Data == "li" || p.Data == "dd" || p.Data == "tr") {
			return p
		}
	}
	return nil
}

func inOptionRow(n *html.Node, qid string) bool {
	for p := n; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if p.Data == "li" || p.Data == "dd" || p.Data == "tr" {
			if rowHasOptionToken(p, qid) {
				return true
			}
		}
	}
	return false
}

func rowHasOptionToken(row *html.Node, qid string) bool {
	return findFirst(row, func(n *html.Node) bool { return isOptionToken(n, qid) }) != nil
}

func secretAt(n *html.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && hasClass(p, "font-cxsecret") {
			return true
		}
	}
	return false
}

// applyTypeLabel 剥掉题干开头的【多选题】之类的题型标记，同时记录题型。
func (q *Question) applyTypeLabel() {
	raw := q.rawStem()
	if raw == "" {
		return
	}
	m := reStemType.FindStringSubmatch(raw)
	if m == nil {
		return
	}
	if q.Type == "" {
		q.Type = m[1]
	}
	q.stemSegs = cleanSegs(cutRunes(q.stemSegs, utf8.RuneCountInString(m[0])))
}

func (q *Question) rawStem() string { return renderSegs(q.stemSegs, nil) }

func cutRunes(segs []textSeg, n int) []textSeg {
	if n <= 0 {
		return segs
	}
	out := make([]textSeg, 0, len(segs))
	for _, s := range segs {
		if n <= 0 {
			out = append(out, s)
			continue
		}
		r := []rune(s.text)
		if len(r) <= n {
			n -= len(r)
			continue
		}
		s.text = string(r[n:])
		n = 0
		out = append(out, s)
	}
	return out
}

func needsOptions(typeCode string) bool {
	return typeCode == "0" || typeCode == "1" || typeCode == "3"
}

func optionsTextEmpty(opts []Option) bool {
	if len(opts) == 0 {
		return false
	}
	for _, o := range opts {
		if cleanText(o.Text) != "" {
			return false
		}
	}
	return true
}

func inferTypeCode(opts []Option) string {
	if len(opts) == 0 {
		return "2"
	}
	for _, o := range opts {
		v := strings.ToLower(o.Value)
		if v != "true" && v != "false" {
			return "0"
		}
	}
	return "3"
}
