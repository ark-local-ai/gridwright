// Package llm 是"脑"：调云端 OpenAI 兼容 API（spec §1/§4），
// 组装 prompt 并把返回的结构化编辑指令 JSON 解析成 plan.Plan。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
)

// Client 封装一次 LLM 调用。
type Client struct {
	baseURL string // 以 /v1 结尾
	apiKey  string
	model   string
	timeout time.Duration
	http    *http.Client
}

// New 构造客户端（baseURL 归一化为 /v1 结尾）。
func New(c config.LLM) *Client {
	base := strings.TrimRight(c.BaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return &Client{
		baseURL: base,
		apiKey:  c.APIKey,
		model:   c.Model,
		timeout: c.Timeout(),
		http:    &http.Client{Timeout: c.Timeout()},
	}
}

// Model 返回当前模型名（记账用）。
func (c *Client) Model() string { return c.model }

// Plan 把 prompt 发给脑，解析结构化输出。
func (c *Client) Plan(ctx context.Context, prompt string) (*plan.Plan, error) {
	body := map[string]any{
		"model":           c.model,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": prompt},
		},
	}
	bs, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(bs))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用 LLM: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM 返回 %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析 LLM 响应: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("LLM 无返回内容: %s", truncate(string(raw), 500))
	}
	content := out.Choices[0].Message.Content
	// 容忍模型在 JSON 前后多吐了说明文字：截取第一个 { 到最后一个 }
	content = extractJSON(content)
	p := &plan.Plan{}
	if err := json.Unmarshal([]byte(content), p); err != nil {
		return nil, fmt.Errorf("解析结构化指令: %w（原始: %s）", err, truncate(content, 300))
	}
	return p, nil
}

const systemPrompt = `你是 Gridwright 的"脑"，负责决定如何更新本地 Excel 台账。
你只输出一个 JSON 对象，不要任何解释、markdown 或额外文字。
JSON 结构固定为：
{"edits":[{"op":"set","sheet":"表名","cell":"A1","value":任意,"reason":"原因"},{"op":"append","sheet":"表名","row":{"列名":值}}],
 "summary":"一句话总结本次改动","skip_reasons":["被跳过的改动及原因"]}
约束：
- 只改规则允许改的列；rules 的 forbid 列绝对不能动，若要动则放进 skip_reasons 并跳过。
- set 的 cell 必须是真实存在的坐标；append 的 row 的键必须是表头里的列名。
- 值类型贴合表格（数字给数字，文本给文本）。`

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
