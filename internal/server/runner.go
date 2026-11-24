package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"cxbrush/internal/cx"
	"cxbrush/internal/store"
)

var cookieHosts = []string{"mooc1-2.chaoxing.com", "mooc1-gray.chaoxing.com", "mooc1.chaoxing.com", "chaoxing.com"}

// Runner 负责跑任务：每个用户一个独立的 cx.Client（各自的 cookie jar），
// 整个进程共享一份字体比对表和答题模型。
type Runner struct {
	st       *store.Store
	fontPath string
	fontData []byte

	mu        sync.Mutex
	clients   map[int64]*clientEntry
	cancels   map[int64]context.CancelFunc
	sem       chan struct{}
	videoConc int

	decOnce sync.Once
	dec     *cx.FontDecoder
	decErr  error

	aiMu   sync.Mutex
	ai     *cx.AIAnswerer
	aiHash string
}

type clientEntry struct {
	hash string
	c    *cx.Client
}

// NewRunner 创建任务调度器。workers 是同时运行的任务数，videos 是单个任务内
// 同时上报的视频数；两者都会被夹到 1..maxParallel。
func NewRunner(st *store.Store, fontPath string, fontData []byte, workers, videos int) *Runner {
	return &Runner{
		st:        st,
		fontPath:  fontPath,
		fontData:  fontData,
		clients:   map[int64]*clientEntry{},
		cancels:   map[int64]context.CancelFunc{},
		sem:       make(chan struct{}, clampParallel(workers)),
		videoConc: clampParallel(videos),
	}
}

const maxParallel = 16

func clampParallel(n int) int {
	if n <= 0 {
		return 2
	}
	if n > maxParallel {
		return maxParallel
	}
	return n
}

// Parallelism 返回当前生效的并行度（同时任务数 / 单任务视频数）。
func (r *Runner) Parallelism() (tasks, videos int) {
	return cap(r.sem), r.videoConc
}

// ClientFor 返回该用户专属的学习通客户端；Cookie 变了会自动重建。
func (r *Runner) ClientFor(userID int64) (*cx.Client, error) {
	cookie, err := r.st.Chaoxing(userID)
	if err != nil {
		return nil, err
	}
	if cookie == "" {
		return nil, fmt.Errorf("还没有配置学习通 Cookie")
	}
	hash := cookieHash(cookie)

	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.clients[userID]; ok && e.hash == hash {
		return e.c, nil
	}
	c := cx.New(nil)
	if err := c.SetCookieHeader(cookie, cookieHosts...); err != nil {
		return nil, err
	}
	r.clients[userID] = &clientEntry{hash: hash, c: c}
	return c, nil
}

// StartKeepAlive 定期调用学习通 SSO 接口给所有用户的 Cookie 续期。
// 服务端的登录态是 7 天滑动窗口：只要在过期前续一次，就能一直用下去。
func (r *Runner) StartKeepAlive(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		r.refreshAllSessions(ctx, true)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.refreshAllSessions(ctx, false)
			}
		}
	}()
}

