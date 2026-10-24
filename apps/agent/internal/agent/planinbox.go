package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
	"github.com/ark-local-ai/ark/apps/agent/internal/xl"
)

// appendPlanBlockedReason 是"这条新增行进不了确认清单"的统一理由。
//
// 为什么宁可挡下也不猜表头行：apply 靠 Item.HeaderRow 找表头列名，
// 猜错会把值写进错的列（甚至把表头本身覆盖掉）。财务台账上宁可让人手工加一行，
// 也不许代码拿不准还往下写。
const appendPlanBlockedReason = "新增行暂不能在作业区确认清单里应用"

// PlanInboxFile 在作业区运行时"只算清单、不写表"。
//
// 与 RunInboxFile 走**同一条**编排（读取 → 规则短路 → 问脑 → forbid 护栏 → 定位），
// 在 xl.ApplyEdits **之前**交回 Proposal。因此：
//   - 文件原样留在 inbox，**不进 done**；
//   - 不备份、不 SaveAs、不记账、不发通知；
//   - 用户确认后由既有 /apply 写（备份、指纹校验、记账都在那条路上）。
//
// 没有规则命中且未配置模型时返回 error：调用方（api）会把它记为失败，
// 数据继续留在 inbox 等配好模型后重试，而不是被当成"处理过了"。
func (a *Agent) PlanInboxFile(ctx context.Context, inboxFile, zoneID string) (*propose.Proposal, error) {
	var prop *propose.Proposal
	err := a.runInboxFile(ctx, inboxFile, inboxRunOpts{
		zoneID: zoneID,
		onPlan: func(p *propose.Proposal) { prop = p },
	})
	if err != nil {
		return nil, err
	}
	if prop == nil {
		// 走到这里说明编排提前返回了（例如目标表被跳过）。不能回一个"空成功"，
		// 否则界面上会多出一条什么都没算的待确认清单。
		return nil, fmt.Errorf("没有产出可确认的清单：%s 仍留在 inbox", displayName(inboxFile))
	}
	return prop, nil
}

// buildInboxProposal 把"已定位的 plan.Edit"翻成待确认清单（**纯内存，不落盘**）。
//
// 与对话路径（planner.go）产出的是同一种 Proposal，所以 /apply、清单界面、
// 账目 zone/source 全都不用改：作业区只是换了一种"产生清单"的入口。
func (a *Agent) buildInboxProposal(f *excelize.File, target, inboxFile, zoneID string,
	p *plan.Plan, blocked []plan.Edit, structs []*xl.Structure) *propose.Proposal {

	prop := &propose.Proposal{
		Root:    a.Layout.Root,
		Target:  target, // 绝对路径：apply 直接按它打开
		Summary: p.Summary,
		Zone:    zoneID,
		Sources: []string{inboxFile},
	}
	// 指纹目标表：确认期间表若被人改过，apply 会拒绝盲改。
	if fp, err := propose.Fingerprint(target); err == nil {
		prop.Finger = fp
	}

	bySheet := make(map[string]*xl.Structure, len(structs))
	for _, s := range structs {
		bySheet[s.Sheet] = s
	}
	defSheet := f.GetSheetName(0)

	for _, e := range p.Edits {
		sheet := e.Sheet
		if sheet == "" {
			sheet = defSheet
		}
		st := bySheet[sheet]

		if strings.EqualFold(e.Op, "append") {
			item, b := appendPlanItem(f, target, sheet, e, st)
			if b != nil {
				prop.Blocked = append(prop.Blocked, *b)
				continue
			}
			prop.Items = append(prop.Items, *item)
			continue
		}

		// set：旧值从工作簿该格读（apply 时还会再核对一次实际值）。
		old, _ := f.GetCellValue(sheet, e.Cell)
		item := propose.Item{
			File: filepath.Base(target), Sheet: sheet, Ref: e.Cell,
			Op: "set", Old: old, New: e.Value, Reason: e.Reason,
		}
		if col, row, cerr := excelize.CellNameToCoordinates(e.Cell); cerr == nil {
			item.Row, item.Col = row, col
			if st != nil && col-1 >= 0 && col-1 < len(st.Header) {
				item.Field = strings.TrimSpace(st.Header[col-1])
			}
		}
		prop.Items = append(prop.Items, item)
	}

	// 护栏挡下的单独列出：让人看见"想改但没让改"，不静默。
	for _, b := range blocked {
		prop.Blocked = append(prop.Blocked, propose.Blocked{
			Sheet: b.Sheet, Ref: b.Cell, Reason: b.Reason,
		})
	}
	// 规则层面的跳过（缺列/空值/定不到行）同样要露出来。
	for _, s := range p.SkipReasons {
		prop.Blocked = append(prop.Blocked, propose.Blocked{Reason: s})
	}

	if prop.Summary == "" {
		prop.Summary = fmt.Sprintf("将改动 %d 处", len(prop.Items))
	}
	if len(prop.Items) == 0 && len(prop.Blocked) == 0 {
		prop.Summary = "没有需要改动的地方"
	}
	return prop
}

// appendPlanItem 把一条 append 转成清单条目；拿不准就挡下（不猜）。
//
// HeaderRow 必须来自真实表结构（xl.Structure 的 FindHeader 结果）：
// 真实台账表头常在第 2–4 行，写死 1 会让 apply 把标题行当表头，
// 于是"列不在表头中"把整条拒掉，或更糟——把值写进错列。
func appendPlanItem(f *excelize.File, target, sheet string, e plan.Edit,
	st *xl.Structure) (*propose.Item, *propose.Blocked) {

	if st == nil || st.HeaderRow < 1 || len(st.Header) == 0 {
		return nil, &propose.Blocked{Sheet: sheet, Reason: appendPlanBlockedReason}
	}
	// 列名必须落在表头里：apply 会整条拒绝，提前挡下来，别让人确认一条注定失败的行。
	for name := range e.Row {
		if !headerHas(st.Header, name) {
			return nil, &propose.Blocked{Sheet: sheet, Field: name,
				Reason: appendPlanBlockedReason + "（列「" + name + "」不在表头中）"}
		}
	}

	// Ref/Row 只是**估算**（"大致加在哪一行"），真正写到第几行由 apply 现算。
	item := &propose.Item{
		File: filepath.Base(target), Sheet: sheet, Col: 1,
		HeaderRow: st.HeaderRow, Op: "append", Values: e.Row,
		Field: fieldLabel(e.Row), Reason: e.Reason,
	}
	if rows, err := f.GetRows(sheet); err == nil {
		last := len(rows)
		if last < st.HeaderRow {
			last = st.HeaderRow
		}
		item.Row = last + 1
		if ref, cerr := excelize.CoordinatesToCellName(1, item.Row); cerr == nil {
			item.Ref = ref
		}
	}
	return item, nil
}

// headerHas 判断列名是否在表头里（与 xl.AppendRow 一样按去空白后精确比较）。
func headerHas(header []string, name string) bool {
	for _, h := range header {
		if strings.TrimSpace(h) == strings.TrimSpace(name) {
			return true
		}
	}
	return false
}
