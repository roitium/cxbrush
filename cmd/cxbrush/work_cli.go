package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cxbrush/internal/cx"
)

const refFontName = "NotoSansCJKsc-DemiLight.otf"
const refFontURL = "https://github.com/notofonts/noto-cjk/raw/main/Sans/OTF/SimplifiedChinese/" + refFontName

type workOutput struct {
	Source        string              `json:"source"`
	Work          *workInfo           `json:"work,omitempty"`
	Font          *fontInfo           `json:"font,omitempty"`
	QuestionCount int                 `json:"questionCount"`
	Questions     []*cx.Question      `json:"questions"`
	AIAnswers     map[string][]string `json:"aiAnswers,omitempty"`
	Submit        *submitInfo         `json:"submit,omitempty"`
	Review        *cx.WorkReview      `json:"review,omitempty"`
}

type workInfo struct {
	Title       string `json:"title,omitempty"`
	CourseID    int64  `json:"courseId"`
	ClassID     int64  `json:"classId"`
	CPI         int64  `json:"cpi"`
	ChapterID   string `json:"chapterId"`
	UserID      string `json:"userId,omitempty"`
	JobID       string `json:"jobId"`
	OriginJobID string `json:"originJobId,omitempty"`
	WorkID      string `json:"workId"`
	WorkType    string `json:"workType,omitempty"`
	Enc         string `json:"enc,omitempty"`
	Passed      bool   `json:"passed"`
}

type fontInfo struct {
	Family        string               `json:"family"`
	Bytes         int                  `json:"bytes"`
	SHA1          string               `json:"sha1"`
	TTF           string               `json:"ttf,omitempty"`
	Carriers      int                  `json:"carriers"`
	SourceFont    string               `json:"sourceFont"`
	LowConfidence map[string]glyphInfo `json:"lowConfidence,omitempty"`
	CharMap       map[string]glyphInfo `json:"charMap"`
}

type glyphInfo struct {
	Char         string   `json:"char"`
	Score        float64  `json:"score"`
	Margin       float64  `json:"margin"`
	Confidence   string   `json:"confidence"`
	RunnerUp     string   `json:"runnerUp,omitempty"`
	Alternatives []string `json:"alternatives,omitempty"`
}

type submitInfo struct {
	Action        string              `json:"action"`
	SubmitURL     string              `json:"submitURL"`
	RandomOptions bool                `json:"randomOptions"`
	Fields        map[string][]string `json:"fields"`
}

