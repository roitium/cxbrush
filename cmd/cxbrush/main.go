package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"cxbrush/internal/cx"
)

type state int

const (
	stLogin state = iota
	stLoading
	stCourses
	stChapters
	stRunning
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	boxStyle   = lipgloss.NewStyle().Padding(1, 2)
)

// ---------- list items ----------

type courseItem struct{ c cx.Course }

func (i courseItem) Title() string { return i.c.Name }
func (i courseItem) Description() string {
	return fmt.Sprintf("%s · courseId=%d classId=%d", i.c.Teacher, i.c.CourseID, i.c.ClassID)
}
func (i courseItem) FilterValue() string { return i.c.Name }

type chapterItem struct {
	ch       cx.Chapter
	selected bool
}

func (i *chapterItem) FilterValue() string { return i.ch.Section + " " + i.ch.Title }

// chapterDelegate.anchor 指向区间选择起点（可见列表下标，-1 表示未开始）。
type chapterDelegate struct{ anchor *int }

func (chapterDelegate) Height() int                         { return 1 }
func (chapterDelegate) Spacing() int                        { return 0 }
func (chapterDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d chapterDelegate) Render(w io.Writer, m list.Model, index int, li list.Item) {
	it := li.(*chapterItem)
	box := "[ ]"
	if it.selected {
		box = okStyle.Render("[✓]")
	}
	if *d.anchor >= 0 && index >= min(*d.anchor, m.Index()) && index <= max(*d.anchor, m.Index()) {
		box = titleStyle.Render(strings.Replace(box, " ", "·", 1))
	}
	var status string
	switch p := it.ch.Pending; {
	case p < 0:
		status = dimStyle.Render("未知")
	case p == 0:
		status = okStyle.Render("已完成")
	default:
		status = errStyle.Render(fmt.Sprintf("待完成 %d", p))
	}
	cursor := "  "
	if index == m.Index() {
		cursor = titleStyle.Render("> ")
	}
	title := it.ch.Title
	if it.ch.Section != "" {
		title = dimStyle.Render(it.ch.Section+" / ") + title
	}
	fmt.Fprintf(w, "%s%s %s  %s", cursor, box, title, status)
}

// ---------- messages ----------

type loginMsg struct{ err error }
type coursesMsg struct {
	courses []cx.Course
	err     error
}
type chaptersMsg struct {
	chapters []cx.Chapter
	cpi      int64
	err      error
}
type eventMsg struct{ ev cx.Event }
type eventsClosedMsg struct{}

// ---------- model ----------

type taskView struct {
	ev cx.EvProgress
}

type model struct {
	client *cx.Client
	opt    cx.RunOptions
	state  state
	width  int
	height int

	inputs    []textinput.Model
	focus     int
	err       string
	loading   string
	spinner   spinner.Model
	autoLogin bool

	courseList  list.Model
	chapterList list.Model
	anchor      *int
	course      cx.Course

	// 运行中
	cancel   context.CancelFunc
	events   chan cx.Event
	scan     cx.EvScan
	queued   *cx.EvQueued
	tasks    map[string]*taskView
	order    []string
	logs     []string
	done     int
	failed   int
	finished *cx.EvFinished
	bar      progress.Model
}

