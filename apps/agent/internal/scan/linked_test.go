package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// 跨表引用核对的测试（见 linked.go）。
//
// 这类检测在财务工具里最大的风险不是漏报，而是**误报**：
// 一个"两个数不一样"就报警的检查，会把面积/金额、上期/本期这些本来就不相等的
// 东西全报出来，用户很快就学会无视它——那比不检测更糟。
// 所以这组用例同时钉住两侧：该抓的抓、不该碰的不碰。

// writeLinkedXlsx 造一张"明细 + 汇总"的表：
// 汇总 A2 是 =明细!A2 的公式，缓存值可指定（用来模拟"没跟着改"）。
func writeLinkedXlsx(t *testing.T, path string, cached float64) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "明细")
	if _, err := f.NewSheet("汇总"); err != nil {
		t.Fatal(err)
	}
	_ = f.SetCellValue("明细", "A1", "金额")
	_ = f.SetCellValue("明细", "A2", 50000) // 明细记的是 5 万
	_ = f.SetCellValue("汇总", "A1", "合计")
	// 先落一个"上次的结果"，再盖公式——得到的正是 Excel 保存时
	// "公式 + 旧缓存值" 的状态（源改了、这格还没重算）。
	_ = f.SetCellValue("汇总", "A2", cached)
	_ = f.SetCellFormula("汇总", "A2", "明细!A2")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

// writeCompoundXlsx 造一张公式含运算的表（=A2+1），用于验证"不碰算式"。
func writeCompoundXlsx(t *testing.T, path string) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "明细")
	if _, err := f.NewSheet("汇总"); err != nil {
		t.Fatal(err)
	}
	_ = f.SetCellValue("明细", "A2", 50000)
	_ = f.SetCellValue("汇总", "A2", 12345) // 与 A2+1 不等，但含运算 → 不该判
	_ = f.SetCellFormula("汇总", "A2", "明细!A2+1")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

// TestLinkedValuesCatchesDroppedZero 正例：明细 5 万、汇总写成 5 千（少个 0）。
// 这就是用户描述的那类错，必须抓到。
func TestLinkedValuesCatchesDroppedZero(t *testing.T) {
	p := filepath.Join(t.TempDir(), "少个0.xlsx")
	writeLinkedXlsx(t, p, 5000)

	rep, err := Run(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var hit *Issue
	for i := range rep.Issues {
		if rep.Issues[i].Kind == "mismatch" {
			hit = &rep.Issues[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("没抓到 50000 vs 5000 的差异；issues=%+v", rep.Issues)
	}
	if hit.Sheet != "汇总" || hit.Ref != "A2" {
		t.Errorf("坐标不对：%s!%s", hit.Sheet, hit.Ref)
	}
	if hit.Severity != SevWarn {
		t.Errorf("严重度应为 warn，得到 %s", hit.Severity)
	}
	t.Logf("抓到：%s!%s — %s", hit.Sheet, hit.Ref, hit.Message)
}

// TestLinkedValuesSilentWhenEqual 反例：两边一致时绝不能报。
func TestLinkedValuesSilentWhenEqual(t *testing.T) {
	p := filepath.Join(t.TempDir(), "一致.xlsx")
	writeLinkedXlsx(t, p, 50000)

	rep, err := Run(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range rep.Issues {
		if i.Kind == "mismatch" {
			t.Fatalf("两边都是 50000，不该报：%s!%s %s", i.Sheet, i.Ref, i.Message)
		}
	}
}

// TestLinkedValuesIgnoresCompoundFormula 反例：含运算的公式不判（没有求值器，猜不得）。
func TestLinkedValuesIgnoresCompoundFormula(t *testing.T) {
	p := filepath.Join(t.TempDir(), "算式.xlsx")
	writeCompoundXlsx(t, p)

	rep, err := Run(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range rep.Issues {
		if i.Kind == "mismatch" {
			t.Fatalf("=明细!A2+1 是算式，不该判：%s!%s %s", i.Sheet, i.Ref, i.Message)
		}
	}
}

// TestLinkedValuesRealWorkbookNoFalsePositive 真实台账上零误报。
//
// 那份"御城二期销售报表"里 汇总表 有 87 条 =销售明细表!xx 的引用，且工作簿
// 自带核对行（=L14-销售明细表!R1000）全部缓存为 0——也就是**本来就一致**。
// 检测若在这里报出任何一条，就是误报。文件不在（换机器）则跳过。
func TestLinkedValuesRealWorkbookNoFalsePositive(t *testing.T) {
	cands := []string{
		filepath.Join("..", "..", "..", "..", "docs", "御城二期销售报表2026.09.13.xlsx"),
		// 用户机器上实测用的那份（放在微信下载目录里，不在仓库里）
		`D:\softWare\微信\微信聊天保存文件夹\xwechat_files\wxid_2ijpo8o37uml22_aac5\msg\file\2026-09\testwork\御城二期销售报表2026.09.13.xlsx`,
	}
	var p string
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			p = c
			break
		}
	}
	if p == "" {
		t.Skip("本机没有这份真实表，跳过")
	}
	rep, err := Run(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range rep.Issues {
		if i.Kind == "mismatch" {
			t.Errorf("真实表本来就一致，不该报误报：%s!%s %s", i.Sheet, i.Ref, i.Message)
		}
	}
}
