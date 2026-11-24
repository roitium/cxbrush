package cx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	studyBase       = "https://mooc1-2.chaoxing.com"
	courseListURL   = "https://mooc1-api.chaoxing.com/mycourse/backclazzdata?view=json&rss=1"
	videoReferer    = studyBase + "/ananas/modules/video/index.html"
	audioReferer    = studyBase + "/ananas/modules/audio/index.html"
	documentReferer = studyBase + "/ananas/modules/pdf/index.html"
	readReferer     = studyBase + "/ananas/modules/read/indexV2.html"
)

var (
	reLoginPage    = regexp.MustCompile(`(?is)<title[^>]*>[^<]*登录|<input[^>]+type\s*=\s*["']password["']`)
	reCpi          = regexp.MustCompile(`cpi\s*[:=]\s*['"]?(\d{5,})`)
	reHeading      = regexp.MustCompile(`(?i)<(h3|h4|h5)\b([^>]*)>([\s\S]*?)</(?:h3|h4|h5)>`)
	reTeacherAjax  = regexp.MustCompile(`getTeacherAjax\s*\(\s*['"][^'"]*['"]\s*,\s*['"][^'"]*['"]\s*,\s*['"](\d+)['"]`)
	reCurID        = regexp.MustCompile(`id\s*=\s*['"]cur(\d+)['"]`)
	reStudyLink    = regexp.MustCompile(`(?i)<a\b([^>]*studentstudy\?chapterId=(\d+)[^>]*)>([\s\S]*?)</a>`)
	reAria         = regexp.MustCompile(`aria-label\s*=\s*["']([^"']+)["']`)
	reJobUnfinishA = regexp.MustCompile(`<input[^>]*value\s*=\s*["'](\d+)["'][^>]*class\s*=\s*["'][^"']*jobUnfinishCount`)
	reJobUnfinishB = regexp.MustCompile(`<input[^>]*class\s*=\s*["'][^"']*jobUnfinishCount[^"']*["'][^>]*value\s*=\s*["'](\d+)["']`)
	reOrangeBadge  = regexp.MustCompile(`<span[^>]*class\s*=\s*["'][^"']*(?:orange01|jobCount)[^"']*["'][^>]*>\s*(\d+)\s*<`)
	reRoundReview  = regexp.MustCompile(`class\s*=\s*["'][^"']*roundpoint[^"']*\b(review|lock)\b`)
	reRoundDone    = regexp.MustCompile(`class\s*=\s*["'][^"']*roundpoint[^"']*\b(blue|noJob)\b`)
	reCardCountA   = regexp.MustCompile(`id\s*=\s*["']cardcount["'][^>]*value\s*=\s*["'](\d+)["']`)
	reCardCountB   = regexp.MustCompile(`value\s*=\s*["'](\d+)["'][^>]*id\s*=\s*["']cardcount["']`)
	reCardsFid     = regexp.MustCompile(`"fid"\s*:\s*['"]?(\d+)`)
	reMArg         = regexp.MustCompile(`mArg\s*=\s*\{`)
	reTags         = regexp.MustCompile(`<[^>]+>`)
	reSpaces       = regexp.MustCompile(`\s+`)
	reLeadingBadge = regexp.MustCompile(`^(\d+)\s+(.+)$`)
	reRestTime     = regexp.MustCompile(`\[restTime:(\d+)\]`)
	xhrHeaders     = map[string]string{"X-Requested-With": "XMLHttpRequest"}
)

type Course struct {
	Name     string
	Teacher  string
	CourseID int64
	ClassID  int64
	CPI      int64
}

type Chapter struct {
	ID      string
	Title   string
	Section string
	// Pending 为待完成任务点数，-1 表示未知。
	Pending int
}

// Video 是 knowledge/cards 里 mArg.attachments 的一个视频/音频任务点。
type Video struct {
	CourseID, ClassID, CPI int64
	ChapterID              string
	UserID, JobID          string
	ObjectID, Name         string
	OtherInfo              string
	DToken                 string
	Duration, AttDuration  int
	AttDurationEnc         string
	FaceCaptureEnc         string
	RT                     float64
	DoubleSpeed            int
	ReportInterval         int
	HeadOffsetMs           int
	Begins, Ends           int
	IsPassed, IsJob        bool
	NeedFace               bool
	ReportURL              string
	DType                  string
}

