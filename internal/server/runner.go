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
	st             *store.Store
	fontPath       string
	fontData       []byte
	allowPrivateAI bool

	mu      sync.Mutex
	clients map[int64]*clientEntry
	cancels map[int64]context.CancelFunc
	sem     chan struct{}
	gates   map[int64]*userGate

	decOnce sync.Once
	dec     *cx.FontDecoder
	decErr  error

	aiMu   sync.Mutex
	ai     *cx.AIAnswerer
	aiHash string

	hubMu sync.Mutex
	hubs  map[int64]*hub
}

type clientEntry struct {
	hash string
	c    *cx.Client
}

// NewRunner 创建任务调度器。workers 是全进程同时运行任务数的硬上限，
// 每个用户自己的并行度存在 prefs 表里，两者取交集。
func NewRunner(st *store.Store, fontPath string, fontData []byte, workers int, allowPrivateAI bool) *Runner {
	return &Runner{
		st:             st,
		fontPath:       fontPath,
		fontData:       fontData,
		allowPrivateAI: allowPrivateAI,
		clients:        map[int64]*clientEntry{},
		cancels:        map[int64]context.CancelFunc{},
		sem:            make(chan struct{}, clampWorkers(workers)),
		gates:          map[int64]*userGate{},
		hubs:           map[int64]*hub{},
	}
}

// openHub 为一次运行建立进程内事件广播。
func (r *Runner) openHub(taskID int64) *hub {
	h := newHub()
	r.hubMu.Lock()
	r.hubs[taskID] = h
	r.hubMu.Unlock()
	return h
}

func (r *Runner) closeHub(taskID int64) {
	r.hubMu.Lock()
	h := r.hubs[taskID]
	delete(r.hubs, taskID)
	r.hubMu.Unlock()
	if h != nil {
		h.close()
	}
}

// HubOf 返回运行中任务的广播（没有则为 nil），SSE 从这里拿实时增量。
func (r *Runner) HubOf(taskID int64) *hub {
	r.hubMu.Lock()
	defer r.hubMu.Unlock()
	return r.hubs[taskID]
}

