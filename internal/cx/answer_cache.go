package cx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// 答案缓存按「题型 + 题干 + 选项正文」索引，保存的是选项正文而不是提交值：
// randomOptions=true 时同一道题的 value 顺序会变，正文才是稳定标识。
type cacheEntry struct {
	Texts   []string `json:"texts,omitempty"`
	Values  []string `json:"values,omitempty"`
	Free    []string `json:"free,omitempty"` // 填空/简答没有选项，直接复用
	Updated int64    `json:"updated,omitempty"`
}

// AnswerStore 是答案缓存的持久层。key 是题目指纹，value 是 cacheEntry 的 JSON。
// 生产环境用 SQLite 实现；测试可以用内存实现。
type AnswerStore interface {
	GetAnswer(key string) (string, bool)
	PutAnswer(key, value string) error
}

// AnswerCache 是可持久化的题目答案缓存；nil 表示不启用。
// store 为 nil 时退化为进程内内存缓存。
type AnswerCache struct {
	mu    sync.Mutex
	store AnswerStore
	mem   map[string]cacheEntry
}

// NewAnswerCache 用持久层构造缓存；store 为 nil 时等价于内存缓存。
func NewAnswerCache(store AnswerStore) *AnswerCache {
	if store == nil {
		return NewMemoryAnswerCache()
	}
	return &AnswerCache{store: store}
}

// NewMemoryAnswerCache 构造只存在于当前进程的缓存（测试用）。
func NewMemoryAnswerCache() *AnswerCache {
	return &AnswerCache{mem: map[string]cacheEntry{}}
}

func normText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func questionCacheKey(q *Question) string {
	stem := q.Stem
	if normText(stem) == "" {
		stem = q.Raw
	}
	parts := []string{q.TypeCode, q.Type, normText(stem)}
	texts := make([]string, 0, len(q.Options))
	for _, o := range q.Options {
		texts = append(texts, normText(o.Text))
	}
	sort.Strings(texts)
	parts = append(parts, texts...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// Lookup 返回命中题目的提交值，以及需要交给模型的题目。
func (c *AnswerCache) Lookup(qs []*Question) (map[string][]string, []*Question) {
	if c == nil {
		return nil, qs
	}
	answers := map[string][]string{}
	var misses []*Question
	for _, q := range qs {
		e, ok := c.get(questionCacheKey(q))
		if !ok {
			misses = append(misses, q)
			continue
		}
		vals, ok := resolveCacheEntry(q, e)
		if !ok {
			misses = append(misses, q)
			continue
		}
		answers[q.ID] = vals
	}
	if len(answers) == 0 {
		answers = nil
	}
	return answers, misses
}

func (c *AnswerCache) get(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil {
		e, ok := c.mem[key]
		return e, ok
	}
	raw, ok := c.store.GetAnswer(key)
	if !ok {
		return cacheEntry{}, false
	}
	var e cacheEntry
	if json.Unmarshal([]byte(raw), &e) != nil {
		return cacheEntry{}, false
	}
	return e, true
}

func resolveCacheEntry(q *Question, e cacheEntry) ([]string, bool) {
	if len(q.Options) == 0 {
		if len(e.Free) == 0 {
			return nil, false
		}
		return append([]string(nil), e.Free...), true
	}
	if len(e.Texts) == 0 || len(e.Texts) != len(e.Values) {
		return nil, false
	}
	byText := make(map[string]string, len(q.Options))
	values := make(map[string]bool, len(q.Options))
	for _, o := range q.Options {
		byText[normText(o.Text)] = o.Value
		values[o.Value] = true
	}
	out := make([]string, 0, len(e.Texts))
	for i, t := range e.Texts {
		normalized := normText(t)
		if normalized == "" && values[e.Values[i]] {
			// 判断题的选项正文可能为空，value（true/false）本身是稳定的。
			out = append(out, e.Values[i])
			continue
		}
		v, ok := byText[normalized]
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// Put 保存一道题的答案；values 是已经归一化的提交值。
func (c *AnswerCache) Put(q *Question, values []string) {
	if c == nil || q == nil || len(values) == 0 {
		return
	}
	e := cacheEntry{Updated: time.Now().Unix()}
	if len(q.Options) == 0 {
		e.Free = append([]string(nil), values...)
	} else {
		byValue := make(map[string]string, len(q.Options))
		for _, o := range q.Options {
			byValue[o.Value] = o.Text
		}
		for _, v := range values {
			text, ok := byValue[v]
			if !ok {
				return // 值不在当前页面，缓存了也对不上
			}
			e.Texts = append(e.Texts, text)
			e.Values = append(e.Values, v)
		}
	}
	key := questionCacheKey(q)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil {
		c.mem[key] = e
		return
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = c.store.PutAnswer(key, string(raw))
}
