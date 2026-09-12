package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
)

// Chat 是"会话"入口（见 docs/agent-architecture/7-对话与自动化任务.md）：
// 用户说一句话 → 模型回话 + 一个结构化 proposal（任务/规则/工具申请/澄清/改表计划）。
// **模型只建议，人拍板**——proposal 要界面上确认才生效。
func (a *Agent) Chat(ctx context.Context, userText string, files []string) (*convo.Message, error) {
	if a.Brain == nil || !a.Brain.Ready() {
		return nil, fmt.Errorf("还没配置模型（脑）：请在设置里填 base_url 与 api_key 后就能对话了")
	}
	prompt := assembleChatPrompt(userText, files)
	content, err := a.Brain.ChatJSON(ctx, prompt)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Reply    string `json:"reply"`
		Proposal *struct {
			Kind     string   `json:"kind"`
			Title    string   `json:"title"`
			Detail   string   `json:"detail"`
			Schedule string   `json:"schedule"`
			Action   string   `json:"action"`
			Tools    []string `json:"tools"`
			Options  []string `json:"options"`
		} `json:"proposal"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		// 模型没按格式回：退回纯文本回话，不让整轮失败
		return &convo.Message{Role: convo.RoleAgent, Text: strings.TrimSpace(content)}, nil
	}
	msg := &convo.Message{Role: convo.RoleAgent, Text: raw.Reply}
	if raw.Proposal != nil && raw.Proposal.Kind != "" {
		msg.Proposal = &convo.Proposal{
			Kind: raw.Proposal.Kind, Title: raw.Proposal.Title, Detail: raw.Proposal.Detail,
			Schedule: raw.Proposal.Schedule, Action: raw.Proposal.Action,
			Tools: raw.Proposal.Tools, Options: raw.Proposal.Options,
		}
	}
	return msg, nil
}

// assembleChatPrompt 组装会话 prompt。
// 关键：明确告诉模型"你是配置入口"，产出结构化 proposal；不确定就问（澄清）。
func assembleChatPrompt(userText string, files []string) string {
	var b strings.Builder
	b.WriteString("# 你在做什么\n")
	b.WriteString("你是「数据管家」的配置入口。用户会用自然语言让你安排工作。\n")
	b.WriteString("工作区里的表：")
	if len(files) == 0 {
		b.WriteString("（暂无）")
	} else {
		b.WriteString(strings.Join(files, "、"))
	}
	b.WriteString("\n\n# 用户说\n")
	b.WriteString(strings.TrimSpace(userText))
	b.WriteString(`

# 输出要求
只输出一个 JSON 对象，不要解释、不要 markdown：
{"reply":"给用户看的回话（中文，简洁）",
 "proposal": {"kind":"task|rule|tool_request|clarify|plan","title":"一句话标题",
   "detail":"展开说明","schedule":"cron 表达式（kind=task 时）","action":"要做什么",
   "tools":["read_sheet"],"options":["澄清选项1","澄清选项2"]}}

规则：
- 如果用户是在下达**改动表**的指令（如"记一笔收款"），kind 用 plan，reply 里说明你会先出清单让他确认。
- 如果是**安排自动化**（"每天下班前体检"），kind 用 task，schedule 给 cron。
- 如果是**立规则**，kind 用 rule，detail 里给出规则草稿。
- 如果**信息不足**（说不清是哪个铺位/哪个月/多少钱），kind 用 clarify，
  reply 里问清楚，options 给可选项。**不要猜。**
- 如果只是**问问题**，proposal 可以为 null，直接 reply 回答。`)
	return b.String()
}
