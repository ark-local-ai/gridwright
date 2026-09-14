package api

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/weight"
)

// 权重接口（见 docs/agent-architecture/22-权重设计.md）。
//
// 权重是 **联动图 + 账目 的派生视图**，现算，不落盘——
// 好处：永远不会与源数据不同步（没有"权重文件过期了"这种问题）。

// handleWeights GET /api/v1/weights —— 各表权重及解释
func (s *Server) handleWeights(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	_, layout, led, _ := s.cur()
	files, err := layout.DataFiles()
	if err != nil || len(files) == 0 {
		writeErr(w, http.StatusBadRequest, "工作区没有 .xlsx 表")
		return
	}
	g, err := graph.ScanWorkspace(layout.Root, files, graph.Options{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 权重也吃人声明的关联：结构信号里应包含"人明确说过的关系"。
	s.mergeDeclared(g)

	// 公式格数（结构信号之一）
	formulas := map[string]int{}
	for _, fp := range files {
		f, err := excelize.OpenFile(fp)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(fp), ".xlsx")
		for _, sh := range f.GetSheetList() {
			formulas[base+"!"+sh] = countFormulasIn(f, sh)
		}
		f.Close()
	}

	// 活跃度（账目算出；没有使用数据时为空 map → 只显示结构分）
	activity := map[string]int{}
	if led != nil {
		if act, err := led.ActivityByNode(30); err == nil {
			activity = act
		}
	}

	scores := weight.Compute(weight.Input{
		Graph: g, Formulas: formulas, Activity: activity, WindowDays: 30,
	})
	hasUsage := len(activity) > 0

	// ?format=text：命令行直接可读（.bat 里不用再塞 Python 解析 JSON）
	if wantsText(r) {
		var b strings.Builder
		b.WriteString(line("权重（%s）", layout.Root))
		if hasUsage {
			b.WriteString(line("使用数据：有（综合结构与最近使用）"))
		} else {
			b.WriteString(line("使用数据：暂无（当前只按结构重要性排序）"))
		}
		b.WriteString("\n")
		for _, sc := range scores {
			why := strings.Join(sc.Reasons, " · ")
			if why == "" {
				why = "—"
			}
			b.WriteString(line("%5.2f  %-28s  结构%.2f 活跃%.2f  | %s",
				sc.Attention, sc.Node.Sheet, sc.Structural, sc.Activity, why))
		}
		writeText(w, b.String())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scores":   scores,
		"hasUsage": hasUsage,
		"note":     noteFor(hasUsage),
	})
}

func noteFor(hasUsage bool) string {
	if hasUsage {
		return "权重综合了结构与最近使用情况"
	}
	return "还没有积累使用数据，当前只按结构重要性排序（被依赖越多的越重要）"
}

// countFormulasIn 数一张工作表的公式格数。
func countFormulasIn(f *excelize.File, sheet string) int {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return 0
	}
	n := 0
	for ri, row := range rows {
		for ci := range row {
			cell, err := excelize.CoordinatesToCellName(ci+1, ri+1)
			if err != nil {
				continue
			}
			if formula, err := f.GetCellFormula(sheet, cell); err == nil && formula != "" {
				n++
			}
		}
	}
	return n
}
