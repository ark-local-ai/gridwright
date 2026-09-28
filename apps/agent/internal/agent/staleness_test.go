package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

// 失效自动检查的测试。这组用例的重点**几乎全在"不能误报"**上。
//
// 理由：过时提示的价值完全建立在可信度上。一条误标就会让人开始无视所有提示
// （狼来了），那时这个机制还不如没有——用户反而会以为记忆已经核对过了。

// setupStaleWorkspace 造一个工作区：一张含指定铺位的表 + 一个记忆库。
func setupStaleWorkspace(t *testing.T, rows [][2]string, mem func(*memory2.Store)) *Agent {
	t.Helper()
	dir := t.TempDir()

	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "2026年9月租金 （日） ")
	sh := "2026年9月租金 （日） "
	for i, h := range []string{"序号", "物业位置", "租户名称", "本月应收租金", "本月实收", "本月欠款"} {
		c, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sh, c, h)
	}
	for i, r := range rows {
		row := i + 2
		_ = f.SetCellValue(sh, cellAt(1, row), i+1)
		_ = f.SetCellValue(sh, cellAt(2, row), r[0]) // 物业位置
		_ = f.SetCellValue(sh, cellAt(3, row), r[1]) // 租户
	}
	if err := f.SaveAs(filepath.Join(dir, "台账.xlsx")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	layout, err := workspace.LayoutOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := memory2.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mem(st)

	led, err := ledger.Open(layout.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	return New(&config.Config{Workspace: dir}, layout, led, nil)
}

func cellAt(col, row int) string {
	c, _ := excelize.CoordinatesToCellName(col, row)
	return c
}

// TestStaleCheckMarksVanishedShop 铺位在表里找不到了 → 标为可能过时。
func TestStaleCheckMarksVanishedShop(t *testing.T) {
	ag := setupStaleWorkspace(t,
		[][2]string{{"A03", "早餐店"}, {"B31", "水果店"}},
		func(st *memory2.Store) {
			_ = st.PutFact(memory2.Fact{ID: "f-b31", Value: "B31 月租金 23540",
				Key: map[string]string{"铺位": "B31"}})
			_ = st.PutDecision(memory2.Decision{ID: "d-a03", Text: "A03 的运费都算进去",
				Key: map[string]string{"铺位": "A03"}})
		})

	marks, err := ag.CheckMemoryStaleness()
	if err != nil {
		t.Fatal(err)
	}
	// 两条都在表里（A03/B31 都出现）→ 一条都不该标
	if len(marks) != 0 {
		t.Fatalf("两条记忆的铺位都还在表里，不该标记；得到 %+v", marks)
	}

	// 现在把 B31 从表里去掉（模拟改名/删除），再查一次
	ag2 := setupStaleWorkspace(t,
		[][2]string{{"A03", "早餐店"}},
		func(st *memory2.Store) {
			_ = st.PutFact(memory2.Fact{ID: "f-b31", Value: "B31 月租金 23540",
				Key: map[string]string{"铺位": "B31"}})
		})
	marks, err = ag2.CheckMemoryStaleness()
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 1 || marks[0].ID != "f-b31" {
		t.Fatalf("B31 已不在表里，应标 1 条；得到 %+v", marks)
	}
	if marks[0].Note == "" {
		t.Error("标记要带说明，人才知道为什么被标")
	}

	// 标记应落盘（stale 机制要求能持久）
	st, _ := memory2.Open(ag2.Layout.Root)
	if n := len(st.All().Stale); n != 1 {
		t.Errorf("过时标记应落盘，得到 %d 条", n)
	}
}

// TestStaleCheckNeverComparesAmounts 金额变化**绝不能**被报成过时。
//
// 这是本检查最重要的边界：金额本来就会变（升租、补缴），
// 若拿数字做存在性判断，每次正常改动都会报一次"记忆失效"——
// 那种噪音会让用户学会无视所有过时提示。
func TestStaleCheckNeverComparesAmounts(t *testing.T) {
	ag := setupStaleWorkspace(t,
		[][2]string{{"A03", "早餐店"}},
		func(st *memory2.Store) {
			// 记忆说月租 23540，表里将来会变成别的数——但**数字本身不该被查**
			_ = st.PutFact(memory2.Fact{ID: "f-amount", Value: "A03 月租金 23540",
				Key: map[string]string{"金额": "23540", "月份": "2026-08"}})
		})
	marks, err := ag.CheckMemoryStaleness()
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 0 {
		t.Fatalf("金额/月份这类值不该做存在性判断，不该标记；得到 %+v", marks)
	}
}

// TestSearchable 界定"什么值才敢拿去查存在性"：**含字母（含汉字）才查**。
//
// 这条线划得很保守，是故意的：查错（误报）的代价远大于查漏。
func TestSearchable(t *testing.T) {
	cases := map[string]bool{
		"B31":         true,  // 典型铺位号：查
		"A03":         true,  // 查
		"HT-2024-07":  true,  // 合同号：查
		"早餐店":         true,  // 租户名：查（标识）
		"23540":       false, // 金额：不查（会变）
		"2026-08":     false, // 月份：不查（会滚动到下一期）
		"2026":        false, // 纯数字：不查
		"001":         false, // 纯数字铺位号：不查（刻意接受的漏报）
		"1":           false, // 太短
		"楼":           false, // 单字太泛
		"":            false,
		"  A03  ":     true, // 两侧空白不该影响判断
	}
	for in, want := range cases {
		if got := searchable(in); got != want {
			t.Errorf("searchable(%q) = %v，期望 %v", in, got, want)
		}
	}
}

// TestStaleCheckSkipsWhenTableUnreadable 有表读不到时**拒绝下结论**，而不是照报。
//
// 关键：读不到的表可能正是装着那个铺位的那张。在"只看了半个工作区"的
// 基础上说某条记忆失效，就是误报——宁可这次不查。
func TestStaleCheckSkipsWhenTableUnreadable(t *testing.T) {
	ag := setupStaleWorkspace(t,
		[][2]string{{"A03", "早餐店"}},
		func(st *memory2.Store) {
			_ = st.PutFact(memory2.Fact{ID: "f1", Value: "X99 月租金 1",
				Key: map[string]string{"铺位": "X99"}})
		})
	// 放一个坏掉的 xlsx：OpenFile 会失败 → 检查必须整体放弃
	bad := filepath.Join(ag.Layout.Root, "坏表.xlsx")
	if err := writeGarbage(bad); err != nil {
		t.Fatal(err)
	}
	_, err := ag.CheckMemoryStaleness()
	if err == nil {
		t.Fatal("有表读不到时应当拒绝下结论（返回错误），而不是照报可能过时")
	}
}

// writeGarbage 写一个不是合法 xlsx 的文件（用于验证"读不到就不下结论"）。
func writeGarbage(path string) error {
	return os.WriteFile(path, []byte("这不是一个 xlsx"), 0o644)
}
