package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

// 这些测试**不联网、不配模型**——规则短路的意义正在于此：
// 断网、没 key，照样按规矩办事。若代码偷偷回了模型，这里会失败。

// setupRuleWorkspace 造一个"台账 + 规则 + inbox 数据"的最小工作区。
func setupRuleWorkspace(t *testing.T, rules string, csvData string) (*workspace.Layout, *ledger.Ledger, *Agent) {
	t.Helper()
	dir := t.TempDir()

	// 台账：一行 A03，表头是真实台账的列名
	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "2026年9月租金 （日） ")
	headers := []string{"序号", "物业位置", "租户名称", "本月应收租金", "本月实收", "本月欠款"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("2026年9月租金 （日） ", cell, h)
	}
	_ = f.SetCellValue("2026年9月租金 （日） ", "A2", 1)
	_ = f.SetCellValue("2026年9月租金 （日） ", "B2", "A03")
	_ = f.SetCellValue("2026年9月租金 （日） ", "C2", "早餐店")
	_ = f.SetCellValue("2026年9月租金 （日） ", "D2", 19354.02)
	_ = f.SetCellValue("2026年9月租金 （日） ", "E2", 19354.02)
	_ = f.SetCellFormula("2026年9月租金 （日） ", "F2", "=D2-E2")
	if err := f.SaveAs(filepath.Join(dir, "台账.xlsx")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	layout, err := workspace.LayoutOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.Rules, []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	if csvData != "" {
		if err := os.WriteFile(filepath.Join(layout.Inbox, "实收日报.csv"), []byte(csvData), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	led, err := ledger.Open(layout.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Workspace: dir, PollSeconds: 5, SampleRows: 10}
	// Brain=nil：**没有模型**。规则短路必须在这种情况下也能跑通。
	ag := New(cfg, layout, led, nil)
	return layout, led, ag
}

const addRule = `rules:
  - name: 记本月实收
    trigger: 有物业位置和实收金额
    action: 按物业位置把实收金额累加进本月实收
    when:
      has_columns: [物业位置, 实收金额]
    then:
      target_file: 台账
      sheet: "2026年9月租金 （日） "
      key:   {物业位置: 物业位置}
      field: {本月实收: 实收金额}
      op: add
`

// 规则短路：无模型也能把 inbox 数据按规则写进表，并留下账目。
func TestRuleShortCircuitNoBrain(t *testing.T) {
	layout, led, ag := setupRuleWorkspace(t, addRule, "物业位置,实收金额\nA03,1000\n")
	if err := ag.RunInboxFiles(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 表被改了：19354.02 + 1000
	f, err := excelize.OpenFile(filepath.Join(layout.Root, "台账.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _ := f.GetCellValue("2026年9月租金 （日） ", "E2")
	if got != "20354.02" {
		t.Fatalf("E2 应为 20354.02（累加），得到 %q", got)
	}

	// 账目里有这条，且标明是规则办的
	recent, _ := led.Recent(20)
	if len(recent) != 1 {
		t.Fatalf("应有 1 条账目，得到 %d: %v", len(recent), recent)
	}
	if !strings.Contains(recent[0], "记本月实收") {
		t.Fatalf("账目该带规则名: %s", recent[0])
	}
	// 数据应被移入 done，且用**原名**（不带认领后缀）
	entries, _ := os.ReadDir(layout.Done)
	if len(entries) != 1 || entries[0].Name() != "实收日报.csv" {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("done 里该是原名 实收日报.csv，得到 %v", names)
	}
}

// 并发回归：同一文件被并发认领时，**只能被处理一次**。
// 起因是 fsnotify 对同一文件连发 CREATE+WRITE，各触发一次运行；
// 规则短路是毫秒级的，两次运行必然重叠 → 累加类规则会重复入账。
func TestConcurrentRunsProcessOnce(t *testing.T) {
	_, led, ag := setupRuleWorkspace(t, addRule, "物业位置,实收金额\nA03,1000\n")

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = ag.RunInboxFiles(context.Background()) }()
	}
	wg.Wait()

	recent, _ := led.Recent(50)
	if len(recent) != 1 {
		t.Fatalf("4 次并发只该产生 1 条账目（重复=重复入账），得到 %d 条:\n%s",
			len(recent), strings.Join(recent, "\n"))
	}
}

// 规则不命中 → 不该动表、不产生账目（并且因为没模型，计划会失败但不留副作用）。
func TestRuleNoMatchLeavesFileAlone(t *testing.T) {
	// 规则要"数量"列，数据里没有 → 不命中
	rules := `rules:
  - name: 别管我
    when: {has_columns: [物业位置, 数量]}
    then:
      target_file: 台账
      key:   {物业位置: 物业位置}
      field: {本月实收: 数量}
`
	layout, led, ag := setupRuleWorkspace(t, rules, "物业位置,实收金额\nA03,1000\n")
	_ = ag.RunInboxFiles(context.Background())

	f, _ := excelize.OpenFile(filepath.Join(layout.Root, "台账.xlsx"))
	defer f.Close()
	got, _ := f.GetCellValue("2026年9月租金 （日） ", "E2")
	if got != "19354.02" {
		t.Fatalf("不该改动，E2 仍应是 19354.02，得到 %q", got)
	}
	if recent, _ := led.Recent(10); len(recent) != 0 {
		t.Fatalf("不该有账目: %v", recent)
	}
}

// forbid 护栏对规则**一视同仁**：规则也不能改禁止列。
func TestRuleRespectsForbid(t *testing.T) {
	rules := `rules:
  - name: 想改禁止列
    when: {has_columns: [物业位置, 实收金额]}
    then:
      target_file: 台账
      key:   {物业位置: 物业位置}
      field: {本月实收: 实收金额}
    forbid: [本月实收]
`
	layout, led, ag := setupRuleWorkspace(t, rules, "物业位置,实收金额\nA03,1000\n")
	_ = ag.RunInboxFiles(context.Background())

	f, _ := excelize.OpenFile(filepath.Join(layout.Root, "台账.xlsx"))
	defer f.Close()
	got, _ := f.GetCellValue("2026年9月租金 （日） ", "E2")
	if got != "19354.02" {
		t.Fatalf("命中 forbid 的规则不该改动，E2 得到 %q", got)
	}
	// 要留下 rejected 账目（不能静默）
	recent, _ := led.Recent(10)
	if len(recent) == 0 {
		t.Fatal("被护栏拦下也该记账（否则用户不知道规则被挡了）")
	}
	if !strings.Contains(strings.Join(recent, "\n"), "rejected") {
		t.Fatalf("该有 rejected 记录: %v", recent)
	}
}

// 没有 when/then 的规则只当提示，不该短路改表。
func TestPromptOnlyRuleDoesNotShortCircuit(t *testing.T) {
	rules := `rules:
  - name: 只是提示
    trigger: 新 csv
    action: 交给模型
`
	layout, led, ag := setupRuleWorkspace(t, rules, "物业位置,实收金额\nA03,1000\n")
	_ = ag.RunInboxFiles(context.Background())

	f, _ := excelize.OpenFile(filepath.Join(layout.Root, "台账.xlsx"))
	defer f.Close()
	got, _ := f.GetCellValue("2026年9月租金 （日） ", "E2")
	if got != "19354.02" {
		t.Fatalf("只当提示的规则不该改表，E2 得到 %q", got)
	}
	if recent, _ := led.Recent(10); len(recent) != 0 {
		t.Fatalf("不该有账目: %v", recent)
	}
}

// 缺行的键/值要单独记账说明（不能静默漏几行）。
func TestRuleSkipsReportedInLedger(t *testing.T) {
	// 第 2 行缺金额 → 该行跳过并说明；第 1 行正常。
	// （账目记的是坐标 E2，不是铺位号 A03——断言要按实际记的东西来。）
	_, led, ag := setupRuleWorkspace(t, addRule, "物业位置,实收金额\nA03,1000\nA05,\n")
	_ = ag.RunInboxFiles(context.Background())
	recent, _ := led.Recent(20)
	joined := strings.Join(recent, "\n")

	// 好的那行真的改了
	if len(recent) < 2 {
		t.Fatalf("该有 1 条改动 + 1 条跳过说明，得到 %d 条:\n%s", len(recent), joined)
	}
	if !strings.Contains(joined, "E2") || !strings.Contains(joined, "ok") {
		t.Fatalf("该有 E2 的成功改动: %s", joined)
	}
	// 缺值那行必须留下说明（否则"漏记"永远看不见）
	if !strings.Contains(joined, "第 2 行") {
		t.Fatalf("缺值的行该留下说明（不能静默漏）: %s", joined)
	}
	if !strings.Contains(joined, "rejected") {
		t.Fatalf("跳过该记为 rejected: %s", joined)
	}
}

// llm.Client 的零值不该被误用（守住"没模型也能跑"这个前提）。
func TestAgentWithoutBrainStillRuns(t *testing.T) {
	_, _, ag := setupRuleWorkspace(t, addRule, "")
	if ag.Brain != nil {
		t.Fatal("这个测试的前提是没有模型")
	}
	var _ *llm.Client = ag.Brain // 类型断言：Brain 可空
}
