package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
	"github.com/ark-local-ai/ark/apps/agent/internal/terms"
)

// 这是"待改清单"的产出（见 docs/agent-architecture/5-编辑语义.md、19-界面设计.md 阶段 4）。
//
// 设计要点：**LLM 只说业务语义，代码负责定位**。
// 模型返回的编辑指令形如：
//
//	{"sheet":"各月租金表","key":{"物业位置":"B31"},
//	 "month":"2026-08","field":"本月实收","op":"add","value":23540,"reason":"8月租金"}
//
// 我们用 locate 把它翻成具体单元格、算出旧值，交给 propose 待确认。
// 模型**永远不吐坐标**——那样极易错行错列。

// PlanOptions 控制一次计划。
type PlanOptions struct {
	File string // 目标文件（空=第一张表）
}

// semanticEdit 是模型返回的一条"业务语义"编辑（**不含坐标**）。
type semanticEdit struct {
	File   string            `json:"file"`
	Sheet  string            `json:"sheet"`
	Key    map[string]string `json:"key"`   // 定位行的键列（如 物业位置: B31）
	Month  string            `json:"month"` // 2026-08（列是月份的汇总表用）
	Field  string            `json:"field"` // 字段列名
	Op     string            `json:"op"`    // set | add
	Value  any               `json:"value"`
	Reason string            `json:"reason"`
}

// Plan 组装 prompt → 问脑 → 语义指令翻成具体格 → 产出待确认清单（**不落盘**）。
//
// 先试**规则短路**：rules.yaml 里 when/then 齐全的规则命中这批输入时，
// 直接由规则产出清单，**不问模型**（省 token、可复现、可审计）。
// 没有规则命中才走模型（见 docs/agent-architecture/30-规则引擎.md）。
//
// 需要配好模型；但**规则命中时不需要**——这正是短路的收益：断网也能按规矩办事。
func (a *Agent) Plan(ctx context.Context, instruction string, opts PlanOptions) (*propose.Proposal, error) {
	// 规则短路优先：命中即返回，不调模型。
	if prop, err := a.planFromRules(instruction, opts); err == nil && prop != nil {
		return prop, nil
	}
	if a.Brain == nil || !a.Brain.Ready() {
		return nil, fmt.Errorf("还没配置模型（脑）：请在设置里填 base_url 与 api_key，之后才能让它判断该改哪些格")
	}
	files, err := a.Layout.DataFiles()
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("工作区没有 .xlsx 表")
	}
	target := files[0]
	if opts.File != "" {
		for _, fp := range files {
			if strings.EqualFold(filepath.Base(fp), opts.File) {
				target = fp
				break
			}
		}
	}

	// 素材：表结构 + 联动图
	f, err := excelize.OpenFile(target)
	if err != nil {
		return nil, fmt.Errorf("打开目标表: %w", err)
	}
	structs, err := a.collectStructures(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	g, _ := graph.ScanWorkspace(a.Layout.Root, files, graph.Options{})

	// 相关记忆（见 docs/agent-architecture/21-记忆设计.md）：**只取与本次指令相关的**，
	// 不把全部记忆塞进 prompt——上下文里没有噪音，模型更容易判对。
	memText := ""
	if mem, merr := memory2.Open(a.Layout.Root); merr == nil {
		memText = mem.RetrieveByText(instruction).Describe()
	}
	// 语义映射（见 24/25）：用户嘴里的模糊词先翻译成明确的表/字段/类别。
	// "那笔钱""老李那家"这类说法，第一次问清、以后复用。
	if tm, terr := terms.Open(a.Layout.Root); terr == nil {
		if hits := tm.Resolve(instruction); len(hits) > 0 {
			memText = terms.Describe(hits) + memText
		}
	}

	prompt := assemblePlanPrompt(instruction, structs, g, memText)
	resp, err := a.Brain.PlanSemantic(ctx, prompt)
	if err != nil {
		return nil, err
	}
	// edits 的原始 JSON 在这里解析成 semanticEdit
	var edits []semanticEdit
	if len(resp.Edits) > 0 {
		if err := json.Unmarshal(resp.Edits, &edits); err != nil {
			return nil, fmt.Errorf("解析编辑指令: %w", err)
		}
	}

	prop := &propose.Proposal{Root: a.Layout.Root, Target: target, Summary: resp.Summary}
	for _, q := range resp.Questions {
		prop.Blocked = append(prop.Blocked, propose.Blocked{Reason: "需要你确认：" + q})
	}
	for _, s := range resp.SkipReasons {
		prop.Blocked = append(prop.Blocked, propose.Blocked{Reason: s})
	}

	// 重新打开用于取值/定位（resolveEdit 会读旧值）
	f2, err := excelize.OpenFile(target)
	if err != nil {
		return nil, err
	}
	defer f2.Close()
	for _, e := range edits {
		item, blocked := resolveEdit(f2, target, e)
		if blocked != nil {
			prop.Blocked = append(prop.Blocked, *blocked)
			continue
		}
		if g != nil {
			item.Affects = g.Propagate(graph.Node{File: filepath.Base(target), Sheet: item.Sheet})
		}
		prop.Items = append(prop.Items, *item)
	}

	// 指纹：Apply 前据此确认文件没被换过
	if fp, err := propose.Fingerprint(target); err == nil {
		prop.Finger = fp
	}
	if prop.Summary == "" {
		prop.Summary = fmt.Sprintf("将改动 %d 处", len(prop.Items))
	}
	if len(prop.Items) == 0 && len(prop.Blocked) == 0 {
		prop.Summary = "没有需要改动的地方"
	}
	return prop, nil
}