func newModel(client *cx.Client, user, pass string, opt cx.RunOptions) model {
	u := textinput.New()
	u.Placeholder = "手机号 / 账号"
	u.SetValue(user)
	p := textinput.New()
	p.Placeholder = "密码"
	p.EchoMode = textinput.EchoPassword
	p.SetValue(pass)
	// 已记住账号时直接聚焦密码框。
	focus := 0
	if user != "" {
		focus = 1
		p.Focus()
	} else {
		u.Focus()
	}

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	cl := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	cl.Title = "选择课程（回车进入，/ 搜索，q 退出）"
	cl.KeyMap.Quit.SetKeys("q")

	anchor := -1
	chl := list.New(nil, chapterDelegate{anchor: &anchor}, 0, 0)
	chl.Title = "选择章节"
	chl.KeyMap.Quit.SetKeys("q")
	chl.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys(" "), key.WithHelp("空格", "勾选")),
			key.NewBinding(key.WithKeys("shift+down"), key.WithHelp("shift+↑↓", "连续勾选")),
			key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "区间选择")),
			key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "整节勾选")),
			key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "全部未完成")),
			key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "反选")),
			key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "清空")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("回车", "开始")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "返回")),
		}
	}

	autoLogin := user != "" && pass != ""
	st := stLogin
	if autoLogin {
		st = stLoading
	}
	return model{
		client:      client,
		opt:         opt,
		state:       st,
		loading:     "登录中…",
		inputs:      []textinput.Model{u, p},
		focus:       focus,
		spinner:     sp,
		autoLogin:   autoLogin,
		courseList:  cl,
		chapterList: chl,
		anchor:      &anchor,
		tasks:       map[string]*taskView{},
		bar:         progress.New(progress.WithDefaultGradient(), progress.WithWidth(36)),
	}
}

func (m model) Init() tea.Cmd {
	if m.autoLogin {
		return tea.Batch(m.spinner.Tick, m.doLogin())
	}
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

func (m *model) doLogin() tea.Cmd {
	m.state = stLoading
	m.loading = "登录中…"
	user, pass := m.inputs[0].Value(), m.inputs[1].Value()
	return func() tea.Msg { return loginMsg{m.client.Login(context.Background(), user, pass)} }
}

func (m *model) loadCourses() tea.Cmd {
	m.state = stLoading
	m.loading = "获取课程列表…"
	return func() tea.Msg {
		cs, err := m.client.Courses(context.Background())
		return coursesMsg{cs, err}
	}
}

func (m *model) loadChapters() tea.Cmd {
	m.state = stLoading
	m.loading = "获取章节：" + m.course.Name
	course := m.course
	return func() tea.Msg {
		chs, err := m.client.Chapters(context.Background(), &course)
		return chaptersMsg{chs, course.CPI, err}
	}
}

func (m *model) start() tea.Cmd {
	var picked []cx.Chapter
	for _, li := range m.chapterList.Items() {
		if it := li.(*chapterItem); it.selected {
			picked = append(picked, it.ch)
		}
	}
	if len(picked) == 0 {
		m.err = "请先用空格勾选章节"
		return nil
	}
	m.err = ""
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = make(chan cx.Event, 256)
	m.state = stRunning
	m.scan, m.queued, m.finished = cx.EvScan{}, nil, nil
	m.tasks, m.order, m.logs = map[string]*taskView{}, nil, nil
	m.done, m.failed = 0, 0
	go m.client.Run(ctx, m.course, picked, m.opt, m.events)
	return waitEvent(m.events)
}

func waitEvent(ch <-chan cx.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return eventsClosedMsg{}
		}
		return eventMsg{ev}
	}
}

func (m *model) addLog(s string) {
	m.logs = append(m.logs, s)
	if len(m.logs) > 200 {
		m.logs = m.logs[len(m.logs)-200:]
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.courseList.SetSize(msg.Width-4, msg.Height-4)
		m.chapterList.SetSize(msg.Width-4, msg.Height-5)
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case loginMsg:
		if msg.err != nil {
			m.state, m.err = stLogin, "登录失败："+msg.err.Error()
			return m, nil
		}
		m.err = ""
		saveConfig(config{Username: strings.TrimSpace(m.inputs[0].Value())})
		return m, m.loadCourses()

	case coursesMsg:
		if msg.err != nil {
			m.state, m.err = stLogin, "获取课程失败："+msg.err.Error()
			return m, nil
		}
		items := make([]list.Item, len(msg.courses))
		for i, c := range msg.courses {
			items[i] = courseItem{c}
		}
		m.courseList.SetItems(items)
		if m.client.Name != "" {
			m.courseList.Title = fmt.Sprintf("%s 的课程（回车进入，/ 搜索，q 退出）", m.client.Name)
		}
		m.state = stCourses
		return m, nil

	case chaptersMsg:
		if msg.err != nil {
			m.state, m.err = stCourses, "获取章节失败："+msg.err.Error()
			return m, nil
		}
		m.course.CPI = msg.cpi
		items := make([]list.Item, len(msg.chapters))
		for i, ch := range msg.chapters {
			items[i] = &chapterItem{ch: ch}
		}
		m.chapterList.SetItems(items)
		m.chapterList.Select(0)
		*m.anchor = -1
		m.refreshChapterTitle()
		m.err = ""
		m.state = stChapters
		return m, nil

	case eventMsg:
		m.handleEvent(msg.ev)
		return m, waitEvent(m.events)

	case eventsClosedMsg:
		if m.finished == nil {
			m.addLog("已停止")
		}
		return m, nil
	}

	switch m.state {
	case stLogin:
		return m.updateLogin(msg)
	case stCourses:
		return m.updateCourses(msg)
	case stChapters:
		return m.updateChapters(msg)
	case stRunning:
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "q":
				if m.cancel != nil {
					m.cancel()
				}
				return m, tea.Quit
			case "esc":
				if m.cancel != nil {
					m.cancel()
				}
				return m, m.loadChapters()
			}
		}
	}
	return m, nil
}

