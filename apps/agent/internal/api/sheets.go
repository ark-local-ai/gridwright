package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
)

// 表详情与预览（见 docs/agent-architecture/19-界面设计.md 阶段 1）：
// 点联动图上的模块 → 出一个"大概汇总" + 一个能进入表格的入口。

type sheetPreview struct {
	File      string     `json:"file"`
	Sheet     string     `json:"sheet"`
	Rows      int        `json:"rows"`     // 总行数（含表头）
	Cols      int        `json:"cols"`     // 总列数
	Formulas  int        `json:"formulas"` // 公式格数
	HeaderRow int        `json:"headerRow"` // 表头在第几行（1 基）
	Header    []string   `json:"header"`
	Sample    [][]string `json:"sample"`    // 前 N 行数据
	Summaries []sheetSum `json:"summaries"` // 识别出的汇总（顶部汇总行 / 合计行）
	Note      string     `json:"note,omitempty"`
}

// sheetSum 是一条"汇总数据"。来源可能是表顶部的概览行，也可能是行内的"合计"行。
type sheetSum struct {
	Label  string   `json:"label"`
	Values []string `json:"values"`
	Ref    string   `json:"ref,omitempty"` // 所在单元格，可跳转
}

// handleSheetPreview GET /api/v1/sheets/preview?file=&sheet=&rows=
func (s *Server) handleSheetPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	q := r.URL.Query()
	fileName := q.Get("file")
	sheetName := q.Get("sheet")
	rows := 50
	if v := q.Get("rows"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			rows = n
		}
	}
	if sheetName == "" {
		writeErr(w, http.StatusBadRequest, "缺少 sheet 参数")
		return
	}
	target, err := s.pickTable(fileName)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := excelize.OpenFile(target)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "打开表失败："+err.Error())
		return
	}
	defer f.Close()

	sh, err := locate.LoadSheet(f, sheetName)
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("表「%s」不存在", sheetName))
		return
	}

	pv := &sheetPreview{File: filepath.Base(target), Sheet: sheetName, Rows: len(sh.Rows)}
	mx := 0
	for _, row := range sh.Rows {
		if len(row) > mx {
			mx = len(row)
		}
	}
	pv.Cols = mx
	pv.Formulas = countFormulas(f, sheetName, sh.Rows)

	// 表头行（真实表标题占了前几行，用 locate 的启发式找）
	hdrIdx, header := sh.FindHeader(10)
	pv.HeaderRow = hdrIdx + 1
	pv.Header = header

	// 样例数据（表头之后）
	for i := hdrIdx + 1; i < len(sh.Rows) && len(pv.Sample) < rows; i++ {
		pv.Sample = append(pv.Sample, sh.Rows[i])
	}

	// 汇总：① 表头之前的概览行（如"本月收租率/应收/实收"）② 行内"合计/小计"
	pv.Summaries = detectSummaries(sh, hdrIdx)
	// 保证是数组而非 null（前端不必到处特判）
	if pv.Header == nil {
		pv.Header = []string{}
	}
	if pv.Sample == nil {
		pv.Sample = [][]string{}
	}
	if pv.Summaries == nil {
		pv.Summaries = []sheetSum{}
	}

	// 诚实边界：excelize 不求值，公式格显示的是上次保存的缓存值
	if pv.Formulas > 0 {
		pv.Note = "该表含公式，显示的是 Excel 上次保存时的值；结果以 Excel 打开为准。"
	}
	writeJSON(w, http.StatusOK, pv)
}

func countFormulas(f *excelize.File, sheet string, rows [][]string) int {
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

// detectSummaries 找汇总信息，两类：
//   - 表头之前的行里，含"率/应收/实收/数量/合计"等关键词的 → 概览
//   - 表头之后，首列含"合计/小计/总计"的 → 行内合计
func detectSummaries(sh *locate.Sheet, hdrIdx int) []sheetSum {
	var out []sheetSum
	overviewKeys := []string{"率", "应收", "实收", "数量", "合计", "总计", "单位"}
	// ① 概览行（表头之前）
	for i := 0; i < hdrIdx && i < len(sh.Rows); i++ {
		row := sh.Rows[i]
		if len(row) == 0 {
			continue
		}
		line := strings.Join(row, " ")
		hits := 0
		for _, k := range overviewKeys {
			if strings.Contains(line, k) {
				hits++
			}
		}
		if hits >= 2 {
			ref, _ := excelize.CoordinatesToCellName(1, i+1)
			out = append(out, sheetSum{Label: strings.TrimSpace(row[0]), Values: compact(row), Ref: ref})
		}
	}
	// ② 行内合计
	for i := hdrIdx + 1; i < len(sh.Rows); i++ {
		row := sh.Rows[i]
		if len(row) == 0 {
			continue
		}
		first := strings.TrimSpace(row[0])
		if first == "合计" || first == "小计" || first == "总计" {
			ref, _ := excelize.CoordinatesToCellName(1, i+1)
			out = append(out, sheetSum{Label: first, Values: compact(row), Ref: ref})
		}
	}
	return out
}

// compact 去掉空串，便于前端展示。
func compact(row []string) []string {
	out := make([]string, 0, len(row))
	for _, v := range row {
		s := strings.TrimSpace(v)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// handleListSheets GET /api/v1/sheets?file=  —— 列出某文件的工作表（供表格入口页用）。
func (s *Server) handleListSheets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	target, err := s.pickTable(r.URL.Query().Get("file"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := excelize.OpenFile(target)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if sheets == nil {
		sheets = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":   filepath.Base(target),
		"sheets": sheets,
	})
}
