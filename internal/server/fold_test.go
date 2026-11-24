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

func foldAll(evs ...store.Event) []ItemView {
	idx := map[string]int{}
	var out []ItemView
	for _, e := range evs {
		foldEvent(&out, idx, e)
	}
	return out
}

func TestFoldEventVideoKeepsProgress(t *testing.T) {
	out := foldAll(
		ev("progress", map[string]any{
			"id": "task_video_1", "name": "1.1 视频", "chapter": "1.1",
			"confirmed": 12, "total": 30,
		}),
		ev("task", map[string]any{
			"id": "task_video_1", "name": "1.1 视频", "kind": "video", "chapter": "1.1", "passed": true,
		}),
	)
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
}

func TestFoldEventVideoFailedKeepsReason(t *testing.T) {
	out := foldAll(ev("task", map[string]any{
		"id": "task_video_2", "name": "1.2 视频", "kind": "video", "chapter": "1.2",
		"passed": false, "reason": "超时",
	}))
	if len(out) != 1 || out[0].State != "failed" || out[0].Reason != "超时" {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestFoldEventDocDone(t *testing.T) {
	out := foldAll(ev("task", map[string]any{
		"id": "task_doc_1", "name": "1.3 文档", "kind": "doc", "chapter": "1.3", "passed": true,
	}))
	if len(out) != 1 || out[0].Kind != "doc" || out[0].State != "passed" {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestFoldEventWorkStates(t *testing.T) {
	out := foldAll(
		ev("work", map[string]any{
			"chapter": "2.1", "title": "2.1 测验", "questions": 5,
			"submitted": true, "score": 100, "correct": 5, "wrong": 0,
		}),
		ev("work", map[string]any{
			"chapter": "2.2", "title": "2.2 测验", "skipped": true, "reason": "已完成",
		}),
		ev("work", map[string]any{
			"chapter": "2.3", "title": "2.3 测验", "reason": "解析失败",
		}),
	)
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

func TestFoldEventIgnoresUnknownAndEmpty(t *testing.T) {
	out := foldAll(
		ev("log", map[string]any{"msg": "hello"}),
		ev("work", map[string]any{"chapter": "3.1"}),
		ev("progress", map[string]any{"name": "no id"}),
	)
	if len(out) != 0 {
		t.Fatalf("want no items, got %+v", out)
	}
}

// TestTaskItemsPagesThroughEvents 覆盖 taskItems 按 500 条游标翻页的行为：
// 关键事件落在第二页时也必须折叠出来。
func TestTaskItemsPagesThroughEvents(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	u, err := st.CreateUser("fold", "x")
	if err != nil {
		t.Fatal(err)
	}
	task := &store.Task{UserID: u.ID, CourseName: "测试课程", Chapters: nil}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 520; i++ {
		if err := st.AddEvent(task.ID, "log", map[string]any{"msg": "filler"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []store.Event{
		ev("progress", map[string]any{
			"id": "task_video_9", "name": "9.9 视频", "chapter": "9.9", "confirmed": 5, "total": 10,
		}),
		ev("work", map[string]any{
			"chapter": "9.9", "title": "9.9 测验", "submitted": true, "score": 80,
		}),
	} {
		if err := st.AddEvent(task.ID, e.Kind, json.RawMessage(e.Data)); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{st: st}
	items, after, err := s.taskItems(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after == 0 {
		t.Fatal("taskItems 应返回最后一条事件的 ID")
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items after paging, got %+v", items)
	}
	if items[0].ID != "task_video_9" || items[0].State != "running" || items[0].Total != 10 {
		t.Fatalf("video item: %+v", items[0])
	}
	if items[1].ID != "work_9.9" || items[1].State != "passed" || items[1].Score != 80 {
		t.Fatalf("work item: %+v", items[1])
	}
}

// 扫描阶段的 plan 事件要能落进快照：刷新后仍能看到全部未开始的任务点。
func TestFoldEventPlan(t *testing.T) {
	out := foldAll(
		ev("plan", map[string]any{"items": []map[string]any{
			{"id": "v1", "name": "1.1 视频", "kind": "video", "chapter": "ch1", "state": "pending", "total": 600},
			{"id": "v2", "name": "1.2 视频", "kind": "video", "chapter": "ch1", "state": "passed", "cur": 300, "total": 300},
		}}),
		ev("task", map[string]any{"id": "v1", "name": "1.1 视频", "kind": "video", "chapter": "ch1", "passed": true}),
	)
	if len(out) != 2 {
		t.Fatalf("want 2 items, got %+v", out)
	}
	if out[0].ID != "v1" || out[0].State != "passed" || out[0].Total != 600 {
		t.Fatalf("video item: %+v", out[0])
	}
	if out[1].ID != "v2" || out[1].State != "passed" || out[1].Cur != 300 {
		t.Fatalf("passed item: %+v", out[1])
	}
}
