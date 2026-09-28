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
		Sheet     string     `json:"sheet"`
		Rows      int        `json:"rows"`
		Cols      int        `json:"cols"`
		Formulas  int        `json:"formulas"`
		HeaderRow int        `json:"headerRow"`
		Header    []string   `json:"header"`
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

// TestPreviewBlankRowIsEmptyArrayNotNull 空行必须是 []，不能是 null。
//
// 回归用例：真实台账里"隔一行空一行"很常见，而 excelize 把空行读成 nil 切片，
// 序列化就是 null。前端渲染时取 row[0] 会抛 "Cannot read properties of null"，
// 整个界面白屏（实测切到 实收日报表 就中招）。这里直接断言 JSON 里没有 null 行。
func TestPreviewBlankRowIsEmptyArrayNotNull(t *testing.T) {
	s := newTestServerWithBlankRow(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/sheets/preview?sheet=Sheet1&rows=20", nil))
	if rec.Code != 200 {
		t.Fatalf("preview 状态 %d：%s", rec.Code, rec.Body.String())
	}
	// 先看原始 JSON：null 行在这里就能抓到，不必依赖结构体解码
	// （解码到 [][]string 时 null 会变成 nil 元素，两种都能断言，这里看原文更直接）
	var raw struct {
		Sample []json.RawMessage `json:"sample"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Sample) == 0 {
		t.Fatal("应返回样例行")
	}
	for i, row := range raw.Sample {
		if string(row) == "null" {
			t.Fatalf("第 %d 行是 null——空行必须序列化成 []，否则前端会白屏", i)
		}
	}
}


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