func (v *Video) ID() string { return v.ChapterID + "_" + v.JobID + "_" + v.ObjectID }
func (v *Video) Total() int { return max(v.Duration, v.AttDuration) }
func (v *Video) ClipStart() int {
	return max(v.Begins, 0)
}
func (v *Video) ClipEnd() int {
	if v.Ends > v.ClipStart() {
		if v.Total() > 0 {
			return min(v.Ends, v.Total())
		}
		return v.Ends
	}
	return v.Total()
}
func (v *Video) ClipTime() string { return fmt.Sprintf("%d_%d", v.ClipStart(), v.ClipEnd()) }
func (v *Video) Reportable() bool {
	return v.IsJob && v.JobID != "" && v.DToken != "" && v.ClipEnd() > v.ClipStart()
}

// MaxRate 是课程 doublespeed 允许的最大倍速。
func (v *Video) MaxRate() float64 {
	if v.DoubleSpeed != 0 {
		return 2
	}
	return 1
}

type Document struct {
	CourseID, ClassID int64
	ChapterID         string
	JobID, JToken     string
	Name              string
	IsJob, IsPassed   bool
}

func (d *Document) Completable() bool {
	return d.IsJob && !d.IsPassed && d.JobID != "" && d.JToken != ""
}

// Read 是 cards 里的一个「阅读」任务点（专题阅读 insertreadV2）。
type Read struct {
	CourseID, ClassID int64
	ChapterID         string
	JobID, JToken     string
	MicroTopicID      string
	Name              string
	IsJob, IsPassed   bool
}

func (r *Read) Completable() bool {
	return r.IsJob && !r.IsPassed && r.JobID != "" && r.JToken != ""
}

type ChapterContent struct {
	Videos []*Video
	Docs   []*Document
	Reads  []*Read
	Works  []*Work
	// Others 为直播等本 POC 不处理的任务点数量。
	Others int
}

// ---------- 课程 ----------

func (c *Client) Courses(ctx context.Context) ([]Course, error) {
	data, code, err := c.getAuth(ctx, courseListURL, map[string]string{"User-Agent": AppUA})
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("获取课程列表失败 (%d)", code)
	}
	root, err := decodeJSON(data)
	if err != nil {
		return nil, fmt.Errorf("课程列表解析失败: %w", err)
	}
	list, _ := root["channelList"].([]any)
	if list == nil {
		return nil, fmt.Errorf("课程列表为空或会话已过期")
	}
	var out []Course
	for _, raw := range list {
		item, _ := raw.(map[string]any)
		if item == nil || item["cataName"] == nil {
			continue
		}
		content, _ := item["content"].(map[string]any)
		courseWrap, _ := content["course"].(map[string]any)
		arr, _ := courseWrap["data"].([]any)
		if len(arr) == 0 {
			continue
		}
		obj, _ := arr[0].(map[string]any)
		cpi := int64v(content["cpi"])
		if cpi <= 0 {
			cpi = int64v(item["cpi"])
		}
		name := strv(obj["name"])
		if name == "" {
			name = "未命名课程"
		}
		out = append(out, Course{
			Name:     name,
			Teacher:  strv(obj["teacherfactor"]),
			CourseID: int64v(obj["id"]),
			ClassID:  int64v(content["id"]),
			CPI:      cpi,
		})
	}
	return out, nil
}

// ---------- 章节 ----------

func (c *Client) Chapters(ctx context.Context, course *Course) ([]Chapter, error) {
	var middleHTML string
	if course.CPI <= 0 {
		u := fmt.Sprintf("%s/visit/stucoursemiddle?courseid=%d&clazzid=%d&vc=1&cpi=0&ismooc2=0", studyBase, course.CourseID, course.ClassID)
		html, err := c.getHTML(ctx, u, nil)
		if err != nil {
			return nil, err
		}
		middleHTML = html
		if m := reCpi.FindStringSubmatch(html); m != nil {
			course.CPI, _ = strconv.ParseInt(m[1], 10, 64)
		}
	}
	if course.CPI > 0 {
		u := fmt.Sprintf("%s/mooc-ans/mycourse/studentstudycourselist?courseId=%d&chapterId=0&clazzid=%d&cpi=%d", studyBase, course.CourseID, course.ClassID, course.CPI)
		if html, err := c.getHTML(ctx, u, xhrHeaders); err == nil {
			if chs := parseHeadingChapters(html, true); len(chs) > 0 {
				return chs, nil
			}
		}
	}
	if middleHTML != "" {
		if chs := parseLinkChapters(middleHTML); len(chs) > 0 {
			return chs, nil
		}
		if chs := parseHeadingChapters(middleHTML, false); len(chs) > 0 {
			return chs, nil
		}
	}
	return nil, fmt.Errorf("未解析到章节（cpi=%d）", course.CPI)
}

