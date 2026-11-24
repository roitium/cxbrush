package cx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultAIModel 是通过 magpie tokenflux provider 使用的 DeepSeek Flash 模型。
const DefaultAIModel = "deepseek/deepseek-flash"

// AIAnswerer 调用 OpenAI 兼容的 /chat/completions 接口给题目作答。
// 默认从本机 magpie 的 providers.json 读取 tokenflux provider 的端点与 key，
// 也可以用 CX_AI_BASE / CX_AI_KEY / CX_AI_MODEL 覆盖。
type AIAnswerer struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
	HTTP    *http.Client
}

// NewAIAnswerer 按环境变量 → magpie tokenflux provider 的顺序构造答题器。
func NewAIAnswerer() (*AIAnswerer, error) { return NewAIAnswererWith("", "", "") }

// NewAIAnswererWith 用显式参数覆盖环境变量；仍为空时回退到 magpie。
func NewAIAnswererWith(base, key, model string) (*AIAnswerer, error) {
	a := &AIAnswerer{
		BaseURL: strings.TrimRight(firstNonEmpty(base, os.Getenv("CX_AI_BASE")), "/"),
		APIKey:  firstNonEmpty(key, os.Getenv("CX_AI_KEY")),
		Model:   firstNonEmpty(model, os.Getenv("CX_AI_MODEL"), DefaultAIModel),
	}
	if a.BaseURL == "" || a.APIKey == "" {
		dir, err := DefaultMagpieDir()
		if err != nil {
			return nil, err
		}
		base, key, err := LoadMagpieProvider(dir, "tokenflux")
		if err != nil {
			return nil, err
		}
		if a.BaseURL == "" {
			a.BaseURL = strings.TrimRight(base, "/")
		}
		if a.APIKey == "" {
			a.APIKey = key
		}
	}
	a.Timeout = 120 * time.Second
	a.HTTP = &http.Client{Timeout: a.Timeout}
	return a, nil
}

// DefaultMagpieDir 返回 magpie 配置目录，可用 MAGPIE_DIR 覆盖。
func DefaultMagpieDir() (string, error) {
	if dir := os.Getenv("MAGPIE_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("找不到用户目录: %w", err)
	}
	return filepath.Join(home, ".config", "magpie"), nil
}

