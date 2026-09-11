package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
	"github.com/ark-local-ai/ark/apps/agent/internal/xl"
)

// 不联网、不问脑：直接验证 读结构 → 执行 edits(set+append) → 备份 → 记账 的链路。
func TestM1Loop(t *testing.T) {
	dir := t.TempDir()

	// 1) 造一张被看管的表：销售.xlsx，表头 品名/数量/价格/成本
	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "销售")
	for i, h := range []string{"品名", "数量", "价格", "成本"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("销售", cell, h)
	}
	// 第一行数据
	_ = f.SetCellValue("销售", "A2", "A料")
	_ = f.SetCellValue("销售", "B2", 5)
	_ = f.SetCellValue("销售", "C2", 320)
	_ = f.SetCellValue("销售", "D2", 200)
	xf := filepath.Join(dir, "销售.xlsx")
	if err := f.SaveAs(xf); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// 2) 读结构
	g, err := excelize.OpenFile(xf)
	if err != nil {
		t.Fatal(err)
	}
	s, err := xl.ReadStructure(g, "销售", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Header) != 4 || s.Header[0] != "品名" {
		t.Fatalf("表头不对: %#v", s.Header)
	}

	// 3) forbid 护栏：禁止改 成本 列
	forbid := map[string]bool{"成本": true}
	p := &plan.Plan{
		Summary: "改价+新增",
		Edits: []plan.Edit{
			{Op: "set", Sheet: "销售", Cell: "C2", Value: 455, Reason: "报价#14"}, // 价格，允许
			{Op: "set", Sheet: "销售", Cell: "D2", Value: 999, Reason: "改成本"},   // 成本，被拦
			{Op: "append", Sheet: "销售", Row: map[string]any{"品名": "B料", "数量": 3, "价格": 100}},
		},
	}
	headers := map[string][]string{"销售": s.Header}
	kept, blocked := guardEdits(p, forbid, headers)
	if len(kept) != 2 || len(blocked) != 1 {
		t.Fatalf("护栏拦截不对: kept=%d blocked=%d", len(kept), len(blocked))
	}
	if blocked[0].Cell != "D2" {
		t.Fatalf("被拦的不是成本列: %s", blocked[0].Cell)
	}
	p.Edits = kept

	// 4) 备份 + 执行 + 写回
	if _, err := xl.Backup(xf, 5); err != nil {
		t.Fatal(err)
	}
	results := xl.ApplyEdits(g, p.Edits)
	ok := 0
	for _, r := range results {
		if r.Status == "ok" {
			ok++
		}
	}
	if ok != 2 {
		t.Fatalf("应有 2 条 ok: %v", results)
	}
	if err := g.SaveAs(xf); err != nil {
		t.Fatal(err)
	}
	g.Close()

	// 5) 验证写回
	h, err := excelize.OpenFile(xf)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := h.GetCellValue("销售", "C2")
	if v != "455" {
		t.Fatalf("C2 应为 455，得到 %v", v)
	}
	rows, _ := h.GetRows("销售")
	if len(rows) != 3 {
		t.Fatalf("应有 3 行（表头+2 行），得到 %d", len(rows))
	}
	if rows[2][0] != "B料" {
		t.Fatalf("追加行品名应为 B料，得到 %v", rows[2][0])
	}
	h.Close()

	// 6) 记账
	lp := filepath.Join(dir, "ledger.csv")
	led, err := ledger.Open(lp)
	if err != nil {
		t.Fatal(err)
	}
	_ = led.Append(time.Now(), "销售", "C2", "set", "320", "455", "报价#14", "quote.csv", "报价同步", "deepseek-chat", "ok")
	recent, err := led.Recent(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || !strings.Contains(recent[0], "455") {
		t.Fatalf("账目不对: %v", recent)
	}
	if _, err := os.Stat(lp); err != nil {
		t.Fatalf("账目文件不存在: %v", err)
	}
}