func runWorkCLI(args []string) int {
	fs := flag.NewFlagSet("work", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		htmlPath = fs.String("html", "", "本地答题页 HTML（离线解析）")
		pageURL  = fs.String("page-url", "", "离线模式下表单 action 的基准 URL（可选）")
		rawURL   = fs.String("url", "", "直接抓取的答题页/api/work URL")
		cookie   = fs.String("cookie", "", "配合 -url 使用的 Cookie 请求头")
		user     = fs.String("u", os.Getenv("CX_USERNAME"), "学习通账号（在线模式）")
		pass     = fs.String("p", os.Getenv("CX_PASSWORD"), "学习通密码（在线模式）")
		courseID = fs.Int64("course", 0, "课程 courseId（在线模式）")
		classID  = fs.Int64("class", 0, "班级 classId（可选，默认取课程列表）")
		cpi      = fs.Int64("cpi", 0, "cpi（可选，默认取课程列表）")
		chapter  = fs.String("chapter", "", "章节 knowledgeid（在线模式）")
		workSel  = fs.String("work", "", "多个测验时按 workid/jobid/title 选择")
		outPath  = fs.String("o", "", "输出 JSON 路径（默认 stdout）")
		mdPath   = fs.String("md", "", "额外输出 Markdown")
		fontOut  = fs.String("font-out", "", "保存页面内联 TTF 的路径")
		refFont  = fs.String("ref-font", "", "比对用完整中文字体（默认 Noto Sans CJK SC DemiLight）")
		fetch    = fs.Bool("fetch-font", false, "下载比对字体后退出")
		answersF = fs.String("answers", "", `答案 JSON，例如 {"405957436":"D"}`)
		aiFlag   = fs.Bool("ai", false, "调用模型自动作答（默认不提交，配合 -submit 才提交）")
		aiBase   = fs.String("ai-base", "", "答题模型 Base URL（默认取 magpie tokenflux）")
		aiKey    = fs.String("ai-key", "", "答题模型 API Key（默认取 magpie tokenflux）")
		aiModel  = fs.String("ai-model", "", "答题模型名（默认 deepseek/deepseek-flash）")
		submit   = fs.Bool("submit", false, "真正提交（默认只输出，不提交）")
		dumpHTML = fs.String("dump-html", "", "保存最终 HTML")
		timeout  = fs.Duration("timeout", 60*time.Second, "网络请求超时")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *fetch {
		p, err := ensureRefFont(ctx, *refFont, true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "下载失败:", err)
			return 1
		}
		fmt.Println(p)
		return 0
	}

	refPath, refData, err := resolveRefFont(*refFont)
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到比对字体:", err)
		return 1
	}

	// 1) 拿到 HTML 和最终 URL
	var (
		html   string
		final  *url.URL
		work   *cx.Work
		client *cx.Client
		source string
	)
	debug := io.Writer(os.Stderr)
	if os.Getenv("CX_DEBUG") == "" {
		debug = nil
	}

	switch {
	case *htmlPath != "":
		data, err := os.ReadFile(*htmlPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		html = string(data)
		source = *htmlPath
		if *pageURL != "" {
			final, _ = url.Parse(*pageURL)
		}
	case *rawURL != "":
		client = cx.New(debug)
		if *cookie != "" {
			if err := client.SetCookieHeader(*cookie, "mooc1-2.chaoxing.com", "mooc1-gray.chaoxing.com", "mooc1.chaoxing.com", "chaoxing.com"); err != nil {
				fmt.Fprintln(os.Stderr, "Cookie 解析失败:", err)
				return 1
			}
		}
		page, err := client.FetchWorkPage(ctx, *rawURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		html, final, source = page.HTML, page.URL, *rawURL
	case *courseID != 0 && *chapter != "":
		if *user == "" || *pass == "" {
			fmt.Fprintln(os.Stderr, "在线模式需要 -u/-p 或 CX_USERNAME/CX_PASSWORD")
			return 2
		}
		client = cx.New(debug)
		if err := client.Login(ctx, *user, *pass); err != nil {
			fmt.Fprintln(os.Stderr, "登录失败:", err)
			return 1
		}
		courses, err := client.Courses(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var course *cx.Course
		for i := range courses {
			if courses[i].CourseID == *courseID && (*classID == 0 || courses[i].ClassID == *classID) {
				course = &courses[i]
				break
			}
		}
		if course == nil {
			fmt.Fprintln(os.Stderr, "课程列表里没有该 courseId；可用课程：")
			for _, c := range courses {
				fmt.Fprintf(os.Stderr, "  course=%d class=%d cpi=%d  %s\n", c.CourseID, c.ClassID, c.CPI, c.Name)
			}
			return 1
		}
		if *cpi != 0 {
			course.CPI = *cpi
		}
		chs, err := client.Chapters(ctx, course)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var ch *cx.Chapter
		for i := range chs {
			if chs[i].ID == *chapter {
				ch = &chs[i]
				break
			}
		}
		if ch == nil {
			fmt.Fprintln(os.Stderr, "章节列表里没有该 chapterId；可用章节：")
			for _, c := range chs {
				fmt.Fprintf(os.Stderr, "  chapter=%s  %s / %s\n", c.ID, c.Section, c.Title)
			}
			return 1
		}
		content, err := client.ChapterContent(ctx, course, ch)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if len(content.Works) == 0 {
			fmt.Fprintln(os.Stderr, "该章节没有测验任务点")
			return 1
		}
		work = pickWork(content.Works, *workSel)
		if work == nil {
			fmt.Fprintln(os.Stderr, "找到多个测验，请用 -work 指定：")
			for _, w := range content.Works {
				fmt.Fprintf(os.Stderr, "  work=%s job=%s title=%s\n", w.WorkID, w.JobID, w.Title)
			}
			return 1
		}
		apiURL := cx.BuildWorkURL(work, "s")
		fmt.Fprintln(os.Stderr, "GET", apiURL)
		page, err := client.FetchWorkPage(ctx, apiURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		page.Work = work
		html, final, source = page.HTML, page.URL, apiURL
	default:
		fmt.Fprintln(os.Stderr, "需要 -html，或 -url，或 -course/-chapter 在线模式")
		fs.Usage()
		return 2
	}

	if *dumpHTML != "" {
		if err := os.WriteFile(*dumpHTML, []byte(html), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "保存 HTML 失败:", err)
			return 1
		}
	}
	page, err := cx.ParseWorkPage(html, final)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if page.Review {
		fmt.Fprintln(os.Stderr, "这是一个已批阅页面，没有题目可抽取（该测验已提交过）。")
		return 3
	}
	if len(page.Questions) == 0 {
		fmt.Fprintln(os.Stderr, "页面里没有解析到题目，可能不是答题页。")
		return 3
	}
	if page.Work == nil {
		page.Work = work
	}

	// 没有混淆字体的答题页本来就不需要还原，不能当成错误。
	var secret *cx.SecretFont
	var ttf []byte
	if page.HasFont {
		family, fontTTF, err := cx.ExtractSecretFont(html)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		ttf = fontTTF
		start := time.Now()
		var decoder *cx.FontDecoder
		if len(refData) > 0 {
			decoder, err = cx.NewFontDecoderBytes(refData, "embedded")
		} else {
			decoder, err = cx.NewFontDecoder(refPath)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "构建比对表失败:", err)
			return 1
		}
		secret, err = decoder.Decode(family, ttf, page.CarrierRunes())
		if err != nil {
			fmt.Fprintln(os.Stderr, "字体还原失败:", err)
			return 1
		}
		page.ApplyFont(secret)
		fmt.Fprintf(os.Stderr, "字体还原: %d 个载体字，比对耗时 %s\n", len(secret.Map), time.Since(start).Round(time.Millisecond))
	}

	out := &workOutput{
		Source:        source,
		QuestionCount: len(page.Questions),
		Questions:     page.Questions,
	}
	if secret != nil {
		out.Font = buildFontInfo(secret, refPath, ttf)
	}
	if w := page.WorkMetadata(); w != nil {
		out.Work = &workInfo{
			Title:       w.Title,
			CourseID:    w.CourseID,
			ClassID:     w.ClassID,
			CPI:         w.CPI,
			ChapterID:   w.ChapterID,
			UserID:      w.UserID,
			JobID:       w.JobID,
			OriginJobID: w.OriginJobID,
			WorkID:      w.WorkID,
			WorkType:    w.WorkType,
			Enc:         w.Enc,
			Passed:      w.IsPassed,
		}
	}
	if page.Form != nil {
		out.Submit = &submitInfo{
			Action:        page.Form.Action,
			SubmitURL:     page.Form.SubmitURL().String(),
			RandomOptions: page.Form.RandomOptions,
			Fields:        page.Form.Fields,
		}
	}
	if *aiFlag {
		answerer, err := cx.NewAIAnswererWith(*aiBase, *aiKey, *aiModel)
		if err != nil {
			fmt.Fprintln(os.Stderr, "答题模型不可用:", err)
			return 1
		}
		start := time.Now()
		answers, err := answerer.Answer(ctx, page.Questions)
		if err != nil {
			fmt.Fprintln(os.Stderr, "自动作答失败:", err)
			return 1
		}
		out.AIAnswers = answers
		fmt.Fprintf(os.Stderr, "自动作答: %d 题，模型 %s，耗时 %s\n", len(answers), answerer.Model, time.Since(start).Round(time.Millisecond))
	}
	if *fontOut != "" {
		if err := os.WriteFile(*fontOut, ttf, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "保存字体失败:", err)
			return 1
		}
		out.Font.TTF = *fontOut
	}

	data, _ := json.MarshalIndent(out, "", "  ")
	if *outPath != "" {
		if err := os.WriteFile(*outPath, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "题目 %d 道 -> %s\n", len(page.Questions), *outPath)
	} else {
		fmt.Println(string(data))
	}
	if *mdPath != "" {
		if err := os.WriteFile(*mdPath, []byte(workMarkdown(page.Questions)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "Markdown -> %s\n", *mdPath)
	}

	if *submit {
		if client == nil {
			fmt.Fprintln(os.Stderr, "-submit 需要 -url/-cookie 或在线登录模式")
			return 2
		}
		answers := map[string]string{}
		if *aiFlag {
			for qid, vals := range out.AIAnswers {
				answers[qid] = strings.Join(vals, "")
			}
		} else {
			var err error
			answers, err = loadAnswers(*answersF)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		res, err := client.SubmitWork(ctx, page, answers)
		if res != nil {
			fmt.Fprintf(os.Stderr, "提交: status=%v stuStatus=%d msg=%s url=%s\n", res.Status, res.StuStatus, res.Msg, res.URL)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "提交失败:", err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "提交成功")
		// 回查批阅页，确认这次提交真的生效、答对了几题。
		if page.Work != nil {
			if review, rerr := client.FetchWorkReview(ctx, page.Work); rerr != nil {
				fmt.Fprintln(os.Stderr, "批阅回查失败:", rerr)
			} else {
				out.Review = review
				fmt.Fprintf(os.Stderr, "批阅结果: %s\n", review)
				if *outPath != "" {
					if body, e := json.MarshalIndent(out, "", "  "); e == nil {
						_ = os.WriteFile(*outPath, body, 0o644)
					}
				}
			}
		}
	}
	return 0
}

func pickWork(works []*cx.Work, sel string) *cx.Work {
	if len(works) == 1 {
		return works[0]
	}
	if sel == "" {
		return nil
	}
	for _, w := range works {
		if w.WorkID == sel || w.JobID == sel || strings.Contains(w.Title, sel) {
			return w
		}
	}
	return nil
}

func loadAnswers(path string) (map[string]string, error) {
	if path == "" {
		return nil, fmt.Errorf("-submit 需要 -answers 答案 JSON")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("答案 JSON 解析失败: %w", err)
	}
	return m, nil
}

func buildFontInfo(f *cx.SecretFont, refPath string, ttf []byte) *fontInfo {
	fi := &fontInfo{
		Family:        f.Family,
		Bytes:         len(ttf),
		SHA1:          f.SHA1(),
		Carriers:      len(f.Map),
		SourceFont:    filepath.Base(refPath),
		CharMap:       map[string]glyphInfo{},
		LowConfidence: map[string]glyphInfo{},
	}
	for carrier, d := range f.Detail {
		gi := glyphInfo{
			Char:       string(d.Char),
			Score:      d.Score,
			Margin:     d.Margin,
			Confidence: d.Confidence,
			RunnerUp:   string(d.RunnerUp),
		}
		for _, a := range d.Alternatives {
			gi.Alternatives = append(gi.Alternatives, string(a))
		}
		fi.CharMap[string(carrier)] = gi
		if d.Confidence == "low" {
			fi.LowConfidence[string(carrier)] = gi
		}
	}
	if len(fi.LowConfidence) == 0 {
		fi.LowConfidence = nil
	}
	return fi
}

func workMarkdown(qs []*cx.Question) string {
	var b strings.Builder
	b.WriteString("# 题目\n\n")
	section := ""
	for _, q := range qs {
		if q.Section != "" && q.Section != section {
			section = q.Section
			fmt.Fprintf(&b, "## %s\n\n", section)
		}
		fmt.Fprintf(&b, "**%s. [%s]** %s\n", q.Number, q.Type, q.Stem)
		if len(q.Uncertain) > 0 {
			var marks []string
			for _, u := range q.Uncertain {
				marks = append(marks, fmt.Sprintf("%s→%s(%s)", u.Raw, u.Char, strings.Join(u.Alternatives, ",")))
			}
			fmt.Fprintf(&b, "> 低置信：%s\n\n", strings.Join(marks, "、"))
		}
		for _, o := range q.Options {
			if o.Letter != "" && o.Letter != o.Value {
				fmt.Fprintf(&b, "- %s. %s （提交值 %s）\n", o.Letter, o.Text, o.Value)
			} else {
				fmt.Fprintf(&b, "- %s. %s\n", o.Letter, o.Text)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func ensureRefFont(ctx context.Context, explicit string, force bool) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	if v := os.Getenv("CX_REF_FONT"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, nil
		}
	}
	cache, _ := os.UserCacheDir()
	cachePath := filepath.Join(cache, "cxbrush", refFontName)
	if force || !exists(cachePath) {
		fmt.Fprintln(os.Stderr, "下载比对字体 ->", cachePath)
		if err := download(ctx, refFontURL, cachePath); err != nil {
			return "", err
		}
	}
	return cachePath, nil
}

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 1<<20
}

func download(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", cx.PCUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if st, err := os.Stat(tmp); err != nil || st.Size() < 1<<20 {
		os.Remove(tmp)
		return fmt.Errorf("下载的字体太小，可能不是字体文件")
	}
	return os.Rename(tmp, dst)
}
