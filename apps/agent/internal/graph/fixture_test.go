package graph

import (
	"testing"

	"github.com/xuri/excelize/v2"
)

// writeBook 写一个含单张 sheet 的最小工作簿，cells 是 "A1"→值。
func writeBook(t *testing.T, path, sheet string, cells map[string]string) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", sheet)
	for ref, v := range cells {
		if len(v) > 0 && v[0] == '=' {
			_ = f.SetCellFormula(sheet, ref, v)
		} else {
			_ = f.SetCellValue(sheet, ref, v)
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}