func (r *Runner) refreshAllSessions(ctx context.Context, announce bool) {
	ids, err := r.st.UsersWithCookies()
	if err != nil {
		log.Printf("会话续期：读取用户失败: %v", err)
		return
	}
	for _, id := range ids {
		c, err := r.ClientFor(id)
		if err != nil {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = c.RefreshSession(rctx)
		cancel()
		if err != nil {
			log.Printf("会话续期：用户 %d 失败（Cookie 可能已过期，需要重新登录）: %v", id, err)
			continue
		}
		header := c.CookieHeader(cookieHosts...)
		if !strings.Contains(header, "_uid=") {
			continue
		}
		if err := r.st.SetChaoxing(id, header); err != nil {
			log.Printf("会话续期：用户 %d 保存失败: %v", id, err)
			continue
		}
		r.mu.Lock()
		r.clients[id] = &clientEntry{hash: cookieHash(header), c: c}
		r.mu.Unlock()
		if announce {
			log.Printf("会话续期：用户 %d 已续期，有效期顺延 7 天", id)
		}
	}
}

func cookieHash(cookie string) string {
	sum := sha1.Sum([]byte(cookie))
	return hex.EncodeToString(sum[:])
}

func (r *Runner) Answerer() (*cx.AIAnswerer, error) {
	// 配置来自 Web 页面（存 SQLite），环境变量由 NewAIAnswererWith 兜底。
	var base, key, model string
	if cfg, err := r.st.AIConfig(); err != nil {
		return nil, fmt.Errorf("读取模型配置: %w", err)
	} else if cfg != nil {
		base, key, model = cfg.Base, cfg.Key, cfg.Model
	}
	sum := sha1.Sum([]byte(base + "\x00" + key + "\x00" + model))
	hash := hex.EncodeToString(sum[:])

	r.aiMu.Lock()
	defer r.aiMu.Unlock()
	if r.ai != nil && r.aiHash == hash {
		return r.ai, nil
	}
	a, err := cx.NewAIAnswererWith(base, key, model)
	if err != nil {
		return nil, err
	}
	r.ai, r.aiHash = a, hash
	return a, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Decoder 是进程级单例：约 200MB，必须所有用户共享。
func (r *Runner) Decoder() (*cx.FontDecoder, error) {
	r.decOnce.Do(func() {
		if len(r.fontData) > 0 {
			r.dec, r.decErr = cx.NewFontDecoderBytes(r.fontData, "embedded")
			return
		}
		if r.fontPath == "" {
			r.decErr = fmt.Errorf("没有配置比对字体")
			return
		}
		r.dec, r.decErr = cx.NewFontDecoder(r.fontPath)
	})
	return r.dec, r.decErr
}

// Start 把任务放进后台执行；同一任务不会重复启动。
func (r *Runner) Start(t *store.Task) error {
	r.mu.Lock()
	if _, ok := r.cancels[t.ID]; ok {
		r.mu.Unlock()
		return fmt.Errorf("任务已在运行")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancels[t.ID] = cancel
	r.mu.Unlock()

	go func() {
		select {
		case r.sem <- struct{}{}:
		case <-ctx.Done():
			r.finish(t.ID, store.StatusCanceled, "已取消")
			r.mu.Lock()
			delete(r.cancels, t.ID)
			r.mu.Unlock()
			return
		}
		defer func() { <-r.sem }()
		r.run(ctx, t)
		r.mu.Lock()
		delete(r.cancels, t.ID)
		r.mu.Unlock()
	}()
	return nil
}

func (r *Runner) Cancel(taskID int64) bool {
	r.mu.Lock()
	cancel, ok := r.cancels[taskID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (r *Runner) finish(taskID int64, status, message string) {
	_ = r.st.UpdateTask(taskID, status, message)
	_ = r.st.AddEvent(taskID, "status", map[string]any{"status": status, "message": message})
}

func (r *Runner) run(ctx context.Context, t *store.Task) {
	c, err := r.ClientFor(t.UserID)
	if err != nil {
		r.finish(t.ID, store.StatusFailed, err.Error())
		return
	}
	opt := cx.RunOptions{Concurrency: r.videoConc, DoDocs: true, DoWorks: t.DoWorks}
	if t.DoWorks {
		dec, derr := r.Decoder()
		if derr != nil {
			r.finish(t.ID, store.StatusFailed, "字体比对表不可用: "+derr.Error())
			return
		}
		_ = dec
		ai, aerr := r.Answerer()
		if aerr != nil {
			r.finish(t.ID, store.StatusFailed, "答题模型不可用: "+aerr.Error())
			return
		}
		opt.Answerer = ai
		opt.FontPath = r.fontPath
	}

	_ = r.st.UpdateTask(t.ID, store.StatusRunning, "")
	_ = r.st.AddEvent(t.ID, "status", map[string]any{"status": store.StatusRunning})

	course := cx.Course{CourseID: t.CourseID, ClassID: t.ClassID, CPI: t.CPI, Name: t.CourseName}
	chapters := make([]cx.Chapter, 0, len(t.Chapters))
	for _, ch := range t.Chapters {
		chapters = append(chapters, cx.Chapter{ID: ch.ID, Title: ch.Title})
	}

	out := make(chan cx.Event, 64)
	drained := make(chan struct{})
	summary := ""
	go func() {
		defer close(drained)
		for e := range out {
			if f, ok := e.(cx.EvFinished); ok {
				summary = fmt.Sprintf("通过 %d，失败 %d，文档 %d，测验 %d", f.Passed, f.Failed, f.Docs, f.Works)
			}
			kind, data := eventPayload(e)
			_ = r.st.AddEvent(t.ID, kind, data)
		}
	}()

	c.Run(ctx, course, chapters, opt, out)
	<-drained

	if ctx.Err() != nil {
		r.finish(t.ID, store.StatusCanceled, "已取消")
		return
	}
	if summary == "" {
		summary = "已完成"
	}
	r.finish(t.ID, store.StatusDone, summary)
}

// eventPayload 把内部事件转成前端友好的 JSON。
func eventPayload(e cx.Event) (string, any) {
	switch v := e.(type) {
	case cx.EvLog:
		return "log", map[string]any{"text": v.Text, "err": v.Err}
	case cx.EvScan:
		return "scan", map[string]any{"done": v.Done, "total": v.Total}
	case cx.EvQueued:
		return "queued", map[string]any{"videos": v.Videos, "docs": v.Docs, "skipped": v.Skipped, "works": v.Works}
	case cx.EvProgress:
		return "progress", map[string]any{
			"id": v.ID, "name": v.Name, "chapter": v.Chapter, "sec": v.Sec, "confirmed": v.Confirmed,
			"total": v.Total, "rate": v.Rate, "status": v.Status,
		}
	case cx.EvTaskDone:
		return "task", map[string]any{"id": v.ID, "name": v.Name, "kind": v.Kind, "chapter": v.Chapter, "passed": v.Passed, "reason": v.Reason}
	case cx.EvWorkDone:
		return "work", map[string]any{
			"chapter": v.Chapter, "title": v.Title, "questions": v.Questions,
			"submitted": v.Submitted, "skipped": v.Skipped, "reason": v.Reason,
			"score": v.Score, "correct": v.Correct, "wrong": v.Wrong,
		}
	case cx.EvFinished:
		return "finished", map[string]any{"passed": v.Passed, "failed": v.Failed, "docs": v.Docs, "works": v.Works}
	}
	return "unknown", map[string]any{"value": fmt.Sprintf("%v", e)}
}
