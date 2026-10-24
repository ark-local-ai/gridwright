package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
)

// 作业区运行的核心承诺：**算清单，不写表**。
// 这几条测试就是从反面盯住它——文件、done、账目、表内容，一个都不许动。

// TestPlanInboxFileRulesOnly 规则命中的 csv：返回清单，文件原样留在 inbox。
func TestPlanInboxFileRulesOnly(t *testing.T) {
	layout, led, ag := setupRuleWorkspace(t, addRule, "物业位置,实收金额\nA03,1000\n")
	inboxFile := filepath.Join(layout.Inbox, "实收日报.csv")

	prop, err := ag.PlanInboxFile(context.Background(), inboxFile, "zone-1")
	if err != nil {
		t.Fatalf("PlanInboxFile 失败: %v", err)
	}
	if len(prop.Items) == 0 {
		t.Fatalf("规则命中应产出条目，得到 %+v", prop)
	}
	if prop.Finger == "" {
		t.Fatal("清单必须带目标表指纹（apply 前要校验）")
	}
	if prop.Zone != "zone-1" {
		t.Errorf("Zone 应为 zone-1，得到 %q", prop.Zone)
	}
	if len(prop.Sources) != 1 || prop.Sources[0] != inboxFile {
		t.Errorf("Sources 应为 [%s]，得到 %v", inboxFile, prop.Sources)
	}
	if prop.Target != filepath.Join(layout.Root, "台账.xlsx") {
		t.Errorf("Target 应为目标表绝对路径，得到 %q", prop.Target)
	}

	// 清单内容：add 规则在清单里表现为 set，Old 从工作簿读、New 是累加结果。
	it := prop.Items[0]
	if it.Op != "set" || it.Ref != "E2" {
		t.Errorf("应是一条 set E2，得到 %s %s", it.Op, it.Ref)
	}
	if it.Old != "19354.02" {
		t.Errorf("Old 应从工作簿读，得到 %v", it.Old)
	}
	if now, _ := planNum(it.New); now != 20354.02 {
		t.Errorf("New 应为 20354.02，得到 %v", it.New)
	}

	// ---- 不写表的证据 ----
	// 1) 文件还在 inbox，且没进 done
	if _, err := os.Stat(inboxFile); err != nil {
		t.Fatalf("inbox 文件必须留在原地: %v", err)
	}
	if ents, _ := os.ReadDir(layout.Done); len(ents) != 0 {
		t.Fatalf("只出清单时不许移入 done，得到 %v", ents)
	}
	// 2) 账目没有新增
	if recent, _ := led.Recent(20); len(recent) != 0 {
		t.Fatalf("只出清单时不许记账，得到 %v", recent)
	}
	// 3) 表内容没变
	f, err := excelize.OpenFile(prop.Target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got, _ := f.GetCellValue("2026年9月租金 （日） ", "E2"); got != "19354.02" {
		t.Fatalf("工作表不该被改，E2 得到 %q", got)
	}
}

// planNum 把清单里的新值转成数字（规则算出来的是 float64，模型可能给 int）。
func planNum(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// TestPlanInboxFileNoRulesNoBrain 没有规则命中、也没配模型 → error（调用方记为失败）。
func TestPlanInboxFileNoRulesNoBrain(t *testing.T) {
	layout, led, ag := setupRuleWorkspace(t, "", "a,b\n1,2\n")
	inboxFile := filepath.Join(layout.Inbox, "实收日报.csv")

	prop, err := ag.PlanInboxFile(context.Background(), inboxFile, "zone-1")
	if err == nil {
		t.Fatalf("没规则又没模型应报错，却返回了清单: %+v", prop)
	}
	if prop != nil {
		t.Fatalf("报错时不该返回清单，得到 %+v", prop)
	}
	// 失败不消费数据、不记账
	if _, serr := os.Stat(inboxFile); serr != nil {
		t.Fatalf("失败时文件应留在 inbox: %v", serr)
	}
	if recent, _ := led.Recent(20); len(recent) != 0 {
		t.Fatalf("失败时不许记账，得到 %v", recent)
	}
}

// TestPlanInboxFileAppend 模型给出 append：清单里必须带正确 HeaderRow，且不写表。
func TestPlanInboxFileAppend(t *testing.T) {
	layout, led, ag := setupRuleWorkspace(t, "", "物业位置,本月应收租金\nA04,5000\n")
	ag.Brain = fakeBrain(t, `{
		"edits":[
			{"op":"append","sheet":"2026年9月租金 （日） ","row":{"物业位置":"A04","本月应收租金":5000},"reason":"补录一行"},
			{"op":"set","sheet":"2026年9月租金 （日） ","cell":"E2","value":100,"reason":"改实收"}
		],
		"summary":"补一行并改一格"
	}`)
	inboxFile := filepath.Join(layout.Inbox, "实收日报.csv")

	prop, err := ag.PlanInboxFile(context.Background(), inboxFile, "zone-2")
	if err != nil {
		t.Fatalf("PlanInboxFile 失败: %v", err)
	}

	app := -1
	for i := range prop.Items {
		if prop.Items[i].Op == "append" {
			app = i
		}
	}
	if app < 0 {
		t.Fatalf("清单里应有 append 条目，得到 %+v", prop.Items)
	}
	it := prop.Items[app]
	// HeaderRow 必须是真实表头行，否则 apply 会找错表头
	if it.HeaderRow != 1 {
		t.Errorf("HeaderRow 应为 1（该表表头在第 1 行），得到 %d", it.HeaderRow)
	}
	if it.Values["物业位置"] != "A04" {
		t.Errorf("Values 应原样带回列名→值，得到 %v", it.Values)
	}
	if it.Ref == "" || it.Row < 2 {
		t.Errorf("append 应给出估算行号/坐标，得到 row=%d ref=%q", it.Row, it.Ref)
	}

	// 不写表：行数没长、E2 没变、账目为空、文件仍在 inbox
	f, err := excelize.OpenFile(prop.Target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, _ := f.GetRows("2026年9月租金 （日） ")
	if len(rows) != 2 {
		t.Errorf("只出清单时不该新增行，现在有 %d 行", len(rows))
	}
	if got, _ := f.GetCellValue("2026年9月租金 （日） ", "E2"); got != "19354.02" {
		t.Errorf("E2 不该被改，得到 %q", got)
	}
	if recent, _ := led.Recent(20); len(recent) != 0 {
		t.Fatalf("只出清单时不许记账，得到 %v", recent)
	}
	if _, serr := os.Stat(inboxFile); serr != nil {
		t.Fatalf("inbox 文件必须留在原地: %v", serr)
	}
}

// TestAppendPlanItemNeedsHeaderRow 拿不到可靠表头行时不猜：挡下并给出约定理由。
func TestAppendPlanItemNeedsHeaderRow(t *testing.T) {
	item, blocked := appendPlanItem(nil, "x.xlsx", "Sheet1",
		plan.Edit{Op: "append", Sheet: "Sheet1", Row: map[string]any{"物业位置": "A04"}}, nil)
	if item != nil || blocked == nil {
		t.Fatalf("没有表结构时应挡下，得到 item=%+v blocked=%+v", item, blocked)
	}
	if blocked.Reason != appendPlanBlockedReason {
		t.Errorf("理由应为 %q，得到 %q", appendPlanBlockedReason, blocked.Reason)
	}
}

// fakeBrain 起一个只回固定 JSON 的假模型（不联网）。
func fakeBrain(t *testing.T, content string) *llm.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		})
	}))
	t.Cleanup(srv.Close)
	return llm.New(config.LLM{BaseURL: srv.URL, APIKey: "test-key", Model: "mock"})
}