// resolveEdit 把一条语义编辑翻成具体单元格（含旧值）。
// 定位失败 → 返回 blocked（让人看见，不静默丢）。
func resolveEdit(f *excelize.File, target string, e semanticEdit) (*propose.Item, *propose.Blocked) {
	sheet := e.Sheet
	if sheet == "" {
		sheet = f.GetSheetName(0)
	}
	s, err := locate.LoadSheet(f, sheet)
	if err != nil {
		return nil, &propose.Blocked{Sheet: sheet, Field: e.Field, Reason: "工作表读取失败：" + err.Error()}
	}
	if e.Field == "" {
		return nil, &propose.Blocked{Sheet: sheet, Reason: "缺少字段名（field）"}
	}

	var cell *locate.Cell
	if e.Month != "" {
		y, m, ok := parseYearMonth(e.Month)
		if !ok {
			return nil, &propose.Blocked{Sheet: sheet, Field: e.Field,
				Reason: "月份格式无法识别（应如 2026-08）：" + e.Month}
		}
		cell, err = s.LocateMonthCell(e.Key, y, m)
	} else {
		cell, err = s.LocateRowField(nil, []string{e.Field}, e.Key)
	}
	if err != nil {
		return nil, &propose.Blocked{Sheet: sheet, Field: e.Field, Reason: "定位失败：" + err.Error()}
	}

	oldVal, _ := f.GetCellValue(sheet, cell.Ref)
	newVal := e.Value
	op := strings.ToLower(strings.TrimSpace(e.Op))
	if op == "" {
		op = "set"
	}
	if op == "add" {
		oldNum, _ := parseNum(oldVal)
		addNum, _ := parseNum(fmt.Sprint(e.Value))
		newVal = oldNum + addNum
	}

	return &propose.Item{
		File: filepath.Base(target), Sheet: sheet, Ref: cell.Ref, Row: cell.Row, Col: cell.Col,
		Key: e.Key, Month: e.Month, Field: e.Field,
		Op: op, Old: oldVal, New: newVal, Reason: e.Reason,
	}, nil
}

// parseYearMonth 解析 "2026-08" / "2026/8" / "2026年8月"。
func parseYearMonth(s string) (int, int, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "年", "-")
	s = strings.ReplaceAll(s, "月", "")
	s = strings.ReplaceAll(s, "/", "-")
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	var y, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &y); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &m); err != nil {
		return 0, 0, false
	}
	if y < 1900 || y > 2200 || m < 1 || m > 12 {
		return 0, 0, false
	}
	return y, m, true
}

// parseNum 宽松解析金额（去千分位/空格），失败返回 false。
func parseNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u3000", "")
	if s == "" {
		return 0, false
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return 0, false
	}
	return v, true
}
