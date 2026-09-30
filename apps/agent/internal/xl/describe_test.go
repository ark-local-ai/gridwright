package xl

import (
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestDescribeCarriesColumnLetters 钉住"列字母 = 列名"这条对应关系。
//
// 为什么必须有它：用户下指令时的原话就是"按 **D 列**的名字填 **E、F 列**"，
// 而提示词禁止模型输出单元格坐标、只允许业务列名。所以模型必须能自己把
// "D 列"翻译成"房号"—— 这就要求发给它的结构里带列字母。
//
// 首格为空是**真实情况**（销售明细表的表头行第一格就是空的，因为上面还有
// 一行合并的上级表头），不是随手造的边界：它同时验证了"列字母不会被错位"。
func TestDescribeCarriesColumnLetters(t *testing.T) {
	s := &Structure{
		Sheet:     "销售明细表",
		HeaderRow: 4,
		Header:    []string{"", "楼栋", "单元", "房号", "客户名称"},
		Sample:    [][]any{{"1", "二期1栋", "A座", "401", ""}},
	}
	got := s.Describe(1)

	for _, want := range []string{"表头在第 4 行", "A=（空）", "B=楼栋", "C=单元", "D=房号", "E=客户名称"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe 里缺 %q\n实际输出：\n%s", want, got)
		}
	}
	// 反向断言：错位的典型表现就是把 B 说成 D
	if strings.Contains(got, "D=楼栋") || strings.Contains(got, "B=房号") {
		t.Errorf("列字母与列名错位了\n实际输出：\n%s", got)
	}
	// 超过 Z 之后要走 AA/AB，不能退化成乱码
	s2 := &Structure{Sheet: "宽表", Header: make([]string, 28)}
	s2.Header[26] = "第二十七列"
	s2.Header[27] = "第二十八列"
	got2 := s2.Describe(0)
	for _, want := range []string{"AA=第二十七列", "AB=第二十八列"} {
		if !strings.Contains(got2, want) {
			t.Errorf("超过 Z 的列字母不对，缺 %q\n实际输出：\n%s", want, got2)
		}
	}
}

// TestReadStructureFindsRealHeader 钉住"表头不在第 1 行"这件事。
//
// 为什么必须有它：真实台账每一张表的第 1 行都是标题（"御城二期销售明细表"），
// 表头在第 2–4 行，有的还是两行合并。老实现写死 rows[0]，于是模型收到的
// "列头"是一个标题字符串，三条"样例"全是表头行 —— WS-7 实测清单因此全空。
func TestReadStructureFindsRealHeader(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "销售明细表")

	// 第 1 行标题、第 2 行日期、第 3 行上级分组、第 4 行叶子表头
	_ = f.SetCellValue("销售明细表", "A1", "御城二期销售明细表")
	_ = f.SetCellValue("销售明细表", "A2", "截止日期：2026-09-13")
	_ = f.SetCellValue("销售明细表", "A3", "序号")
	_ = f.SetCellValue("销售明细表", "B3", "销售基本情况")
	_ = f.SetCellValue("销售明细表", "H3", "销售总价")
	_ = f.SetCellValue("销售明细表", "B4", "楼栋")
	_ = f.SetCellValue("销售明细表", "C4", "单元")
	_ = f.SetCellValue("销售明细表", "D4", "房号")
	_ = f.SetCellValue("销售明细表", "E4", "建筑面积")
	_ = f.SetCellValue("销售明细表", "F4", "客户名称")
	_ = f.SetCellValue("销售明细表", "G4", "实收总额")
	// 只在上级行出现的列，不能因为取了叶子行就丢掉
	_ = f.SetCellValue("销售明细表", "X3", "按揭银行")
	// 表头行以下才是数据
	_ = f.SetCellValue("销售明细表", "B5", "二期1栋")
	_ = f.SetCellValue("销售明细表", "C5", "A座")
	_ = f.SetCellValue("销售明细表", "D5", "401")

	s, err := ReadStructure(f, "销售明细表", 5)
	if err != nil {
		t.Fatal(err)
	}
	if s.HeaderRow != 4 {
		t.Errorf("表头行应为 4，得到 %d（Header=%#v）", s.HeaderRow, s.Header)
	}
	if len(s.Header) < 6 || s.Header[3] != "房号" || s.Header[1] != "楼栋" {
		t.Errorf("表头读错：%#v", s.Header)
	}
	// 叶子行首格为空 → 列字母不能整体左移，房号必须落在 D
	desc := s.Describe(1)
	if !strings.Contains(desc, "D=房号") || !strings.Contains(desc, "B=楼栋") {
		t.Errorf("列字母错位\n%s", desc)
	}
	// 上级行独有的列要保留
	if !strings.Contains(desc, "按揭银行") {
		t.Errorf("上级行独有的列丢了\n%s", desc)
	}
	// 样例必须从表头行之后开始（第 5 行），不能把表头当样例
	if len(s.Sample) != 1 {
		t.Fatalf("样例应为 1 行，得到 %d：%#v", len(s.Sample), s.Sample)
	}
	if len(s.Sample[0]) < 4 || s.Sample[0][3] != "401" {
		t.Errorf("样例取错（可能把表头当成了数据）：%#v", s.Sample)
	}

	// 干净的表（表头就在第 1 行）不受影响
	clean := excelize.NewFile()
	defer clean.Close()
	_ = clean.SetSheetName("Sheet1", "干净表")
	_ = clean.SetCellValue("干净表", "A1", "品名")
	_ = clean.SetCellValue("干净表", "B1", "数量")
	_ = clean.SetCellValue("干净表", "A2", "A料")
	g, err := ReadStructure(clean, "干净表", 5)
	if err != nil {
		t.Fatal(err)
	}
	if g.HeaderRow != 1 || len(g.Header) == 0 || g.Header[0] != "品名" {
		t.Errorf("干净表表头读错：行 %d，%#v", g.HeaderRow, g.Header)
	}
}
