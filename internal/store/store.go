// Package store 是 Web 版的最小持久层：用户、学习通凭据、任务与事件。
// 单文件 SQLite，纯 Go 驱动，无 cgo。
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store 是进程级的数据库句柄，并发安全（sql.DB 自带池，这里限制为单连接）。
type Store struct {
	db   *sql.DB
	aead cipher.AEAD
	key  []byte
}

// Open 打开（必要时创建）dir 下的 cxbrush.db 与 secret.key。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	key, err := loadKey(filepath.Join(dir, "secret.key"))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dir, "cxbrush.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 单连接：写入串行化，避免 SQLITE_BUSY；本工具的写入量很小。
	db.SetMaxOpenConns(1)
	s := &Store{db: db, aead: aead, key: key}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func loadKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != 32 {
			return nil, fmt.Errorf("%s 长度不对（应为 32 字节）", path)
		}
		return data, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("写入 secret.key: %w", err)
	}
	return key, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Key 返回 secret.key，供会话签名复用。
func (s *Store) Key() []byte { return append([]byte(nil), s.key...) }

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  username   TEXT NOT NULL UNIQUE,
  pass_hash  TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS chaoxing (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  cookie     BLOB NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tasks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  course_id   INTEGER NOT NULL,
  class_id    INTEGER NOT NULL,
  cpi         INTEGER NOT NULL,
  course_name TEXT NOT NULL,
  chapters    TEXT NOT NULL,
  do_works    INTEGER NOT NULL,
  status      TEXT NOT NULL,
  message     TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_user ON tasks(user_id, id DESC);
CREATE TABLE IF NOT EXISTS events (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  ts      INTEGER NOT NULL,
  kind    TEXT NOT NULL,
  data    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_task ON events(task_id, id);
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS prefs (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  tasks      INTEGER NOT NULL,
  videos     INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
`

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) CreateUser(username, passHash string) (*User, error) {
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO users(username, pass_hash, created_at) VALUES(?,?,?)`,
		username, passHash, now.Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, CreatedAt: now}, nil
}

func (s *Store) userFromRow(row *sql.Row) (*User, string, error) {
	var u User
	var ts int64
	var hash string
	if err := row.Scan(&u.ID, &u.Username, &hash, &ts); err != nil {
		return nil, "", err
	}
	u.CreatedAt = time.Unix(ts, 0)
	return &u, hash, nil
}

func (s *Store) UserByName(name string) (*User, string, error) {
	return s.userFromRow(s.db.QueryRow(`SELECT id, username, pass_hash, created_at FROM users WHERE username = ?`, name))
}

func (s *Store) UserByID(id int64) (*User, error) {
	u, _, err := s.userFromRow(s.db.QueryRow(`SELECT id, username, pass_hash, created_at FROM users WHERE id = ?`, id))
	return u, err
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// SetChaoxing 加密保存学习通 Cookie 请求头。
func (s *Store) SetChaoxing(userID int64, cookie string) error {
	enc, err := s.seal([]byte(cookie))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO chaoxing(user_id, cookie, updated_at) VALUES(?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET cookie=excluded.cookie, updated_at=excluded.updated_at`,
		userID, enc, time.Now().Unix())
	return err
}

func (s *Store) Chaoxing(userID int64) (string, error) {
	var enc []byte
	err := s.db.QueryRow(`SELECT cookie FROM chaoxing WHERE user_id = ?`, userID).Scan(&enc)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, err := s.open(enc)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// UsersWithCookies 返回所有已保存学习通 Cookie 的用户，供会话续期使用。
func (s *Store) UsersWithCookies() ([]int64, error) {
	rows, err := s.db.Query(`SELECT user_id FROM chaoxing ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

func (s *Store) open(enc []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(enc) < n {
		return nil, fmt.Errorf("密文太短")
	}
	return s.aead.Open(nil, enc[:n], enc[n:], nil)
}

// ---------- 运行配置 ----------

// AIConfig 是 Web 上可编辑的答题模型配置，Key 加密落库。
type AIConfig struct {
	Base  string `json:"base"`
	Key   string `json:"key"`
	Model string `json:"model"`
}

const aiConfigKey = "ai"

// AIConfig 读取已保存的模型配置；没有配置过时返回 (nil, nil)。
func (s *Store) AIConfig() (*AIConfig, error) {
	var enc []byte
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, aiConfigKey).Scan(&enc)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.open(enc)
	if err != nil {
		return nil, err
	}
	var cfg AIConfig
	if err := json.Unmarshal(plain, &cfg); err != nil {
		return nil, fmt.Errorf("解析模型配置: %w", err)
	}
	return &cfg, nil
}

// SetAIConfig 加密保存模型配置。
func (s *Store) SetAIConfig(cfg AIConfig) error {
	plain, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	enc, err := s.seal(plain)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, aiConfigKey, enc)
	return err
}

// ClearAIConfig 删除自定义配置，回退到环境变量。
func (s *Store) ClearAIConfig() error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, aiConfigKey)
	return err
}

// ---------- 每个用户的运行偏好 ----------

const (
	DefaultParallelTasks  = 2
	DefaultParallelVideos = 2
	MaxParallelTasks      = 4
	MaxParallelVideos     = 8
)

// Prefs 是用户自己的并行度：同时几个任务、每个任务内几路视频。
type Prefs struct {
	Tasks  int `json:"tasks"`
	Videos int `json:"videos"`
}

