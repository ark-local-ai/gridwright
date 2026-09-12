package agent

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
	"github.com/ark-local-ai/ark/apps/agent/internal/xl"
)

// readInbox 读 inbox 文件为文本（csv 逐行；xlsx 读第一个 sheet 的表格文本）。
func readInbox(path string) (string, error) {
	ext := strings.ToLower(strings.TrimSpace("." + extOf(path)))
	switch ext {
	case ".csv":
		return readCSV(path)
	case ".xlsx":
		return readXLSXText(path)
	default:
		b, err := os.ReadFile(path)
		return string(b), err
	}
}

func extOf(p string) string {
	i := strings.LastIndexByte(p, '.')
	if i < 0 {
		return ""
	}
	return p[i+1:]
}

func readCSV(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, rec := range records {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.Join(rec, " | "))
	}
	return b.String(), nil
}

func readXLSXText(path string) (string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.Join(row, " | "))
	}
	return b.String(), nil
}

// assemblePrompt 组装发给脑的 prompt（spec §4 ①~⑤），发送量 O(1) 恒定。
// assemblePlanPrompt 组装"待改清单"用的 prompt（见 docs/agent-architecture/5-编辑语义.md）。
//
// 与旧的 assemblePrompt 关键区别：**要求模型输出业务语义坐标，不要输出单元格坐标**。
// 因为真实台账里（合并单元格、汇总表列是日期序列号）坐标极易错行错列，坐标由代码算。
func assemblePlanPrompt(instruction string, structures []*xl.Structure, g *graph.Graph, memoryText string) string {
	var b strings.Builder
	b.WriteString("# 任务\n")
	b.WriteString(strings.TrimSpace(instruction))
	b.WriteString("\n\n# 工作区表结构（表头 + 样例行）\n")
	for _, s := range structures {
		b.WriteString(s.Describe(5))
		b.WriteString("\n")
	}
	if g != nil {
		b.WriteString("# 表间依赖（改动会沿这些关系牵动其他表）\n")
		for _, e := range g.Edges {
			if e.Confidence != graph.ConfHigh {
				continue
			}
			fmt.Fprintf(&b, "  「%s」→「%s」(%s ×%d)\n", e.To.ID(), e.From.ID(), e.Kind, e.Count)
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(memoryText) != "" {
		b.WriteString(memoryText)
		b.WriteString("\n")
	}
	b.WriteString(`# 输出要求
只输出一个 JSON 对象，不要解释、不要 markdown。结构：
{"edits":[{"sheet":"工作表名","key":{"物业位置":"B31"},"month":"2026-08","field":"本月实收","op":"add","value":23540,"reason":"8月租金"}],
 "summary":"一句话总结","skip_reasons":["被跳过及原因"],"questions":["你不确定而需要问人的问题"]}
规则：
- **绝对不要给单元格坐标**。用 key（业务键列，如 物业位置/铺位号）、field（字段列名）、
  可选的 month（年月，格式 2026-08）来描述位置；坐标由程序计算。
- op=add 表示在该格原值上累加（收款类通常用 add）；op=set 表示覆盖。
- 若一行备注说了要拆到多个月（如"收到7月租金19256元，8月租金4284元"），
  **拆成多条 edits**，金额之和必须等于原额，且逐条给出 month。
- 不确定、信息不足、或疑似要动不允许改的列时，放进 questions 或 skip_reasons，
  **不要猜**。宁可问，也不要在财务表上猜错。`)
	return b.String()
}

// assemblePrompt 组装发给脑的 prompt（spec §4 ①~⑤），发送量 O(1) 恒定。
// 这是"inbox 自动处理"那条老路径用的（输出坐标为 edit 契约）；见 planner.go 的语义版。
func assemblePrompt(cfg *config.Config, structures []*xl.Structure, source, data string,
	recentLedger []string, rules memory.RulesFile, state memory.State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 新数据（来自 %s）\n%s\n\n", source, data)

	b.WriteString("# 工作区表结构\n")
	for _, s := range structures {
		b.WriteString(s.Describe(cfg.SampleRows))
	}

	b.WriteString("\n# 最近账目（最近 20 条）\n")
	if len(recentLedger) == 0 {
		b.WriteString("（暂无）\n")
	} else {
		for _, l := range recentLedger {
			b.WriteString(l + "\n")
		}
	}

	b.WriteString("\n# 滚动摘要（state.yaml）\n")
	b.WriteString(state.Describe())

	b.WriteString("\n# 规则（rules.yaml）\n")
	if len(rules.Rules) == 0 {
		b.WriteString("（无规则）\n")
	} else {
		for _, r := range rules.Rules {
			fmt.Fprintf(&b, "- %s：触发=%s，动作=%s", r.Name, r.Trigger, r.Action)
			if len(r.Forbid) > 0 {
				b.WriteString("，禁止修改列=[" + strings.Join(r.Forbid, ",") + "]")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n请按输出契约返回 JSON 编辑指令。禁止列绝对不能动，若要动请放进 skip_reasons 并跳过。\n")
	return b.String()
}

// guardEdits 把触碰 forbid 列的 edit 拦下来（硬性护栏，spec §4 校验）。
// set：由 cell 的列号 + 该 sheet 表头推出列名，在 forbid 集合则拦。
// append：row 任一列名在 forbid 集合则拦。
// headers: sheet 名 → 表头列名数组。
func guardEdits(p *plan.Plan, forbid map[string]bool, headers map[string][]string) (kept, blocked []plan.Edit) {
	if len(forbid) == 0 {
		return p.Edits, nil
	}
	for _, e := range p.Edits {
		hit := false
		switch strings.ToLower(e.Op) {
		case "set":
			if col := colNameFromCell(e.Sheet, e.Cell, headers); forbid[normKey(col)] {
				hit = true
			}
		case "append":
			for name := range e.Row {
				if forbid[normKey(name)] {
					hit = true
					break
				}
			}
		}
		if hit {
			b := e
			b.Reason = "命中 forbid 护栏"
			blocked = append(blocked, b)
		} else {
			kept = append(kept, e)
		}
	}
	return kept, blocked
}

// colNameFromCell 由 cell 坐标的列号 + 该 sheet 表头推出列名；推不出返回空（放行）。
func colNameFromCell(sheet, cell string, headers map[string][]string) string {
	col := colIndexOf(cell) // 1-based
	hs := headers[sheet]
	if col-1 >= 0 && col-1 < len(hs) {
		return hs[col-1]
	}
	return ""
}

// colIndexOf 把 "B7" 解析成 1-based 列号 2（纯 A1 字母解析，不依赖 excelize）。
func colIndexOf(cell string) int {
	n := 0
	for _, r := range cell {
		if r >= 'A' && r <= 'Z' {
			n = n*26 + int(r-'A'+1)
		} else if r >= 'a' && r <= 'z' {
			n = n*26 + int(r-'a'+1)
		} else {
			break
		}
	}
	return n
}

func normKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	return strings.ToLower(s)
}

func cellNew(e plan.Edit) string {
	if e.Row != nil {
		return "新行"
	}
	return fmt.Sprintf("%v", e.Value)
}

// ruleName 从 reason 里粗略匹配规则名（记账 rule 列）。
func ruleName(rules memory.RulesFile, reason string) string {
	for _, r := range rules.Rules {
		if strings.Contains(reason, r.Name) {
			return r.Name
		}
	}
	return "-"
}
