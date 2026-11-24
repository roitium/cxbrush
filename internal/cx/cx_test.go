package cx

import "testing"

// 向量取自 Caochaoxing 的 CryptoRegressionTests / ChaoxingCourseStudyServiceTests。
func TestEncryptField(t *testing.T) {
	cases := map[string]string{
		"test-account-001": "l4yAQPAzbLzfFxr9mqXAJ/2nEzt/bhpxJkywgDme93Y=",
		"学习用-password":     "GaDwrMeYCYfXqmR728+wOWUmjioVVFmq+BzxT7Px6R0=",
	}
	for in, want := range cases {
		if got := encryptField(in); got != want {
			t.Errorf("encryptField(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestBuildEnc(t *testing.T) {
	v := &Video{ClassID: 200001, UserID: "1000001", JobID: "job-demo-001", ObjectID: "0123456789abcdef0123456789abcdef", Duration: 571}
	if got := buildEnc(v, 13); got != "32eda22ad175618495f92e4b35cc6320" {
		t.Errorf("enc@13 = %s", got)
	}
	if got := buildEnc(v, 0); got != "e62813166084e621739d268180008003" {
		t.Errorf("enc@0 = %s", got)
	}
}

func TestParseCards(t *testing.T) {
	html := `<script>window.AttachmentSetting = {}; mArg = {"defaults":{"cpi":123456,"userid":"1000001","reportTimeInterval":60,"schooldoublespeed":1,"ktoken":"tk","reportUrl":"http://mooc1-2.chaoxing.com/mooc-ans/multimedia/log/a/123456"},"mooc2":1,
	"attachments":[{"type":"video","job":true,"jobid":"j1","otherInfo":"nodeId_1-cpi_1&courseId=2","attDuration":300,"headOffset":42000,"property":{"objectid":"obj1","name":"第一讲.mp4","doublespeed":1}},
	{"type":"document","jobid":"d1","jtoken":"tk","property":{"name":"讲义.pdf"}},
	{"type":"workid","job":true,"jobid":"work-abc","enc":"deadbeef","mid":"m1","property":{"module":"work","worktype":"workA","workid":"abc","_jobid":"work-abc","title":"1.1","mid":"m1"}}]};</script>`
	c := parseCards(html, &Course{CourseID: 2, ClassID: 3}, "99")
	if len(c.Videos) != 1 || len(c.Docs) != 1 || len(c.Works) != 1 || c.Others != 0 {
		t.Fatalf("got videos=%d docs=%d works=%d others=%d", len(c.Videos), len(c.Docs), len(c.Works), c.Others)
	}
	v := c.Videos[0]
	if v.ReportURL != "https://mooc1-2.chaoxing.com/mooc-ans/multimedia/log/a/123456" || v.OtherInfo != "nodeId_1-cpi_1" ||
		v.MaxRate() != 2 || v.HeadOffsetMs != 42000 || v.ClipEnd() != 300 || !v.IsJob || v.IsPassed {
		t.Errorf("video parsed wrong: %+v", v)
	}
	if !c.Docs[0].IsPassed {
		t.Errorf("document without job flag should be treated as passed")
	}
	w := c.Works[0]
	if w.WorkID != "abc" || w.JobID != "work-abc" || w.Enc != "deadbeef" || w.KToken != "tk" || w.Mooc2 != 1 || w.Title != "1.1" {
		t.Errorf("work parsed wrong: %+v", w)
	}
}

func TestParseHeadingChapters(t *testing.T) {
	html := `<h3>第一章 绪论</h3>
	<h4 id="cur1001"><span class="catalog_points_yi">2</span><input type="hidden" value="2" class="jobUnfinishCount"/> 1.1 课程介绍</h4>
	<h5 id="cur1002"><span class="roundpoint blue"></span>1.1.1 小节</h5>`
	chs := parseHeadingChapters(html, true)
	if len(chs) != 2 || chs[0].ID != "1001" || chs[0].Pending != 2 || chs[1].Pending != 0 || chs[0].Section != "第一章 绪论" {
		t.Fatalf("got %+v", chs)
	}
}
