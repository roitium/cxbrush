package cx

import (
	"path/filepath"
	"testing"
)

func choiceQuestion(id, stem string, options ...[2]string) *Question {
	q := &Question{ID: id, TypeCode: "0", Type: "单选题", Stem: stem}
	for i, o := range options {
		q.Options = append(q.Options, Option{Letter: string(rune('A' + i)), Value: o[0], Text: o[1]})
	}
	return q
}

func TestAnswerCacheReusesAcrossShuffle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers.json")
	cache := NewAnswerCache(path)
	q := choiceQuestion("1", "中国的首都是？", [2]string{"A", "北京"}, [2]string{"B", "上海"})
	cache.Put(q, []string{"A"})

	reloaded := NewAnswerCache(path)
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
	cache := NewAnswerCache("")
	q := choiceQuestion("1", "中国的首都是？", [2]string{"A", "北京"}, [2]string{"B", "上海"})
	cache.Put(q, []string{"A"})

	changed := choiceQuestion("2", "中国的首都是？", [2]string{"A", "北京"}, [2]string{"B", "广州"})
	answers, misses := cache.Lookup([]*Question{changed})
	if len(answers) != 0 || len(misses) != 1 {
		t.Fatalf("answers=%v misses=%d", answers, len(misses))
	}
}

func TestAnswerCachePartialHit(t *testing.T) {
	cache := NewAnswerCache("")
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
	path := filepath.Join(t.TempDir(), "answers.json")
	cache := NewAnswerCache(path)
	q := &Question{ID: "1", TypeCode: "2", Type: "填空题", Stem: "中国的首都是____"}
	cache.Put(q, []string{"北京"})

	answers, misses := NewAnswerCache(path).Lookup([]*Question{{ID: "2", TypeCode: "2", Type: "填空题", Stem: "中国的首都是____"}})
	if len(misses) != 0 || len(answers["2"]) != 1 || answers["2"][0] != "北京" {
		t.Fatalf("answers=%v misses=%v", answers, misses)
	}
}

func TestAnswerCacheJudgmentEmptyText(t *testing.T) {
	cache := NewAnswerCache("")
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
