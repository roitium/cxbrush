package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"cxbrush/internal/cx"
	"cxbrush/internal/store"
)

//go:embed web/dist
var webFS embed.FS

const sessionCookie = "cxbrush_session"

type Options struct {
	Addr      string
	DataDir   string
	FontPath  string
	FontData  []byte
	AllowOpen bool // 允许任何人注册（否则只在没有任何用户时允许注册第一个）
	Workers   int  // 全进程同时运行任务数的硬上限
	// AllowPrivateAI 允许模型地址指向内网/回环（本地模型端点）。
	AllowPrivateAI bool
	// SecureCookie 强制会话 Cookie 带 Secure（反向代理终结 TLS 时打开）。
	SecureCookie bool
	// TrustProxy 信任 X-Forwarded-For / X-Forwarded-Proto（仅在反向代理后面打开）。
	TrustProxy bool
}

type Server struct {
	st      *store.Store
	runner  *Runner
	key     []byte
	opt     Options
	mux     *http.ServeMux
	handler http.Handler
	limiter *loginLimiter
}

func New(st *store.Store, opts Options) *Server {
	s := &Server{st: st, key: st.Key(), opt: opts, mux: http.NewServeMux(), limiter: newLoginLimiter()}
	s.runner = NewRunner(st, opts.FontPath, opts.FontData, opts.Workers, opts.AllowPrivateAI)
	s.routes()
	s.handler = s.guard(s.mux)
	return s
}

func Run(ctx context.Context, opts Options) error {
	st, err := store.Open(opts.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if n, err := st.RecoverInterrupted(); err == nil && n > 0 {
		log.Printf("恢复：%d 个中断的任务已标记为失败", n)
	}
	srv := New(st, opts)
	srv.runner.StartKeepAlive(ctx, time.Hour)

	httpSrv := &http.Server{
		Addr:              opts.Addr,
		Handler:           srv.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("cxbrush web 已启动: http://%s  （数据目录 %s）", opts.Addr, opts.DataDir)
		errCh <- httpSrv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) routes() {
	sub, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("GET /", http.FileServer(http.FS(sub)))

	s.mux.HandleFunc("POST /api/register", s.handleRegister)
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/me", s.auth(s.handleMe))

	s.mux.HandleFunc("POST /api/chaoxing/login", s.auth(s.handleChaoxingLogin))
	s.mux.HandleFunc("PUT /api/chaoxing", s.auth(s.handleSetCookie))
	s.mux.HandleFunc("GET /api/settings/ai", s.auth(s.handleGetAI))
	s.mux.HandleFunc("PUT /api/settings/ai", s.auth(s.handleSetAI))
	s.mux.HandleFunc("POST /api/settings/ai/test", s.auth(s.handleTestAI))
	s.mux.HandleFunc("GET /api/settings/parallel", s.auth(s.handleGetParallel))
	s.mux.HandleFunc("PUT /api/settings/parallel", s.auth(s.handleSetParallel))
	s.mux.HandleFunc("GET /api/courses", s.auth(s.handleCourses))
	s.mux.HandleFunc("GET /api/chapters", s.auth(s.handleChapters))

	s.mux.HandleFunc("GET /api/tasks", s.auth(s.handleTasks))
	s.mux.HandleFunc("POST /api/tasks", s.auth(s.handleCreateTask))
	s.mux.HandleFunc("GET /api/tasks/{id}", s.auth(s.handleTask))
	s.mux.HandleFunc("POST /api/tasks/{id}/cancel", s.auth(s.handleCancelTask))
	s.mux.HandleFunc("POST /api/tasks/{id}/retry", s.auth(s.handleRetryTask))
	s.mux.HandleFunc("GET /api/tasks/{id}/events", s.auth(s.handleEvents))
}

// ---------- 认证 ----------

type userKey struct{}

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey{}).(*store.User)
	return u
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			httpError(w, http.StatusUnauthorized, "未登录")
			return
		}
		uid, ok := s.parseToken(c.Value)
		if !ok {
			httpError(w, http.StatusUnauthorized, "登录已过期")
			return
		}
		u, err := s.st.UserByID(uid)
		if err != nil {
			httpError(w, http.StatusUnauthorized, "用户不存在")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	}
}

