package cx

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const encSalt = "d_yHJ!$pdA~5"

// isdrag 事件
const (
	EvPlaying = 0
	EvDrag    = 1
	EvPause   = 2
	EvPlay    = 3
	EvEnded   = 4
)

type LogResp struct {
	IsPassed       bool `json:"isPassed"`
	VideoTimeLimit bool `json:"videoTimeLimit"`
	HasJobLimit    bool `json:"hasJobLimit"`
}

// RateLimitError 是 /multimedia/log 返回 403（学习时长达到上限或被限流）。
type RateLimitError struct {
	RestSec int
	Body    string
}

func (e *RateLimitError) Error() string {
	if e.RestSec > 0 {
		return fmt.Sprintf("学习时长已达上限，需休息 %d 秒", e.RestSec)
	}
	return "被服务端限流 (403): " + truncate(e.Body, 80)
}

func buildEnc(v *Video, playingSec int) string {
	raw := fmt.Sprintf("[%d][%s][%s][%s][%d][%s][%d][%s]",
		v.ClassID, v.UserID, v.JobID, v.ObjectID, playingSec*1000, encSalt, v.Total()*1000, v.ClipTime())
	sum := md5.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func formatRT(rt float64) string {
	if math.Abs(rt-0.9) < 1e-4 {
		return "0.9"
	}
	return strconv.FormatFloat(rt, 'g', 2, 64)
}

// SendLog 发送一次视频进度上报（心跳就是进度）。
func (c *Client) SendLog(ctx context.Context, v *Video, playingSec, event int) (*LogResp, error) {
	if v.DToken == "" {
		return nil, errors.New("缺少 dtoken")
	}
	start, end := v.ClipStart(), max(v.ClipStart(), v.ClipEnd())
	sec := max(start, playingSec)
	if end > 0 {
		sec = min(sec, end)
	}
	att := v.AttDuration
	if att <= 0 {
		att = v.Total()
	}
	params := [][2]string{
		{"clazzId", strconv.FormatInt(v.ClassID, 10)},
		{"playingTime", strconv.Itoa(sec)},
		{"duration", strconv.Itoa(v.Total())},
		{"clipTime", v.ClipTime()},
		{"objectId", v.ObjectID},
		{"otherInfo", v.OtherInfo},
		{"courseId", strconv.FormatInt(v.CourseID, 10)},
		{"jobid", v.JobID},
		{"userid", v.UserID},
		{"isdrag", strconv.Itoa(event)},
		{"view", "pc"},
		{"enc", buildEnc(v, sec)},
		{"rt", formatRT(v.RT)},
		{"videoFaceCaptureEnc", v.FaceCaptureEnc},
		{"dtype", v.DType},
		{"_t", strconv.FormatInt(time.Now().UnixMilli(), 10)},
		{"attDuration", strconv.Itoa(att)},
		{"attDurationEnc", v.AttDurationEnc},
		{"courseEngineInfo", "false"},
	}
	var qs []string
	for _, p := range params {
		qs = append(qs, p[0]+"="+url.QueryEscape(p[1]))
	}
	u := v.ReportURL + "/" + v.DToken + "?" + strings.Join(qs, "&")
	referer := videoReferer
	if v.DType == "Audio" {
		referer = audioReferer
	}
	headers := map[string]string{"Referer": referer, "Content-Type": "application/json"}

	data, resp, err := c.get(ctx, u, headers)
	if err != nil {
		return nil, err
	}
	body := string(data)
	// 只在真正掉登录时重登；403 是限流，不要重登。
	if resp.StatusCode == 401 || resp.StatusCode == 202 || isLoginPage(data, resp) ||
		(resp.StatusCode/100 == 2 && !strings.Contains(body, "isPassed")) {
		if err := c.relogin(ctx); err != nil {
			return nil, err
		}
		if data, resp, err = c.get(ctx, u, headers); err != nil {
			return nil, err
		}
		body = string(data)
	}
	if resp.StatusCode == 403 {
		rest, _ := strconv.Atoi(firstGroup(reRestTime, body))
		return nil, &RateLimitError{RestSec: rest, Body: body}
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("进度上报失败 (HTTP %d)", resp.StatusCode)
	}
	var r LogResp
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("上报响应解析失败: %s", truncate(body, 120))
	}
	return &r, nil
}