// LoadMagpieProvider 从 magpie 的 providers.json 里取指定 provider 的 chat 端点与 key。
// 只读取，不修改 magpie 配置，也不会把 key 写进日志。
func LoadMagpieProvider(magpieDir, id string) (base, key string, err error) {
	path := filepath.Join(magpieDir, "providers.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("读取 magpie providers.json: %w", err)
	}
	var root struct {
		Providers []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Chat string `json:"chat"`
			Key  string `json:"key"`
			Off  bool   `json:"off"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return "", "", fmt.Errorf("解析 magpie providers.json: %w", err)
	}
	for _, p := range root.Providers {
		if p.ID != id && !strings.EqualFold(p.Name, id) {
			continue
		}
		if p.Off {
			return "", "", fmt.Errorf("magpie provider %q 已停用", id)
		}
		if p.Chat == "" || p.Key == "" {
			return "", "", fmt.Errorf("magpie provider %q 缺少 chat 或 key", id)
		}
		return p.Chat, p.Key, nil
	}
	return "", "", fmt.Errorf("magpie providers.json 里没有 %q", id)
}

type aiOption struct {
	// 只发 value：提交用的是它，页面上那个显示字母是给人看的，
	// 两个一起发反而容易让模型混着答。
	Value string `json:"value"`
	Text  string `json:"text"`
}

type aiQuestion struct {
	ID      string     `json:"id"`
	Number  string     `json:"number,omitempty"`
	Type    string     `json:"type,omitempty"`
	Stem    string     `json:"stem,omitempty"`
	Options []aiOption `json:"options,omitempty"`
	// Raw 是题干/选项没解析干净时的整块明文（已解密），模型可据此作答。
	Raw string `json:"raw,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	MaxTokens      int           `json:"max_tokens"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Answer 把题目发给模型，返回归一化后的选项 value。
// 单选题/判断题是单元素切片，多选题是排序后的多个 value。
func (a *AIAnswerer) Answer(ctx context.Context, questions []*Question) (map[string][]string, error) {
	if len(questions) == 0 {
		return nil, fmt.Errorf("没有题目")
	}
	if a == nil || a.BaseURL == "" || a.APIKey == "" {
		return nil, fmt.Errorf("答题器未配置")
	}
	brief := make([]aiQuestion, 0, len(questions))
	for _, q := range questions {
		aq := aiQuestion{
			ID:     q.ID,
			Number: q.Number,
			Type:   q.Type,
			Stem:   q.Stem,
		}
		for _, o := range q.Options {
			aq.Options = append(aq.Options, aiOption{Value: o.Value, Text: o.Text})
		}
		if q.Degraded {
			aq.Raw = q.Raw
		}
		brief = append(brief, aq)
	}
	payload, err := json.Marshal(brief)
	if err != nil {
		return nil, err
	}

	reqBody := chatRequest{
		Model:       a.Model,
		Temperature: 0,
		MaxTokens:   16384,
		Messages: []chatMessage{
			{Role: "system", Content: "你是严格的学习通章节测验答题助手。只输出 JSON 对象，不要解释、不要 Markdown 代码块。"},
			{Role: "user", Content: aiPrompt(string(payload))},
		},
		ResponseFormat: &struct {
			Type string `json:"type"`
		}{Type: "json_object"},
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, finish, err := a.do(ctx, reqBody)
		if err != nil {
			return nil, err
		}
		answers, err := parseAIAnswers(raw)
		if err == nil {
			if norm, nerr := normalizeAIAnswers(questions, answers); nerr == nil {
				return norm, nil
			} else {
				lastErr = nerr
			}
		} else {
			lastErr = fmt.Errorf("解析模型答案失败: %w（finish=%s，原文：%s）", err, finish, truncate(raw, 300))
		}
		// reasoning 模型可能把预算耗在思考上导致 content 为空，翻倍预算再试一次。
		if attempt == 0 {
			reqBody.MaxTokens *= 2
		}
	}
	return nil, lastErr
}

func aiPrompt(brief string) string {
	return `下面是题目数组（JSON）。请逐题作答，输出一个 JSON 对象：
- key 是题目的 id；
- 单选题和判断题的 value 是字符串；
- 多选题的 value 是字符串数组；
- 值只能取该题 options 里的 value，原样返回；
- 判断题的 value 是 "true" 或 "false"；
- 某题带 raw 字段时表示题干/选项没解析干净，raw 是整块原文（已还原混淆字体），
  请先读 raw 再结合 options 的 value 作答。

题目：
` + brief
}

func (a *AIAnswerer) do(ctx context.Context, body chatRequest) (string, string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	endpoint := a.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("请求答题模型: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("答题模型 HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", "", fmt.Errorf("答题模型响应不是 JSON: %s", truncate(string(raw), 200))
	}
	if cr.Error != nil && cr.Error.Message != "" {
		return "", "", fmt.Errorf("答题模型错误: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", "", fmt.Errorf("答题模型没有返回 choices: %s", truncate(string(raw), 200))
	}
	return cr.Choices[0].Message.Content, cr.Choices[0].FinishReason, nil
}

// Ping 用一次最小请求验证端点、Key 和模型名是否可用。
func (a *AIAnswerer) Ping(ctx context.Context) (time.Duration, error) {
	if a == nil || a.BaseURL == "" || a.APIKey == "" {
		return 0, fmt.Errorf("答题模型未配置")
	}
	start := time.Now()
	body := chatRequest{
		Model:       a.Model,
		Temperature: 0,
		MaxTokens:   4096,
		Messages: []chatMessage{
			{Role: "user", Content: `只输出 JSON：{"ok":true}`},
		},
		ResponseFormat: &struct {
			Type string `json:"type"`
		}{Type: "json_object"},
	}
	content, finish, err := a.do(ctx, body)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(content) == "" {
		return 0, fmt.Errorf("模型返回空内容（finish=%s）", finish)
	}
	return time.Since(start), nil
}

// parseAIAnswers 兼容 {"id":"value"}、{"answers":{...}}、{"answers":[{...}]} 三种形态。
func parseAIAnswers(raw string) (map[string]any, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		if j := strings.IndexByte(s, '\n'); j >= 0 && !strings.Contains(s[:j], "{") {
			s = s[j+1:]
		}
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	lo, hi := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if lo < 0 || hi <= lo {
		return nil, fmt.Errorf("没有找到 JSON 对象")
	}
	s = s[lo : hi+1]
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return unfoldAIAnswers(m), nil
}

func unfoldAIAnswers(m map[string]any) map[string]any {
	if v, ok := m["answers"]; ok {
		switch t := v.(type) {
		case map[string]any:
			return t
		case []any:
			out := map[string]any{}
			for _, item := range t {
				obj, _ := item.(map[string]any)
				if obj == nil {
					continue
				}
				id := firstNonEmpty(strv(obj["id"]), strv(obj["qid"]), strv(obj["questionId"]))
				if id == "" {
					continue
				}
				out[id] = firstNonNil(obj["answer"], obj["value"], obj["selected"])
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return m
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

// normalizeAIAnswers 用题目定义校验并归一化模型答案，避免提交非法 value。
func normalizeAIAnswers(questions []*Question, raw map[string]any) (map[string][]string, error) {
	out := make(map[string][]string, len(questions))
	var missing []string
	for _, q := range questions {
		v, ok := raw[q.ID]
		if !ok {
			missing = append(missing, q.ID)
			continue
		}
		vals, err := stringList(v)
		if err != nil || len(vals) == 0 {
			return nil, fmt.Errorf("题目 %s 的答案格式不对: %v", q.ID, v)
		}
		normalized, err := q.NormalizeAnswer(strings.Join(vals, ","))
		if err != nil {
			return nil, err
		}
		if q.TypeCode == "1" {
			rs := []rune(normalized)
			vals = make([]string, 0, len(rs))
			for _, r := range rs {
				vals = append(vals, string(r))
			}
			sort.Strings(vals)
		} else {
			vals = []string{normalized}
		}
		out[q.ID] = vals
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("模型漏答题目: %s", strings.Join(missing, ","))
	}
	return out, nil
}

func stringList(v any) ([]string, error) {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("空字符串")
		}
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("数组里不是字符串")
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		return t, nil
	case float64:
		return []string{fmt.Sprintf("%.0f", t)}, nil
	default:
		return nil, fmt.Errorf("不支持的类型 %T", v)
	}
}
