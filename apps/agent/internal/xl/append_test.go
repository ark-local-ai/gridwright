package xl

import (
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestAppendRowUsesGivenHeaderRow 表头在第 3 行时，新增行也要按第 3 行对齐。
//
// 为什么必须传 headerRow：真实台账的表头常在第 2–4 行（前面是标题/日期——
// 那份销售明细表就是）。以前写死 rows[0]，于是把标题行当表头，
// “列不在表头中”把每条新增都拒掉——功能看起来“不支持新增行”，其实只是认错了表头。
func TestAppendRowUsesGivenHeaderRow(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetCellValue("Sheet1", "A1", "御城二期销售明细表")
	_ = f.SetCellValue("Sheet1", "A2", "2026年9月")
	_ = f.SetCellValue("Sheet1", "A3", "楼栋")
	_ = f.SetCellValue("Sheet1", "B3", "房号")
	_ = f.SetCellValue("Sheet1", "C3", "客户名称")
	_ = f.SetCellValue("Sheet1", "A4", "1栋")
	_ = f.SetCellValue("Sheet1", "B4", "101")
	_ = f.SetCellValue("Sheet1", "C4", "张三")

	row, err := AppendRow(f, "Sheet1", 3, map[string]any{"楼栋": "1栋", "房号": "102", "客户名称": "李四"})
	if err != nil {
		t.Fatalf("新增失败：%v", err)
	}
	if row != 5 {
		t.Fatalf("应写到第 5 行，得到 %d", row)
	}
	if v, _ := f.GetCellValue("Sheet1", "B5"); v != "102" {
		t.Errorf("B5 应为 102，得到 %q", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "C5"); v != "李四" {
		t.Errorf("C5 应为 李四，得到 %q", v)
	}
	// 标题行、表头行、原有数据行都不能被碰
	if v, _ := f.GetCellValue("Sheet1", "A1"); v != "御城二期销售明细表" {
		t.Errorf("标题被改了：%q", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "A3"); v != "楼栋" {
		t.Errorf("表头被改了：%q", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "A4"); v != "1栋" {
		t.Errorf("原有数据行被改了：%q", v)
	}
}

// TestAppendRowRejectsUnknownColumn 列名不在表头 → 报错，不猜列、不写半行。
func TestAppendRowRejectsUnknownColumn(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetCellValue("Sheet1", "A1", "楼栋")
	_ = f.SetCellValue("Sheet1", "A2", "1栋")

	_, err := AppendRow(f, "Sheet1", 1, map[string]any{"楼栋": "2栋", "不存在的列": "x"})
	if err == nil {
		t.Fatal("列名不在表头时应报错")
	}
	// 一个格都不该写（先全量校验、再统一写，是刻意的：不允许写一半）
	if v, _ := f.GetCellValue("Sheet1", "A3"); v != "" {
		t.Errorf("拒绝时不该写入，得到 A3=%q", v)
	}
}
