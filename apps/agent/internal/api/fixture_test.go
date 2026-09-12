package api

import (
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
)

// ledgerOpen 是 ledger.Open 的薄封装，供测试用。
func ledgerOpen(path string) (*ledger.Ledger, error) { return ledger.Open(path) }

// configForTest 造一个最小配置（只需工作区）。
func configForTest(workspace string) *config.Config {
	return &config.Config{Workspace: workspace, PollSeconds: 5, SampleRows: 10}
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
