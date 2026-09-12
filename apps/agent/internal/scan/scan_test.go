package scan

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseAmount(t *testing.T) {
	cases := map[string]float64{
		"19,354.02":    19354.02,
		"1,761,143.10": 1761143.10,
		" 803,333.40 ": 803333.40,
		"0.00":         0,
	}
	for in, want := range cases {
		got, ok := parseAmount(in)
		if !ok {
			t.Errorf("%q 解析失败", in)
			continue
		}
		if !near(got, want) {
			t.Errorf("%q 得到 %v 期望 %v", in, got, want)
		}
	}
	if _, ok := parseAmount("#REF!"); ok {
		t.Error("#REF! 不应解析成金额")
	}
	if _, ok := parseAmount("本月未交滞纳金58644元"); ok {
		t.Error("整句话不应解析成金额")
	}
}

// TestScanRealWorkbook 对真实表跑体检，并报告发现了什么。
// 这是可行性验证：离线扫描能不能找出真问题。
func TestScanRealWorkbook(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	matches, _ := filepath.Glob(filepath.Join(root, "docs", "*.xlsx"))
	if len(matches) == 0 {
		t.Skip("未找到真实表，跳过")
	}
	opt := Options{
		CrossMonthCheck: true,
		AnchorNames:     []string{"物业位置", "铺位", "商铺位"},
		PrevField:       []string{"上期欠款"},
		CurField:        []string{"本月欠款"},
		MonthSheets: []string{
			"2025年11月租金 （日）", "2025年12月租金 （日）",
			"2026年1月租金 （日）", "2026年2月租金 （日） ",
			"2026年3月租金 （日） ", "2026年4月租金 （日）",
			"2026年5月租金 （日）", "2026年6月租金 （日） ",
			"2026年7月租金 （日） ", "2026年8月租金 （日） ",
			"2026年9月租金 （日） ",
		},
	}
	start := time.Now()
	rep, err := Run(matches[0], opt)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	rep.Elapsed = time.Since(start).String()

	errs, warns, infos := rep.CountBySeverity()
	t.Logf("扫描完成：%d 张表，%d 个非空格，耗时 %s", rep.Sheets, rep.Cells, rep.Elapsed)
	t.Logf("发现：错误 %d，警告 %d，提示 %d", errs, warns, infos)

	// 按 kind 归类计数
	byKind := map[string]int{}
	for _, i := range rep.Issues {
		byKind[i.Kind]++
	}
	for k, n := range byKind {
		t.Logf("  %s: %d", k, n)
	}
	// 打印前几条错误样例，确认坐标可用（界面要能跳转）
	shown := 0
	for _, i := range rep.Issues {
		if i.Severity != SevError {
			continue
		}
		t.Logf("  样例: [%s] %s!%s → %s", i.Kind, i.Sheet, i.Ref, i.Message)
		shown++
		if shown >= 5 {
			break
		}
	}
	if len(rep.Issues) == 0 {
		t.Log("该表本次未发现错误值（也可能确实干净）")
	}
	// 坐标必须可用：错误类问题都要有 Ref
	for _, i := range rep.Issues {
		if i.Severity == SevError && i.Ref == "" {
			t.Errorf("错误类问题缺少坐标，界面无法跳转: %+v", i)
		}
	}
}
