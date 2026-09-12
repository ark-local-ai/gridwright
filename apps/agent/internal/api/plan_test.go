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

// TestApplyWritesAndLedgers 确认后执行：真写入 + 记账。
func TestApplyWritesAndLedgers(t *testing.T) {
	s := newTestServer(t)
	path := filepath.Join(s.Layout.Root, "测试表.xlsx")
	finger, err := propose.Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	// 直接构造一份清单（不走模型）：把 E2（本月实收）改成 20000
	prop := &propose.Proposal{
		Root:   s.Layout.Root,
		Target: path,
		Finger: finger,
		Summary: "测试",
		Items: []propose.Item{
			{File: "测试表.xlsx", Sheet: "Sheet1", Ref: "E2", Row: 2, Col: 5,
				Field: "本月实收", Op: "set", Old: "19354.02", New: 20000},
		},
	}
	id := s.proposals().Put(prop)

	body, _ := json.Marshal(map[string]string{"id": id})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("apply 状态 %d：%s", rec.Code, rec.Body.String())
	}
	var res struct {
		OK       bool `json:"ok"`
		Applied  int  `json:"applied"`
		Rejected int  `json:"rejected"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.OK || res.Applied != 1 {
		t.Fatalf("应应用 1 条，得到 %+v", res)
	}

	// 值真的写进去了
	f, _ := excelize.OpenFile(path)
	defer f.Close()
	if v, _ := f.GetCellValue("Sheet1", "E2"); v != "20000" {
		t.Fatalf("E2 应为 20000，得到 %q", v)
	}
	// 有账目（含旧值）
	entries, _ := s.Ledger.Entries(10)
	if len(entries) == 0 {
		t.Fatal("应有账目记录")
	}
	if entries[0].Cell != "E2" || entries[0].Old == "" {
		t.Fatalf("账目应记 E2 且含旧值，得到 %+v", entries[0])
	}
	// 备份已生成
	baks, _ := filepath.Glob(path + ".bak-*")
	if len(baks) == 0 {
		t.Fatal("应生成备份文件")
	}
}

// TestApplyRejectsStaleFingerprint 确认期间文件被改过 → 拒绝执行（防盲改）。
func TestApplyRejectsStaleFingerprint(t *testing.T) {
	s := newTestServer(t)
	path := filepath.Join(s.Layout.Root, "测试表.xlsx")
	finger, _ := propose.Fingerprint(path)

	prop := &propose.Proposal{
		Target: path, Finger: finger, Summary: "测试",
		Items: []propose.Item{{Sheet: "Sheet1", Ref: "E2", Op: "set", New: 1}},
	}
	id := s.proposals().Put(prop)

	// 模拟"有人在此期间改了文件"
	f, _ := excelize.OpenFile(path)
	_ = f.SetCellValue("Sheet1", "C2", "被人改过")
	_ = f.SaveAs(path)
	f.Close()

	body, _ := json.Marshal(map[string]string{"id": id})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("文件被改过应返回 409，得到 %d：%s", rec.Code, rec.Body.String())
	}
	// 且不该写入
	f2, _ := excelize.OpenFile(path)
	defer f2.Close()
	if v, _ := f2.GetCellValue("Sheet1", "E2"); v == "1" {
		t.Fatal("被拒绝时不应写入")
	}
}

// TestApplyUnknownID 不存在的清单 → 404，且不报 500。
func TestApplyUnknownID(t *testing.T) {
	s := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"id": "nope"})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("期望 404，得到 %d", rec.Code)
	}
}

// TestPlanWithoutBrain 没配模型时 /plan 应给明确提示（而不是崩）。
func TestPlanWithoutBrain(t *testing.T) {
	s := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"instruction": "记一笔收款"})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/plan", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未配模型应返回 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("模型")) {
		t.Errorf("提示应说明是模型未配：%s", rec.Body.String())
	}
}

// TestApplyOnceOnly 同一份清单不能重复应用（防重复改表）。
func TestApplyOnceOnly(t *testing.T) {
	s := newTestServer(t)
	path := filepath.Join(s.Layout.Root, "测试表.xlsx")
	finger, _ := propose.Fingerprint(path)
	prop := &propose.Proposal{Target: path, Finger: finger,
		Items: []propose.Item{{Sheet: "Sheet1", Ref: "E2", Op: "set", New: 111}}}
	id := s.proposals().Put(prop)

	body, _ := json.Marshal(map[string]string{"id": id})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("首次 apply 应成功，得到 %d", rec.Code)
	}
	// 再来一次：清单已丢弃
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("重复 apply 应 404，得到 %d", rec.Code)
	}
}
