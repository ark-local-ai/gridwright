package api

import (
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
)

// 待确认清单的"整行预览"（见界面需求：不要只给一排坐标，要把那一行放进来，
// 改动的格子标色）。
//
// 为什么由后端算而不是前端拼：整行数据在 xlsx 里，前端只有被改的那一格。
// 让前端为每条改动去拉一次 sheets/preview 既慢又对不齐表头；后端打开一次表，
// 按表头把整行取出来，前端只负责画。
//
// 只读：不写任何东西。清单仍在服务端，这里只是它的"看板视图"。

type propRowChange struct {
	Col     int      `json:"col"`   // 列号（1 基）
	Ref     string   `json:"ref"`   // A1 坐标（点击跳到那一格用）
	Field   string   `json:"field"` // 字段列名
	Op      string   `json:"op"`    // set | add | append
	Old     any      `json:"old"`
	New     any      `json:"new"`
	Reason  string   `json:"reason,omitempty"`
	Affects []string `json:"affects,omitempty"`
}

type propRow struct {
	Row     int             `json:"row"`   // 原表行号（1 基）；新增行为预估行号
	Cells   []string        `json:"cells"` // 整行（已补齐到表头列数）
	Kind    string          `json:"kind"`  // edit（改已有行）| new（新增行）
	Changes []propRowChange `json:"changes"`
}

type propSheetRows struct {
	Sheet     string    `json:"sheet"`
	HeaderRow int       `json:"headerRow"` // 表头在第几行（1 基）
	Header    []string  `json:"header"`
	Rows      []propRow `json:"rows"`
}

type propRowsResp struct {
	ID     string          `json:"id"`
	Target string          `json:"target"`
	Sheets []propSheetRows `json:"sheets"`
}

// handleProposalRows GET /api/v1/proposal/rows?id= —— 把清单里的改动还原成整行预览。
func (s *Server) handleProposalRows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "缺少 id")
		return
	}
	prop := s.proposals().Get(id)
	if prop == nil {
		writeErr(w, http.StatusNotFound, "清单不存在或已过期")
		return
	}
	f, err := excelize.OpenFile(prop.Target)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "打开表失败："+err.Error())
		return
	}
	defer f.Close()

	// 按工作表分组，保持出现顺序（界面按它分块显示）
	order := []string{}
	bySheet := map[string][]propose.Item{}
	for _, it := range prop.Items {
		if _, ok := bySheet[it.Sheet]; !ok {
			order = append(order, it.Sheet)
		}
		bySheet[it.Sheet] = append(bySheet[it.Sheet], it)
	}

	resp := propRowsResp{ID: prop.ID, Target: filepath.Base(prop.Target), Sheets: []propSheetRows{}}
	for _, sheet := range order {
		// 读不到的 sheet 不整体失败：跳到下一个，界面至少显示能显示的
		sh, serr := locate.LoadSheet(f, sheet)
		if serr != nil {
			continue
		}
		hdrIdx, header := sh.FindHeader(10)
		ps := propSheetRows{Sheet: sheet, HeaderRow: hdrIdx + 1, Header: header, Rows: []propRow{}}
		if ps.Header == nil {
			ps.Header = []string{}
		}
		width := len(header)
		rowAt := map[int]int{} // 原表行号 → ps.Rows 下标（同一行多条改动合并）

		for _, it := range bySheet[sheet] {
			if it.Op == "append" {
				// 新增行：表里还没有这一行，按表头把 values 摆好
				cells := make([]string, width)
				var changes []propRowChange
				for ci, h := range header {
					if v, ok := it.Values[h]; ok {
						cells[ci] = fmt.Sprint(v)
						ref, _ := excelize.CoordinatesToCellName(ci+1, it.Row)
						changes = append(changes, propRowChange{Col: ci + 1, Ref: ref, Field: h, Op: "append", New: v,
							Reason: it.Reason, Affects: it.Affects})
					}
				}
				if changes == nil {
					changes = []propRowChange{}
				}
				ps.Rows = append(ps.Rows, propRow{Row: it.Row, Cells: cells, Kind: "new", Changes: changes})
				continue
			}

			ref, _ := excelize.CoordinatesToCellName(it.Col, it.Row)
			change := propRowChange{Col: it.Col, Ref: ref, Field: it.Field, Op: it.Op, Old: it.Old, New: it.New,
				Reason: it.Reason, Affects: it.Affects}
			if idx, ok := rowAt[it.Row]; ok { // 同一行的第二条及以后：并到已有行
				ps.Rows[idx].Changes = append(ps.Rows[idx].Changes, change)
				continue
			}
			cells := rowCells(sh, it.Row, width)
			ps.Rows = append(ps.Rows, propRow{
				Row: it.Row, Cells: cells, Kind: "edit", Changes: []propRowChange{change},
			})
			rowAt[it.Row] = len(ps.Rows) - 1
		}
		resp.Sheets = append(resp.Sheets, ps)
	}
	writeJSON(w, http.StatusOK, resp)
}

// rowCells 取原表某一行的值并补齐到 width 列（excelize 的行可能短于表头）。
func rowCells(sh *locate.Sheet, row, width int) []string {
	out := make([]string, width)
	if row >= 1 && row <= len(sh.Rows) {
		src := sh.Rows[row-1]
		for i := 0; i < width && i < len(src); i++ {
			out[i] = src[i]
		}
		// 表头比实际数据窄时，别把多出来的值截掉
		if len(src) > width {
			out = append(out, src[width:]...)
		}
	}
	return out
}
