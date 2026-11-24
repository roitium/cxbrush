package cx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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

type cacheFile struct {
	Version int                   `json:"version"`
	Entries map[string]cacheEntry `json:"entries"`
}

// AnswerCache 是可持久化的题目答案缓存；nil 表示不启用。
type AnswerCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]cacheEntry
}

// NewAnswerCache 从 path 读取缓存；文件不存在或损坏时返回空缓存。
func NewAnswerCache(path string) *AnswerCache {
	c := &AnswerCache{path: path, entries: map[string]cacheEntry{}}
	if path == "" {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var f cacheFile
	if json.Unmarshal(data, &f) == nil && f.Entries != nil {
		c.entries = f.Entries
	}
	return c
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
	c.mu.Lock()
	defer c.mu.Unlock()
	answers := map[string][]string{}
	var misses []*Question
	for i := range qs {
		q := qs[i]
		e, ok := c.entries[questionCacheKey(q)]
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
	c.entries[key] = e
	c.saveLocked()
}

// Len 返回缓存条目数（测试与诊断用）。
func (c *AnswerCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *AnswerCache) saveLocked() {
	if c.path == "" {
		return
	}
	data, err := json.Marshal(cacheFile{Version: 1, Entries: c.entries})
	if err != nil {
		return
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".answers-*.json")
	if err != nil {
		return
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return
	}
	if err := os.Rename(name, c.path); err != nil {
		os.Remove(name)
	}
}
