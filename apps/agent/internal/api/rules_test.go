package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
)

// 规则引擎接口测试（见 docs/agent-architecture/30-规则引擎.md）。
//
// 覆盖三件最要紧的事：
//  1. 保存的规则**真的落盘**（回读一致）——规则文件是唯一真相，不能只存内存
//  2. 跑不起来的规则要被**明确指出来**（"以为在跑"是最坏的情况）
//  3. 坏文件**写不进去**（解析不过就拒绝）——它直接决定改表行为

func rulesServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := newTestServer(t)
	_, layout, _, _ := s.cur()
	return s, layout.Rules
}

func TestRulesGetEmpty(t *testing.T) {
	s, _ := rulesServer(t)
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/rules", nil))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Rules      []ruleView `json:"rules"`
		Total      int        `json:"total"`
		Executable int        `json:"executable"`
		Path       string     `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 0 || len(resp.Rules) != 0 {
		t.Fatalf("空工作区期望 0 条规则，得到 %d", resp.Total)
	}
	if resp.Path == "" {
		t.Fatal("应返回规则文件路径（用户要能直接去编辑）")
	}
}

func TestRulesSaveAndReadBack(t *testing.T) {
	s, rulesPath := rulesServer(t)
	h := s.Handler()

	body := `{"rules":[{"name":"收租入账","trigger":"收到租金实收数据","action":"累加到本月实收",
	  "when":{"file":"实收","format":"csv","has_columns":["铺位","实收金额"]},
	  "then":{"sheet":"租金表","key":{"铺位":"铺位"},"field":{"本月实收":"实收金额"},
	    "op":"add","target_file":"测试表"}}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("保存状态 %d：%s", rec.Code, rec.Body.String())
	}
	var saved struct {
		Saved      bool `json:"saved"`
		Total      int  `json:"total"`
		Executable int  `json:"executable"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Saved || saved.Total != 1 || saved.Executable != 1 {
		t.Fatalf("期望存下 1 条且可执行，得到 %+v", saved)
	}

	// **落盘**：文件必须真的存在，且能被 memory 包读回来
	rf, _, _, _, err := memory.Load(rulesPath, "")
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if len(rf.Rules) != 1 || rf.Rules[0].Name != "收租入账" {
		t.Fatalf("回读内容不符：%+v", rf.Rules)
	}
	if rf.Rules[0].Then == nil || rf.Rules[0].Then.Op != "add" {
		t.Fatalf("then 未完整落盘：%+v", rf.Rules[0].Then)
	}
}

func TestRulesSaveViaRaw(t *testing.T) {
	s, rulesPath := rulesServer(t)
	h := s.Handler()

	raw := "rules:\n  - name: 只提示的一条\n    trigger: 每月末\n    action: 提醒我核对\n"
	body, _ := json.Marshal(map[string]string{"raw": raw})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(string(body))))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	b, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "只提示的一条") {
		t.Fatalf("原文没落盘：%s", string(b))
	}
}

func TestRulesRejectBadYAML(t *testing.T) {
	s, rulesPath := rulesServer(t)
	h := s.Handler()

	// 先存一条好的，确认坏文件不会把它冲掉
	good := `{"rules":[{"name":"好规则","trigger":"t","action":"a"}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(good)))
	if rec.Code != 200 {
		t.Fatalf("先存好规则失败：%s", rec.Body.String())
	}

	// 坏 yaml：必须被拒
	body, _ := json.Marshal(map[string]string{"raw": "rules: [ this is not valid yaml ::: "})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(string(body))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏 yaml 应被拒（400），得到 %d", rec.Code)
	}

	// 原文件必须**没被破坏**
	rf, _, _, _, err := memory.Load(rulesPath, "")
	if err != nil {
		t.Fatalf("坏写入把规则文件弄坏了：%v", err)
	}
	if len(rf.Rules) != 1 || rf.Rules[0].Name != "好规则" {
		t.Fatalf("坏写入冲掉了已有规则：%+v", rf.Rules)
	}
}

func TestRulesRejectDuplicateNames(t *testing.T) {
	s, _ := rulesServer(t)
	h := s.Handler()
	body := `{"rules":[{"name":"同名","trigger":"t","action":"a"},{"name":"同名","trigger":"t2","action":"a2"}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("重名应被拒，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "同名") {
		t.Fatalf("错误信息应点出是哪个名字重了：%s", rec.Body.String())
	}
}

func TestRulesValidateDoesNotWrite(t *testing.T) {
	s, rulesPath := rulesServer(t)
	h := s.Handler()
	body := `{"rules":[{"name":"半条规则","when":{"file":"x"}}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/rules/validate", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK         bool                 `json:"ok"`
		Executable int                  `json:"executable"`
		Problems   []memory.RuleProblem `json:"problems"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Executable != 0 {
		t.Fatalf("半条规则不该可执行：%+v", resp)
	}
	if len(resp.Problems) != 1 || !strings.Contains(resp.Problems[0].Reason, "then") {
		t.Fatalf("应指出缺 then：%+v", resp.Problems)
	}
	// 校验接口**不能**写文件
	if _, err := os.Stat(rulesPath); err == nil {
		t.Fatal("validate 不应创建规则文件")
	}
}

func TestRulesGetReportsWhyNotRunnable(t *testing.T) {
	s, rulesPath := rulesServer(t)
	// 直接写一个"半条"规则：有 when 没 then
	rf := memory.RulesFile{Rules: []memory.Rule{{
		Name: "半条", Trigger: "t", Action: "a",
		When: &memory.RuleWhen{File: "x"},
	}}}
	if err := rf.Save(rulesPath); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/rules", nil))
	var resp struct {
		Rules      []ruleView `json:"rules"`
		Executable int        `json:"executable"`
		Total      int        `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 || resp.Executable != 0 {
		t.Fatalf("期望 1 条但 0 条可执行：%+v", resp)
	}
	if resp.Rules[0].Runnable {
		t.Fatal("半条规则不该标成可执行")
	}
	// **必须说明为什么**——"以为在跑"比"跑不了"更坏
	if resp.Rules[0].Reason == "" {
		t.Fatalf("不可执行的规则必须给出原因：%+v", resp.Rules[0])
	}
	// 人话说明也要有（给不识 yaml 的人看）
	if resp.Rules[0].Summary == "" {
		t.Fatal("应给出人话说明")
	}
}

func TestRulesDryRunNoData(t *testing.T) {
	s, _ := rulesServer(t)
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/rules/dry-run", nil))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Wrote bool   `json:"wrote"`
		Note  string `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Wrote {
		t.Fatal("试跑绝不能写文件")
	}
	if resp.Note == "" {
		t.Fatal("没有数据时应给出说明，而不是空响应")
	}
}

// 试跑**命中**时：要列出会改什么，并且**一个字节都不许写**。
// 这条是 dry-run 的全部意义所在——改表前能先看清楚。
func TestRulesDryRunReportsHitsWithoutWriting(t *testing.T) {
	s, rulesPath := rulesServer(t)
	_, layout, _, _ := s.cur()

	// 规则：把 inbox 的 实收金额 按 物业位置 累加进 测试表 的 本月实收
	rf := memory.RulesFile{Rules: []memory.Rule{{
		Name: "记实收",
		When: &memory.RuleWhen{HasColumns: []string{"物业位置", "实收金额"}},
		Then: &memory.RuleThen{
			TargetFile: "测试表", Sheet: "Sheet1",
			Key:   map[string]string{"物业位置": "物业位置"},
			Field: map[string]string{"本月实收": "实收金额"},
			Op:    "add",
		},
	}}}
	if err := rf.Save(rulesPath); err != nil {
		t.Fatal(err)
	}
	inboxCSV := filepath.Join(layout.Inbox, "实收.csv")
	if err := os.WriteFile(inboxCSV, []byte("物业位置,实收金额\nA03,100\n没有这个铺位,50\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 记下试跑前的文件指纹——试跑后必须一模一样
	before, err := os.ReadFile(filepath.Join(layout.Root, "测试表.xlsx"))
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/rules/dry-run", nil))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		File  string `json:"file"`
		Hits  []string
		Items []struct {
			Rule  string
			Sheet string
			Ref   string
			New   string
			Line  int
			Why   string
		} `json:"items"`
		Skips []struct {
			Why  string
			Line int
		} `json:"skips"`
		Wrote bool `json:"wrote"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Wrote {
		t.Fatal("试跑绝不能写文件")
	}
	if len(resp.Hits) != 1 || resp.Hits[0] != "记实收" {
		t.Fatalf("该报出命中的规则：%+v", resp.Hits)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("该报出 1 处会改的地方（定不到行的那条要落到 skips）：%+v", resp.Items)
	}
	it := resp.Items[0]
	if it.Sheet != "Sheet1" || it.Ref == "" {
		t.Fatalf("该说清改哪张表、哪个格：%+v", it)
	}
	// 累加值要对：表里原本 19354.02，加 100 → 19454.02
	if !strings.Contains(it.New, "19454.02") {
		t.Fatalf("该显示累加后的新值：%+v", it)
	}
	// 定不到行的那条要出现在 skips 里（试跑也要暴露"会漏"），而不是被吞成一条假成功
	if len(resp.Skips) != 1 || !strings.Contains(resp.Skips[0].Why, "第 2 行") {
		t.Fatalf("定不到的行该报出来：%+v", resp.Skips)
	}

	// **核心断言**：试跑后表文件字节不变
	after, err := os.ReadFile(filepath.Join(layout.Root, "测试表.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("试跑改动了表文件——这违背了「只计算、不写文件」的承诺")
	}
	// inbox 里的数据也不该被移走（试跑不消费数据）
	if _, err := os.Stat(inboxCSV); err != nil {
		t.Fatalf("试跑不该动 inbox 里的文件：%v", err)
	}
	// 不该产生账目（账目文件本身有表头行，所以查"有没有数据行"而不是"文件空不空"）
	if b, _ := os.ReadFile(layout.Ledger); len(strings.Split(strings.TrimSpace(string(b)), "\n")) > 1 {
		t.Fatalf("试跑不该记账，但账目有数据行：%s", string(b))
	}
}

// ---------- 声明连线（link） ----------

func TestDeclaredLinkMergesIntoGraph(t *testing.T) {
	s, rulesPath := rulesServer(t)
	_, layout, _, _ := s.cur()

	// 声明"改了 测试表!Sheet1 会牵动 测试表!Sheet1"是同表自指，不算；
	// 这里造两张表来验证跨表声明。
	writeMinimalXlsx(t, filepath.Join(layout.Root, "另一张.xlsx"))
	rf := memory.RulesFile{Rules: []memory.Rule{{
		Name: "人工搬运关系",
		When: &memory.RuleWhen{File: "x"},
		Then: &memory.RuleThen{Key: map[string]string{"a": "b"}, Field: map[string]string{"c": "d"}},
		Link: &memory.RuleLink{From: "测试表!Sheet1", To: "另一张!Sheet1", Note: "合计人工抄过去"},
	}}}
	if err := rf.Save(rulesPath); err != nil {
		t.Fatal(err)
	}

	g, err := s.scanGraph()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range g.Edges {
		if e.Kind == "declared" && strings.Contains(e.From.ID(), "测试表") && strings.Contains(e.To.ID(), "另一张") {
			found = true
			if e.Confidence != "high" {
				t.Fatalf("声明边必须是 high 置信度，得到 %s", e.Confidence)
			}
			if !e.CrossFile {
				t.Fatal("跨文件声明应标 CrossFile")
			}
		}
	}
	if !found {
		t.Fatalf("声明的关联没进图：%+v", g.Edges)
	}
}

func TestDeclaredLinkIgnoresHalfPairs(t *testing.T) {
	_, rulesPath := rulesServer(t)
	rf := memory.RulesFile{Rules: []memory.Rule{{
		Name: "半条关系",
		Link: &memory.RuleLink{From: "只有下游"},
	}}}
	if err := rf.Save(rulesPath); err != nil {
		t.Fatal(err)
	}
	links := rf.DeclaredLinks()
	if len(links) != 0 {
		t.Fatalf("半条关系不该生效（少一半等于连错）：%+v", links)
	}
}