func (s *Server) newToken(uid int64) string {
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	body := fmt.Sprintf("%d.%d", uid, exp)
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) parseToken(tok string) (int64, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return 0, false
	}
	body := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[2])) != 1 {
		return 0, false
	}
	uid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return 0, false
	}
	return uid, true
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, uid int64) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.newToken(uid),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * 3600,
	})
}

// dummyHash 用于「用户不存在」时也跑一次 bcrypt，避免通过响应耗时枚举用户名。
var dummyHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	if !readJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 2 || len(req.Password) < 6 {
		httpError(w, http.StatusBadRequest, "用户名至少 2 位，密码至少 6 位")
		return
	}
	if n, _ := s.st.CountUsers(); n > 0 && !s.opt.AllowOpen {
		httpError(w, http.StatusForbidden, "已关闭注册（启动时加 -open 可放开）")
		return
	}
	key, ip := s.loginKey(r, "reg", req.Username)
	if s.tooMany(w, key, ip) {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	u, err := s.st.CreateUser(req.Username, string(hash))
	if err != nil {
		s.limiter.fail(key, ip)
		httpError(w, http.StatusBadRequest, "用户名已存在")
		return
	}
	s.limiter.success(key)
	s.setSession(w, r, u.ID)
	writeJSON(w, map[string]any{"user": u})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	if !readJSON(w, r, &req) {
		return
	}
	key, ip := s.loginKey(r, "login", req.Username)
	if s.tooMany(w, key, ip) {
		return
	}
	u, hash, err := s.st.UserByName(strings.TrimSpace(req.Username))
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		s.limiter.fail(key, ip)
		httpError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		s.limiter.fail(key, ip)
		httpError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	s.limiter.success(key)
	s.setSession(w, r, u.ID)
	writeJSON(w, map[string]any{"user": u})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cookieSecure(r), SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	cookie, _ := s.st.Chaoxing(u.ID)
	hasAI, aiModel := false, ""
	if a, err := s.runner.Answerer(u.ID); err == nil && a != nil {
		hasAI, aiModel = true, a.Model
	}
	prefs, _ := s.st.Prefs(u.ID)
	writeJSON(w, map[string]any{
		"user": u, "hasCookie": cookie != "", "hasAI": hasAI, "aiModel": aiModel,
		"parallel": map[string]int{
			"tasks": prefs.Tasks, "videos": prefs.Videos, "ceiling": s.runner.Ceiling(),
		},
	})
}

// ---------- 学习通数据 ----------

// handleChaoxingLogin 用手机号/密码登录学习通，成功后只保存会话 Cookie。
func (s *Server) handleChaoxingLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	u := userFrom(r)
	phone := strings.TrimSpace(req.Phone)
	if phone == "" || req.Password == "" {
		httpError(w, http.StatusBadRequest, "手机号和密码不能为空")
		return
	}
	key, ip := s.loginKey(r, "cx", phone)
	if s.tooMany(w, key, ip) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	c := cx.New(nil)
	if err := c.Login(ctx, phone, req.Password); err != nil {
		s.limiter.fail(key, ip)
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	courses, err := c.Courses(ctx)
	if err != nil {
		s.limiter.fail(key, ip)
		httpError(w, http.StatusBadRequest, "登录成功但读取课程失败: "+err.Error())
		return
	}
	cookie := c.CookieHeader(cookieHosts...)
	if !strings.Contains(cookie, "_uid=") && !strings.Contains(cookie, "UID=") && !strings.Contains(cookie, "_d=") {
		s.limiter.fail(key, ip)
		httpError(w, http.StatusBadGateway, "登录成功但没拿到会话 Cookie，请改用 Cookie 方式导入")
		return
	}
	if err := s.st.SetChaoxing(u.ID, cookie); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.limiter.success(key)
	writeJSON(w, map[string]any{"ok": true, "courses": len(courses), "name": c.Name})
}

func (s *Server) handleSetCookie(w http.ResponseWriter, r *http.Request) {
	var req struct{ Cookie string }
	if !readJSON(w, r, &req) {
		return
	}
	u := userFrom(r)
	cookie := strings.TrimSpace(req.Cookie)
	if cookie == "" {
		httpError(w, http.StatusBadRequest, "Cookie 不能为空")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	// 先校验再落库，避免一次粘贴错误把原来的 Cookie 覆盖掉。
	c := cx.New(nil)
	if err := c.SetCookieHeader(cookie, cookieHosts...); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	courses, err := c.Courses(ctx)
	if err != nil {
		httpError(w, http.StatusBadRequest, "Cookie 校验失败: "+err.Error())
		return
	}
	if err := s.st.SetChaoxing(u.ID, cookie); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "courses": len(courses)})
}

