package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateAddsDoLive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "cxbrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`CREATE TABLE tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL,
		course_id INTEGER NOT NULL, class_id INTEGER NOT NULL, cpi INTEGER NOT NULL,
		course_name TEXT NOT NULL, chapters TEXT NOT NULL, do_works INTEGER NOT NULL,
		status TEXT NOT NULL, message TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`)
	legacy.Close()
	if err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := st.CreateUser("bob", "hash")
	if err != nil {
		t.Fatal(err)
	}
	task := &Task{UserID: u.ID, CourseID: 1, ClassID: 2, CPI: 3, CourseName: "c", DoLive: true}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.Task(task.ID)
	if err != nil || !loaded.DoLive {
		t.Fatalf("loaded = %+v, err = %v", loaded, err)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	u, err := st.CreateUser("alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("alice", "hash"); err == nil {
		t.Fatal("重名用户应该失败")
	}
	got, hash, err := st.UserByName("alice")
	if err != nil || got.ID != u.ID || hash != "hash" {
		t.Fatalf("UserByName = %+v %q %v", got, hash, err)
	}

	if err := st.SetChaoxing(u.ID, `_uid=1; fid=2; secret=中文`); err != nil {
		t.Fatal(err)
	}
	if ids, err := st.UsersWithCookies(); err != nil || len(ids) != 1 || ids[0] != u.ID {
		t.Fatalf("UsersWithCookies = %v %v", ids, err)
	}
	cookie, err := st.Chaoxing(u.ID)
	if err != nil || cookie != `_uid=1; fid=2; secret=中文` {
		t.Fatalf("cookie round trip = %q %v", cookie, err)
	}
	// 密文不应包含明文。
	var enc []byte
	if err := st.db.QueryRow(`SELECT cookie FROM chaoxing WHERE user_id = ?`, u.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("fid=2")) {
		t.Fatal("Cookie 应该加密存储")
	}

	task := &Task{
		UserID: u.ID, CourseID: 1, ClassID: 2, CPI: 3, CourseName: "c",
		Chapters: []ChapterRef{{ID: "10", Title: "t1"}, {ID: "11", Title: "t2"}},
		DoWorks:  true,
		DoLive:   true,
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.Task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Chapters) != 2 || !loaded.DoWorks || !loaded.DoLive || loaded.Status != StatusQueued {
		t.Fatalf("task = %+v", loaded)
	}

	if err := st.PutAnswer("k1", `{"free":["北京"]}`); err != nil {
		t.Fatal(err)
	}
	if v, ok := st.GetAnswer("k1"); !ok || v != `{"free":["北京"]}` {
		t.Fatalf("answer cache = %q %v", v, ok)
	}
	if _, ok := st.GetAnswer("missing"); ok {
		t.Fatal("missing key should not exist")
	}

	if err := st.AddEvent(task.ID, "log", map[string]any{"text": "hi"}); err != nil {
		t.Fatal(err)
	}
	evs, err := st.Events(task.ID, 0, 10)
	if err != nil || len(evs) != 1 || evs[0].Kind != "log" {
		t.Fatalf("events = %+v %v", evs, err)
	}
	var payload map[string]string
	if err := json.Unmarshal(evs[0].Data, &payload); err != nil || payload["text"] != "hi" {
		t.Fatalf("event data = %s", evs[0].Data)
	}

	if err := st.UpdateTask(task.ID, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if n, err := st.RecoverInterrupted(); err != nil || n != 1 {
		t.Fatalf("RecoverInterrupted = %d %v", n, err)
	}
	loaded, _ = st.Task(task.ID)
	if loaded.Status != StatusFailed || !Finished(loaded.Status) {
		t.Fatalf("after recover = %+v", loaded)
	}

	tasks, err := st.Tasks(u.ID, 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("Tasks = %+v %v", tasks, err)
	}
}

func TestPrefs(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := st.CreateUser("bob", "hash")
	if err != nil {
		t.Fatal(err)
	}

	// 没设置过时是默认值。
	p, err := st.Prefs(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tasks != DefaultParallelTasks || p.Videos != DefaultParallelVideos {
		t.Fatalf("默认 Prefs = %+v", p)
	}

	if err := st.SetPrefs(u.ID, Prefs{Tasks: 3, Videos: 6}); err != nil {
		t.Fatal(err)
	}
	if p, err = st.Prefs(u.ID); err != nil || p.Tasks != 3 || p.Videos != 6 {
		t.Fatalf("Prefs = %+v %v", p, err)
	}

	// 越界的值会被夹回范围。
	if err := st.SetPrefs(u.ID, Prefs{Tasks: 99, Videos: -1}); err != nil {
		t.Fatal(err)
	}
	if p, err = st.Prefs(u.ID); err != nil || p.Tasks != MaxParallelTasks || p.Videos != DefaultParallelVideos {
		t.Fatalf("越界 Prefs = %+v %v", p, err)
	}
}

func TestAIConfigRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if cfg, err := st.AIConfig(); err != nil || cfg != nil {
		t.Fatalf("空库应返回 nil：%+v %v", cfg, err)
	}
	want := AIConfig{Base: "https://api.example.com/v1", Key: "sk-dummy-0000", Model: "deepseek/deepseek-flash"}
	if err := st.SetAIConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := st.AIConfig()
	if err != nil || got == nil || *got != want {
		t.Fatalf("AIConfig = %+v %v", got, err)
	}
	// Key 不能明文落库。
	var enc []byte
	if err := st.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, aiConfigKey).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("sk-dummy-0000")) {
		t.Fatal("模型配置应该加密存储")
	}

	if err := st.ClearAIConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg, err := st.AIConfig(); err != nil || cfg != nil {
		t.Fatalf("清除后应返回 nil：%+v %v", cfg, err)
	}
}
