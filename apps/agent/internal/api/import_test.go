package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// 导入接口测的是"会不会弄坏用户的东西"，所以这几条都要覆盖：
//   · 正常复制进来
//   · 非 Excel 明确拒绝（而不是静默忽略）
//   · 同名不覆盖（源表就在工作区里时尤其危险）
//   · 同一个文件重复导入不报错、也不产生第二份
func TestWorkspaceImport(t *testing.T) {
	s, dir := newTestServerWithRegistry(t)
	h := s.Handler()

	// 工作区外的一个真表
	outside := filepath.Join(t.TempDir(), "外来表.xlsx")
	writeMinimalXlsx(t, outside)

	post := func(paths ...string) map[string]any {
		body, _ := json.Marshal(map[string]any{"paths": paths})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspace/import", bytes.NewReader(body)))
		var got map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		return got
	}

	// ① 正常导入
	got := post(outside)
	if ok, _ := got["ok"].(bool); !ok {
		t.Fatalf("导入应成功：%v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "外来表.xlsx")); err != nil {
		t.Fatal("导入后工作区里应有这个文件")
	}
	// 源文件必须还在（是复制不是移动）
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("导入不应移动/删除源文件")
	}

	// ② 非 Excel：明确拒绝并给出原因
	txt := filepath.Join(t.TempDir(), "说明.txt")
	_ = os.WriteFile(txt, []byte("hello"), 0o644)
	got = post(txt)
	results, _ := got["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("应回一条结果：%v", got)
	}
	first, _ := results[0].(map[string]any)
	if first["ok"] == true {
		t.Fatal("非 Excel 不该算成功")
	}
	if first["err"] == "" {
		t.Fatal("被拒时要说明原因，不能静默")
	}

	// ③ 同名不覆盖：把工作区里已有的表再导一次，内容不能被改
	target := filepath.Join(dir, "外来表.xlsx")
	before, _ := os.ReadFile(target)
	got = post(target) // 源即目标
	results, _ = got["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("应回一条结果：%v", got)
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(before, after) {
		t.Fatal("同名时绝不能覆盖现有文件")
	}

	// ④ 重复导入外部同一文件：不报错、不产生第二份
	got = post(outside)
	results, _ = got["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("应回一条结果：%v", got)
	}
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if e.Name() == "外来表.xlsx" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("工作区里应只有一份，实际 %d", n)
	}
}

// 没有工作区时应明确报错，而不是往临时目录里写。
func TestWorkspaceImportNoWorkspace(t *testing.T) {
	s, _ := newTestServerWithRegistry(t)
	h := s.Handler()
	s.Layout = nil // 模拟还没选工作区

	body, _ := json.Marshal(map[string]any{"paths": []string{"x.xlsx"}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspace/import", bytes.NewReader(body)))
	if rec.Code == http.StatusOK {
		t.Fatal("没有工作区时应报错")
	}
}