// ---------- 模型配置 ----------

func (s *Server) checkAIBase(ctx context.Context, base string) error {
	return cx.ValidateAIBaseURL(ctx, base, s.opt.AllowPrivateAI)
}

func (s *Server) handleGetAI(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	cfg, err := s.st.AIConfig(u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	base, model, source := "", "", "none"
	keyHint := ""
	switch {
	case cfg != nil && (cfg.Base != "" || cfg.Key != "" || cfg.Model != ""):
		source, base, model = "custom", cfg.Base, cfg.Model
		if cfg.Key != "" {
			keyHint = maskKey(cfg.Key)
		}
	case os.Getenv("CX_AI_BASE") != "" || os.Getenv("CX_AI_KEY") != "":
		source = "env"
		base, model = os.Getenv("CX_AI_BASE"), os.Getenv("CX_AI_MODEL")
	}
	if model == "" {
		model = cx.DefaultAIModel
	}
	writeJSON(w, map[string]any{
		"base": base, "model": model, "source": source,
		"custom": cfg != nil, "hasKey": keyHint != "", "keyHint": keyHint,
	})
}

func (s *Server) handleSetAI(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req struct {
		Base  string `json:"base"`
		Key   string `json:"key"`
		Model string `json:"model"`
		Clear bool   `json:"clear"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Clear {
		if err := s.st.ClearAIConfig(u.ID); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "cleared": true})
		return
	}
	base := strings.TrimRight(strings.TrimSpace(req.Base), "/")
	model := strings.TrimSpace(req.Model)
	key := strings.TrimSpace(req.Key)
	if base == "" || model == "" {
		httpError(w, http.StatusBadRequest, "接口地址和模型名不能为空")
		return
	}
	if key == "" {
		if old, err := s.st.AIConfig(u.ID); err == nil && old != nil {
			key = old.Key
		}
	}
	if key == "" {
		httpError(w, http.StatusBadRequest, "API Key 不能为空")
		return
	}
	if err := s.checkAIBase(r.Context(), base); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := store.AIConfig{Base: base, Key: key, Model: model}
	if err := s.st.SetAIConfig(u.ID, cfg); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "base": base, "model": model, "keyHint": maskKey(key)})
}

func (s *Server) handleTestAI(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var req struct {
		Base  string `json:"base"`
		Key   string `json:"key"`
		Model string `json:"model"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	base, key, model := req.Base, req.Key, req.Model
	if cfg, err := s.st.AIConfig(u.ID); err == nil && cfg != nil {
		base = firstNonEmpty(base, cfg.Base)
		key = firstNonEmpty(key, cfg.Key)
		model = firstNonEmpty(model, cfg.Model)
	}
	if err := s.checkAIBase(r.Context(), base); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := cx.NewAIAnswererWithOptions(base, key, model, s.opt.AllowPrivateAI)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	took, err := a.Ping(ctx)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "model": a.Model, "latencyMs": took.Milliseconds()})
}

// ---------- 并行度 ----------

func (s *Server) handleGetParallel(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	p, err := s.st.Prefs(u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"tasks": p.Tasks, "videos": p.Videos,
		"maxTasks": store.MaxParallelTasks, "maxVideos": store.MaxParallelVideos,
		"ceiling": s.runner.Ceiling(),
	})
}

