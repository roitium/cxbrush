package server

import (
	"encoding/json"
	"sync"
)

// hubRingSize 是内存里保留的事件条数：只够 SSE 断线重连补一小段，
// 更早的历史靠页面的快照接口，不再落库。
const hubRingSize = 200

type hubMsg struct {
	ID   int64
	Kind string
	Data json.RawMessage
}

// hub 是单个运行中任务的进程内广播：任务状态写进 SQLite，
// 实时增量只走内存，所以任务跑多久都不会撑大数据库。
type hub struct {
	mu     sync.Mutex
	seq    int64
	ring   []hubMsg
	subs   map[chan hubMsg]struct{}
	closed bool
}

func newHub() *hub { return &hub{subs: map[chan hubMsg]struct{}{}} }

func (h *hub) publish(kind string, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.seq++
	m := hubMsg{ID: h.seq, Kind: kind, Data: body}
	h.ring = append(h.ring, m)
	if len(h.ring) > hubRingSize {
		h.ring = h.ring[len(h.ring)-hubRingSize:]
	}
	for ch := range h.subs {
		select {
		case ch <- m:
		default:
			// 订阅者太慢：断开它，前端重连时会重新拉快照。
			close(ch)
			delete(h.subs, ch)
		}
	}
}

func (h *hub) subscribe(after int64) (ch <-chan hubMsg, backlog []hubMsg, reset bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := make(chan hubMsg, 256)
	if h.closed {
		close(c)
		return c, nil, false
	}
	var oldest int64
	if len(h.ring) > 0 {
		oldest = h.ring[0].ID
	}
	if after > h.seq {
		after = 0
	}
	// 客户端要的起点已经滚出环形缓冲：让它重新拉一次快照。
	if after > 0 && oldest > 0 && after+1 < oldest {
		reset = true
	}
	if !reset {
		for _, m := range h.ring {
			if m.ID > after {
				backlog = append(backlog, m)
			}
		}
	}
	h.subs[c] = struct{}{}
	return c, backlog, reset
}

func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for ch := range h.subs {
		close(ch)
		delete(h.subs, ch)
	}
}
