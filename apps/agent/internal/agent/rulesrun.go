package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
	"github.com/ark-local-ai/ark/apps/agent/internal/rules"
)

// DryRun 是"规则试跑"的公开入口：**只计算，不写文件、不记账、不消费 inbox**。
//
// 它复用 runRulesOn（与真实执行同一条定位逻辑），所以试跑看到什么，真跑就改什么
// ——包括"这行定不到位"也要如实报出来。若试跑另写一套简化逻辑，它会报出一堆
// "看起来能改、其实定不到行"的假结果，那比不给试跑更坏。
func (a *Agent) DryRun(inboxFile string, rf memory.RulesFile) (*plan.Plan, []string) {
	// 先算"哪些规则命中"（给人看的一句话），再走真实定位拿到逐条结果。
	var hits []string
	if d, err := rules.Load(inboxFile); err == nil {
		for _, r := range rf.PlanRules() {
			if rules.Match(r, d) {
				hits = append(hits, r.Name)
			}
		}
	}
	p, _ := a.runRulesOn(inboxFile, rf)
	return p, hits
}

// runRulesOn 是 inbox 自动链路里的"规则短路"：把命中的规则展开成**带坐标的**
// plan.Edit（inbox 路径用的是坐标，不是语义句）。
//
// 与对话路径（planner.go 的 planFromRules）的关键不同：这条路的输入是
// "inbox 里那个文件"（已知），所以要在这里直接定位；定位逻辑仍**复用 locate**，
// 不另写一套。
//
// 返回 (plan, 说明)。plan 为 nil 表示没有规则命中 → 调用方走模型。
func (a *Agent) runRulesOn(inboxFile string, rf memory.RulesFile) (*plan.Plan, string) {
	ok := rf.PlanRules()
	if len(ok) == 0 {
		return nil, ""
	}
	d, err := rules.Load(inboxFile)
	if err != nil {
		// 读不了就不短路（可能是奇怪格式），交给模型；但不能静默——记日志。
		return nil, "读不了 " + filepath.Base(inboxFile) + "（" + err.Error() + "）"
	}

	// 该批数据要往哪张表写：规则自己声明（最稳），否则第一张表。
	files, err := a.Layout.DataFiles()
	if err != nil || len(files) == 0 {
		return nil, ""
	}
	target := files[0]
	for _, r := range ok {
		if rules.Target(r) != "" {
			target = matchTable(files, rules.Target(r))
			break
		}
	}

	f, err := excelize.OpenFile(target)
	if err != nil {
		return nil, "打开目标表失败：" + err.Error()
	}
	defer f.Close()

	p := &plan.Plan{}
	var hits, skips []string
	for _, r := range ok {
		if !rules.Match(r, d) {
			continue
		}
		hits = append(hits, r.Name)
		intents, sks := rules.Expand(r, d)
		for _, s := range sks {
			skips = append(skips, fmt.Sprintf("规则「%s」：%s", r.Name, s.Reason))
		}
		for _, in := range intents {
			sheet := in.Sheet
			if sheet == "" {
				sheet = f.GetSheetName(0)
			}
			s, lerr := locate.LoadSheet(f, sheet)
			if lerr != nil {
				skips = append(skips, fmt.Sprintf("规则「%s」：读不了工作表「%s」", r.Name, sheet))
				continue
			}
			// 定位：按月（汇总表）或按键行 + 字段列
			var cell *locate.Cell
			if in.Month != "" {
				y, m, okYM := parseYearMonth(in.Month)
				if !okYM {
					skips = append(skips, fmt.Sprintf("规则「%s」：月份认不出来「%s」（第 %d 行）", r.Name, in.Month, in.Line))
					continue
				}
				cell, lerr = s.LocateMonthCell(in.Key, y, m)
			} else {
				cell, lerr = s.LocateRowField(nil, []string{in.Field}, in.Key)
			}
			if lerr != nil {
				skips = append(skips, fmt.Sprintf("规则「%s」：%s（第 %d 行）", r.Name, lerr.Error(), in.Line))
				continue
			}
			// 取值：set 直接写；add 要先读旧值累加（**与对话路径同一套语义**）
			val := any(in.Value)
			if in.Op == "add" {
				cur, _ := f.GetCellValue(sheet, cell.Ref)
				oldNum, okOld := parseNum(cur)
				addNum, okAdd := parseNum(in.Value)
				if !okOld || !okAdd {
					skips = append(skips, fmt.Sprintf("规则「%s」：%s 不是数字，无法累加（第 %d 行）", r.Name, cell.Ref, in.Line))
					continue
				}
				val = oldNum + addNum
			}
			p.Edits = append(p.Edits, plan.Edit{
				Op: "set", Sheet: sheet, Cell: cell.Ref, Value: val, Reason: in.Reason,
			})
		}
	}
	if len(hits) == 0 {
		return nil, ""
	}
	p.SkipReasons = skips
	if len(p.Edits) == 0 {
		// 命中了但一条也没落成：仍返回非 nil 的说明，让调用方知道规则出了什么问题，
		// 而不是静默回退（用户会以为规则在跑）。
		p.Summary = fmt.Sprintf("规则「%s」命中 %s，但没有一行能定位成功", strings.Join(hits, "、"), d.File)
		return p, p.Summary
	}
	p.Summary = fmt.Sprintf("规则「%s」处理 %s：%d 处", strings.Join(hits, "、"), d.File, len(p.Edits))
	return p, p.Summary
}

// declaredTarget 找出一条可执行规则声明的目标表名（空=没声明，需要分诊）。
func declaredTarget(rf memory.RulesFile) string {
	ok := rf.PlanRules()
	for _, r := range ok {
		if t := rules.Target(r); t != "" {
			return t
		}
	}
	return ""
}
