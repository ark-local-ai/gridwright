package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/sheetnotes"
	"github.com/ark-local-ai/ark/apps/agent/internal/xl"
)

// TestLastChangeOf 从账目里取"上次改动"：从后往前、只认 ok、表名宽容、sheet 精确。
func TestLastChangeOf(t *testing.T) {
	entries := []ledger.Entry{
		{Ts: "2026-09-20 10:00", Table: "测试表", Sheet: "Sheet1", Cell: "A2", Old: "1", New: "2", Status: "ok"},
		{Ts: "2026-09-21 10:00", Table: "别的表", Sheet: "Sheet1", Cell: "A2", Old: "1", New: "2", Status: "ok"},
		{Ts: "2026-09-22 10:00", Table: "测试表", Sheet: "Sheet1", Cell: "B2", Old: "A03", New: "A05", Status: "rejected"},
		{Ts: "2026-09-23 10:00", Table: `C:\ws\测试表.xlsx`, Sheet: "Sheet1", Cell: "E12", Old: "100", New: "23540", Status: "ok"},
	}
	change, at := lastChangeOf(entries, "测试表.xlsx", "Sheet1")
	if change != "E12 100 → 23540" {
		t.Errorf("lastChange=%q", change)
	}
	if at != "2026-09-23 10:00" {
		t.Errorf("lastChangeAt=%q", at)
	}

	// 指定了 sheet，旧账目（无 sheet）不参与匹配 → 空
	old := []ledger.Entry{{Ts: "2026-09-20 10:00", Table: "测试表", Cell: "A2", Old: "1", New: "2", Status: "ok"}}
	if c, _ := lastChangeOf(old, "测试表.xlsx", "Sheet1"); c != "" {
		t.Errorf("无 sheet 的旧账目不该匹配有 sheet 的表，得到 %q", c)
	}
	// 值空时用（空）
	if c, _ := lastChangeOf([]ledger.Entry{{Table: "测试表", Sheet: "Sheet1", Cell: "F5", Status: "ok"}}, "测试表.xlsx", "Sheet1"); c != "F5 （空） → （空）" {
		t.Errorf("空值渲染不对：%q", c)
	}
}

// TestCleanDescription 清洗模型输出：去换行/项目符号/Markdown，套话与空串判失败。
func TestCleanDescription(t *testing.T) {
	cases := []struct{ in, want string }{
		{"- 销售明细台账：按房号记录每套房的建筑面积与客户。", "销售明细台账：按房号记录每套房的建筑面积与客户。"},
		{"```\n这是一张销售流水表，含房号与客户名称两列。\n```", "这是一张销售流水表，含房号与客户名称两列。"},
		{"描述：租赁台账，记录铺位、租户、月租金。", "租赁台账，记录铺位、租户、月租金。"},
		{"   ", ""},
		{"这是一张表格", ""}, // 套话
		{"包含多列数据", ""}, // 套话
		{"这是一张表", ""},  // 太短
		{"第一行。\n第二行会被压成一行。", "第一行。 第二行会被压成一行。"},
	}
	for _, c := range cases {
		if got := cleanDescription(c.in); got != c.want {
			t.Errorf("cleanDescription(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	// 超长截断到 200 字
	long := strings.Repeat("台", 500)
	if got := cleanDescription(long); len([]rune(got)) != 201 { // 200 + 省略号
		t.Errorf("超长应截断到 200 字 + …，得到 %d", len([]rune(got)))
	}
}

// TestSheetDetailEndpoint 详情接口：描述来自缓存，上次改动来自账目。
func TestSheetDetailEndpoint(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// ① 没有描述、没有账目 → 200，字段为空串（不是 null）
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/sheets/detail?file=测试表.xlsx&sheet=Sheet1", nil))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	var d sheetDetailResp
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Description != "" || d.LastChange != "" {
		t.Errorf("空态应为空串：%+v", d)
	}
	if strings.Contains(rec.Body.String(), "null") {
		t.Errorf("字段不该是 null：%s", rec.Body.String())
	}

	// ② 塞一条描述缓存 + 一条账目 → 两处都读得到
	if store := s.notesStore(); store != nil {
		if err := store.Put("测试表.xlsx!Sheet1", sheetnotes.Note{
			Description: "租赁台账：记录铺位、租户与月租金。", At: "2026-10-01 16:40", Model: "deepseek-chat",
		}); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal("测试服务端应有描述缓存")
	}
	ts, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-29 17:05", time.Local)
	if err := s.Ledger.Append(ts, "测试表.xlsx", "Sheet1", "E12", "set", "100", "23540", "改价", "test", "", "deepseek-chat", "ok"); err != nil {
		t.Fatal(err)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet,
		"/api/v1/sheets/detail?file=测试表&sheet=Sheet1", nil)) // 不带 .xlsx 也要匹配
	if rec2.Code != 200 {
		t.Fatalf("状态 %d：%s", rec2.Code, rec2.Body.String())
	}
	var d2 sheetDetailResp
	if err := json.Unmarshal(rec2.Body.Bytes(), &d2); err != nil {
		t.Fatal(err)
	}
	if d2.Description == "" || d2.DescriptionModel != "deepseek-chat" {
		t.Errorf("描述没读回来：%+v", d2)
	}
	if d2.LastChange != "E12 100 → 23540" || d2.LastChangeAt != "2026-09-29 17:05" {
		t.Errorf("上次改动不对：%+v", d2)
	}
}

// TestSheetDescribeNeedsBrain 没配模型 → 400 人话。
func TestSheetDescribeNeedsBrain(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sheets/describe",
		strings.NewReader(`{"file":"测试表.xlsx","sheet":"Sheet1"}`))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "未配置模型") {
		t.Errorf("错误应是人话：%s", rec.Body.String())
	}
}

// TestSheetDetailMissingSheet 找不到工作表 → 400。
func TestSheetDetailMissingSheet(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/sheets/detail?sheet=不存在", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

// TestHeaderHashChangesWithHeader 表头变了指纹要变（描述才会被重算）。
func TestHeaderHashChangesWithHeader(t *testing.T) {
	a := headerHash(&xl.Structure{HeaderRow: 1, Header: []string{"序号", "房号"}})
	b := headerHash(&xl.Structure{HeaderRow: 1, Header: []string{"序号", "面积"}})
	if a == b {
		t.Error("表头不同，指纹却相同")
	}
	c := headerHash(&xl.Structure{HeaderRow: 2, Header: []string{"序号", "房号"}})
	if a == c {
		t.Error("表头行不同，指纹却相同")
	}
}