// HubSeq 返回当前事件序号，页面快照带上它，SSE 从该序号续接。
func (r *Runner) HubSeq(taskID int64) int64 {
	h := r.HubOf(taskID)
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

// publish 只在任务有活动 hub 时推送；任务结束后自然丢弃。
func (r *Runner) publish(taskID int64, kind string, data any) {
	if h := r.HubOf(taskID); h != nil {
		h.publish(kind, data)
	}
}

const maxWorkers = 32

func clampWorkers(n int) int {
	if n <= 0 {
		return 8
	}
	if n > maxWorkers {
		return maxWorkers
	}
	return n
}

// Ceiling 返回全进程允许同时运行的任务数上限。
func (r *Runner) Ceiling() int { return cap(r.sem) }

// userGate 限制单个用户同时运行的任务数；limit 可以随时调整。
type userGate struct {
	mu      sync.Mutex
	running int
	limit   int
}

func (g *userGate) setLimit(n int) {
	g.mu.Lock()
	g.limit = n
	g.mu.Unlock()
}

// acquire 等到用户级槽位或 ctx 取消。轮询间隔只影响排队时的反应速度。
func (g *userGate) acquire(ctx context.Context) bool {
	for {
		g.mu.Lock()
		if g.running < g.limit {
			g.running++
			g.mu.Unlock()
			return true
		}
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (g *userGate) release() {
	g.mu.Lock()
	if g.running > 0 {
		g.running--
	}
	g.mu.Unlock()
}

func (r *Runner) gateFor(userID int64, limit int) *userGate {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.gates[userID]
	if g == nil {
		g = &userGate{limit: limit}
		r.gates[userID] = g
		return g
	}
	g.setLimit(limit)
	return g
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

// Answerer 返回全局答题模型：配置由管理员在后台维护，所有用户共用一份。
func (r *Runner) Answerer() (*cx.AIAnswerer, error) {
	// 配置存 SQLite 并加密，环境变量由 NewAIAnswererWith 兜底。
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
	a, err := cx.NewAIAnswererWithOptions(base, key, model, r.allowPrivateAI)
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

	prefs, err := r.st.Prefs(t.UserID)
	if err != nil {
		prefs = store.Prefs{Tasks: store.DefaultParallelTasks, Videos: store.DefaultParallelVideos}
	}
	gate := r.gateFor(t.UserID, prefs.Tasks)

	go func() {
		if !gate.acquire(ctx) {
			r.finish(t.ID, store.StatusCanceled, "已取消")
			r.drop(t.ID)
			return
		}
		defer gate.release()
		select {
		case r.sem <- struct{}{}:
		case <-ctx.Done():
			r.finish(t.ID, store.StatusCanceled, "已取消")
			r.drop(t.ID)
			return
		}
		defer func() { <-r.sem }()
		r.run(ctx, t, prefs.Videos)
		r.drop(t.ID)
	}()
	return nil
}

func (r *Runner) drop(taskID int64) {
	r.mu.Lock()
	delete(r.cancels, taskID)
	r.mu.Unlock()
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
	if status != store.StatusDone {
		// 中断/取消时把还在跑的任务点收尾，界面不会继续显示「正在播放」。
		_ = r.st.ResolveRunningItems(taskID, "failed", message)
	}
	r.publish(taskID, "status", map[string]any{"status": status, "message": message})
}

func (r *Runner) run(ctx context.Context, t *store.Task, videos int) {
	r.openHub(t.ID)
	defer r.closeHub(t.ID)

	c, err := r.ClientFor(t.UserID)
	if err != nil {
		r.finish(t.ID, store.StatusFailed, err.Error())
		return
	}
	opt := cx.RunOptions{
		Concurrency: videos, DoDocs: true, DoReads: true, DoLive: t.DoLive, DoWorks: t.DoWorks,
		Cache: cx.NewAnswerCache(r.st),
	}
	if t.DoWorks {
		dec, derr := r.Decoder()
		if derr != nil {
			r.finish(t.ID, store.StatusFailed, "字体比对表不可用: "+derr.Error())
			return
		}
		ai, aerr := r.Answerer()
		if aerr != nil {
			r.finish(t.ID, store.StatusFailed, "答题模型不可用: "+aerr.Error())
			return
		}
		opt.Decoder = dec
		opt.Answerer = ai
	}

	_ = r.st.UpdateTask(t.ID, store.StatusRunning, "")
	r.publish(t.ID, "status", map[string]any{"status": store.StatusRunning})

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
				summary = fmt.Sprintf("通过 %d，失败 %d，文档 %d，阅读 %d，直播 %d，测验 %d", f.Passed, f.Failed, f.Docs, f.Reads, f.Lives, f.Works)
			}
			kind, data := eventPayload(e)
			if err := applyEventState(r.st, t.ID, kind, data); err != nil {
				log.Printf("任务 %d 写入状态失败: %v", t.ID, err)
			}
			r.publish(t.ID, kind, data)
			// 完成/失败同时在日志里留一行，前端只负责展示。
			if line, isErr, ok := logLineFor(kind, data); ok {
				r.publish(t.ID, "log", map[string]any{"text": line, "err": isErr})
			}
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
		return "queued", map[string]any{"videos": v.Videos, "docs": v.Docs, "reads": v.Reads, "lives": v.Lives, "skipped": v.Skipped, "works": v.Works}
	case cx.EvPlan:
		return "plan", map[string]any{"items": v.Items}
	case cx.EvProgress:
		return "progress", map[string]any{
			"id": v.ID, "name": v.Name, "chapter": v.Chapter, "sec": v.Sec, "confirmed": v.Confirmed,
			"kind": v.Kind, "total": v.Total, "rate": v.Rate, "status": v.Status,
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
		return "finished", map[string]any{"passed": v.Passed, "failed": v.Failed, "docs": v.Docs, "reads": v.Reads, "lives": v.Lives, "works": v.Works}
	}
	return "unknown", map[string]any{"value": fmt.Sprintf("%v", e)}
}
