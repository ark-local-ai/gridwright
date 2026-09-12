package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSheetPreviewEndpoint 表详情：概览 + 表头 + 行列数 + 公式数。
func TestSheetPreviewEndpoint(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/sheets/preview?sheet=Sheet1&rows=5", nil))
	if rec.Code != 200 {
		t.Fatalf("preview 状态 %d：%s", rec.Code, rec.Body.String())
	}
	var pv struct {
		Sheet     string   `json:"sheet"`
		Rows      int      `json:"rows"`
		Cols      int      `json:"cols"`
		Formulas  int      `json:"formulas"`
		HeaderRow int      `json:"headerRow"`
		Header    []string `json:"header"`
		Sample    [][]string `json:"sample"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatal(err)
	}
	if pv.Sheet != "Sheet1" {
		t.Errorf("sheet=%q", pv.Sheet)
	}
	if pv.HeaderRow != 1 {
		t.Errorf("表头应在第 1 行，得到 %d", pv.HeaderRow)
	}
	if len(pv.Header) == 0 || pv.Header[0] != "序号" {
		t.Errorf("表头读取错误：%v", pv.Header)
	}
	if pv.Formulas != 1 { // fixture 里 F2 是公式
		t.Errorf("期望 1 个公式格，得到 %d", pv.Formulas)
	}
	if len(pv.Sample) == 0 {
		t.Error("应返回样例行")
	}
}

// TestSheetPreviewMissingSheet 不存在的表要明确报错，不能 500。
func TestSheetPreviewMissingSheet(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/sheets/preview?sheet=不存在", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("期望 404，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

// TestListSheetsEndpoint 列出某文件的工作表。
func TestListSheetsEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sheets", nil))
	if rec.Code != 200 {
		t.Fatalf("sheets 状态 %d", rec.Code)
	}
	var out struct {
		File   string   `json:"file"`
		Sheets []string `json:"sheets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sheets) == 0 {
		t.Error("应列出至少一个工作表")
	}
}

// TestPreviewReadOnly 表详情/预览绝不能改文件。
func TestPreviewReadOnly(t *testing.T) {
	s := newTestServer(t)
	path := s.Layout.Root + "/测试表.xlsx"
	before := hashFile(t, path)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/sheets/preview?sheet=Sheet1", nil))
	if rec.Code != 200 {
		t.Fatalf("状态 %d", rec.Code)
	}
	if after := hashFile(t, path); after != before {
		t.Fatal("预览改动了文件！必须只读")
	}
}
