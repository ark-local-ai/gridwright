// Package llm 是"脑"：调云端 OpenAI 兼容 API（spec §1/§4），
// 组装 prompt 并把返回的结构化编辑指令 JSON 解析成 plan.Plan。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
	"github.com/ark-local-ai/ark/apps/agent/internal/trace"
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
// nil 接收者返回 "rule"：**规则短路时根本没有模型**，账目里该如实写"这格是规则改的"，
// 而不是崩掉、也不是假装有个模型。
func (c *Client) Model() string {
	if c == nil {
		return "rule"
	}
	return c.model
}

// Ready 表示"脑"是否配好（有 baseURL + apiKey）。未配好时只读能力照常，
// 但需要判断的动作应明确报错，而不是发出一个必然 401 的请求。
// nil 接收者视为"没配"——规则短路路径会走到这里，必须安全。
func (c *Client) Ready() bool { return c != nil && c.baseURL != "" && c.apiKey != "" }

// Plan 把 prompt 发给脑，解析结构化输出。
func (c *Client) Plan(ctx context.Context, prompt string) (*plan.Plan, error) {
	content, err := c.chat(ctx, systemPrompt, prompt)
	if err != nil {
		return nil, err
	}
	p := &plan.Plan{}
	if err := json.Unmarshal([]byte(content), p); err != nil {
		return nil, fmt.Errorf("解析结构化指令: %w（原始: %s）", err, truncate(content, 300))
	}
	return p, nil
}

// ChatJSON 用"会话"契约问脑：返回 reply + 结构化 proposal 的原始 JSON。
func (c *Client) ChatJSON(ctx context.Context, prompt string) (string, error) {
	return c.chat(ctx, chatSystemPrompt, prompt)
}

// ImageInput 是一张随消息一起发出去的图。
// DataURL 形如 "data:image/png;base64,...."。
type ImageInput struct {
	DataURL string
}

// ChatJSONWithImages 与 ChatJSON 相同，但带图（截图/照片里的数据）。
//
// 为什么需要它：财务常拿到的不是 csv，而是**一张截图**——微信里对方发的
// 收款记录、别人拍的表格。只能收文字，等于把最省事的那条输入通道关掉。
//
// 实现要点：图片在 OpenAI 兼容接口里是**内容数组**里的一个对象，不是字符串。
// 原来那套 map[string]string 表达不了，所以这里换成 map[string]any。
func (c *Client) ChatJSONWithImages(ctx context.Context, prompt string, imgs []ImageInput) (string, error) {
	if len(imgs) == 0 {
		return c.ChatJSON(ctx, prompt)
	}
	if !c.Ready() {
		return "", fmt.Errorf("还没配置模型（脑）：请在设置里填 base_url 与 api_key")
	}
	user := []any{map[string]any{"type": "text", "text": prompt}}
	for _, im := range imgs {
		user = append(user, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": im.DataURL},
		})
	}
	body := map[string]any{
		"model":           c.model,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]any{
			{"role": "system", "content": chatSystemPrompt},
			{"role": "user", "content": user},
		},
	}
	return c.post(ctx, body)
}

const chatSystemPrompt = `你是 gridwright「数据管家」的配置入口，负责把用户的自然语言变成可执行的安排。
你只输出一个 JSON 对象，不要解释、不要 markdown。`

// 注意 edits 的具体形状由 agent 包定义（semanticEdit）；这里只保留原始 JSON，
// 由上层解析，避免 llm 包反向依赖 agent。
type SemanticResponse struct {
	Summary     string
	SkipReasons []string
	Questions   []string
	Edits       []byte // 原始 edits 数组
}

// PlanSemantic 用"语义坐标"契约问脑：模型给业务键（铺位/月份/字段），不给单元格坐标。
func (c *Client) PlanSemantic(ctx context.Context, prompt string) (*SemanticResponse, error) {
	return c.PlanSemanticWithImages(ctx, prompt, nil)
}

