package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
)

// TestApplyAppendAddsRow 确认后新增一行：真的写到表尾、列按表头对齐、账目记 op=append。
//
// 为什么必须钉住这条：新增行是“按 D 列名字填 E/F，查不到就新增”这条链路的终点。
// 以前 prompt 里写着“系统不能新增行”，整条链路在**出清单那一步**就断了；
// 现在能写通了，就得保证它真的落到表里，且记账记的是 append——
// 回滚层据此明确告诉用户“这一条不能自动倒回，要手工删行”。
func TestApplyAppendAddsRow(t *testing.T) {
	s := newTestServer(t)
	path := filepath.Join(s.Layout.Root, "测试表.xlsx")
	finger, err := propose.Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}

	prop := &propose.Proposal{
		Root: s.Layout.Root, Target: path, Finger: finger, Summary: "新增一行",
		Items: []propose.Item{{
			File: "测试表.xlsx", Sheet: "Sheet1", Ref: "A3", Row: 3, Col: 1,
			HeaderRow: 1, Key: map[string]string{"物业位置": "B07"},
			Field: "物业位置、租户名称、本月实收",
			Op:    "append",
			Values: map[string]any{
				"物业位置": "B07", "租户名称": "新租户", "本月实收": 5000,
			},
		}},
	}
	id := s.proposals().Put(prop)

	body, _ := json.Marshal(map[string]string{"id": id})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("apply 状态 %d：%s", rec.Code, rec.Body.String())
	}
	var res struct {
		OK      bool `json:"ok"`
		Applied int  `json:"applied"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.OK || res.Applied != 1 {
		t.Fatalf("应应用 1 条，得到 %+v", res)
	}

	f, _ := excelize.OpenFile(path)
	defer f.Close()
	if v, _ := f.GetCellValue("Sheet1", "B3"); v != "B07" {
		t.Errorf("B3 应为 B07，得到 %q", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "C3"); v != "新租户" {
		t.Errorf("C3 应为 新租户，得到 %q", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "E3"); v != "5000" {
		t.Errorf("E3 应为 5000，得到 %q", v)
	}
	// 原来那行不能被碰
	if v, _ := f.GetCellValue("Sheet1", "B2"); v != "A03" {
		t.Errorf("B2 不该变，得到 %q", v)
	}

	entries, _ := s.Ledger.Entries(10)
	found := false
	for _, e := range entries {
		if e.Op == "append" && e.Status == "ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("账目里应有 op=append 且 ok 的记录（回滚层靠它标注“不能自动倒回”），得到 %+v", entries)
	}
}

// TestApplyAppendRejectsUnknownColumn 列名不在表头 → 整条拒绝，表里不多一行。
func TestApplyAppendRejectsUnknownColumn(t *testing.T) {
	s := newTestServer(t)
	path := filepath.Join(s.Layout.Root, "测试表.xlsx")
	finger, _ := propose.Fingerprint(path)
	prop := &propose.Proposal{Target: path, Finger: finger, Summary: "新增一行",
		Items: []propose.Item{{
			Sheet: "Sheet1", HeaderRow: 1, Op: "append", Field: "不存在的列",
			Values: map[string]any{"不存在的列": "x"},
		}}}
	id := s.proposals().Put(prop)

	body, _ := json.Marshal(map[string]string{"id": id})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	var res struct {
		Applied  int `json:"applied"`
		Rejected int `json:"rejected"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Applied != 0 || res.Rejected != 1 {
		t.Fatalf("应 0 应用 1 拒绝，得到 %+v", res)
	}
	f, _ := excelize.OpenFile(path)
	defer f.Close()
	if v, _ := f.GetCellValue("Sheet1", "A3"); v != "" {
		t.Errorf("拒绝时不该写第 3 行，得到 A3=%q", v)
	}
}
