package api

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

// ledgerOpen 是 ledger.Open 的薄封装，供测试用。
func ledgerOpen(path string) (*ledger.Ledger, error) { return ledger.Open(path) }

// configForTest 造一个最小配置（只需工作区）。
func configForTest(workspace string) *config.Config {
	return &config.Config{Workspace: workspace, PollSeconds: 5, SampleRows: 10}
}

// writeXlsxWithBlankRow 写一张**中间有空行**的表。
//
// 为什么要这个 fixture：空行在 excelize 里读出来是 nil 切片，JSON 序列化成 null，
// 前端 `row[0]` 会直接抛 "Cannot read properties of null"——实测切到真实台账的
// "实收日报表"就白屏了。这是回归用例，钉住"空行必须变成空数组而不是 null"。
func writeXlsxWithBlankRow(t *testing.T, path string) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetCellValue("Sheet1", "A1", "序号")
	_ = f.SetCellValue("Sheet1", "B1", "物业位置")
	_ = f.SetCellValue("Sheet1", "A2", 1)
	_ = f.SetCellValue("Sheet1", "B2", "A03")
	// 第 3 行留空（不写任何格）——真实表里"隔一行空一行"很常见
	_ = f.SetCellValue("Sheet1", "A4", 3)
	_ = f.SetCellValue("Sheet1", "B4", "A05")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

// newTestServerWithBlankRow 起一个测试服务端，工作区里那张表**中间带空行**。
func newTestServerWithBlankRow(t *testing.T) *Server {
	t.Helper()
	t.Setenv("GRIDWRIGHT_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	layout, err := workspace.LayoutOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledgerOpen(layout.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	writeXlsxWithBlankRow(t, filepath.Join(dir, "空行表.xlsx"))
	return New(configForTest(dir), layout, led, "127.0.0.1:0")
}

// writeMinimalXlsx 写一张最小可用的表，供 API 测试用（表头 + 一行数据 + 一个公式）。
func writeMinimalXlsx(t *testing.T, path string) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "Sheet1")
	headers := []string{"序号", "物业位置", "租户名称", "本月应收租金", "本月实收", "本月欠款"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("Sheet1", cell, h)
	}
	_ = f.SetCellValue("Sheet1", "A2", 1)
	_ = f.SetCellValue("Sheet1", "B2", "A03")
	_ = f.SetCellValue("Sheet1", "C2", "早餐店")
	_ = f.SetCellValue("Sheet1", "D2", 19354.02)
	_ = f.SetCellValue("Sheet1", "E2", 19354.02)
	// 本月欠款 = 应收 - 实收（表内公式）
	_ = f.SetCellFormula("Sheet1", "F2", "=D2-E2")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}