// Normalized 把缺失或越界的值夹回合理范围，读库时也能兜底。
func (p Prefs) Normalized() Prefs {
	if p.Tasks < 1 {
		p.Tasks = DefaultParallelTasks
	} else if p.Tasks > MaxParallelTasks {
		p.Tasks = MaxParallelTasks
	}
	if p.Videos < 1 {
		p.Videos = DefaultParallelVideos
	} else if p.Videos > MaxParallelVideos {
		p.Videos = MaxParallelVideos
	}
	return p
}

// Prefs 读取用户的并行度，没设置过时返回默认值。
func (s *Store) Prefs(userID int64) (Prefs, error) {
	def := Prefs{Tasks: DefaultParallelTasks, Videos: DefaultParallelVideos}
	var tasks, videos int
	err := s.db.QueryRow(`SELECT tasks, videos FROM prefs WHERE user_id = ?`, userID).Scan(&tasks, &videos)
	if err == sql.ErrNoRows {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	return Prefs{Tasks: tasks, Videos: videos}.Normalized(), nil
}

// SetPrefs 保存用户的并行度。
func (s *Store) SetPrefs(userID int64, p Prefs) error {
	p = p.Normalized()
	_, err := s.db.Exec(`INSERT INTO prefs(user_id, tasks, videos, updated_at) VALUES(?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET tasks=excluded.tasks, videos=excluded.videos, updated_at=excluded.updated_at`,
		userID, p.Tasks, p.Videos, time.Now().Unix())
	return err
}

type ChapterRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Task struct {
	ID         int64        `json:"id"`
	UserID     int64        `json:"userId"`
	CourseID   int64        `json:"courseId"`
	ClassID    int64        `json:"classId"`
	CPI        int64        `json:"cpi"`
	CourseName string       `json:"courseName"`
	Chapters   []ChapterRef `json:"chapters"`
	DoWorks    bool         `json:"doWorks"`
	Status     string       `json:"status"`
	Message    string       `json:"message,omitempty"`
	CreatedAt  time.Time    `json:"createdAt"`
	UpdatedAt  time.Time    `json:"updatedAt"`
}

const (
	StatusQueued   = "queued"
	StatusRunning  = "running"
	StatusDone     = "done"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
)

func Finished(status string) bool {
	return status == StatusDone || status == StatusFailed || status == StatusCanceled
}

func (s *Store) CreateTask(t *Task) error {
	now := time.Now()
	t.CreatedAt, t.UpdatedAt = now, now
	if t.Status == "" {
		t.Status = StatusQueued
	}
	chapters, _ := json.Marshal(t.Chapters)
	res, err := s.db.Exec(`INSERT INTO tasks(user_id, course_id, class_id, cpi, course_name, chapters, do_works, status, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		t.UserID, t.CourseID, t.ClassID, t.CPI, t.CourseName, string(chapters), boolInt(t.DoWorks), t.Status, now.Unix(), now.Unix())
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) UpdateTask(id int64, status, message string) error {
	_, err := s.db.Exec(`UPDATE tasks SET status = ?, message = ?, updated_at = ? WHERE id = ?`,
		status, message, time.Now().Unix(), id)
	return err
}

const taskCols = `id, user_id, course_id, class_id, cpi, course_name, chapters, do_works, status, message, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanTask(sc scanner) (*Task, error) {
	var t Task
	var chapters, status, message string
	var doWorks int
	var created, updated int64
	if err := sc.Scan(&t.ID, &t.UserID, &t.CourseID, &t.ClassID, &t.CPI, &t.CourseName,
		&chapters, &doWorks, &status, &message, &created, &updated); err != nil {
		return nil, err
	}
	t.Chapters = decodeChapters(chapters)
	t.DoWorks = doWorks == 1
	t.Status = status
	t.Message = message
	t.CreatedAt = time.Unix(created, 0)
	t.UpdatedAt = time.Unix(updated, 0)
	return &t, nil
}

func decodeChapters(raw string) []ChapterRef {
	var out []ChapterRef
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func (s *Store) Task(id int64) (*Task, error) {
	return scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id = ?`, id))
}

func (s *Store) Tasks(userID int64, limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+taskCols+` FROM tasks WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type Event struct {
	ID     int64           `json:"id"`
	TaskID int64           `json:"taskId"`
	TS     time.Time       `json:"ts"`
	Kind   string          `json:"kind"`
	Data   json.RawMessage `json:"data"`
}

func (s *Store) AddEvent(taskID int64, kind string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO events(task_id, ts, kind, data) VALUES(?,?,?,?)`,
		taskID, time.Now().UnixMilli(), kind, string(body))
	return err
}

func (s *Store) Events(taskID, afterID int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT id, task_id, ts, kind, data FROM events WHERE task_id = ? AND id > ? ORDER BY id LIMIT ?`,
		taskID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts int64
		var data string
		if err := rows.Scan(&e.ID, &e.TaskID, &ts, &e.Kind, &data); err != nil {
			return nil, err
		}
		e.TS = time.UnixMilli(ts)
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecoverInterrupted 把上次进程退出时仍在跑的任务标成失败，避免重启后卡在 running。
func (s *Store) RecoverInterrupted() (int64, error) {
	res, err := s.db.Exec(`UPDATE tasks SET status = ?, message = ?, updated_at = ? WHERE status IN (?, ?)`,
		StatusFailed, "服务重启，任务中断", time.Now().Unix(), StatusRunning, StatusQueued)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
