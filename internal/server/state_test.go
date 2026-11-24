package server

import (
	"encoding/json"
	"testing"

	"cxbrush/internal/store"
)

func ev(kind string, data any) store.Event {
	b, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return store.Event{Kind: kind, Data: b}
}

func applyAll(t *testing.T, st *store.Store, taskID int64, evs ...store.Event) {
	t.Helper()
	for _, e := range evs {
		if err := applyEventState(st, taskID, e.Kind, e.Data); err != nil {
			t.Fatalf("apply %s: %v", e.Kind, err)
		}
	}
}

func items(t *testing.T, st *store.Store, taskID int64) []store.Item {
	t.Helper()
	out, err := st.Items(taskID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newTask(t *testing.T, st *store.Store) *store.Task {
	t.Helper()
	u, err := st.CreateUser("state", "x")
	if err != nil {
		t.Fatal(err)
	}
	task := &store.Task{UserID: u.ID, CourseName: "测试课程", Status: store.StatusRunning}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestStateVideoKeepsProgress(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := newTask(t, st)
	applyAll(t, st, task.ID,
		ev("progress", map[string]any{"id": "v1", "name": "1.1 视频", "chapter": "1.1", "confirmed": 12, "total": 30}),
		ev("task", map[string]any{"id": "v1", "name": "1.1 视频", "kind": "video", "chapter": "1.1", "passed": true}),
	)
	out := items(t, st, task.ID)
	if len(out) != 1 {
		t.Fatalf("want 1 item, got %d", len(out))
	}
	it := out[0]
	if it.State != "passed" || it.Kind != "video" || it.Chapter != "1.1" || it.Name != "1.1 视频" {
		t.Fatalf("unexpected item: %+v", it)
	}
	if it.Cur != 12 || it.Total != 30 {
		t.Fatalf("progress lost: %+v", it)
	}
	logs, _ := st.Logs(task.ID, 0)
	if len(logs) != 1 || logs[0].Text != "✓ 1.1 视频" || logs[0].Err {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestStateVideoFailedKeepsReason(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := newTask(t, st)
	applyAll(t, st, task.ID, ev("task", map[string]any{
		"id": "v2", "name": "1.2 视频", "kind": "video", "chapter": "1.2", "passed": false, "reason": "超时",
	}))
	out := items(t, st, task.ID)
	if len(out) != 1 || out[0].State != "failed" || out[0].Reason != "超时" {
		t.Fatalf("unexpected: %+v", out)
	}
	logs, _ := st.Logs(task.ID, 0)
	if len(logs) != 1 || !logs[0].Err || logs[0].Text != "✗ 1.2 视频 — 超时" {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestStateDocDone(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := newTask(t, st)
	applyAll(t, st, task.ID, ev("task", map[string]any{
		"id": "d1", "name": "1.3 文档", "kind": "doc", "chapter": "1.3", "passed": true,
	}))
	out := items(t, st, task.ID)
	if len(out) != 1 || out[0].Kind != "doc" || out[0].State != "passed" {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestStateWorkStates(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := newTask(t, st)
	applyAll(t, st, task.ID,
		ev("work", map[string]any{"chapter": "2.1", "title": "2.1 测验", "questions": 5, "submitted": true, "score": 100, "correct": 5, "wrong": 0}),
		ev("work", map[string]any{"chapter": "2.2", "title": "2.2 测验", "skipped": true, "reason": "已完成"}),
		ev("work", map[string]any{"chapter": "2.3", "title": "2.3 测验", "reason": "解析失败"}),
	)
	out := items(t, st, task.ID)
	if len(out) != 3 {
		t.Fatalf("want 3 items, got %d", len(out))
	}
	if out[0].ID != "work_2.1" || out[0].State != "passed" || out[0].Score != 100 || out[0].Correct != 5 {
		t.Fatalf("submitted work: %+v", out[0])
	}
	if out[1].State != "skipped" || out[1].Reason != "已完成" {
		t.Fatalf("skipped work: %+v", out[1])
	}
	if out[2].State != "failed" || out[2].Reason != "解析失败" {
		t.Fatalf("failed work: %+v", out[2])
	}
}

// plan 只补空缺，不能把已经开始/完成的任务点倒退回 pending。
func TestStatePlanDoesNotClobber(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := newTask(t, st)
	applyAll(t, st, task.ID,
		ev("progress", map[string]any{"id": "v1", "name": "1.1", "chapter": "c1", "confirmed": 30, "total": 60}),
		ev("plan", map[string]any{"items": []map[string]any{
			{"id": "v1", "name": "1.1", "kind": "video", "chapter": "c1", "state": "pending", "total": 60},
			{"id": "v2", "name": "1.2", "kind": "video", "chapter": "c1", "state": "pending", "total": 90},
		}}),
	)
	out := items(t, st, task.ID)
	if len(out) != 2 {
		t.Fatalf("want 2 items, got %+v", out)
	}
	if out[0].State != "running" || out[0].Cur != 30 {
		t.Fatalf("plan 覆盖了运行中的任务点: %+v", out[0])
	}
	if out[1].State != "pending" || out[1].Total != 90 {
		t.Fatalf("plan 条目: %+v", out[1])
	}
}

func TestStateIgnoresUnknownAndEmpty(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := newTask(t, st)
	applyAll(t, st, task.ID,
		ev("log", map[string]any{"msg": "hello"}),
		ev("work", map[string]any{"chapter": "3.1"}),
		ev("progress", map[string]any{"name": "no id"}),
		ev("scan", map[string]any{"done": 1, "total": 2}),
	)
	if out := items(t, st, task.ID); len(out) != 0 {
		t.Fatalf("want no items, got %+v", out)
	}
}

// 旧事件表在启动时折叠成状态表并被删除。
func TestMigrateLegacyEvents(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := newTask(t, st)
	// 直接建旧表并写入事件，模拟上一版的库。
	if _, err := st.DB().Exec(`CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT, task_id INTEGER NOT NULL, ts INTEGER NOT NULL,
		kind TEXT NOT NULL, data TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.Event{
		ev("progress", map[string]any{"id": "v1", "name": "1.1", "chapter": "c1", "confirmed": 30, "total": 60}),
		ev("log", map[string]any{"text": "历史日志"}),
	} {
		if _, err := st.DB().Exec(`INSERT INTO events(task_id, ts, kind, data) VALUES(?,?,?,?)`,
			task.ID, 1, e.Kind, string(e.Data)); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{st: st}
	if err := s.migrateLegacyEvents(); err != nil {
		t.Fatal(err)
	}
	out := items(t, st, task.ID)
	if len(out) != 1 || out[0].Cur != 30 {
		t.Fatalf("items = %+v", out)
	}
	logs, _ := st.Logs(task.ID, 0)
	if len(logs) != 1 || logs[0].Text != "历史日志" {
		t.Fatalf("logs = %+v", logs)
	}
	if _, err := st.DB().Exec(`SELECT 1 FROM events`); err == nil {
		t.Fatal("迁移后事件表应被删除")
	}
}