func parseHeadingChapters(html string, explicitCounts bool) []Chapter {
	var out []Chapter
	seen := map[string]bool{}
	var h3, h4 string
	for _, m := range reHeading.FindAllStringSubmatch(html, -1) {
		tag, attrs, inner := strings.ToLower(m[1]), m[2], m[3]
		if tag == "h3" {
			if t := stripHTML(inner); t != "" && t != "提示" {
				h3, h4 = t, ""
			}
			continue
		}
		id := firstGroup(reTeacherAjax, inner)
		if id == "" {
			id = firstGroup(reCurID, attrs)
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		text := stripHTML(inner)
		badge := -1
		if bm := reLeadingBadge.FindStringSubmatch(text); bm != nil {
			badge, _ = strconv.Atoi(bm[1])
			text = bm[2]
		}
		if text == "" {
			text = "章节 " + id
		}
		section := h3
		if tag == "h4" {
			h4 = text
		} else if h3 != "" && h4 != "" {
			section = h3 + " / " + h4
		} else if h4 != "" {
			section = h4
		}
		out = append(out, Chapter{ID: id, Title: text, Section: section, Pending: pendingCount(inner, badge, explicitCounts)})
	}
	return out
}

func parseLinkChapters(html string) []Chapter {
	var out []Chapter
	seen := map[string]bool{}
	for _, m := range reStudyLink.FindAllStringSubmatch(html, -1) {
		id := m[2]
		if seen[id] {
			continue
		}
		seen[id] = true
		title := stripHTML(m[3])
		if a := firstGroup(reAria, m[1]); a != "" {
			title = stripHTML(a)
		}
		if title == "" {
			title = "章节 " + id
		}
		out = append(out, Chapter{ID: id, Title: title, Pending: -1})
	}
	return out
}

func pendingCount(inner string, badge int, explicit bool) int {
	for _, re := range []*regexp.Regexp{reJobUnfinishA, reJobUnfinishB, reOrangeBadge} {
		if s := firstGroup(re, inner); s != "" {
			n, _ := strconv.Atoi(s)
			return n
		}
	}
	if badge >= 0 {
		return badge
	}
	if reRoundReview.MatchString(inner) {
		return -1
	}
	if reRoundDone.MatchString(inner) {
		return 0
	}
	if explicit {
		return 0
	}
	return -1
}

// ---------- 章节内容 ----------

func (c *Client) ChapterContent(ctx context.Context, course *Course, ch *Chapter) (*ChapterContent, error) {
	cardCount := 1
	ajax := fmt.Sprintf("%s/mooc-ans/mycourse/studentstudyAjax?courseId=%d&clazzid=%d&chapterId=%s&cpi=%d&verificationcode=", studyBase, course.CourseID, course.ClassID, ch.ID, course.CPI)
	if html, err := c.getHTML(ctx, ajax, xhrHeaders); err == nil {
		s := firstGroup(reCardCountA, html)
		if s == "" {
			s = firstGroup(reCardCountB, html)
		}
		if n, _ := strconv.Atoi(s); n >= 1 {
			cardCount = min(n, 12)
		}
	}

	content := &ChapterContent{}
	seenVideo, seenDoc, seenRead, seenWork := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	var lastErr error
	okTabs := 0
	for num := 0; num < cardCount; num++ {
		u := fmt.Sprintf("%s/mooc-ans/knowledge/cards?clazzid=%d&courseid=%d&knowledgeid=%s&num=%d&ut=s&cpi=%d&mooc2=0&isMicroCourse=false&editorPreview=0",
			studyBase, course.ClassID, course.CourseID, ch.ID, num, course.CPI)
		html, err := c.getHTML(ctx, u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		okTabs++
		fid := firstGroup(reCardsFid, html)
		if fid == "" {
			fid = "0"
		}
		tab := parseCards(html, course, ch.ID)
		for _, v := range tab.Videos {
			if seenVideo[v.ID()] {
				continue
			}
			seenVideo[v.ID()] = true
			if err := c.fillVideoStatus(ctx, v, fid); err != nil {
				c.logf("获取视频状态失败 %s: %v", v.Name, err)
			}
			content.Videos = append(content.Videos, v)
		}
		for _, d := range tab.Docs {
			if !seenDoc[d.JobID] {
				seenDoc[d.JobID] = true
				content.Docs = append(content.Docs, d)
			}
		}
		for _, r := range tab.Reads {
			if !seenRead[r.JobID] {
				seenRead[r.JobID] = true
				content.Reads = append(content.Reads, r)
			}
		}
		for _, w := range tab.Works {
			if !seenWork[w.ID()] {
				seenWork[w.ID()] = true
				content.Works = append(content.Works, w)
			}
		}
		content.Others += tab.Others
	}
	if okTabs == 0 && lastErr != nil {
		return nil, lastErr
	}
	return content, nil
}

func parseCards(html string, course *Course, chapterID string) *ChapterContent {
	out := &ChapterContent{}
	raw := extractMArg(html)
	if raw == "" {
		return out
	}
	root, err := decodeJSON([]byte(raw))
	if err != nil {
		return out
	}
	defaults, _ := root["defaults"].(map[string]any)
	cpi := int64v(defaults["cpi"])
	if cpi <= 0 {
		cpi = course.CPI
	}
	userID := strv(defaults["userid"])
	ktoken := strv(defaults["ktoken"])
	mooc2 := intv(root["mooc2"], 0)
	interval := intv(defaults["reportTimeInterval"], 60)
	schoolDS := intv(defaults["schooldoublespeed"], 1)
	chapterCapture := intv(defaults["chapterCapture"], 0)
	reportURL := strv(defaults["reportUrl"])
	if reportURL == "" {
		reportURL = fmt.Sprintf("%s/mooc-ans/multimedia/log/a/%d", studyBase, cpi)
	}
	if strings.HasPrefix(reportURL, "//") {
		reportURL = "https:" + reportURL
	}
	reportURL = strings.Replace(reportURL, "http://", "https://", 1)

	atts, _ := root["attachments"].([]any)
	for _, a := range atts {
		item, _ := a.(map[string]any)
		if item == nil {
			continue
		}
		prop, _ := item["property"].(map[string]any)
		typ := strings.ToLower(strv(item["type"]))
		module := strings.ToLower(strv(prop["module"]))
		jobID := firstNonEmpty(strv(item["jobid"]), strv(prop["jobid"]), strv(prop["_jobid"]))
		jobFlag, hasJobFlag := boolv(item["job"])
		isJob := jobFlag
		if !hasJobFlag {
			isJob = jobID != ""
		}
		passed, hasPassed := boolv(item["isPassed"])
		objectID := firstNonEmpty(strv(prop["objectid"]), strv(item["objectid"]))
		isAudio := (typ == "audio" || module == "insertaudio") && objectID != ""

		switch {
		case typ == "video" || isAudio:
			if objectID == "" {
				continue
			}
			name := strv(prop["name"])
			if name == "" {
				name = map[bool]string{true: "课程音频", false: "课程视频"}[isAudio]
			}
			rt := floatv(item["rt"], floatv(prop["rt"], 0.9))
			// 与官方 index.js 一致的倍速优先级。
			ds := schoolDS
			if isAudio {
				ds = 0
			} else if p, ok := prop["doublespeed"]; ok && p != nil {
				switch pd := intv(p, 1); {
				case pd == 0:
					ds = 0
				case pd == 2:
					ds = schoolDS
				case schoolDS == 0:
					ds = 0
				default:
					ds = pd
				}
			}
			dtype := "Video"
			if isAudio {
				dtype = "Audio"
			}
			att := intv(item["attDuration"], 0)
			out.Videos = append(out.Videos, &Video{
				CourseID: course.CourseID, ClassID: course.ClassID, CPI: cpi,
				ChapterID: chapterID, UserID: userID, JobID: jobID,
				ObjectID: objectID, Name: name,
				OtherInfo:      strings.SplitN(strv(item["otherInfo"]), "&", 2)[0],
				Duration:       att,
				AttDuration:    att,
				AttDurationEnc: strv(item["attDurationEnc"]),
				FaceCaptureEnc: strv(item["videoFaceCaptureEnc"]),
				RT:             rt,
				DoubleSpeed:    ds,
				ReportInterval: interval,
				HeadOffsetMs:   intv(item["headOffset"], 0),
				Begins:         intv(item["begins"], 0),
				Ends:           intv(item["ends"], 0),
				IsPassed:       hasPassed && passed,
				IsJob:          isJob,
				NeedFace:       !isAudio && chapterCapture == 1,
				ReportURL:      reportURL,
				DType:          dtype,
			})
		case typ == "document" || module == "insertdoc":
			// documentJob.js：有 jobid 但 job 缺失或为 false，表示已完成。
			done := passed
			if !hasPassed {
				done = jobID != "" && !jobFlag
			}
			name := firstNonEmpty(strv(prop["name"]), strv(prop["title"]), "文档任务点")
			out.Docs = append(out.Docs, &Document{
				CourseID: course.CourseID, ClassID: course.ClassID, ChapterID: chapterID,
				JobID: jobID, JToken: strv(item["jtoken"]), Name: strings.TrimSpace(name),
				IsJob: isJob, IsPassed: done,
			})
		case typ == "read" || module == "insertreadv2":
			// readJobV2：有 jobid 但 job 缺失或为 false，表示已完成。
			done := passed
			if !hasPassed {
				done = jobID != "" && !jobFlag
			}
			name := firstNonEmpty(strv(prop["title"]), strv(prop["name"]), "阅读任务点")
			out.Reads = append(out.Reads, &Read{
				CourseID: course.CourseID, ClassID: course.ClassID, ChapterID: chapterID,
				JobID: jobID, JToken: strv(item["jtoken"]),
				MicroTopicID: strv(item["microTopicId"]),
				Name:         strings.TrimSpace(name),
				IsJob:        isJob, IsPassed: done,
			})
		case typ == "workid" || module == "work":
			workID := strv(prop["workid"])
			if workID == "" {
				out.Others++
				continue
			}
			done := passed
			if !hasPassed && jobID != "" && !jobFlag {
				done = true
			}
			title := firstNonEmpty(strv(prop["title"]), strv(prop["name"]), "章节测验")
			out.Works = append(out.Works, &Work{
				CourseID: course.CourseID, ClassID: course.ClassID, CPI: cpi,
				ChapterID: chapterID, UserID: userID,
				JobID:       firstNonEmpty(jobID, strv(prop["_jobid"])),
				OriginJobID: firstNonEmpty(strv(prop["_jobid"]), jobID),
				WorkID:      workID,
				SchoolID:    strv(prop["schoolid"]),
				WorkType:    strv(prop["worktype"]),
				Title:       title,
				Enc:         strv(item["enc"]),
				MID:         firstNonEmpty(strv(item["mid"]), strv(prop["mid"])),
				KToken:      ktoken,
				Mooc2:       mooc2,
				IsJob:       isJob,
				IsPassed:    done,
			})
		default:
			if isJob {
				out.Others++
			}
		}
	}
	return out
}

func (c *Client) fillVideoStatus(ctx context.Context, v *Video, fid string) error {
	u := fmt.Sprintf("%s/ananas/status/%s?k=%s&flag=normal&ro=0&_dc=%d", studyBase, v.ObjectID, fid, time.Now().UnixMilli())
	data, code, err := c.getAuth(ctx, u, map[string]string{"Referer": videoReferer, "X-Requested-With": "XMLHttpRequest"})
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("HTTP %d", code)
	}
	root, err := decodeJSON(data)
	if err != nil {
		return err
	}
	if d := strv(root["dtoken"]); d != "" {
		v.DToken = d
	}
	if d := intv(root["duration"], 0); d > 0 {
		v.Duration = d
		if v.AttDuration <= 0 {
			v.AttDuration = d
		}
	}
	return nil
}

// ---------- helpers ----------

// ScanChapters 以 4 路并发拉取多个章节的内容，结果顺序与输入一致。
func (c *Client) ScanChapters(ctx context.Context, course *Course, chs []Chapter, onDone func(done int)) ([]*ChapterContent, []error) {
	results := make([]*ChapterContent, len(chs))
	errs := make([]error, len(chs))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for i := range chs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i], errs[i] = c.ChapterContent(ctx, course, &chs[i])
			mu.Lock()
			done++
			if onDone != nil {
				onDone(done)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	return results, errs
}

func extractMArg(html string) string {
	loc := reMArg.FindStringIndex(html)
	if loc == nil {
		return ""
	}
	start := loc[1] - 1
	depth, inStr, esc := 0, false, false
	for i := start; i < len(html); i++ {
		ch := html[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case ch == '\\':
				esc = true
			case ch == '"':
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return html[start : i+1]
			}
		}
	}
	return ""
}

func decodeJSON(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	err := dec.Decode(&m)
	return m, err
}

func stripHTML(s string) string {
	s = reTags.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&").Replace(s)
	return strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func strv(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}

func intv(v any, def int) int {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
		if f, err := t.Float64(); err == nil {
			return int(f)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return i
		}
	}
	return def
}

func int64v(v any) int64 { return int64(intv(v, 0)) }

func floatv(v any, def float64) float64 {
	switch t := v.(type) {
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f
		}
	}
	return def
}

func boolv(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case json.Number:
		return t.String() != "0", true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}
