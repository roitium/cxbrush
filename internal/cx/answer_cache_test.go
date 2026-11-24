package cx

import (
	"sync"
	"testing"
)

type memAnswerStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemAnswerStore() *memAnswerStore { return &memAnswerStore{m: map[string]string{}} }

func (s *memAnswerStore) GetAnswer(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok
}

func (s *memAnswerStore) PutAnswer(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func choiceQuestion(id, stem string, options ...[2]string) *Question {
	q := &Question{ID: id, TypeCode: "0", Type: "单选题", Stem: stem}
	for i, o := range options {
		q.Options = append(q.Options, Option{Letter: string(rune('A' + i)), Value: o[0], Text: o[1]})
	}
	return q
}

func TestAnswerCacheReusesAcrossShuffle(t *testing.T) {
	store := newMemAnswerStore()
	cache := NewAnswerCache(store, 1)
	q := choiceQuestion("1", "中国的首都是？", [2]string{"A", "北京"}, [2]string{"B", "上海"})
	cache.Put(q, []string{"A"})

	reloaded := NewAnswerCache(store, 1)
	shuffled := choiceQuestion("9", "中国的首都是？", [2]string{"C", "上海"}, [2]string{"D", "北京"})
	answers, misses := reloaded.Lookup([]*Question{shuffled})
	if len(misses) != 0 {
		t.Fatalf("misses = %d", len(misses))
	}
	if got := answers["9"]; len(got) != 1 || got[0] != "D" {
		t.Fatalf("answers = %v, want [D]", got)
	}
}

func TestAnswerCacheMissWhenOptionsChange(t *testing.T) {
	cache := NewMemoryAnswerCache(1)
	q := choiceQuestion("1", "中国的首都是？", [2]string{"A", "北京"}, [2]string{"B", "上海"})
	cache.Put(q, []string{"A"})

	changed := choiceQuestion("2", "中国的首都是？", [2]string{"A", "北京"}, [2]string{"B", "广州"})
	answers, misses := cache.Lookup([]*Question{changed})
	if len(answers) != 0 || len(misses) != 1 {
		t.Fatalf("answers=%v misses=%d", answers, len(misses))
	}
}

func TestAnswerCachePartialHit(t *testing.T) {
	cache := NewMemoryAnswerCache(1)
	known := choiceQuestion("1", "1+1=?", [2]string{"A", "2"}, [2]string{"B", "3"})
	cache.Put(known, []string{"A"})

	other := choiceQuestion("2", "2+2=?", [2]string{"A", "4"}, [2]string{"B", "5"})
	answers, misses := cache.Lookup([]*Question{known, other})
	if len(misses) != 1 || misses[0].ID != "2" {
		t.Fatalf("misses = %+v", misses)
	}
	if got := answers["1"]; len(got) != 1 || got[0] != "A" {
		t.Fatalf("answers = %v", answers)
	}
}

func TestAnswerCacheFreeText(t *testing.T) {
	store := newMemAnswerStore()
	q := &Question{ID: "1", TypeCode: "2", Type: "填空题", Stem: "中国的首都是____"}
	NewAnswerCache(store, 1).Put(q, []string{"北京"})

	answers, misses := NewAnswerCache(store, 1).Lookup([]*Question{{ID: "2", TypeCode: "2", Type: "填空题", Stem: "中国的首都是____"}})
	if len(misses) != 0 || len(answers["2"]) != 1 || answers["2"][0] != "北京" {
		t.Fatalf("answers=%v misses=%v", answers, misses)
	}
}

func TestAnswerCacheJudgmentEmptyText(t *testing.T) {
	cache := NewMemoryAnswerCache(1)
	q := &Question{ID: "1", TypeCode: "3", Type: "判断题", Stem: "地球是圆的", Options: []Option{
		{Value: "true", Text: ""}, {Value: "false", Text: ""},
	}}
	cache.Put(q, []string{"true"})

	shuffled := &Question{ID: "2", TypeCode: "3", Type: "判断题", Stem: "地球是圆的", Options: []Option{
		{Value: "false", Text: ""}, {Value: "true", Text: ""},
	}}
	answers, misses := cache.Lookup([]*Question{shuffled})
	if len(misses) != 0 || len(answers["2"]) != 1 || answers["2"][0] != "true" {
		t.Fatalf("answers=%v misses=%v", answers, misses)
	}
}

// 一个用户的答案不能泄漏给另一个用户（不同老师同一道题的正确答案也可能不同）。
func TestAnswerCacheIsolatedPerUser(t *testing.T) {
	store := newMemAnswerStore()
	q := choiceQuestion("1", "中国的首都是？", [2]string{"A", "北京"}, [2]string{"B", "上海"})
	NewAnswerCache(store, 1).Put(q, []string{"A"})

	if _, misses := NewAnswerCache(store, 2).Lookup([]*Question{q}); len(misses) != 1 {
		t.Fatalf("user 2 不应命中 user 1 的缓存")
	}
	if _, misses := NewAnswerCache(store, 1).Lookup([]*Question{q}); len(misses) != 0 {
		t.Fatalf("user 1 应该命中自己的缓存")
	}
}
