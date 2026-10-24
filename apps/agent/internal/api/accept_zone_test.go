package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

// 对话创建/修改作业区：kind=zone 采纳后才落 zones.json；
// 缺目录、未知 id 都不该留下半条配置。
func TestAcceptZoneProposal(t *testing.T) {
	s := newTestServer(t)
	_, layout, _, _ := s.cur()
	inDir := t.TempDir()
	store, err := s.convoStore()
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Ensure("", "建区")
	if err != nil {
		t.Fatal(err)
	}
	appendProp := func(p convo.Proposal) int {
		if _, err := store.Append(c.ID, convo.Message{Role: convo.RoleAgent, Text: "建议", Proposal: &p}); err != nil {
			t.Fatal(err)
		}
		got := store.Get(c.ID)
		return len(got.Messages) - 1
	}
	accept := func(idx int) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"conversationId": c.ID, "index": idx})
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/conversation/accept", strings.NewReader(string(body))))
		return rec
	}

	idx := appendProp(convo.Proposal{
		Kind: "zone", Title: "新建作业区：发票区",
		ZoneName: "发票区", ZoneInputType: zones.InputFolder, ZoneInputPath: inDir,
		ZoneTargetFile: "测试表.xlsx", ZoneTargetSheet: "Sheet1", Mode: zones.ModeDryRun,
	})
	rec := accept(idx)
	if rec.Code != http.StatusOK {
		t.Fatalf("采纳建区 = %d：%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Zone zones.Zone `json:"zone"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Zone.ID == "" || created.Zone.Name != "发票区" {
		t.Fatalf("应建出作业区，得到 %+v", created.Zone)
	}
	if created.Zone.Input.Path != inDir || created.Zone.Mode != zones.ModeDryRun {
		t.Fatalf("字段不对：%+v", created.Zone)
	}

	// 修改：只填 pattern，名字/目录不能被清空。
	idx = appendProp(convo.Proposal{
		Kind: "zone", ZoneID: created.Zone.ID, ZoneInputPattern: "*.pdf",
	})
	rec = accept(idx)
	if rec.Code != http.StatusOK {
		t.Fatalf("采纳改区 = %d：%s", rec.Code, rec.Body.String())
	}
	got, ok := s.zonesStore().Get(created.Zone.ID)
	if !ok || got.Input.Pattern != "*.pdf" || got.Name != "发票区" || got.Input.Path != inDir {
		t.Fatalf("修改应只改 pattern，得到 %+v", got)
	}

	// 未知 id → 404。
	idx = appendProp(convo.Proposal{Kind: "zone", ZoneID: "z_nope", ZoneInputPattern: "*.xlsx"})
	if rec := accept(idx); rec.Code != http.StatusNotFound {
		t.Fatalf("未知区应 404，得到 %d：%s", rec.Code, rec.Body.String())
	}

	// 新建缺目录 → 400，且不落盘。
	idx = appendProp(convo.Proposal{Kind: "zone", ZoneName: "没目录"})
	if rec := accept(idx); rec.Code != http.StatusBadRequest {
		t.Fatalf("缺目录应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if list := s.zonesStore().List(); len(list) != 1 {
		t.Fatalf("失败提案不该落盘，现有 %d 个区", len(list))
	}
	_ = layout
}
