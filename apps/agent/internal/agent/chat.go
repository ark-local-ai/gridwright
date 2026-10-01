package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/trace"
)

// Chat 是"会话"入口（见 docs/agent-architecture/7-对话与自动化任务.md）：
// 用户说一句话 → 模型回话 + 一个结构化 proposal（任务/规则/工具申请/澄清/改表计划）。
// **模型只建议，人拍板**——proposal 要界面上确认才生效。
func (a *Agent) Chat(ctx context.Context, userText string, files []string) (*convo.Message, error) {
	return a.ChatWithImages(ctx, userText, files, nil, nil)
}

// ChatWithImages 与 Chat 相同，但可以随消息带上图片（截图/照片里的数据）。
//
// 为什么值得单独一条路径：财务手上常常是**一张截图**——微信里发来的收款
// 记录、别人拍的表格——而不是一个规整的 csv。只能收文字，等于把最省事的
// 那条输入通道关掉。
//
// files 仍会拼进提示词（模型据此知道有哪些表），图片则走多模态消息。
//
// history 是**这段对话之前说过的话**。不加它，模型每轮都是失忆重启：
// 同一件事会被反复追问（实测连问 4 轮，用户答过“先出对照清单”之后又被重新问一遍），
// 而图里的数据也会因为“这一轮没带图”被当成“未提供数据”。所以必须带上。
func (a *Agent) ChatWithImages(ctx context.Context, userText string, files []string, imgs []llm.ImageInput, history []convo.Message) (*convo.Message, error) {
	if a.Brain == nil || !a.Brain.Ready() {
		return nil, fmt.Errorf("还没配置模型（脑）：请在设置里填 base_url 与 api_key 后就能对话了")
	}
	tr := trace.From(ctx)
	donePrompt := tr.Step("组装提示")
	prompt := assembleChatPrompt(userText, files, history)
	if len(imgs) > 0 {
		// 明确告诉模型图里是什么，否则它可能只当装饰
		prompt += fmt.Sprintf("\n\n（用户随消息附了 %d 张图，通常是截图或照片里的数据；请从中读数。）", len(imgs))
	}
	tr.SetPrompt(prompt)
	donePrompt(fmt.Sprintf("%d 字 · 历史 %d 条 · 工作区表 %d 张 · 附图 %d 张",
		len([]rune(prompt)), len(history), len(files), len(imgs)))

	doneBrain := tr.Step("调模型")
	content, err := a.Brain.ChatJSONWithImages(ctx, prompt, imgs)
	if err != nil {
		doneBrain("失败：" + err.Error())
		return nil, err
	}
	doneBrain(fmt.Sprintf("模型 %s", a.Brain.Model()))

	doneParse := tr.Step("解析回话")
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
		doneParse("模型没按 JSON 回，退回纯文本")
		return &convo.Message{Role: convo.RoleAgent, Text: strings.TrimSpace(content)}, nil
	}
	msg := &convo.Message{Role: convo.RoleAgent, Text: raw.Reply}
	if raw.Proposal != nil && raw.Proposal.Kind != "" {
		msg.Proposal = &convo.Proposal{
			Kind: raw.Proposal.Kind, Title: raw.Proposal.Title, Detail: raw.Proposal.Detail,
			Schedule: raw.Proposal.Schedule, Action: raw.Proposal.Action,
			Tools: raw.Proposal.Tools, Options: raw.Proposal.Options,
		}
		doneParse("proposal=" + raw.Proposal.Kind)
	} else {
		doneParse("无 proposal（纯回话）")
	}
	return msg, nil
}

// 历史注入的上限：只给最近若干条、总长截到若干字。
//
// 为什么必须限：会话越长，历史块越大，token 会随轮次**线性增长**——
// 而 docs/agent-architecture/15-对话与提示词.md 的“恒定上下文”原则要求
// 每次发给模型的量是 O(1)。所以只带“最近的上下文”，不带整段历史。
const (
	historyMaxMsgs  = 8
	historyMaxRunes = 2000
)

const historyHead = "# 这段对话之前说过什么（**接着往下做，已经问过、用户已答过的不要再问一遍**）\n"

