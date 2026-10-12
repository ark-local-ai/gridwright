package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/prompts"
)

// 从对话里提炼"快捷任务模板"（复核反馈 → 沉淀，见 prompts 包注释）。
//
// 与 Distill（提炼记忆）的分工：
//   - 记忆记的是"这件事是什么"（事实/决策），影响模型判断；
//   - 快捷任务记的是"这件事怎么说"，作为一个可点的入口，仍然走模型+确认。
//
// 模型只**提议**，落盘由人在界面上改好名字/模板后再存。
// 槽位一律由模板重算（prompts.ExtractSlots），不采信模型给的 slots。

// PromptCandidate 是一条快捷任务候选（还没落盘）。
type PromptCandidate struct {
	Name     string   `json:"name"`
	When     string   `json:"when,omitempty"`
	Template string   `json:"template"`
	Note     string   `json:"note,omitempty"`
	Slots    []string `json:"slots,omitempty"`
}

// DistillPrompt 把一段会话提炼成快捷任务候选。只读，不写文件。
func (a *Agent) DistillPrompt(ctx context.Context, c *convo.Conversation) ([]PromptCandidate, error) {
	if a.Brain == nil || !a.Brain.Ready() {
		return nil, fmt.Errorf("还没配置模型（脑）：沉淀快捷任务需要模型总结")
	}
	if c == nil || len(c.Messages) == 0 {
		return nil, fmt.Errorf("这条记录里还没有对话内容")
	}
	content, err := a.Brain.ChatJSON(ctx, assemblePromptFilePrompt(c))
	if err != nil {
		return nil, err
	}
	var raw struct {
		Candidates []struct {
			Name     string `json:"name"`
			When     string `json:"when"`
			Template string `json:"template"`
			Note     string `json:"note"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("解析总结结果: %w（原始: %.200s）", err, content)
	}
	out := make([]PromptCandidate, 0, len(raw.Candidates))
	for _, x := range raw.Candidates {
		name := strings.TrimSpace(x.Name)
		tmpl := strings.TrimSpace(x.Template)
		// 名字或模板空的直接丢：宁可少给一条让人确认，也不要塞垃圾。
		if name == "" || tmpl == "" {
			continue
		}
		out = append(out, PromptCandidate{
			Name: name, When: strings.TrimSpace(x.When), Template: tmpl,
			Note: strings.TrimSpace(x.Note), Slots: prompts.ExtractSlots(tmpl),
		})
		if len(out) >= 2 { // 上界：一次总结不该甩出十几个让人挑
			break
		}
	}
	return out, nil
}

func assemblePromptFilePrompt(c *convo.Conversation) string {
	var b strings.Builder
	b.WriteString("# 任务\n")
	b.WriteString("下面是一段「数据管家」的使用记录：用户用自然语言让系统改 Excel 台账，\n")
	b.WriteString("中间可能经过几轮复核与纠正，最终确认。\n")
	b.WriteString("请把**最终成功的做法**总结成一个可复用的「快捷任务模板」，下次同类事情一句话触发。\n\n")
	b.WriteString("# 输出要求\n")
	b.WriteString("只输出一个 JSON 对象，不要解释、不要 markdown：\n")
	b.WriteString(`{"candidates":[
  {"name":"给这个快捷任务起个短名（不超过 12 字）",
   "when":"什么时候用（一句话，给人判断该点哪个）",
   "template":"把用户最终要的指令写成模板；只把每次都不同的具体业务值替换成 {槽位}，其余保留成完整指令",
   "note":"触发后要注意什么（可选）"}
]}` + "\n\n")
	b.WriteString("规则：\n")
	b.WriteString("- template 必须是一句**可以直接发给系统**的中文指令，把 {槽位} 填上后即完整。\n")
	b.WriteString("- 只把\"每次会变\"的业务值抽成槽位（铺位号、月份、金额、租户名）；\n")
	b.WriteString("  固定的口径、表名、字段名保留成文字，不要抽成槽位。\n")
	b.WriteString("- 总结的是**最终确认的做法**，不要把被否掉的方案、一次性的闲聊、报错也抽进来。\n")
	b.WriteString("- 最多 2 条；没有可复用的就返回 {\"candidates\":[]}——这很正常。\n\n")
	b.WriteString("# 对话记录\n")
	for _, m := range c.Messages {
		if m.Role == convo.RoleSystem {
			continue
		}
		who := "用户"
		if m.Role == convo.RoleAgent {
			who = "系统"
		}
		txt := strings.TrimSpace(m.Text)
		if txt == "" {
			continue
		}
		if len([]rune(txt)) > 600 {
			txt = string([]rune(txt)[:600]) + "…"
		}
		fmt.Fprintf(&b, "%s：%s\n", who, txt)
	}
	return b.String()
}