func (c *Client) CompleteDocument(ctx context.Context, d *Document) (bool, error) {
	q := url.Values{}
	q.Set("jobid", d.JobID)
	q.Set("knowledgeid", d.ChapterID)
	q.Set("courseid", strconv.FormatInt(d.CourseID, 10))
	q.Set("clazzid", strconv.FormatInt(d.ClassID, 10))
	q.Set("jtoken", d.JToken)
	q.Set("checkMicroTopic", "true")
	q.Set("microTopicId", "")
	q.Set("courseEngineInfo", "false")
	q.Set("_dc", strconv.FormatInt(time.Now().UnixMilli(), 10))
	data, code, err := c.getAuth(ctx, studyBase+"/mooc-ans/job/document?"+q.Encode(),
		map[string]string{"Referer": documentReferer, "X-Requested-With": "XMLHttpRequest"})
	if err != nil {
		return false, err
	}
	if code/100 != 2 {
		return false, fmt.Errorf("HTTP %d", code)
	}
	if root, err := decodeJSON(data); err == nil {
		if ok, has := boolv(root["status"]); has {
			return ok, nil
		}
	}
	body := string(data)
	return strings.Contains(body, `"status":true`) || strings.Contains(body, "ok"), nil
}

// CompleteRead 完成一个专题阅读任务点（insertreadV2）。
// 和文档任务点一样，服务端只认 jobid + jtoken，不需要真的打开专题页。
func (c *Client) CompleteRead(ctx context.Context, r *Read) (bool, error) {
	q := url.Values{}
	q.Set("jobid", r.JobID)
	q.Set("knowledgeid", r.ChapterID)
	q.Set("courseid", strconv.FormatInt(r.CourseID, 10))
	q.Set("clazzid", strconv.FormatInt(r.ClassID, 10))
	q.Set("jtoken", r.JToken)
	q.Set("checkMicroTopic", "true")
	q.Set("microTopicId", r.MicroTopicID)
	q.Set("courseEngineInfo", "false")
	q.Set("_dc", strconv.FormatInt(time.Now().UnixMilli(), 10))
	data, code, err := c.getAuth(ctx, studyBase+"/mooc-ans/job/readv2?"+q.Encode(),
		map[string]string{"Referer": readReferer, "X-Requested-With": "XMLHttpRequest"})
	if err != nil {
		return false, err
	}
	if code/100 != 2 {
		return false, fmt.Errorf("HTTP %d", code)
	}
	if root, err := decodeJSON(data); err == nil {
		ok, _ := boolv(root["status"])
		if !ok {
			if msg := strv(root["msg"]); msg != "" {
				return false, errors.New(msg)
			}
		}
		return ok, nil
	}
	return strings.Contains(string(data), `"status":true`), nil
}

// ---------- 调度 ----------

type Event any

type EvLog struct {
	Text string
	Err  bool
}

type EvScan struct {
	Done, Total int
}

type EvQueued struct {
	Videos, Docs, Reads, Skipped, Works int
}

type EvProgress struct {
	ID, Name  string
	Chapter   string
	Sec       int // 界面显示的进度
	Confirmed int // 服务端已确认的进度
	Total     int
	Rate      float64
	Countdown int
	Status    string
}

type EvTaskDone struct {
	ID, Name string
	Kind     string // video / doc
	Chapter  string
	Passed   bool
	Reason   string
}

type EvFinished struct {
	Passed, Failed, Docs, Reads int
	Works                       int
}

// EvWorkDone 是一个章节测验的作答结果。
type EvWorkDone struct {
	Chapter, Title string
	Questions      int
	Submitted      bool
	Skipped        bool
	Reason         string
	Score          float64
	Correct        int
	Wrong          int
}

type RunOptions struct {
	Concurrency int
	DoDocs      bool
	// DoReads 打开后，扫描到的专题阅读任务点会自动完成。
	DoReads bool
	// DoWorks 打开后，扫描到的章节测验会自动抓题、还原字体、调用模型作答并提交。
	DoWorks bool
	// Answerer 是答题模型；DoWorks 时必须提供。
	Answerer *AIAnswerer
	// FontPath 是比对用的完整中文字体；答题页没有混淆字体时可为空。
	FontPath string
	// FontData 是内嵌字体数据；优先于 FontPath，避免运行时依赖外部文件。
	FontData []byte
}