// renderHistory 把之前的对话渲染成给模型看的一小段。
//
// 图片不贴 data URL（那是几百 KB）：只写“（附图 N 张）”。模型的视觉输入
// 走多模态消息那条路，这里只需要它知道“当时有图”，别因为这一轮没带图就说数据没给。
func renderHistory(history []convo.Message) string {
	if len(history) == 0 {
		return ""
	}
	if len(history) > historyMaxMsgs {
		history = history[len(history)-historyMaxMsgs:]
	}
	var b strings.Builder
	b.WriteString(historyHead)
	for _, m := range history {
		trimmed := strings.TrimSpace(m.Text)
		if trimmed == "" && len(m.Images) == 0 && m.Proposal == nil {
			continue
		}
		who := "用户"
		switch m.Role {
		case convo.RoleAgent:
			who = "你"
		case convo.RoleSystem:
			who = "系统"
		}
		if trimmed == "" {
			trimmed = "（只发了图，没写字）"
		}
		if len(m.Images) > 0 {
			trimmed += fmt.Sprintf("（附图 %d 张）", len(m.Images))
		}
		if m.Proposal != nil && m.Proposal.Title != "" {
			trimmed += "［已给建议：" + m.Proposal.Title + "］"
		}
		fmt.Fprintf(&b, "%s：%s\n", who, trimmed)
	}
	out := b.String()
	// 超长就留尾部（尾部是最近的话，比开头更相关）
	if r := []rune(out); len(r) > historyMaxRunes {
		out = historyHead + "…\n" + string(r[len(r)-historyMaxRunes:])
	}
	return out
}

// assembleChatPrompt 组装会话 prompt。
// 关键：明确告诉模型"你是配置入口"，产出结构化 proposal；不确定就问（澄清）。
func assembleChatPrompt(userText string, files []string, history []convo.Message) string {
	var b strings.Builder
	b.WriteString("# 你在做什么\n")
	b.WriteString("你是「数据管家」的配置入口。用户会用自然语言让你安排工作。\n")
	// 清单精确到工作表：用户嘴里说的是 sheet 名（"填进销售明细表"），
	// 只给文件名会让他以为"工作区里没这张表"而白问一轮澄清。
	b.WriteString("工作区里的表（括号内是各工作簿的工作表名）：")
	if len(files) == 0 {
		b.WriteString("（暂无）")
	} else {
		b.WriteString("\n- ")
		b.WriteString(strings.Join(files, "\n- "))
	}
	b.WriteString("\n\n")
	if h := renderHistory(history); h != "" {
		b.WriteString(h)
		b.WriteString("\n")
	}
	b.WriteString("# 用户说\n")
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
- **用户提到的名字要对照上面那份清单**：他说"销售明细表"而清单里某个工作簿有同名工作表，
  那就是它——**不要说"工作区里没有这张表"**，把目标说清楚（哪个文件里的哪个工作表）即可。
- 只有在清单里**确实找不到**对应名字时，才用 clarify 问是哪个文件/工作表。

## 什么时候**不该**问（照做就行）

系统按业务键定位到行、再写到某一列；**查不到的行可以新增**。所以：
- 用户说"把 X 列填进某表"、"按某列匹配"这类，**目标与列都明确时直接出 plan**，
  不要在回复里追问"匹配键是什么"——匹配键系统自己按业务键（铺位/房号/租户）找，
  找不到就新增，**这不是需要先问的事**。
- 用户附了图/数据时，**图里就是数据源**。不要问"数据来自哪张表"。
- 一次最多问**一个问题**，且必须是真的挡住整件事的那个。
  能做的部分先做，做不了的部分在 reply 里一句话说清——
  **不要用一串追问把整件事停住**。

## 接着上面那段历史做

- 历史里**你已经问过的、或用户已经答过的，不要再问第二次**。
  用户刚回答完（如"先出对照清单"、"用这张图的数据"），就直接往下做那件事。
- 用户之前某一轮附过图，**图里的数据在后面的轮次里一直有效**。
  不要因为这一轮没带图就说"未提供图片"或反问数据来源。

## 系统能做到的事（别低估自己）

- 用户说"按某列的名字匹配、**查不到就新增**"时，这能做：出清单时系统逐个键去表里找，
  找到就改那一行，找不到就**新增一行**；新增的条在清单里会**明确标成「新增行」**，
  用户确认后才写。**不要把"不能新增行"当成拒绝或反问的理由**。
- **不能新增工作表、不能建公式、不能动被禁止的列**。
- 说不出清楚的地方，用 clarify 并把**可选项**给出来（options），让人点一下就能回答。
- 如果只是**问问题**，proposal 可以为 null，直接 reply 回答。`)
	return b.String()
}