func (m *model) handleEvent(ev cx.Event) {
	switch e := ev.(type) {
	case cx.EvLog:
		if e.Err {
			m.addLog(errStyle.Render("⚠ " + e.Text))
		} else {
			m.addLog(e.Text)
		}
	case cx.EvScan:
		m.scan = e
	case cx.EvQueued:
		m.queued = &e
		m.addLog(fmt.Sprintf("扫描完成：待刷视频 %d，待答测验 %d，完成文档 %d，跳过 %d", e.Videos, e.Works, e.Docs, e.Skipped))
	case cx.EvProgress:
		if t, ok := m.tasks[e.ID]; ok {
			t.ev = e
		} else {
			m.tasks[e.ID] = &taskView{ev: e}
			m.order = append(m.order, e.ID)
		}
	case cx.EvTaskDone:
		delete(m.tasks, e.ID)
		for i, id := range m.order {
			if id == e.ID {
				m.order = append(m.order[:i], m.order[i+1:]...)
				break
			}
		}
		if e.Passed {
			m.done++
			m.addLog(okStyle.Render("✅ 已通过：" + e.Name))
		} else {
			m.failed++
			m.addLog(errStyle.Render("❌ 失败：" + e.Name + "：" + e.Reason))
		}
	case cx.EvWorkDone:
		switch {
		case e.Skipped:
			m.addLog(fmt.Sprintf("⏭ 测验跳过：%s（%s）", e.Title, e.Reason))
		case e.Submitted:
			line := fmt.Sprintf("📝 测验已提交：%s（%d 题）", e.Title, e.Questions)
			if e.Wrong > 0 || e.Correct > 0 {
				line += fmt.Sprintf("，成绩 %g 分（对 %d / 错 %d）", e.Score, e.Correct, e.Wrong)
			}
			m.addLog(okStyle.Render(line))
		default:
			m.addLog(errStyle.Render(fmt.Sprintf("⚠ 测验未完成：%s：%s", e.Title, e.Reason)))
		}
	case cx.EvFinished:
		m.finished = &e
		m.addLog(okStyle.Render(fmt.Sprintf("🎉 全部结束：通过 %d，失败 %d，文档 %d，测验 %d", e.Passed, e.Failed, e.Docs, e.Works)))
	}
}

func (m model) updateLogin(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return m, tea.Quit
		case "tab", "shift+tab", "up", "down":
			m.inputs[m.focus].Blur()
			m.focus = 1 - m.focus
			m.inputs[m.focus].Focus()
			return m, textinput.Blink
		case "enter":
			if m.focus == 0 {
				m.inputs[0].Blur()
				m.focus = 1
				m.inputs[1].Focus()
				return m, textinput.Blink
			}
			return m, m.doLogin()
		}
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m model) updateCourses(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && m.courseList.FilterState() != list.Filtering && k.String() == "enter" {
		if it, ok := m.courseList.SelectedItem().(courseItem); ok {
			m.course = it.c
			return m, m.loadChapters()
		}
	}
	var cmd tea.Cmd
	m.courseList, cmd = m.courseList.Update(msg)
	return m, cmd
}