func (s *Server) handleSetParallel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tasks  int `json:"tasks"`
		Videos int `json:"videos"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Tasks < 1 || req.Tasks > store.MaxParallelTasks {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("同时任务数需在 1-%d 之间", store.MaxParallelTasks))
		return
	}
	if req.Videos < 1 || req.Videos > store.MaxParallelVideos {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("视频并发需在 1-%d 之间", store.MaxParallelVideos))
		return
	}
	u := userFrom(r)
	if err := s.st.SetPrefs(u.ID, store.Prefs{Tasks: req.Tasks, Videos: req.Videos}); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "tasks": req.Tasks, "videos": req.Videos})
}

func maskKey(k string) string {
	if len(k) <= 4 {
		return "****"
	}
	return "****" + k[len(k)-4:]
}

type courseDTO struct {
	Name     string `json:"name"`
	Teacher  string `json:"teacher,omitempty"`
	CourseID int64  `json:"courseId"`
	ClassID  int64  `json:"classId"`
	CPI      int64  `json:"cpi"`
}

func (s *Server) handleCourses(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	c, err := s.runner.ClientFor(u.ID)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	courses, err := c.Courses(ctx)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]courseDTO, 0, len(courses))
	for _, co := range courses {
		out = append(out, courseDTO{Name: co.Name, Teacher: co.Teacher, CourseID: co.CourseID, ClassID: co.ClassID, CPI: co.CPI})
	}
	writeJSON(w, map[string]any{"courses": out})
}

type chapterDTO struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Section string `json:"section,omitempty"`
	Pending int    `json:"pending"`
}

func (s *Server) handleChapters(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	q := r.URL.Query()
	courseID, _ := strconv.ParseInt(q.Get("courseId"), 10, 64)
	classID, _ := strconv.ParseInt(q.Get("classId"), 10, 64)
	cpi, _ := strconv.ParseInt(q.Get("cpi"), 10, 64)
	if courseID == 0 || classID == 0 {
		httpError(w, http.StatusBadRequest, "缺少 courseId/classId")
		return
	}
	c, err := s.runner.ClientFor(u.ID)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	chs, err := c.Chapters(ctx, &cx.Course{CourseID: courseID, ClassID: classID, CPI: cpi})
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]chapterDTO, 0, len(chs))
	for _, ch := range chs {
		out = append(out, chapterDTO{ID: ch.ID, Title: ch.Title, Section: ch.Section, Pending: ch.Pending})
	}
	writeJSON(w, map[string]any{"chapters": out})
}

// ---------- 任务 ----------

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CourseID   int64              `json:"courseId"`
		ClassID    int64              `json:"classId"`
		CPI        int64              `json:"cpi"`
		CourseName string             `json:"courseName"`
		Chapters   []store.ChapterRef `json:"chapters"`
		DoWorks    bool               `json:"doWorks"`
		DoLive     bool               `json:"doLive"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	u := userFrom(r)
	if req.CourseID == 0 || req.ClassID == 0 || len(req.Chapters) == 0 {
		httpError(w, http.StatusBadRequest, "缺少课程或章节")
		return
	}
	t := &store.Task{
		UserID: u.ID, CourseID: req.CourseID, ClassID: req.ClassID, CPI: req.CPI,
		CourseName: req.CourseName, Chapters: req.Chapters, DoWorks: req.DoWorks, DoLive: req.DoLive,
	}
	if err := s.st.CreateTask(t); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.runner.Start(t); err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]any{"task": t})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	tasks, err := s.st.Tasks(u.ID, 50)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"tasks": tasks})
}

func (s *Server) taskOwned(w http.ResponseWriter, r *http.Request) (*store.Task, bool) {
	u := userFrom(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	t, err := s.st.Task(id)
	if err != nil || t.UserID != u.ID {
		httpError(w, http.StatusNotFound, "任务不存在")
		return nil, false
	}
	return t, true
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	t, ok := s.taskOwned(w, r)
	if !ok {
		return
	}
	items, err := s.taskItems(t.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"task": t, "items": items})
}

// ItemView 是任务内某个任务点（视频/文档/测验）的最新状态。
type ItemView struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	Chapter string  `json:"chapter,omitempty"`
	Name    string  `json:"name"`
	State   string  `json:"state"`
	Cur     int     `json:"cur,omitempty"`
	Total   int     `json:"total,omitempty"`
	Score   float64 `json:"score,omitempty"`
	Correct int     `json:"correct,omitempty"`
	Wrong   int     `json:"wrong,omitempty"`
	Reason  string  `json:"reason,omitempty"`
}

// taskItems 直接折叠已持久化的事件流，不额外维护投影表。
func (s *Server) taskItems(taskID int64) ([]ItemView, error) {
	idx := map[string]int{}
	var out []ItemView
	var after int64
	for {
		evs, err := s.st.Events(taskID, after, 500)
		if err != nil {
			return nil, err
		}
		if len(evs) == 0 {
			break
		}
		for _, e := range evs {
			after = e.ID
			foldEvent(&out, idx, e)
		}
	}
	return out, nil
}

