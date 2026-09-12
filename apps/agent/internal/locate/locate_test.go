package locate

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestParseHeaderMonth(t *testing.T) {
	cases := []struct {
		in    string
		y, m  int
		ok    bool
	}{
		{"2026年9月", 2026, 9, true},
		{"2026年09月", 2026, 9, true},
		{" 2025年11月 ", 2025, 11, true},
		{"45292", 2024, 1, true}, // Excel 序列号
		{"45292.0", 0, 0, false},
		{"合计", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		got, ok := parseHeaderMonth(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok=%v 期望 %v", c.in, ok, c.ok)
			continue
		}
		if ok && (got.Year() != c.y || int(got.Month()) != c.m) {
			t.Errorf("%q: 得到 %d-%d 期望 %d-%d", c.in, got.Year(), got.Month(), c.y, c.m)
		}
	}
}

func TestNormalize(t *testing.T) {
	if Normalize(" A03 ") != "a03" {
		t.Fatal("去空格+小写失败")
	}
	if Normalize("物业 位置") != "物业位置" {
		t.Fatal("去中间空格失败")
	}
}

func TestLocateRowFieldAndMonth(t *testing.T) {
	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "月租金")
	// 表头在第 2 行（模拟真实表：第 1 行是标题）
	_ = f.SetCellValue("月租金", "A1", "某月租金情况表")
	for i, h := range []string{"序号", "物业位置", "租户名称", "本月实收", "本月欠款"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		_ = f.SetCellValue("月租金", cell, h)
	}
	_ = f.SetCellValue("月租金", "B3", "A03")
	_ = f.SetCellValue("月租金", "C3", "早餐店")
	_ = f.SetCellValue("月租金", "D3", 19354.02)
	// 合并单元格：铺位占两行，值只在首行
	_ = f.SetCellValue("月租金", "B5", "A05")
	_ = f.SetCellValue("月租金", "C5", "美宜佳")

	s, err := LoadSheet(f, "月租金")
	if err != nil {
		t.Fatal(err)
	}
	cell, err := s.LocateRowField(nil, []string{"本月实收"},
		map[string]string{"物业位置": "A03"})
	if err != nil {
		t.Fatalf("定位失败: %v", err)
	}
	if cell.Ref != "D3" {
		t.Fatalf("A03 本月实收 得到 %s，期望 D3", cell.Ref)
	}
	// 向下填充：A05 在行 5，其"序号"列空着也不影响定位
	cell2, err := s.LocateRowField(nil, []string{"本月欠款"},
		map[string]string{"物业位置": "A05"})
	if err != nil {
		t.Fatalf("定位 A05 失败: %v", err)
	}
	if cell2.Ref != "E5" {
		t.Fatalf("A05 本月欠款 得到 %s，期望 E5", cell2.Ref)
	}
}

// TestLocateRealWorkbook 用真实台账验证定位器（表在就测，不在就跳过）。
func TestLocateRealWorkbook(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	matches, _ := filepath.Glob(filepath.Join(root, "docs", "*.xlsx"))
	if len(matches) == 0 {
		t.Skip("未找到真实表，跳过")
	}
	f, err := excelize.OpenFile(matches[0])
	if err != nil {
		t.Skipf("打不开真实表: %v", err)
	}
	defer f.Close()

	// 1) 2025年11月租金表：定位 A03（李德红/早餐店）的"本月实收"。
	//    注意：A03 在 2023 年的表里还不存在（该租户 2024.11 才起租），
	//    所以必须挑一张真的含 A03 的表，否则测的是"表里没这个人"而不是定位逻辑。
	s, err := LoadSheet(f, "2025年11月租金 （日）")
	if err != nil {
		t.Skipf("找不到该 sheet: %v", err)
	}
	hdrIdx, header := s.FindHeader(10)
	t.Logf("2025年11月租金表 表头在第 %d 行(1基)，列头: %v", hdrIdx+1, header)

	cell, err := s.LocateRowField(nil, []string{"本月实收"},
		map[string]string{"物业位置": "A03"})
	if err != nil {
		t.Errorf("定位 A03 本月实收 失败: %v", err)
	} else {
		t.Logf("A03 本月实收 → %s（值=%v）", cell.Ref, valueAt(s, cell.Row-1, cell.Col-1))
	}

	// 不存在的铺位应干净地报错，而不是命中错误行
	if _, err := s.LocateRowField(nil, []string{"本月实收"},
		map[string]string{"物业位置": "Z99"}); err == nil {
		t.Error("不存在的铺位 Z99 应报错，却定位成功了")
	}

	// 2) 汇总表：定位某铺位某月的列（列头是日期序列号）
	sum, err := LoadSheet(f, "租金汇总表（月末）")
	if err != nil {
		t.Skipf("找不到汇总表: %v", err)
	}
	shdr, sheader := sum.FindHeader(10)
	t.Logf("汇总表 表头在第 %d 行(1基)", shdr+1)
	found := 0
	for _, y := range []int{2025, 2026} {
		for m := 1; m <= 12; m++ {
			col := ColByMonth(sheader, y, m)
			if col >= 0 {
				found++
			}
		}
	}
	t.Logf("汇总表里能识别出的月份列: %d/24", found)
	if found == 0 {
		t.Error("汇总表应能识别出至少一个月份列")
	}
}

func valueAt(s *Sheet, r, c int) string {
	if r < 0 || r >= len(s.Rows) {
		return ""
	}
	row := s.Rows[r]
	if c < 0 || c >= len(row) {
		return ""
	}
	return row[c]
}