// toggleGroup：组内有未勾选项则全部勾选，否则全部取消。
func toggleGroup(items []*chapterItem) {
	anyUnselected := false
	for _, it := range items {
		anyUnselected = anyUnselected || !it.selected
	}
	for _, it := range items {
		it.selected = anyUnselected
	}
}

// visibleChapters 返回当前可见（经过 / 过滤后）的章节，批量操作都只作用于它们。
func (m *model) visibleChapters() []*chapterItem {
	vis := m.chapterList.VisibleItems()
	out := make([]*chapterItem, len(vis))
	for i, li := range vis {
		out[i] = li.(*chapterItem)
	}
	return out
}

func (m *model) refreshChapterTitle() {
	n := 0
	for _, li := range m.chapterList.Items() {
		if li.(*chapterItem).selected {
			n++
		}
	}
	title := fmt.Sprintf("选择章节 · %s · 已选 %d", m.course.Name, n)
	if *m.anchor >= 0 {
		title += " · 区间选择中（移动光标后再按 v 确认，esc 取消）"
	}
	m.chapterList.Title = title
}

func (m model) updateChapters(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && m.chapterList.FilterState() != list.Filtering {
		defer m.refreshChapterTitle()
		vis := m.visibleChapters()
		cur, hasCur := m.chapterList.SelectedItem().(*chapterItem)
		switch k.String() {
		case " ":
			if hasCur {
				cur.selected = !cur.selected
			}
			return m, nil
		case "shift+down", "shift+up", "J", "K":
			if hasCur {
				cur.selected = true
			}
			if k.String() == "shift+down" || k.String() == "J" {
				m.chapterList.CursorDown()
			} else {
				m.chapterList.CursorUp()
			}
			if it, ok := m.chapterList.SelectedItem().(*chapterItem); ok {
				it.selected = true
			}
			m.refreshChapterTitle()
			return m, nil
		case "v":
			if *m.anchor < 0 {
				*m.anchor = m.chapterList.Index()
			} else {
				lo, hi := min(*m.anchor, m.chapterList.Index()), max(*m.anchor, m.chapterList.Index())
				if hi < len(vis) {
					toggleGroup(vis[lo : hi+1])
				}
				*m.anchor = -1
			}
			return m, nil
		case "s":
			if hasCur {
				var group []*chapterItem
				for _, it := range vis {
					if it.ch.Section == cur.ch.Section {
						group = append(group, it)
					}
				}
				toggleGroup(group)
			}
			return m, nil
		case "a":
			var group []*chapterItem
			for _, it := range vis {
				if it.ch.Pending != 0 {
					group = append(group, it)
				}
			}
			toggleGroup(group)
			return m, nil
		case "i":
			for _, it := range vis {
				it.selected = !it.selected
			}
			return m, nil
		case "n":
			for _, it := range vis {
				it.selected = false
			}
			return m, nil
		case "enter":
			if m.chapterList.FilterState() == list.Unfiltered {
				*m.anchor = -1
				return m, m.start()
			}
		case "esc":
			if *m.anchor >= 0 {
				*m.anchor = -1
				return m, nil
			}
			if m.chapterList.FilterState() == list.Unfiltered {
				m.state, m.err = stCourses, ""
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.chapterList, cmd = m.chapterList.Update(msg)
	return m, cmd
}

func (m model) View() string {
	var b strings.Builder
	switch m.state {
	case stLogin:
		b.WriteString(titleStyle.Render("超星学习通登录") + "\n\n")
		b.WriteString("账号  " + m.inputs[0].View() + "\n")
		b.WriteString("密码  " + m.inputs[1].View() + "\n\n")
		b.WriteString(dimStyle.Render("tab 切换 · 回车登录 · esc 退出"))
	case stLoading:
		b.WriteString(m.spinner.View() + " " + m.loading)
	case stCourses:
		b.WriteString(m.courseList.View())
	case stChapters:
		b.WriteString(m.chapterList.View())
	case stRunning:
		b.WriteString(m.runningView())
	}
	if m.err != "" {
		b.WriteString("\n" + errStyle.Render(m.err))
	}
	return boxStyle.Render(b.String())
}

func (m model) runningView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("刷课中 · "+m.course.Name) + "\n")
	if m.queued == nil {
		b.WriteString(fmt.Sprintf("%s 扫描章节 %d/%d…\n", m.spinner.View(), m.scan.Done, m.scan.Total))
	} else {
		running := len(m.order)
		waiting := max(0, m.queued.Videos-m.done-m.failed-running)
		b.WriteString(fmt.Sprintf("视频 %d · 通过 %s · 失败 %s · 进行中 %d · 排队 %d · 并发 %d\n",
			m.queued.Videos, okStyle.Render(fmt.Sprint(m.done)), errStyle.Render(fmt.Sprint(m.failed)), running, waiting, m.opt.Concurrency))
	}
	b.WriteString("\n")
	for _, id := range m.order {
		e := m.tasks[id].ev
		pct := 0.0
		if e.Total > 0 {
			pct = float64(e.Sec) / float64(e.Total)
		}
		b.WriteString(e.Name + "\n")
		b.WriteString(fmt.Sprintf("  %s %s/%s %s\n", m.bar.ViewAs(pct), fmtSec(e.Sec), fmtSec(e.Total),
			dimStyle.Render(fmt.Sprintf("已确认 %s · %s · 下次上报 %ds", fmtSec(e.Confirmed), e.Status, e.Countdown))))
	}
	b.WriteString("\n" + dimStyle.Render("── 日志 ──") + "\n")
	n := max(5, m.height-10-3*len(m.order))
	start := max(0, len(m.logs)-n)
	for _, l := range m.logs[start:] {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("esc 停止并返回章节 · q 退出"))
	return b.String()
}

func fmtSec(s int) string { return fmt.Sprintf("%02d:%02d", s/60, s%60) }

func main() {
	if len(os.Args) > 1 && os.Args[1] == "work" {
		os.Exit(runWorkCLI(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(runServeCLI(os.Args[2:]))
	}
	defaultUser := os.Getenv("CX_USERNAME")
	if defaultUser == "" {
		defaultUser = loadConfig().Username
	}
	user := flag.String("u", defaultUser, "账号（默认使用环境变量 CX_USERNAME 或上次登录的账号）")
	pass := flag.String("p", os.Getenv("CX_PASSWORD"), "密码（也可用环境变量 CX_PASSWORD）")
	conc := flag.Int("c", 2, "并发视频数 (1-5)")
	docs := flag.Bool("docs", true, "顺便完成文档任务点")
	works := flag.Bool("works", true, "自动完成章节测验（抓题、还原字体、调用模型作答并提交）")
	aiBase := flag.String("ai-base", "", "答题模型 Base URL（默认取 magpie tokenflux provider）")
	aiKey := flag.String("ai-key", "", "答题模型 API Key（默认取 magpie tokenflux provider）")
	aiModel := flag.String("ai-model", "", "答题模型名（默认 deepseek/deepseek-flash）")
	logPath := flag.String("log", "cxbrush.log", "请求调试日志路径，空字符串表示不记录")
	flag.Parse()

	var debug io.Writer
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "打开日志失败:", err)
			os.Exit(1)
		}
		defer f.Close()
		debug = f
	}

	client := cx.New(debug)
	opt := cx.RunOptions{Concurrency: min(5, max(1, *conc)), DoDocs: *docs}
	if *works {
		answerer, err := cx.NewAIAnswererWith(*aiBase, *aiKey, *aiModel)
		if err != nil {
			fmt.Fprintln(os.Stderr, "自动答题已禁用：", err)
		} else {
			fontPath, fontData, ferr := resolveRefFont("")
			if ferr != nil {
				fmt.Fprintln(os.Stderr, "自动答题已禁用：找不到比对字体：", ferr)
			} else {
				opt.DoWorks, opt.Answerer, opt.FontPath, opt.FontData = true, answerer, fontPath, fontData
			}
		}
	}
	if _, err := tea.NewProgram(newModel(client, *user, *pass, opt), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
