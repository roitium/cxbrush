package cx

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseAIAnswers(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]any
	}{
		{
			name: "bare object",
			raw:  `{"1":"D","2":["C","A"]}`,
			want: map[string]any{"1": "D", "2": []any{"C", "A"}},
		},
		{
			name: "markdown fence",
			raw:  "```json\n{\"1\":\"D\"}\n```",
			want: map[string]any{"1": "D"},
		},
		{
			name: "answers object",
			raw:  `{"answers":{"1":"false"}}`,
			want: map[string]any{"1": "false"},
		},
		{
			name: "answers array",
			raw:  `{"answers":[{"id":"1","answer":"B"},{"qid":"2","value":["C","D"]}]}`,
			want: map[string]any{"1": "B", "2": []any{"C", "D"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAIAnswers(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestNormalizeAIAnswersUsesValue(t *testing.T) {
	qs := []*Question{
		{
			ID: "1", TypeCode: "0", Type: "单选题",
			Options: []Option{{Letter: "A", Value: "B", Text: "x"}, {Letter: "B", Value: "C", Text: "y"}},
		},
		{
			ID: "2", TypeCode: "1", Type: "多选题",
			Options: []Option{
				{Letter: "A", Value: "C"}, {Letter: "B", Value: "A"},
				{Letter: "C", Value: "B"}, {Letter: "D", Value: "D"},
			},
		},
		{
			ID: "3", TypeCode: "3", Type: "判断题",
			Options: []Option{{Letter: "A", Value: "true"}, {Letter: "B", Value: "false"}},
		},
	}
	got, err := normalizeAIAnswers(qs, map[string]any{
		"1": "A",                  // 显示字母 -> value B
		"2": []any{"D", "B", "A"}, // value 顺序打乱 -> 升序 ABC
		"3": "对",                  // 判断题中文
	})
	if err != nil {
		t.Fatal(err)
	}
	// 多选 [D,B,A] 里 A/B 优先按 value 命中（A->value A，B->value B），排序后 ABD。
	want := map[string][]string{"1": {"B"}, "2": {"A", "B", "D"}, "3": {"true"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	if _, err := normalizeAIAnswers(qs, map[string]any{"1": "Z", "2": []any{"A"}, "3": "true"}); err == nil {
		t.Fatal("非法 value 应该报错")
	}
	if _, err := normalizeAIAnswers(qs, map[string]any{"1": "A", "3": "true"}); err == nil {
		t.Fatal("漏答应该报错")
	}
}

func TestLoadMagpieProvider(t *testing.T) {
	dir := os.Getenv("MAGPIE_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config", "magpie")
	}
	if _, err := os.Stat(filepath.Join(dir, "providers.json")); err != nil {
		t.Skip("本机没有 magpie providers.json")
	}
	base, key, err := LoadMagpieProvider(dir, "tokenflux")
	if err != nil {
		t.Fatalf("读取 tokenflux provider 失败: %v", err)
	}
	if base == "" || key == "" {
		t.Fatal("base/key 为空")
	}
	if _, _, err := LoadMagpieProvider(dir, "not-a-provider"); err == nil {
		t.Fatal("不存在的 provider 应该报错")
	}
}

func TestSolveWorkSkipsPassed(t *testing.T) {
	c := New(nil)
	res, err := c.SolveWork(context.Background(), &Work{IsPassed: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped || res.SkipReason != "已完成" {
		t.Fatalf("passed work should be skipped: %+v", res)
	}
	if _, err := c.SolveWork(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("nil work should error")
	}
}
