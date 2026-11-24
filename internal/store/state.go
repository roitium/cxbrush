package store

import (
	"encoding/json"
	"strings"
	"time"
)

// Item 是任务内某个任务点的当前状态。任务运行期间每次进度上报只更新这一行，
// 不再把每条上报都追加成事件，所以页面打开与任务长度无关。
type Item struct {
	ID      string  `json:"id" db:"item_id"`
	Kind    string  `json:"kind" db:"kind"`
	Chapter string  `json:"chapter,omitempty" db:"chapter"`
	Name    string  `json:"name" db:"name"`
	State   string  `json:"state" db:"state"`
	Cur     int     `json:"cur,omitempty" db:"cur"`
	Total   int     `json:"total,omitempty" db:"total"`
	Score   float64 `json:"score,omitempty" db:"score"`
	Correct int     `json:"correct,omitempty" db:"correct"`
	Wrong   int     `json:"wrong,omitempty" db:"wrong"`
	Reason  string  `json:"reason,omitempty" db:"reason"`
}

// itemRow 是 Item 加上所属任务和更新时间，只用于写库。
type itemRow struct {
	TaskID    int64 `db:"task_id"`
	UpdatedAt int64 `db:"updated_at"`
	Item
}

// TaskLog 是任务的一行运行日志。
type TaskLog struct {
	ID   int64     `json:"id"`
	TS   time.Time `json:"ts"`
	Text string    `json:"text"`
	Err  bool      `json:"err,omitempty"`
}

// MaxTaskLogs 是每个任务保留的日志条数：日志只用于展示，不需要全量历史。
const MaxTaskLogs = 500

const itemCols = `item_id, kind, chapter, name, state, cur, total, score, correct, wrong, reason`

// UpsertItem 写入任务点最新状态。进度类事件频繁，走 UPSERT 而不是追加。
// 空字段/零值不会覆盖已有值，避免「完成」事件把之前的时长和进度清掉。
func (s *Store) UpsertItem(taskID int64, it Item) error {
	_, err := s.db.NamedExec(`INSERT INTO task_items(
			task_id, item_id, kind, chapter, name, state, cur, total, score, correct, wrong, reason, updated_at)
		VALUES (:task_id, :item_id, :kind, :chapter, :name, :state, :cur, :total, :score, :correct, :wrong, :reason, :updated_at)
		ON CONFLICT(task_id, item_id) DO UPDATE SET
			kind    = CASE WHEN excluded.kind != '' THEN excluded.kind ELSE task_items.kind END,
			chapter = CASE WHEN excluded.chapter != '' THEN excluded.chapter ELSE task_items.chapter END,
			name    = CASE WHEN excluded.name != '' THEN excluded.name ELSE task_items.name END,
			state   = CASE WHEN excluded.state != '' THEN excluded.state ELSE task_items.state END,
			cur     = CASE WHEN excluded.cur > 0 THEN excluded.cur ELSE task_items.cur END,
			total   = CASE WHEN excluded.total > 0 THEN excluded.total ELSE task_items.total END,
			score   = CASE WHEN excluded.score > 0 THEN excluded.score ELSE task_items.score END,
			correct = CASE WHEN excluded.correct > 0 THEN excluded.correct ELSE task_items.correct END,
			wrong   = CASE WHEN excluded.wrong > 0 THEN excluded.wrong ELSE task_items.wrong END,
			reason  = CASE WHEN excluded.reason != '' THEN excluded.reason ELSE task_items.reason END,
			updated_at = excluded.updated_at`,
		itemRow{TaskID: taskID, UpdatedAt: time.Now().Unix(), Item: it})
	return err
}

// InsertItemIfAbsent 只插入不存在的任务点。扫描计划先于执行发出，
// 用它避免把已经开始（甚至完成）的条目倒退成 pending。
func (s *Store) InsertItemIfAbsent(taskID int64, it Item) error {
	_, err := s.db.NamedExec(`INSERT INTO task_items(
			task_id, item_id, kind, chapter, name, state, cur, total, score, correct, wrong, reason, updated_at)
		VALUES (:task_id, :item_id, :kind, :chapter, :name, :state, :cur, :total, :score, :correct, :wrong, :reason, :updated_at)
		ON CONFLICT(task_id, item_id) DO NOTHING`,
		itemRow{TaskID: taskID, UpdatedAt: time.Now().Unix(), Item: it})
	return err
}

