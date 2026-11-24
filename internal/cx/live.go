package cx

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	zhiboBase = "https://zhibo.chaoxing.com"
	// liveReportInterval 与网页播放器一致：每分钟上报一次观看时长。
	liveReportInterval = 60 * time.Second
)

// LiveInfo 是 /ananas/live/liveinfo 返回的观看状态。
type LiveInfo struct {
	Title      string
	Duration   int     // 总时长（秒）
	WatchedSec int     // 平台已记录观看时长（秒）
	Percent    float64 // 平台确认的完成百分比（0-100）
	LiveStatus int     // 0 未开始 / 1 直播中 / 4 已结束
	IfReview   int     // 1 表示结束后不允许回放
	U          string  // 播放器鉴权参数
}

func (i *LiveInfo) Replayable() bool {
	return i.LiveStatus != 0 && !(i.LiveStatus == 4 && i.IfReview == 1)
}

// LiveInfo 拉取直播状态。直播点播的进度都以平台返回为准。
func (c *Client) LiveInfo(ctx context.Context, l *Live) (*LiveInfo, error) {
	q := url.Values{}
	q.Set("liveid", l.LiveID)
	q.Set("userid", l.UserID)
	q.Set("clazzid", strconv.FormatInt(l.ClassID, 10))
	q.Set("knowledgeid", l.ChapterID)
	q.Set("courseid", strconv.FormatInt(l.CourseID, 10))
	q.Set("jobid", l.JobID)
	q.Set("ut", "s")
	data, code, err := c.getAuth(ctx, studyBase+"/ananas/live/liveinfo?"+q.Encode(),
		map[string]string{"Referer": studyBase + "/ananas/modules/live/index.html", "X-Requested-With": "XMLHttpRequest"})
	if err != nil {
		return nil, err
	}
	if code/100 != 2 {
		return nil, fmt.Errorf("直播状态查询失败 (HTTP %d)", code)
	}
	return ParseLiveInfo(data)
}

// ParseLiveInfo 解析 liveinfo 的 JSON，兼容数值是字符串或数字。
func ParseLiveInfo(data []byte) (*LiveInfo, error) {
	root, err := decodeJSON(data)
	if err != nil {
		return nil, fmt.Errorf("直播状态不是 JSON: %s", truncate(string(data), 120))
	}
	if ok, has := boolv(root["status"]); has && !ok {
		msg := firstNonEmpty(strv(root["msg"]), "查询直播状态失败")
		return nil, errors.New(msg)
	}
	temp, _ := root["temp"].(map[string]any)
	detail, _ := temp["data"].(map[string]any)
	if detail == nil {
		return nil, errors.New("直播状态缺少 temp.data")
	}
	info := &LiveInfo{
		Title:      strv(detail["title"]),
		Duration:   intv(detail["duration"], 0),
		Percent:    floatv(detail["percentValue"], 0),
		LiveStatus: intv(detail["liveStatus"], 0),
		IfReview:   intv(detail["ifReview"], 0),
		U:          strv(temp["u"]),
	}
	if ms := int64v(detail["timeLong"]); ms > 0 {
		info.WatchedSec = int(ms / 1000)
	} else {
		info.WatchedSec = int(floatv(detail["timeLongValue"], 0) * 60)
	}
	if info.Percent <= 0 && info.Duration > 0 && info.WatchedSec > 0 {
		info.Percent = float64(info.WatchedSec) / float64(info.Duration) * 100
	}
	return info, nil
}

// LiveWarmup 打开一次播放器地址，让 zhibo.chaoxing.com 建立会话（Set-Cookie）。
func (c *Client) LiveWarmup(ctx context.Context, l *Live, u string) error {
	q := url.Values{}
	q.Set("courseId", strconv.FormatInt(l.CourseID, 10))
	q.Set("classId", strconv.FormatInt(l.ClassID, 10))
	q.Set("knowledgeId", l.ChapterID)
	q.Set("jobId", l.JobID)
	q.Set("userId", l.UserID)
	q.Set("rt", strconv.FormatFloat(l.RT, 'g', 3, 64))
	q.Set("livesetenc", l.LiveSetEnc)
	q.Set("isjob", "true")
	q.Set("watchingInCourse", "1")
	q.Set("customPara1", strconv.FormatInt(l.ClassID, 10)+"_"+strconv.FormatInt(l.CourseID, 10))
	q.Set("customPara2", l.AuthEnc)
	q.Set("isNotDrag", "1")
	q.Set("jobfs", "0")
	if u != "" {
		q.Set("u", u)
	}
	raw := zhiboBase + "/" + url.PathEscape(l.LiveID) + "?" + q.Encode()
	data, resp, err := c.get(ctx, raw, map[string]string{"Referer": studyBase + "/ananas/modules/live/index.html"})
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("直播页面准备失败 (HTTP %d)", resp.StatusCode)
	}
	if isLoginPage(data, resp) {
		return errors.New("直播页面要求重新登录")
	}
	return nil
}

