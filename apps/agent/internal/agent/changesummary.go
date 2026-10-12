package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
)

// SheetChangeSummary 是"某张表这次改了什么"的一句话总结。
type SheetChangeSummary struct {
	Sheet string `json:"sheet"`
	Text  string `json:"text"`
}

// maxSummaryRunes 是总结的硬上限（用户要求"不超过 20 个字"）。
const maxSummaryRunes = 20

// SummarizeChange 把一次**已应用**的改动总结成"每张表一句话"（≤20 字），
// 给看板的"上次改动"与对话回执用。
//
// 为什么要结合对话上下文：只看坐标只能写出"B2、C2 改了值"，没用；把用户那句
// "按名字索引，补充最后两列：按揭银行和日期"带上，才能写出
// "销售明细表补充按揭信息和日期"这种看得懂的话。
func (a *Agent) SummarizeChange(ctx context.Context, c *convo.Conversation, results []propose.ApplyResult) ([]SheetChangeSummary, error) {
	if a.Brain == nil || !a.Brain.Ready() {
		return nil, fmt.Errorf("未配置模型")
	}
	var ok []propose.ApplyResult
	for _, r := range results {
		if r.Status == "ok" {
			ok = append(ok, r)
		}
	}
	if len(ok) == 0 {
		return nil, fmt.Errorf("没有成功的改动可总结")
	}
	content, err := a.Brain.ChatJSON(ctx, assembleChangeSummaryPrompt(c, ok))
	if err != nil {
		return nil, err
	}
	var raw struct {
		Summaries []struct {
			Sheet string `json:"sheet"`
			Text  string `json:"text"`
		} `json:"summaries"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("解析改动总结: %w（原始: %.200s）", err, content)
	}
	// 实际动过的工作表名（逐字、含首尾空格）——模型给的 sheet 常丢尾随空格，
	// 这里按 trim 后的名字对齐回真实名，否则笔记键对不上、看板读不到。
	exact := map[string]string{}
	for _, r := range ok {
		if strings.TrimSpace(r.Sheet) != "" {
			exact[strings.TrimSpace(r.Sheet)] = r.Sheet
		}
	}
	onlySheet := ""
	if len(exact) == 1 {
		for _, v := range exact {
			onlySheet = v
		}
	}
	out := make([]SheetChangeSummary, 0, len(raw.Summaries))
	for _, x := range raw.Summaries {
		text := strings.TrimSpace(x.Text)
		if text == "" {
			continue
		}
		if len([]rune(text)) > maxSummaryRunes {
			text = string([]rune(text)[:maxSummaryRunes])
		}
		sheet := ""
		if real, ok := exact[strings.TrimSpace(x.Sheet)]; ok {
			sheet = real
		} else if onlySheet != "" {
			sheet = onlySheet // 只动了一张表：名字对不上也挂到它名下
		} else {
			continue // 多张表又对不上名：跳过，不错误挂到别的表
		}
		out = append(out, SheetChangeSummary{Sheet: sheet, Text: text})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("模型没有给出可用的总结")
	}
	return out, nil
}

func assembleChangeSummaryPrompt(c *convo.Conversation, results []propose.ApplyResult) string {
	var b strings.Builder
	b.WriteString("# 任务\n")
	b.WriteString("用户刚确认并写入了一笔对 Excel 台账的改动。请用**一句不超过 20 个字**的中文，\n")
	b.WriteString("说清每张表这次改了什么（给看板上的「上次改动」用）。\n\n")
	if c != nil && len(c.Messages) > 0 {
		b.WriteString("# 背景（用户是怎么说的）\n")
		n := 0
		for i := len(c.Messages) - 1; i >= 0 && n < 6; i-- {
			m := c.Messages[i]
			if m.Role == convo.RoleSystem || strings.TrimSpace(m.Text) == "" {
				continue
			}
			who := "用户"
			if m.Role == convo.RoleAgent {
				who = "助手"
			}
			txt := strings.TrimSpace(m.Text)
			if len([]rune(txt)) > 200 {
				txt = string([]rune(txt)[:200]) + "…"
			}
			b.WriteString(who + "：" + txt + "\n")
			n++
		}
		b.WriteString("\n")
	}
	b.WriteString("# 实际写入的改动\n")
	b.WriteString("（sheet 列是工作表名，summary 里必须**逐字照抄**）\n")
	for _, r := range results {
		fmt.Fprintf(&b, "- %s %s：%v → %v", r.Sheet, r.Field, r.Old, r.New)
		if r.Op == "append" {
			b.WriteString("（新增行）")
		}
		b.WriteString("\n")
	}
	b.WriteString(`
# 输出要求
只输出一个 JSON 对象，不要解释、不要 markdown：
{"summaries":[{"sheet":"工作表名","text":"不超过 20 字的一句话"}]}

规则：
- 一张表一条；text 面向不懂表格的人，**不要写单元格坐标**（如 H8）。
- 结合用户的话来概括（用户说"按揭银行和日期"，就写"补充按揭信息和日期"），
  不要只罗列字段名。
- 最多 20 个字，言简意赅。`)
	return b.String()
}
