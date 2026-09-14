package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
)

func writeCSV(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "实收汇总.csv")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCSVAndNormalize(t *testing.T) {
	p := writeCSV(t, "铺位号,实收金额,月份\nA201,\"10,000.00\",2026-09\nA202,2000,2026-09\n")
	d, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if d.Format != "csv" {
		t.Fatalf("format=%s", d.Format)
	}
	if len(d.Rows) != 2 {
		t.Fatalf("rows=%d", len(d.Rows))
	}
	// 列名带全角空格也要能取到：让"本月实收"和"本月 实收"等价
	if got := d.Rows[0].Get("铺位号"); got != "A201" {
		t.Fatalf("铺位号=%q", got)
	}
	if got := d.Rows[0].Get("实收金额"); got != "10,000.00" {
		t.Fatalf("实收金额=%q（引号里的逗号不该被当分隔符）", got)
	}
	if d.Note() != "" {
		t.Fatalf("不该有提醒：%s", d.Note())
	}
}

func TestLoadCSVStripsBOM(t *testing.T) {
	p := writeCSV(t, "\ufeff铺位号,实收金额\nA201,100\n")
	d, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	// BOM 若不处理，第一个列名会变成 "\ufeff铺位号"，规则永远匹配不上
	if !d.HasColumn("铺位号") {
		t.Fatalf("BOM 没去掉，表头=%q", d.Header)
	}
}

func TestLoadCSVSkipsEmptyRows(t *testing.T) {
	p := writeCSV(t, "铺位号,实收金额\nA201,100\n,\nA202,200\n")
	d, _ := Load(p)
	if len(d.Rows) != 2 {
		t.Fatalf("空行没跳过，rows=%d", len(d.Rows))
	}
}

func TestLoadHeaderOnlyNotes(t *testing.T) {
	p := writeCSV(t, "铺位号,实收金额\n")
	d, _ := Load(p)
	if !strings.Contains(d.Note(), "没有数据行") {
		t.Fatalf("只有表头要提醒，note=%q", d.Note())
	}
}

func rule() memory.Rule {
	return memory.Rule{
		Name: "记实收",
		When: &memory.RuleWhen{HasColumns: []string{"铺位号", "实收金额"}},
		Then: &memory.RuleThen{
			Sheet: "2026年9月租金",
			Key:   map[string]string{"铺位": "铺位号"},
			Field: map[string]string{"本月实收": "实收金额"},
			Op:    "add",
		},
	}
}

func TestMatchAllConditions(t *testing.T) {
	p := writeCSV(t, "铺位号,实收金额\nA201,100\n")
	d, _ := Load(p)
	if !Match(rule(), d) {
		t.Fatal("该命中")
	}
	// 少一列就不该命中
	r := rule()
	r.When.HasColumns = []string{"铺位号", "不存在的列"}
	if Match(r, d) {
		t.Fatal("缺列不该命中")
	}
	// 文件名条件
	r = rule()
	r.When.File = "催缴"
	if Match(r, d) {
		t.Fatal("文件名不匹配不该命中")
	}
	r.When.File = "实收" // d.Base = 实收汇总
	if !Match(r, d) {
		t.Fatal("文件名子串匹配该命中")
	}
}

func TestMatchFormat(t *testing.T) {
	p := writeCSV(t, "铺位号,实收金额\nA201,100\n")
	d, _ := Load(p)
	r := rule()
	r.When.Format = "xlsx"
	if Match(r, d) {
		t.Fatal("格式不符不该命中")
	}
	r.When.Format = "csv"
	if !Match(r, d) {
		t.Fatal("格式相符该命中")
	}
}

func TestExpandProducesIntents(t *testing.T) {
	p := writeCSV(t, "铺位号,实收金额\nA201,\"10,000.00\"\nA202,2000\n")
	d, _ := Load(p)
	intents, skips := Expand(rule(), d)
	if len(skips) != 0 {
		t.Fatalf("不该有跳过：%+v", skips)
	}
	if len(intents) != 2 {
		t.Fatalf("intents=%d", len(intents))
	}
	it := intents[0]
	if it.Key["铺位"] != "A201" {
		t.Fatalf("key=%v", it.Key)
	}
	if it.Field != "本月实收" {
		t.Fatalf("field=%s", it.Field)
	}
	if it.Value != "10,000.00" {
		t.Fatalf("value=%q", it.Value)
	}
	if it.Op != "add" {
		t.Fatalf("op=%s", it.Op)
	}
	if it.Rule != "记实收" || it.Sheet != "2026年9月租金" {
		t.Fatalf("rule/sheet=%s/%s", it.Rule, it.Sheet)
	}
	if it.Line != 1 {
		t.Fatalf("line=%d", it.Line)
	}
}