// Run 扫描所选章节，然后按合法倍速并发上报视频进度。结束后关闭 out。
func (c *Client) Run(ctx context.Context, course Course, chapters []Chapter, opt RunOptions, out chan<- Event) {
	defer close(out)
	emit := func(e Event) {
		select {
		case out <- e:
		case <-ctx.Done():
		}
	}
	logf := func(err bool, f string, a ...any) { emit(EvLog{Text: fmt.Sprintf(f, a...), Err: err}) }

	emit(EvScan{0, len(chapters)})
	contents, errs := c.ScanChapters(ctx, &course, chapters, func(done int) { emit(EvScan{done, len(chapters)}) })
	if ctx.Err() != nil {
		return
	}

	var queue []*Video
	var workQueue []*Work
	skipped, docsDone, readsDone := 0, 0, 0
	for i, content := range contents {
		ch := chapters[i]
		if errs[i] != nil {
			logf(true, "[%s] 读取失败：%v", ch.Title, errs[i])
			continue
		}
		for _, v := range content.Videos {
			switch {
			case v.IsPassed || !v.IsJob:
			case v.NeedFace:
				skipped++
				logf(true, "跳过需要人脸抓拍的视频：%s", v.Name)
			case !v.Reportable():
				skipped++
				logf(true, "视频不可上报（缺少 dtoken、jobid 或时长）：%s", v.Name)
			default:
				queue = append(queue, v)
			}
		}
		if opt.DoDocs {
			for _, d := range content.Docs {
				if !d.Completable() {
					continue
				}
				ok, err := c.CompleteDocument(ctx, d)
				reason := ""
				if err != nil {
					reason = err.Error()
				} else if !ok {
					reason = "接口未确认"
				}
				if reason != "" {
					logf(true, "文档完成失败：%s %v", d.Name, err)
				} else {
					docsDone++
					logf(false, "📄 已完成文档：%s", d.Name)
				}
				emit(EvTaskDone{ID: "doc_" + d.JobID, Name: d.Name, Kind: "doc", Chapter: d.ChapterID, Passed: reason == "", Reason: reason})
			}
		}
		if opt.DoReads {
			for _, r := range content.Reads {
				if !r.Completable() {
					continue
				}
				ok, err := c.CompleteRead(ctx, r)
				reason := ""
				if err != nil {
					reason = err.Error()
				} else if !ok {
					reason = "接口未确认"
				}
				if reason != "" {
					logf(true, "阅读完成失败：%s %v", r.Name, reason)
				} else {
					readsDone++
					logf(false, "📖 已完成阅读：%s", r.Name)
				}
				emit(EvTaskDone{ID: "read_" + r.JobID, Name: r.Name, Kind: "read", Chapter: r.ChapterID, Passed: reason == "", Reason: reason})
			}
		}
		if opt.DoWorks {
			for _, w := range content.Works {
				if w != nil && !w.IsPassed {
					workQueue = append(workQueue, w)
				}
			}
		}
		if content.Others > 0 {
			logf(false, "[%s] 有 %d 个直播等未支持的任务点，已跳过", ch.Title, content.Others)
		}
	}
	emit(EvQueued{Videos: len(queue), Docs: docsDone, Reads: readsDone, Skipped: skipped, Works: len(workQueue)})

	var (
		mu                    sync.Mutex
		passed, failed, works int
		wg                    sync.WaitGroup
		sem                   = make(chan struct{}, max(1, opt.Concurrency))
	)

	if opt.DoWorks && len(workQueue) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var (
				dec    *FontDecoder
				loaded bool
			)
			ensureDecoder := func() *FontDecoder {
				if loaded {
					return dec
				}
				loaded = true
				if len(opt.FontData) == 0 && opt.FontPath == "" {
					return nil
				}
				var (
					d   *FontDecoder
					err error
				)
				if len(opt.FontData) > 0 {
					d, err = NewFontDecoderBytes(opt.FontData, "embedded")
				} else {
					d, err = NewFontDecoder(opt.FontPath)
				}
				if err != nil {
					logf(true, "加载比对字体失败：%v", err)
					return nil
				}
				dec = d
				return dec
			}
			for _, w := range workQueue {
				if ctx.Err() != nil {
					return
				}
				title := firstNonEmpty(w.Title, w.WorkID, w.JobID)
				logf(false, "[%s] 发现测验：%s，开始自动作答", w.ChapterID, title)
				res, err := c.SolveWork(ctx, w, ensureDecoder(), opt.Answerer)
				if err != nil {
					logf(true, "[%s] 测验 %s 作答失败：%v", w.ChapterID, title, err)
					emit(EvWorkDone{Chapter: w.ChapterID, Title: title, Reason: err.Error()})
					continue
				}
				ev := EvWorkDone{Chapter: w.ChapterID, Title: title}
				if res.Page != nil {
					ev.Questions = len(res.Page.Questions)
				}
				switch {
				case res.Skipped:
					ev.Skipped, ev.Reason = true, res.SkipReason
					logf(false, "[%s] 测验 %s 跳过：%s", w.ChapterID, title, res.SkipReason)
				case res.Submitted:
					ev.Submitted = true
					detail := fmt.Sprintf("%d 题已提交", ev.Questions)
					if res.FontChars > 0 {
						detail = fmt.Sprintf("还原 %d 个字体字符，%s", res.FontChars, detail)
					}
					if res.Review != nil {
						ev.Score, ev.Correct, ev.Wrong = res.Review.Score, res.Review.Correct, res.Review.Wrong
						detail += "，成绩 " + res.Review.String()
					} else if res.ReviewErr != nil {
						detail += "，批阅回查失败：" + res.ReviewErr.Error()
					}
					logf(false, "[%s] 测验 %s：%s", w.ChapterID, title, detail)
					mu.Lock()
					works++
					mu.Unlock()
				}
				emit(ev)
			}
		}()
	}
	for _, v := range queue {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(v *Video) {
			defer wg.Done()
			defer func() { <-sem }()
			ok, err := c.watchVideo(ctx, v, emit)
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			if ok {
				passed++
			} else {
				failed++
			}
			mu.Unlock()
			reason := ""
			if err != nil {
				reason = err.Error()
			}
			emit(EvTaskDone{ID: v.ID(), Name: v.Name, Kind: "video", Chapter: v.ChapterID, Passed: ok, Reason: reason})
		}(v)
	}
	wg.Wait()
	if ctx.Err() == nil {
		mu.Lock()
		worksDone := works
		mu.Unlock()
		emit(EvFinished{Passed: passed, Failed: failed, Docs: docsDone, Reads: readsDone, Works: worksDone})
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// watchVideo 复刻 runLegalVideoWorker：真实时间 × 允许倍速推进，每个间隔上报一次。
func (c *Client) watchVideo(ctx context.Context, v *Video, emit func(Event)) (bool, error) {
	clipStart, clipEnd := v.ClipStart(), v.ClipEnd()
	reported := clipStart
	if head := v.HeadOffsetMs / 1000; head > clipStart && head < max(clipStart+1, clipEnd-3) {
		reported = head
	}
	interval := min(60, max(15, v.ReportInterval))
	rate := v.MaxRate()
	prog := func(sec, countdown int, status string) {
		emit(EvProgress{ID: v.ID(), Name: v.Name, Chapter: v.ChapterID, Sec: sec, Confirmed: reported, Total: clipEnd, Rate: rate, Countdown: countdown, Status: status})
	}

	// 遇到 403 就按 restTime（默认 60 秒）休息，然后从当前位置重新发起播。
	restAndReplay := func(rl *RateLimitError) error {
		wait := rl.RestSec
		if wait <= 0 {
			wait = 60
		}
		wait = min(wait, 600)
		for i := wait; i > 0; i-- {
			prog(reported, i, fmt.Sprintf("403 限流，休息中 %ds", i))
			if err := sleepCtx(ctx, time.Second); err != nil {
				return err
			}
		}
		_, err := c.SendLog(ctx, v, reported, EvPlay)
		return err
	}

	prog(reported, interval, "发送起播 play(3)")
	for {
		res, err := c.SendLog(ctx, v, reported, EvPlay)
		var rl *RateLimitError
		if errors.As(err, &rl) {
			if err := restAndReplay(rl); err != nil && ctx.Err() != nil {
				return false, err
			}
			continue
		}
		if err != nil {
			return false, err
		}
		if res.IsPassed {
			return true, nil
		}
		break
	}

	passes, consecutiveErrs := 0, 0
	for {
		remaining := max(1, clipEnd-reported)
		step := min(interval, max(1, int(math.Ceil(float64(remaining)/rate))))
		for s := 1; s <= step; s++ {
			if err := sleepCtx(ctx, time.Second); err != nil {
				return false, err
			}
			prog(min(clipEnd, reported+int(math.Floor(float64(s)*rate))), step-s, fmt.Sprintf("播放中 %.0fx", rate))
		}

		next := min(clipEnd, reported+int(math.Ceil(float64(step)*rate)))
		atEnd := next >= clipEnd
		ev := EvPlaying
		if atEnd {
			ev = EvEnded
		}
		res, err := c.SendLog(ctx, v, next, ev)
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		var rl *RateLimitError
		if errors.As(err, &rl) {
			if err := restAndReplay(rl); err != nil && ctx.Err() != nil {
				return false, err
			}
			continue
		}
		if err != nil {
			consecutiveErrs++
			emit(EvLog{Text: fmt.Sprintf("[%s] 上报重试 %d/3：%v", v.Name, consecutiveErrs, err), Err: true})
			if consecutiveErrs >= 3 {
				return false, err
			}
			if err := sleepCtx(ctx, 5*time.Second); err != nil {
				return false, err
			}
			continue
		}
		consecutiveErrs = 0
		reported = next
		prog(reported, interval, "已确认")
		if res.IsPassed {
			return true, nil
		}
		if res.VideoTimeLimit || res.HasJobLimit {
			return false, errors.New("触发服务端学习时长或任务点上限")
		}
		if atEnd {
			passes++
			if passes >= 2 {
				return false, errors.New("已完整播放 2 遍但服务端仍未标记通过")
			}
			reported = clipStart
			_, _ = c.SendLog(ctx, v, reported, EvPlay)
		}
	}
}