// Items 按扫描顺序返回任务的全部任务点。
func (s *Store) Items(taskID int64) ([]Item, error) {
	out := []Item{}
	if err := s.db.Select(&out, `SELECT `+itemCols+`
		FROM task_items WHERE task_id = ? ORDER BY id`, taskID); err != nil {
		return nil, err
	}
	return out, nil
}

type logRow struct {
	ID   int64  `db:"id"`
	TS   int64  `db:"ts"`
	Text string `db:"text"`
	Err  int    `db:"err"`
}

// AddLog 追加一行日志，并只保留最近 MaxTaskLogs 条。
func (s *Store) AddLog(taskID int64, text string, isErr bool) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	type logWrite struct {
		TaskID int64  `db:"task_id"`
		TS     int64  `db:"ts"`
		Text   string `db:"text"`
		Err    int    `db:"err"`
	}
	if _, err := s.db.NamedExec(`INSERT INTO task_logs(task_id, ts, text, err)
		VALUES (:task_id, :ts, :text, :err)`,
		logWrite{TaskID: taskID, TS: time.Now().UnixMilli(), Text: text, Err: boolInt(isErr)}); err != nil {
		return err
	}
	// 每个任务只留最近 MaxTaskLogs 条，日志量不随任务时长增长。
	_, err := s.db.Exec(`DELETE FROM task_logs WHERE task_id = ? AND id <=
		(SELECT COALESCE(MAX(id), 0) FROM task_logs WHERE task_id = ?) - ?`, taskID, taskID, MaxTaskLogs)
	return err
}

// Logs 返回最近 limit 条日志，按时间正序。
func (s *Store) Logs(taskID int64, limit int) ([]TaskLog, error) {
	if limit <= 0 || limit > MaxTaskLogs {
		limit = MaxTaskLogs
	}
	var rows []logRow
	if err := s.db.Select(&rows, `SELECT id, ts, text, err FROM (
			SELECT id, ts, text, err FROM task_logs WHERE task_id = ? ORDER BY id DESC LIMIT ?
		) ORDER BY id`, taskID, limit); err != nil {
		return nil, err
	}
	out := make([]TaskLog, 0, len(rows))
	for _, r := range rows {
		out = append(out, TaskLog{ID: r.ID, TS: time.UnixMilli(r.TS), Text: r.Text, Err: r.Err != 0})
	}
	return out, nil
}

type legacyEventRow struct {
	ID     int64  `db:"id"`
	TaskID int64  `db:"task_id"`
	TS     int64  `db:"ts"`
	Kind   string `db:"kind"`
	Data   string `db:"data"`
}

// LegacyEvents 读出旧版 events 表的全部事件（仅用于一次性迁移）。
// 这里一次性读完，避免在单连接池里一边遍历结果集一边回写造成死锁。
// 表不存在时返回空。
func (s *Store) LegacyEvents() ([]Event, error) {
	var rows []legacyEventRow
	if err := s.db.Select(&rows, `SELECT id, task_id, ts, kind, data FROM events ORDER BY id`); err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, Event{ID: r.ID, TaskID: r.TaskID, TS: time.UnixMilli(r.TS),
			Kind: r.Kind, Data: json.RawMessage(r.Data)})
	}
	return out, nil
}

// DropLegacyEvents 迁移完成后删掉废弃的事件表。
func (s *Store) DropLegacyEvents() error {
	_, err := s.db.Exec(`DROP TABLE IF EXISTS events`)
	if err != nil && !strings.Contains(err.Error(), "no such table") {
		return err
	}
	// 回收事件表占用的空间；失败不影响功能（只是文件暂时偏大）。
	_, _ = s.db.Exec(`VACUUM`)
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

// ResolveRunningItems 把某个任务里残留的「运行中」任务点收尾，避免任务已经失败
// 但界面还显示「正在播放」。只影响指定任务。
func (s *Store) ResolveRunningItems(taskID int64, state, reason string) error {
	_, err := s.db.Exec(`UPDATE task_items SET state = ?, reason = ?, updated_at = ?
		WHERE task_id = ? AND state = 'running'`, state, reason, time.Now().Unix(), taskID)
	return err
}

// ResolveAllRunningItems 在启动时收尾所有已结束任务里残留的运行中任务点。
func (s *Store) ResolveAllRunningItems(state, reason string) error {
	_, err := s.db.Exec(`UPDATE task_items SET state = ?, reason = ?, updated_at = ?
		WHERE state = 'running'
		  AND task_id IN (SELECT id FROM tasks WHERE status IN (?, ?, ?))`,
		state, reason, time.Now().Unix(), StatusDone, StatusFailed, StatusCanceled)
	return err
}
