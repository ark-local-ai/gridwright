package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
)

func TestProposalRowsPreview(t *testing.T) {
	s := newTestServer(t)
	path := filepath.Join(s.Layout.Root, "测试表.xlsx")
	finger, err := propose.Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	prop := &propose.Proposal{
		Root:    s.Layout.Root,
		Target:  path,
		Finger:  finger,
		Summary: "测试预览",
		Items: []propose.Item{
			{File: "测试表.xlsx", Sheet: "Sheet1", Ref: "E2", Row: 2, Col: 5,
				Field: "本月实收", Op: "set", Old: "19354.02", New: 20000, Reason: "收到补款"},
			{File: "测试表.xlsx", Sheet: "Sheet1", Ref: "A3", Row: 3, Col: 1,
				Field: "本月实收", Op: "append", HeaderRow: 1,
				Values: map[string]any{"物业位置": "A09", "本月实收": 5000}},
		},
	}
	id := s.proposals().Put(prop)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/proposal/rows?id="+id, nil))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	var resp propRowsResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sheets) != 1 || resp.Sheets[0].Sheet != "Sheet1" {
		t.Fatalf("应有 1 张表 Sheet1，得到 %+v", resp.Sheets)
	}
	sh := resp.Sheets[0]
	if len(sh.Rows) != 2 {
		t.Fatalf("应有 2 行，得到 %d", len(sh.Rows))
	}
	// 改动行：整行取回，改动列标出
	edit := sh.Rows[0]
	if edit.Kind != "edit" || edit.Row != 2 {
		t.Fatalf("第一行应是 edit@2，得到 %+v", edit)
	}
	if len(edit.Cells) < 3 || edit.Cells[1] != "A03" {
		t.Fatalf("整行应包含 A03，得到 %v", edit.Cells)
	}
	if len(edit.Changes) != 1 || edit.Changes[0].Col != 5 || edit.Changes[0].Field != "本月实收" {
		t.Fatalf("改动应在第 5 列本月实收，得到 %+v", edit.Changes)
	}
	if edit.Changes[0].Ref != "E2" {
		t.Fatalf("改动格应带 A1 坐标 E2，得到 %q", edit.Changes[0].Ref)
	}
	// 新增行：按表头摆好 values
	add := sh.Rows[1]
	if add.Kind != "new" || len(add.Cells) < 2 || add.Cells[1] != "A09" {
		t.Fatalf("第二行应是新增且含 A09，得到 %+v", add)
	}
	// 同表头对齐：物业位置列的索引一致
	idx := -1
	for i, h := range sh.Header {
		if h == "物业位置" {
			idx = i
		}
	}
	if idx < 0 || add.Cells[idx] != "A09" {
		t.Fatalf("新增行应按表头对齐，header=%v cells=%v", sh.Header, add.Cells)
	}

	// 不存在的清单 → 404
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/proposal/rows?id=nope", nil))
	if rec.Code != 404 {
		t.Fatalf("不存在的清单应 404，得到 %d", rec.Code)
	}
}
