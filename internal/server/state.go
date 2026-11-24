package server

import (
	"encoding/json"
	"fmt"
	"log"

	"cxbrush/internal/store"
)

// applyEventState 把一条任务事件落到「任务点当前状态」和「日志」两张表。
// 实时推送走内存 hub，这里只负责持久化最终状态，事件本身不再入库。
func applyEventState(st *store.Store, taskID int64, kind string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	switch kind {
	case "plan":
		var d struct {
			Items []store.Item
		}
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		for _, it := range d.Items {
			if it.ID == "" {
				continue
			}
			if err := st.InsertItemIfAbsent(taskID, it); err != nil {
				return err
			}
		}
	case "progress":
		var d struct {
			ID, Name  string
			Kind      string
			Chapter   string
			Confirmed int
			Total     int
		}
		if json.Unmarshal(raw, &d) != nil || d.ID == "" {
			return nil
		}
		if d.Kind == "" {
			d.Kind = "video"
		}
		return st.UpsertItem(taskID, store.Item{
			ID: d.ID, Kind: d.Kind, Chapter: d.Chapter, Name: d.Name,
			State: "running", Cur: d.Confirmed, Total: d.Total,
		})
	case "task":
		var d struct {
			ID, Name string
			Kind     string
			Chapter  string
			Passed   bool
			Reason   string
		}
		if json.Unmarshal(raw, &d) != nil || d.ID == "" {
			return nil
		}
		if d.Kind == "" {
			d.Kind = "video"
		}
		state := "failed"
		if d.Passed {
			state = "passed"
		}
		if err := st.UpsertItem(taskID, store.Item{
			ID: d.ID, Kind: d.Kind, Chapter: d.Chapter, Name: d.Name,
			State: state, Reason: d.Reason,
		}); err != nil {
			return err
		}
		if line, isErr, ok := logLineFor(kind, data); ok {
			return st.AddLog(taskID, line, isErr)
		}
	case "work":
		var d struct {
			Chapter            string
			Title              string
			Questions          int
			Submitted, Skipped bool
			Reason             string
			Score              float64
			Correct, Wrong     int
		}
		if json.Unmarshal(raw, &d) != nil || d.Title == "" {
			return nil
		}
		state := "failed"
		switch {
		case d.Submitted:
			state = "passed"
		case d.Skipped:
			state = "skipped"
		}
		return st.UpsertItem(taskID, store.Item{
			ID: "work_" + d.Chapter, Kind: "work", Chapter: d.Chapter, Name: d.Title,
			State: state, Reason: d.Reason, Score: d.Score, Correct: d.Correct, Wrong: d.Wrong,
		})
	case "log":
		var d struct {
			Text string
			Err  bool
		}
		if json.Unmarshal(raw, &d) != nil {
			return nil
		}
		return st.AddLog(taskID, d.Text, d.Err)
	}
	return nil
}

// logLineFor 生成任务点结束时的一行日志；runner 用它把同样的内容推给前端，
// 迁移时则写入日志表，保证实时和刷新后看到的一致。
func logLineFor(kind string, data any) (line string, isErr bool, ok bool) {
	if kind != "task" {
		return "", false, false
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", false, false
	}
	var d struct {
		Name, Kind, Reason string
		Passed             bool
	}
	if json.Unmarshal(raw, &d) != nil || d.Name == "" {
		return "", false, false
	}
	mark, suffix := "✓", ""
	if !d.Passed {
		mark = "✗"
		if d.Reason != "" {
			suffix = " — " + d.Reason
		}
	}
	return fmt.Sprintf("%s %s%s", mark, d.Name, suffix), !d.Passed, true
}

// migrateLegacyEvents 把旧版事件表折叠进新的状态表，然后删掉事件表。
// 迁移是幂等的：中途失败时事件表还在，下次启动会重新折叠。
func (s *Server) migrateLegacyEvents() error {
	evs, err := s.st.LegacyEvents()
	if err != nil {
		return err
	}
	for _, e := range evs {
		if err := applyEventState(s.st, e.TaskID, e.Kind, e.Data); err != nil {
			return err
		}
	}
	n := len(evs)
	if err := s.st.DropLegacyEvents(); err != nil {
		return err
	}
	if n > 0 {
		log.Printf("迁移：%d 条历史事件已折叠为任务状态", n)
	}
	return nil
}