func TestExpandSkipsEmptyKeyAndValue(t *testing.T) {
	// 第 1 行缺铺位号、第 2 行缺金额、第 3 行正常 → 应产出 1 条 + 2 条 skip
	p := writeCSV(t, "铺位号,实收金额\n,100\nA202,\nA203,300\n")
	d, _ := Load(p)
	intents, skips := Expand(rule(), d)
	if len(intents) != 1 || len(skips) != 2 {
		t.Fatalf("intents=%d skips=%d", len(intents), len(skips))
	}
	if intents[0].Key["铺位"] != "A203" {
		t.Fatalf("该只剩 A203：%v", intents[0].Key)
	}
	// skip 要说清是哪一行哪一列（否则用户不知道去改什么）
	if !strings.Contains(skips[0].Reason, "第 1 行") || !strings.Contains(skips[0].Reason, "铺位号") {
		t.Fatalf("skip 说明不清：%s", skips[0].Reason)
	}
}

func TestExpandMonthFrom(t *testing.T) {
	p := writeCSV(t, "铺位号,月份,实收金额\nA201,2026-09,100\nA201,,200\n")
	d, _ := Load(p)
	r := rule()
	r.Then.MonthFrom = "月份"
	intents, skips := Expand(r, d)
	if len(intents) != 1 {
		t.Fatalf("该只剩 1 条：%+v", intents)
	}
	if intents[0].Month != "2026-09" {
		t.Fatalf("month=%q", intents[0].Month)
	}
	if len(skips) != 1 || !strings.Contains(skips[0].Reason, "月份") {
		t.Fatalf("缺月份要 skip：%+v", skips)
	}
}

func TestExpandDefaultOpIsSet(t *testing.T) {
	p := writeCSV(t, "铺位号,实收金额\nA201,100\n")
	d, _ := Load(p)
	r := rule()
	r.Then.Op = ""
	intents, _ := Expand(r, d)
	if len(intents) != 1 || intents[0].Op != "set" {
		t.Fatalf("默认该是 set：%+v", intents)
	}
}

func TestExecutableValidation(t *testing.T) {
	// 齐全 → 可执行
	if err := rule().Executable(); err != nil {
		t.Fatalf("该可执行：%v", err)
	}
	// 只有 when 没有 then → 不可执行
	r := rule()
	r.Then = nil
	if err := r.Executable(); err == nil {
		t.Fatal("缺 then 该报错")
	}
	// when 一个条件都没有 → 会命中一切，必须报错
	r = rule()
	r.When = &memory.RuleWhen{}
	if err := r.Executable(); err == nil {
		t.Fatal("when 无条件该报错")
	}
	// field 两个 → 报错（一条规则一次只改一个字段）
	r = rule()
	r.Then.Field = map[string]string{"本月实收": "实收金额", "上月欠款": "欠款"}
	if err := r.Executable(); err == nil {
		t.Fatal("field 两个该报错")
	}
	// 无 key 且无 month_from → 定位不了
	r = rule()
	r.Then.Key = nil
	if err := r.Executable(); err == nil {
		t.Fatal("无定位方式该报错")
	}
	// month_from 单独用（无 key）→ 报错
	r = rule()
	r.Then.Key = nil
	r.Then.MonthFrom = "月份"
	if err := r.Executable(); err == nil {
		t.Fatal("month_from 无 key 该报错")
	}
	// op 非法 → 报错
	r = rule()
	r.Then.Op = "multiply"
	if err := r.Executable(); err == nil {
		t.Fatal("非法 op 该报错")
	}
}

func TestExecutableRulesSeparatesProblems(t *testing.T) {
	rf := memory.RulesFile{Rules: []memory.Rule{
		rule(),
		{Name: "只有提示的规则", Trigger: "新 csv", Action: "追加"}, // 无 when/then
	}}
	ok, bad := rf.ExecutableRules()
	if len(ok) != 1 || ok[0].Name != "记实收" {
		t.Fatalf("可执行=%+v", ok)
	}
	if len(bad) != 1 || bad[0].Name != "只有提示的规则" || bad[0].Reason == "" {
		t.Fatalf("问题规则=%+v", bad)
	}
}

func TestDeclaredTargetFile(t *testing.T) {
	r := rule()
	if r.Declared() {
		t.Fatal("没写 target_file 不该算声明")
	}
	r.Then.TargetFile = "台账"
	if !r.Declared() || Target(r) != "台账" {
		t.Fatal("声明了要认得")
	}
}