// PlanSemanticWithImages 同 PlanSemantic，但可带图。
//
// 为什么必须有它：财务最常见的输入形式是**一张截图**（对方发来的收款记录、
// 拍下来的表格）。而举例里"按 D 列名字把 E、F 列填进某表"这种指令，
// 数据本身就在图里——只给文字的话，模型只能反过来问"数据来自哪张表"，
// 把一件能做的事问成澄清（实测发生过）。图必须能进到出清单这一步。
func (c *Client) PlanSemanticWithImages(ctx context.Context, prompt string, imgs []ImageInput) (*SemanticResponse, error) {
	var content string
	var err error
	if len(imgs) == 0 {
		content, err = c.chat(ctx, semanticSystemPrompt, prompt)
	} else {
		content, err = c.chatWithImages(ctx, semanticSystemPrompt, prompt, imgs)
	}
	if err != nil {
		return nil, err
	}
	var raw struct {
		Edits       json.RawMessage `json:"edits"`
		Summary     string          `json:"summary"`
		SkipReasons []string        `json:"skip_reasons"`
		Questions   []string        `json:"questions"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("解析结构化指令: %w（原始: %s）", err, truncate(content, 300))
	}
	return &SemanticResponse{
		Summary: raw.Summary, SkipReasons: raw.SkipReasons,
		Questions: raw.Questions, Edits: raw.Edits,
	}, nil
}

// chatWithImages 带图的一次 JSON 对话（与 chat 同样的解析路径，
// 区别只在 messages 的形状——与 ChatJSONWithImages 同理，共用 post）。
func (c *Client) chatWithImages(ctx context.Context, system, prompt string, imgs []ImageInput) (string, error) {
	if !c.Ready() {
		return "", fmt.Errorf("还没配置模型（脑）：请在设置里填 base_url 与 api_key")
	}
	user := []any{map[string]any{"type": "text", "text": prompt}}
	for _, im := range imgs {
		user = append(user, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": im.DataURL},
		})
	}
	return c.post(ctx, map[string]any{
		"model":           c.model,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
}

// chat 发一次对话请求，返回模型正文（已去掉 JSON 外的说明文字）。
func (c *Client) chat(ctx context.Context, system, user string) (string, error) {
	if !c.Ready() {
		return "", fmt.Errorf("还没配置模型（脑）：请在设置里填 base_url 与 api_key，之后才能让它判断/改表；只读的看表与体检不受影响")
	}
	return c.post(ctx, map[string]any{
		"model":           c.model,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
}

// redactSecret 在把请求/响应正文写进日志前抹掉密钥。
//
// 这行日志会落到用户本机的 engine.log 里，而 engine.log 正是用户可能随手发给
// 别人排障的文件（本项目自己的排障流程就是这么做的）。密钥绝不能出现在里面。
// 正常路径下 Authorization 头不进 body，但多一层保险的成本为零。
func redactSecret(b []byte, secret string) string {
	if secret != "" {
		b = bytes.ReplaceAll(b, []byte(secret), []byte("***"))
	}
	return truncate(string(b), 500)
}

// post 是所有请求共用的出口（单条消息与带图消息都走这里）。
// 抽出来是因为带图那条路（ChatJSONWithImages）只有一个地方不同——messages 的
// 形状——其余（鉴权头、超时、错误包装、取正文）必须完全一致，否则两条路
// 迟早会在错误处理上分叉。
func (c *Client) post(ctx context.Context, body map[string]any) (string, error) {
	bs, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(bs))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	// 记下**问了什么**。此前这里只有一行 "ok（7.2s，4501B）"，于是出问题时
	// 完全无法回答"模型到底收到了什么提示、返回了哪句话"，只能靠猜。
	// 放在 Debug：平时不吵，需要时 -log-level=debug 就有摘要。
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.Debug("[llm] 请求正文", "model", c.model, "body", redactSecret(bs, c.apiKey))
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		slog.Error("[llm] 请求失败", "model", c.model,
			"耗时", time.Since(start).Round(time.Millisecond), "err", err)
		return "", fmt.Errorf("调用 LLM: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// 失败必须记全文：模型方的报错（额度/参数/鉴权）就藏在这个 body 里，
		// 只回给前端一句"调用失败"等于把线索丢了。
		slog.Error("[llm] 返回非 200", "model", c.model, "status", resp.StatusCode,
			"耗时", time.Since(start).Round(time.Millisecond), "body", truncate(string(raw), 800))
		if tr := trace.From(ctx); tr != nil {
			tr.Add("模型报错", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 800)))
		}
		return "", fmt.Errorf("LLM 返回 %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}
	log.Printf("[llm] %s ok（%s，%dB）", c.model, time.Since(start).Round(time.Millisecond), len(raw))
	// 记下**答了什么**：同样只在 Debug。
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.Debug("[llm] 响应正文", "model", c.model, "body", redactSecret(raw, c.apiKey))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("解析 LLM 响应: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("LLM 无返回内容: %s", truncate(string(raw), 500))
	}
	// 模型原样返回的正文也收进运行详情（界面直接看；与 debug 日志无关）。
	if tr := trace.From(ctx); tr != nil {
		tr.SetRaw(out.Choices[0].Message.Content)
	}
	// 容忍模型在 JSON 前后多吐了说明文字：截取第一个 { 到最后一个 }
	return extractJSON(out.Choices[0].Message.Content), nil
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

// semanticSystemPrompt 是"语义坐标"版的系统提示（见 docs/agent-architecture/5-编辑语义.md）：
// **禁止模型输出单元格坐标**——真实台账里坐标极易错行错列，坐标一律由代码算。
const semanticSystemPrompt = `你是 Gridwright 的"脑"，负责决定如何更新本地 Excel 台账。
你只输出一个 JSON 对象，不要任何解释、markdown 或额外文字。
结构固定为：
{"edits":[{"sheet":"工作表名","key":{"物业位置":"B31"},"month":"2026-08","field":"本月实收","op":"add","value":23540,"reason":"8月租金"}],
 "summary":"一句话总结","skip_reasons":["被跳过及原因"],"questions":["需要问人的问题"]}
硬性规则：
- **绝对不要输出单元格坐标**（不要 A1、不要行列号）。用 key（业务键列）、field（字段列名）、
  可选的 month（年月如 2026-08）描述位置，坐标由程序计算。
- op=add 表示在原值上累加（收退款类通常用 add）；op=set 表示覆盖。
- **op=upsert 表示“查不到就新增”**，形状是 key + row（不给 field/value）：
  {"sheet":"销售明细表","key":{"客户名称":"张三"},
   "row":{"楼栋":"1栋","实收金额":12000},"op":"upsert","reason":"补录收款"}
  语义：拿 key 里的列去表里找行，找到就按 row 覆盖那几列，找不到就在表尾新增一行。
  用户说“按某列的名字填某几列，查不到就新增”时就用它。**不要**说“不能新增行”。
- 一行备注若要拆到多个月（如"收到7月租金19256元，8月租金4284元"），拆成多条 edits，
  金额之和必须等于原额，逐条给 month。
- 不确定 / 信息不足 / 疑似要动禁止列 → 放进 questions 或 skip_reasons。**不要猜。**
  在财务台账上宁可问，也不许猜错。`

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

// ChatText 要一段**纯文本**回复（不加 JSON 约束）。
// 用于文书生成：模型组织语言，数字由代码喂进去。
func (c *Client) ChatText(ctx context.Context, prompt string) (string, error) {
	if !c.Ready() {
		return "", fmt.Errorf("还没配置模型（脑）：请在设置里填 base_url 与 api_key")
	}
	body := map[string]any{
		"model":       c.model,
		"temperature": 0.3,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	bs, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(bs))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用 LLM: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM 返回 %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("解析 LLM 响应: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("LLM 无返回内容")
	}
	// 纯文本模式**不做 JSON 截取**（文书本身就是文本）
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