func foldEvent(out *[]ItemView, idx map[string]int, e store.Event) {
	switch e.Kind {
	case "progress":
		var d struct {
			ID        string
			Name      string
			Kind      string
			Chapter   string
			Confirmed int
			Total     int
		}
		if json.Unmarshal(e.Data, &d) != nil || d.ID == "" {
			return
		}
		kind := d.Kind
		if kind == "" {
			kind = "video"
		}
		upsertItem(out, idx, ItemView{
			ID: d.ID, Kind: kind, Chapter: d.Chapter, Name: d.Name,
			State: "running", Cur: d.Confirmed, Total: d.Total,
		})
	case "task":
		var d struct {
			ID      string
			Name    string
			Kind    string
			Chapter string
			Passed  bool
			Reason  string
		}
		if json.Unmarshal(e.Data, &d) != nil || d.ID == "" {
			return
		}
		kind := d.Kind
		if kind == "" {
			kind = "video"
		}
		state := "failed"
		if d.Passed {
			state = "passed"
		}
		upsertItem(out, idx, ItemView{
			ID: d.ID, Kind: kind, Chapter: d.Chapter, Name: d.Name,
			State: state, Reason: d.Reason,
		})
	case "work":
		var d struct {
			Chapter   string
			Title     string
			Questions int
			Submitted bool
			Skipped   bool
			Reason    string
			Score     float64
			Correct   int
			Wrong     int
		}
		if json.Unmarshal(e.Data, &d) != nil || d.Title == "" {
			return
		}
		state := "failed"
		switch {
		case d.Submitted:
			state = "passed"
		case d.Skipped:
			state = "skipped"
		}
		upsertItem(out, idx, ItemView{
			ID: "work_" + d.Chapter, Kind: "work", Chapter: d.Chapter, Name: d.Title,
			State: state, Reason: d.Reason, Score: d.Score, Correct: d.Correct, Wrong: d.Wrong,
		})
	}
}

func upsertItem(out *[]ItemView, idx map[string]int, it ItemView) {
	if i, ok := idx[it.ID]; ok {
		prev := (*out)[i]
		// 后到的事件只覆盖有值的字段，避免把看过的进度清零。
		if it.Name == "" {
			it.Name = prev.Name
		}
		if it.Chapter == "" {
			it.Chapter = prev.Chapter
		}
		if it.Total == 0 && it.Kind == prev.Kind {
			it.Total = prev.Total
		}
		if it.Cur == 0 && it.Kind == prev.Kind {
			it.Cur = prev.Cur
		}
		(*out)[i] = it
		return
	}
	idx[it.ID] = len(*out)
	*out = append(*out, it)
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	t, ok := s.taskOwned(w, r)
	if !ok {
		return
	}
	if store.Finished(t.Status) {
		httpError(w, http.StatusBadRequest, "任务已结束")
		return
	}
	s.runner.Cancel(t.ID)
	writeJSON(w, map[string]any{"ok": true})
}

// handleRetryTask 用同样的参数新建一个任务，方便失败后重跑。
func (s *Server) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	t, ok := s.taskOwned(w, r)
	if !ok {
		return
	}
	if !store.Finished(t.Status) {
		httpError(w, http.StatusConflict, "任务还没结束")
		return
	}
	clone := &store.Task{
		UserID: t.UserID, CourseID: t.CourseID, ClassID: t.ClassID, CPI: t.CPI,
		CourseName: t.CourseName, Chapters: t.Chapters, DoWorks: t.DoWorks, DoLive: t.DoLive,
	}
	if err := s.st.CreateTask(clone); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.runner.Start(clone); err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]any{"task": clone})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	t, ok := s.taskOwned(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "不支持流式输出")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		evs, err := s.st.Events(t.ID, after, 200)
		if err != nil {
			return
		}
		for _, e := range evs {
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Kind, e.Data)
			after = e.ID
		}
		if len(evs) > 0 {
			flusher.Flush()
		}
		if cur, err := s.st.Task(t.ID); err == nil && store.Finished(cur.Status) && len(evs) == 0 {
			fmt.Fprintf(w, "event: end\ndata: {\"status\":%q,\"message\":%q}\n\n", cur.Status, cur.Message)
			flusher.Flush()
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// ---------- 工具 ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return false
	}
	return true
}
