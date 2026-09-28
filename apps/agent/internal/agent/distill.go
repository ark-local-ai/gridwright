package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
)

// 从对话里提炼"记忆候选"（见 docs/agent-architecture/21-记忆设计.md）。
//
// 这是"记录"与"记忆"之间的那道闸门，也是本文件存在的理由：
//
//	记录 = 逐字流水（conversations.json），全自动写，不判断。
//	记忆 = 沉淀下来的结论（memory.json），要精炼、会过期、**必须人点头**。
//
// 模型在这里只做一件事：**提议**。它把一段对话里"值得长期记住的东西"抽成
// 候选条目，标上类型与出处；落盘与否由人在界面上逐条决定。
// 为什么不让模型直接写：记忆是长期资产，一条错的记忆会污染以后所有判断，
// 而且用户很难事后发现是哪条记忆带偏的。所以与工具权限同一个模式——提议 → 人授权。
//
// 抽什么、不抽什么（prompt 里逐条写明，因为这是最容易抽错的地方）：
//   · 抽：业务事实（某个铺位的租金/合同期）、人拍板过的决策（"含运费的都算进去"）、
//     长期规矩（"每笔都要看滞纳金"）
//   · 不抽：一次性的问答、寒暄、对某次改动的临时说明、模型自己说的话
//

// DistillCandidate 是一条记忆候选（还没落盘）。
type DistillCandidate struct {
	// Kind: fact（事实）| decision（决策）。与 memory2 的两种记忆对应。
	Kind string `json:"kind"`
	// Title: 一句话，给列表看（人一眼判断"这条要不要留"）
	Title string `json:"title"`
	// Text: 完整内容。事实类给"键 值"式描述，决策类给判断本身。
	Text string `json:"text"`
	// Key: 业务键（如 {"铺位":"B31"}），可为空
	Key map[string]string `json:"key,omitempty"`
	// Source: 出处（对话里的哪句），便于人核对
	Source string `json:"source,omitempty"`
	// Why: 模型认为为什么值得记住（人据此判断是不是真该留）
	Why string `json:"why,omitempty"`
}

// Distill 把一段会话提炼成记忆候选。
//
// 只读：不写任何文件。落盘要人逐条批准（见 api 的 /memory 接口）。
func (a *Agent) Distill(ctx context.Context, c *convo.Conversation) ([]DistillCandidate, error) {
	if a.Brain == nil || !a.Brain.Ready() {
		return nil, fmt.Errorf("还没配置模型（脑）：提炼记忆需要模型判断")
	}
	if c == nil || len(c.Messages) == 0 {
		return nil, fmt.Errorf("这条记录里还没有对话内容")
	}

	content, err := a.Brain.ChatJSON(ctx, assembleDistillPrompt(c))
	if err != nil {
		return nil, err
	}
	var raw struct {
		Candidates []struct {
			Kind   string            `json:"kind"`
			Title  string            `json:"title"`
			Text   string            `json:"text"`
			Key    map[string]string `json:"key"`
			Source string            `json:"source"`
			Why    string            `json:"why"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("解析提取结果: %w（原始: %.200s）", err, content)
	}

	out := make([]DistillCandidate, 0, len(raw.Candidates))
	for _, x := range raw.Candidates {
		// 模型偶尔会给不合规的条目：这里**只收两边都说得清的**，
		// 其余丢掉。宁可少给几条让人确认，也不要塞垃圾让人逐条否决。
		k := strings.ToLower(strings.TrimSpace(x.Kind))
		if k != "fact" && k != "decision" {
			continue
		}
		if strings.TrimSpace(x.Title) == "" || strings.TrimSpace(x.Text) == "" {
			continue
		}
		out = append(out, DistillCandidate{
			Kind: k, Title: strings.TrimSpace(x.Title), Text: strings.TrimSpace(x.Text),
			Key: x.Key, Source: strings.TrimSpace(x.Source), Why: strings.TrimSpace(x.Why),
		})
		if len(out) >= 12 { // 上界：一次提炼不该甩出几十条让人审
			break
		}
	}
	return out, nil
}

func assembleDistillPrompt(c *convo.Conversation) string {
	var b strings.Builder
	b.WriteString("# 任务\n")
	b.WriteString("下面是一段「数据管家」的使用记录（用户与系统关于 Excel 台账的对话）。\n")
	b.WriteString("请从中提炼出**值得长期记住**的条目。\n\n")
	b.WriteString("# 要抽的（只有这两类）\n")
	b.WriteString("- kind=fact：**业务事实**。某个铺位的租金/合同期/租户、某个字段的含义、某张表的口径。\n")
	b.WriteString("  要具体、可核对，带上业务键（如 铺位、月份）。\n")
	b.WriteString("- kind=decision：**人拍板过的判断或规矩**。用户明确说\"以后都这样\"、\"含运费的都算进去\"、\n")
	b.WriteString("  \"这张表不要动\" 这类。记的是**为什么**这样处理。\n\n")
	b.WriteString("# 不要抽的\n")
	b.WriteString("- 一次性的问答（\"这张图里写了什么\"）、寒暄、对某次改动的临时说明\n")
	b.WriteString("- 系统自己说的话（只从**用户**的话里提炼）\n")
	b.WriteString("- 猜测：用户没说清的，不要替他补全\n")
	b.WriteString("- 已经在对话里被用户否掉的\n\n")
	b.WriteString("# 对话记录\n")
	for _, m := range c.Messages {
		if m.Role == convo.RoleSystem {
			continue // 系统提示/报错不算素材
		}
		who := "用户"
		if m.Role == convo.RoleAgent {
			who = "系统"
		}
		txt := strings.TrimSpace(m.Text)
		if txt == "" {
			if len(m.Images) > 0 {
				txt = "（附了一张图）"
			} else {
				continue
			}
		}
		// 单条截断：一段记录可能有很长的回复，但提炼只需要要点
		if len([]rune(txt)) > 500 {
			txt = string([]rune(txt)[:500]) + "…"
		}
		fmt.Fprintf(&b, "%s：%s\n", who, txt)
	}
	b.WriteString(`
# 输出要求
只输出一个 JSON 对象，不要解释、不要 markdown：
{"candidates":[
  {"kind":"fact|decision","title":"一句话标题（人一眼判断要不要留）",
   "text":"完整内容","key":{"铺位":"B31"},"source":"对应对话里的那句原话","why":"为什么值得记住"}
]}

规则：
- 一条都没提炼出来就返回 {"candidates":[]}——**这很正常**，不要为了凑数硬编。
- 最多 12 条。宁可少而准，不要多而杂。
- title 要说清"这条讲的是什么"，不要写"一条事实"这种空话。`)
	return b.String()
}