// ReportLiveTime 上报一次观看时长。isStart=false 用于第一次。
func (c *Client) ReportLiveTime(ctx context.Context, l *Live, isStart bool) error {
	q := url.Values{}
	q.Set("streamName", l.StreamName)
	q.Set("vdoid", l.Vdoid)
	q.Set("userId", l.UserID)
	if isStart {
		q.Set("isStart", "1")
	} else {
		q.Set("isStart", "0")
	}
	q.Set("t", strconv.FormatInt(time.Now().UnixMilli(), 10))
	q.Set("courseId", strconv.FormatInt(l.CourseID, 10))
	data, resp, err := c.get(ctx, zhiboBase+"/saveTimePc?"+q.Encode(), map[string]string{
		"Referer":          zhiboBase + "/" + url.PathEscape(l.LiveID),
		"X-Requested-With": "XMLHttpRequest",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("直播时长上报失败 (HTTP %d)", resp.StatusCode)
	}
	body := strings.TrimSpace(string(data))
	if !strings.Contains(body, "success") {
		return fmt.Errorf("直播时长上报未被接受: %s", truncate(body, 80))
	}
	return nil
}

// LiveProgress 是给界面用的观看进度。
type LiveProgress struct {
	Watched, Total int
	Percent        float64
	Status         string
}

// WatchLive 按真实时长完成一个直播任务点。
// 平台只在每分钟上报后累计进度，所以这里按 60s 节奏上报，直到 percentValue 达到 rt。
func (c *Client) WatchLive(ctx context.Context, l *Live, onProgress func(LiveProgress)) (bool, error) {
	info, err := c.LiveInfo(ctx, l)
	if err != nil {
		return false, err
	}
	if info.LiveStatus == 0 {
		return false, errors.New("直播尚未开始")
	}
	if info.LiveStatus == 4 && info.IfReview == 1 {
		return false, errors.New("直播已结束且不允许回放")
	}
	target := l.RT * 100
	if target <= 0 || target > 100 {
		target = 90
	}
	status := "回放"
	if info.LiveStatus == 1 {
		status = "直播中"
	}
	if onProgress != nil {
		onProgress(LiveProgress{Watched: info.WatchedSec, Total: info.Duration, Percent: info.Percent, Status: status})
	}
	if info.Duration <= 0 {
		return false, errors.New("直播缺少总时长")
	}
	if info.Percent >= target {
		return true, nil
	}
	if err := c.LiveWarmup(ctx, l, info.U); err != nil {
		c.logf("直播页面准备失败（继续尝试上报）: %v", err)
	}

	watched := info.WatchedSec
	reports := 0
	failures := 0
	maxReports := info.Duration/60 + 3
	for reports < maxReports {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := c.ReportLiveTime(ctx, l, reports > 0); err != nil {
			failures++
			if failures >= 3 {
				return false, err
			}
		} else {
			failures = 0
		}
		reports++
		if reports*60 > watched {
			watched = reports * 60
		}
		if onProgress != nil {
			onProgress(LiveProgress{Watched: watched, Total: info.Duration, Percent: float64(watched) / float64(info.Duration) * 100, Status: status})
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(liveReportInterval):
		}
		next, err := c.LiveInfo(ctx, l)
		if err != nil {
			continue
		}
		info = next
		watched = max(watched, info.WatchedSec)
		if onProgress != nil {
			onProgress(LiveProgress{Watched: watched, Total: info.Duration, Percent: info.Percent, Status: status})
		}
		if info.Percent >= target {
			return true, nil
		}
	}
	if info.Percent >= target {
		return true, nil
	}
	return false, errors.New("直播时长已报满，但平台未确认完成")
}
