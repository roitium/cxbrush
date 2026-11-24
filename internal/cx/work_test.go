package cx

import (
	"net/url"
	"os"
	"testing"
)

func TestBuildWorkURL(t *testing.T) {
	w := &Work{
		CourseID: 1000001, ClassID: 2000001, CPI: 3000001,
		ChapterID: "4000001", UserID: "9000001",
		JobID:       "work-0123456789abcdef0123456789abcdef",
		OriginJobID: "work-0123456789abcdef0123456789abcdef",
		WorkID:      "0123456789abcdef0123456789abcdef",
		WorkType:    "workA", Enc: "0123456789abcdef0123456789abcdef",
		KToken: "0123456789abcdef0123456789abcdef", Mooc2: 1,
	}
	raw := BuildWorkURL(w, "s")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "mooc1-2.chaoxing.com" || u.Path != "/mooc-ans/api/work" {
		t.Fatalf("url = %s", raw)
	}
	q := u.Query()
	want := map[string]string{
		"api": "1", "workId": w.WorkID, "jobid": w.JobID, "originJobId": w.OriginJobID,
		"needRedirect": "true", "skipHeader": "true", "knowledgeid": w.ChapterID,
		"ktoken": w.KToken, "cpi": "3000001", "ut": "s", "clazzId": "2000001",
		"type": "", "enc": w.Enc, "mooc2": "1", "courseid": "1000001",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	w.WorkType = "workB"
	w.SchoolID = "9999"
	q = mustURL(t, BuildWorkURL(w, "t")).Query()
	if q.Get("workId") != "9999-"+w.WorkID || q.Get("type") != "b" || q.Get("ut") != "t" {
		t.Errorf("workB/schoolid/teacher url wrong: %v", q)
	}
}

func TestParseWorkPageAndDecode(t *testing.T) {
	refPath := "../assets/NotoSansCJKsc-DemiLight.otf"
	if _, err := os.Stat(refPath); err != nil {
		t.Skip("缺少比对字体 internal/assets/NotoSansCJKsc-DemiLight.otf")
	}
	data, err := os.ReadFile("testdata/work.html")
	if err != nil {
		t.Fatal(err)
	}
	pageURL, _ := url.Parse("https://mooc1-gray.chaoxing.com/mooc-ans/work/doHomeWorkNew?courseId=2000001")
	page, err := ParseWorkPage(string(data), pageURL)
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasFont || page.Review {
		t.Fatalf("HasFont=%v Review=%v", page.HasFont, page.Review)
	}
	if len(page.Questions) != 2 {
		t.Fatalf("questions = %d", len(page.Questions))
	}
	if page.Form == nil || !page.Form.RandomOptions {
		t.Fatalf("form = %+v", page.Form)
	}

	family, ttf, err := ExtractSecretFont(string(data))
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := NewFontDecoder(refPath)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := decoder.Decode(family, ttf, page.CarrierRunes())
	if err != nil {
		t.Fatal(err)
	}
	page.ApplyFont(secret)

	q := page.Questions[0]
	if q.ID != "111111111" || q.TypeCode != "0" {
		t.Fatalf("q1 = %+v", q)
	}
	const wantStem = "面对腐败问题严峻挑战，（）直抵基层，聚焦民生领域微腐败，切实维护群众利益。"
	if q.Stem != wantStem {
		t.Errorf("stem = %q, want %q", q.Stem, wantStem)
	}
	wantOpts := []struct{ letter, value, text string }{
		{"A", "B", "“打虎”"},
		{"B", "C", "“猎狐”"},
		{"C", "A", "“天网行动”"},
		{"D", "D", "“拍蝇”"},
	}
	if len(q.Options) != len(wantOpts) {
		t.Fatalf("options = %d", len(q.Options))
	}
	for i, w := range wantOpts {
		o := q.Options[i]
		if o.Letter != w.letter || o.Value != w.value || o.Text != w.text {
			t.Errorf("option[%d] = %+v, want %+v", i, o, w)
		}
	}
	// value 优先：这里 A/B/C/D 恰好都是 value，所以 "A" 含义是提交值 A，
	// 不能再按显示字母解释成 B。这正是 randomOptions 的坑。
	if v, err := q.NormalizeAnswer("A"); err != nil || v != "A" {
		t.Errorf("NormalizeAnswer(A) = %q, %v", v, err)
	}
	if v, err := q.NormalizeAnswer("D"); err != nil || v != "D" {
		t.Errorf("NormalizeAnswer(D) = %q, %v", v, err)
	}

	jq := page.Questions[1]
	if jq.TypeCode != "3" || jq.Stem != "党的十八大以来，推进全面从严治党，推动管党治党实现了从严紧硬向宽松软的深刻转变。" {
		t.Errorf("judgment = %+v", jq)
	}
	if v, err := jq.NormalizeAnswer("对"); err != nil || v != "true" {
		t.Errorf("judgment 对 = %q, %v", v, err)
	}
	if v, err := jq.NormalizeAnswer("A"); err != nil || v != "true" {
		t.Errorf("judgment A = %q, %v", v, err)
	}

	// 当输入的字母不是任何 value 时，才退回按显示字母映射。
	synthetic := &Question{ID: "x", TypeCode: "0", Options: []Option{
		{Letter: "A", Value: "X"}, {Letter: "B", Value: "Y"},
	}}
	if v, err := synthetic.NormalizeAnswer("B"); err != nil || v != "Y" {
		t.Errorf("display letter mapping = %q, %v", v, err)
	}

	fields, err := page.Form.BuildAnswers(page.Questions, map[string]string{
		"111111111": "D",
		"222222222": "对",
	}, "enc-from-validate")
	if err != nil {
		t.Fatal(err)
	}
	if fields.Get("answer111111111") != "D" || fields.Get("answertype111111111") != "0" {
		t.Errorf("q1 fields = %v", fields)
	}
	if fields.Get("answer222222222") != "true" || fields.Get("answertype222222222") != "3" {
		t.Errorf("q2 fields = %v", fields)
	}
	if fields.Get("answerwqbid") != "111111111,222222222," {
		t.Errorf("answerwqbid = %q", fields.Get("answerwqbid"))
	}
	if fields.Get("enc") != "enc-from-validate" || fields.Get("randomOptions") != "true" {
		t.Errorf("common fields = %v", fields)
	}
	su := page.Form.SubmitURL()
	if su.Query().Get("ua") != "pc" || su.Query().Get("saveStatus") != "1" || su.Query().Get("version") != "1" {
		t.Errorf("submit url = %s", su)
	}
}

// 结构 B：div.TiMu.singleQuesId + input[name=answercheck<qid>] 选项，
// 判断题直接是 input[name=answer<qid>][value=true|false]。
// 这份页面没有混淆字体，同时覆盖「无字体也要渲染题干」的路径。
func TestParseWorkPageVariantB(t *testing.T) {
	data, err := os.ReadFile("testdata/work_variant_b.html")
	if err != nil {
		t.Fatal(err)
	}
	page, err := ParseWorkPage(string(data), mustURL(t, "https://mooc1.example.com/mooc-ans/api/work?api=1"))
	if err != nil {
		t.Fatal(err)
	}
	if page.HasFont || page.Review {
		t.Fatalf("HasFont=%v Review=%v", page.HasFont, page.Review)
	}
	if len(page.Questions) != 2 {
		t.Fatalf("questions = %d", len(page.Questions))
	}

	q := page.Questions[0]
	if q.ID != "333333333" || q.TypeCode != "1" || q.Type != "多选题" {
		t.Fatalf("q1 = %+v", q)
	}
	if q.Stem != "金融要做好“五篇大文章”，下列选项中属于这一范畴的有（）。" {
		t.Errorf("stem = %q", q.Stem)
	}
	if q.Degraded || q.Raw != "" {
		t.Errorf("不该退化: degraded=%v raw=%q", q.Degraded, q.Raw)
	}
	want := []struct{ letter, value, text string }{
		{"A", "A", "科技金融"},
		{"B", "D", "消费金融"},
		{"C", "C", "普惠金融"},
	}
	if len(q.Options) != len(want) {
		t.Fatalf("options = %d", len(q.Options))
	}
	for i, w := range want {
		o := q.Options[i]
		if o.Letter != w.letter || o.Value != w.value || o.Text != w.text {
			t.Errorf("option[%d] = %+v, want %+v", i, o, w)
		}
	}
	if v, err := q.NormalizeAnswer("AD"); err != nil || v != "AD" {
		t.Errorf("NormalizeAnswer(AD) = %q, %v", v, err)
	}

	jq := page.Questions[1]
	if jq.ID != "444444444" || jq.TypeCode != "3" || jq.Stem != "数字金融能够完全替代传统线下金融服务。" {
		t.Fatalf("judgement = %+v", jq)
	}
	if len(jq.Options) != 2 || jq.Options[0].Value != "true" || jq.Options[1].Value != "false" {
		t.Fatalf("judgement options = %+v", jq.Options)
	}
	if v, err := jq.NormalizeAnswer("对"); err != nil || v != "true" {
		t.Errorf("NormalizeAnswer(对) = %q, %v", v, err)
	}
	if v, err := jq.NormalizeAnswer("错"); err != nil || v != "false" {
		t.Errorf("NormalizeAnswer(错) = %q, %v", v, err)
	}

	fields, err := page.Form.BuildAnswers(page.Questions, map[string]string{
		"333333333": "AD",
		"444444444": "错",
	}, "enc")
	if err != nil {
		t.Fatal(err)
	}
	if fields.Get("answer333333333") != "AD" || fields.Get("answertype333333333") != "1" {
		t.Errorf("q1 fields = %v", fields)
	}
	if fields.Get("answer444444444") != "false" {
		t.Errorf("q2 fields = %v", fields)
	}
	if fields.Get("answerwqbid") != "333333333,444444444," {
		t.Errorf("answerwqbid = %q", fields.Get("answerwqbid"))
	}
}

// 完全认不出的结构也要能答：题干靠 font-cxsecret 兜底，
// 选项靠 answer<qid> 的 data 取 value，正文取载体节点后面的兄弟文本；
// 连正文都取不到时退化为 Degraded + 带 value 标记的整块明文。
func TestParseQuestionDegradesGracefully(t *testing.T) {
	const page = `<form id="form1" action="/mooc-ans/work/addStudentWorkNewWeb?courseId=1">
<input name="answerwqbid" value="">
<div class="x">
  <div class="y">
    <span class="font-cxsecret">【单选题】这是题干？</span>
    <span name="answer555" data="P">A</span>选项一
    <span name="answer555" data="Q">B</span>选项二
    <input type="hidden" name="answer555" value="">
    <input type="hidden" name="answertype555" value="0">
  </div>
  <div class="z">
    <div>题目文字一</div>
    <span name="answer777" data="X">A</span>
    <span name="answer777" data="Y">B</span>
    <input type="hidden" name="answer777" value="">
    <input type="hidden" name="answertype777" value="0">
  </div>
</div>
</form>`
	p, err := ParseWorkPage(page, mustURL(t, "https://mooc1.example.com/mooc-ans/api/work?api=1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Questions) != 2 {
		t.Fatalf("questions = %d", len(p.Questions))
	}

	q := p.Questions[0]
	if q.ID != "555" || q.Stem != "这是题干？" || q.Degraded {
		t.Fatalf("q1 = %+v", q)
	}
	if len(q.Options) != 2 || q.Options[0].Value != "P" || q.Options[0].Text != "选项一" ||
		q.Options[1].Value != "Q" || q.Options[1].Text != "选项二" {
		t.Fatalf("q1 options = %+v", q.Options)
	}
	if v, err := q.NormalizeAnswer("Q"); err != nil || v != "Q" {
		t.Errorf("NormalizeAnswer(Q) = %q, %v", v, err)
	}

	d := p.Questions[1]
	if d.ID != "777" || d.Stem != "题目文字一" {
		t.Fatalf("q2 = %+v", d)
	}
	if !d.Degraded {
		t.Errorf("q2 应该退化")
	}
	if d.Raw != "题目文字一 〔X〕A 〔Y〕B" {
		t.Errorf("raw = %q", d.Raw)
	}
	if len(d.Options) != 2 || d.Options[0].Value != "X" || d.Options[1].Value != "Y" {
		t.Errorf("q2 options = %+v", d.Options)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
