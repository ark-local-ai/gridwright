package api

import (
	"net/http"
	"path/filepath"

	"github.com/xuri/excelize/v2"
)

// shapes.go 提供"批量取表形状"的接口（给左栏的微缩缩略图用）。
//
// 为什么要单独一个接口：缩略图要给每张表画"行×列"的格子，靠形状认表。
// 按现有的 /sheets/preview 一张表一次请求，24 张表就是 24 次往返、24 次开文件
// （excelize.OpenFile 每次都要解压整包）。这里一次开文件、遍历所有 sheet，
// 只取"行数/列数/公式数"这三个画缩略图真正需要的数，不取任何单元格内容。

// sheetShape 是一张表的"形状"——足够画缩略图的最小信息。
type sheetShape struct {
	Sheet    string `json:"sheet"`
	File     string `json:"file"`
	Rows     int    `json:"rows"`     // 总行数（含表头）
	Cols     int    `json:"cols"`     // 总列数
	Formulas int    `json:"formulas"` // 公式格数
}

// handleSheetShapes GET /api/v1/sheets/shapes?file=
// 返回该文件里**所有**工作表的形状（一次开文件）。
func (s *Server) handleSheetShapes(w http.ResponseWriter, r *http.Request) {
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

	shapes := []sheetShape{}
	for _, name := range f.GetSheetList() {
		rows, err := f.GetRows(name)
		if err != nil {
			// 单张表读不出不该拖垮整批：给个 0 形状，缩略图画成空表
			shapes = append(shapes, sheetShape{Sheet: name, File: filepath.Base(target)})
			continue
		}
		cols := 0
		for _, row := range rows {
			if len(row) > cols {
				cols = len(row)
			}
		}
		shapes = append(shapes, sheetShape{
			Sheet:    name,
			File:     filepath.Base(target),
			Rows:     len(rows),
			Cols:     cols,
			Formulas: countFormulas(f, name, rows),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":   filepath.Base(target),
		"shapes": shapes,
	})
}
