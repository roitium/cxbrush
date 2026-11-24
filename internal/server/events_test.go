package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cxbrush/internal/store"
)

func testTask(t *testing.T, s *Server) (*store.User, *store.Task) {
	t.Helper()
	u, err := s.st.CreateUser("streamer", "hash")
	if err != nil {
		t.Fatal(err)
	}
	task := &store.Task{UserID: u.ID, CourseName: "测试课程", Status: store.StatusRunning}
	if err := s.st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	return u, task
}

func authReq(t *testing.T, s *Server, u *store.User, method, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.newToken(u.ID)})
	return req
}

func TestTaskSnapshotAndStream(t *testing.T) {
	s := newTestServer(t, Options{})
	u, task := testTask(t, s)

	// 模拟 runner：写状态 + 推事件。
	h := s.runner.openHub(task.ID)
	defer s.runner.closeHub(task.ID)
	publish := func(kind string, data any) {
		if err := applyEventState(s.st, task.ID, kind, data); err != nil {
			t.Fatal(err)
		}
		h.publish(kind, data)
	}
	publish("plan", map[string]any{"items": []map[string]any{
		{"id": "v1", "name": "1.1 视频", "kind": "video", "chapter": "c1", "state": "pending", "total": 60},
	}})
	publish("progress", map[string]any{"id": "v1", "name": "1.1 视频", "chapter": "c1", "confirmed": 30, "total": 60})

	// 快照直接来自状态表，并带上 hub 序号。
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, authReq(t, s, u, http.MethodGet, fmt.Sprintf("/api/tasks/%d", task.ID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot status %d: %s", rec.Code, rec.Body.String())
	}
	var snap struct {
		Items []store.Item    `json:"items"`
		Logs  []store.TaskLog `json:"logs"`
		Seq   int64           `json:"seq"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Items) != 1 || snap.Items[0].State != "running" || snap.Items[0].Cur != 30 {
		t.Fatalf("items = %+v", snap.Items)
	}
	if snap.Seq != 2 {
		t.Fatalf("seq = %d, want 2", snap.Seq)
	}

	// SSE：从 0 续接，先回放缓冲，再推新事件，最后收到 end。
	srv := httptest.NewServer(s.handler)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/tasks/%d/events?after=0", srv.URL, task.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.newToken(u.ID)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	events := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				events <- strings.TrimPrefix(line, "event: ")
			}
		}
		close(events)
	}()

	waitEvent := func(want string) {
		t.Helper()
		select {
		case got, ok := <-events:
			if !ok {
				t.Fatalf("stream closed, want %q", want)
			}
			if got != want {
				t.Fatalf("got event %q, want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for %q", want)
		}
	}
	waitEvent("plan")
	waitEvent("progress")

	// 新的实时事件不需要落库也能立刻到达。
	h.publish("log", map[string]any{"text": "实时日志"})
	waitEvent("log")

	// 任务结束：hub 关闭，SSE 应该补一条 end。
	if err := s.st.UpdateTask(task.ID, store.StatusDone, "完成"); err != nil {
		t.Fatal(err)
	}
	s.runner.closeHub(task.ID)
	waitEvent("end")
}

func TestEventsForFinishedTaskEndsImmediately(t *testing.T) {
	s := newTestServer(t, Options{})
	u, task := testTask(t, s)
	if err := s.st.UpdateTask(task.ID, store.StatusDone, "已完成"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.handler)
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/api/tasks/%d/events?after=0", srv.URL, task.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.newToken(u.ID)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "event: end") {
			return
		}
	}
	t.Fatal("已结束的任务应立即收到 end")
}
