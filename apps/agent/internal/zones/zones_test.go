package zones

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

func mustPut(t *testing.T, s *Store, z Zone) Zone {
	t.Helper()
	got, err := s.Put(z)
	if err != nil {
		t.Fatalf("Put(%q): %v", z.Name, err)
	}
	return got
}

func TestOpenEmptyDoesNotCreateFile(t *testing.T) {
	s, dir := open(t)
	if got := s.List(); len(got) != 0 {
		t.Fatalf("空工作区不该有 Zone，得到 %d 个", len(got))
	}
	if _, ok := s.Get("z_nope"); ok {
		t.Fatal("Get 不该命中")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Fatalf("只读打开不该建文件，err=%v", err)
	}
	if s.Path() != filepath.Join(dir, FileName) {
		t.Fatalf("path=%s", s.Path())
	}
}

func TestPutGeneratesIDAndKeepsCreatedAt(t *testing.T) {
	s, _ := open(t)
	first := mustPut(t, s, Zone{Name: "实收核对", Input: Input{Type: InputFolder}})
	if !strings.HasPrefix(first.ID, "z_") || len(first.ID) != 10 {
		t.Fatalf("id=%q，期望 z_ + 8 hex", first.ID)
	}
	if first.CreatedAt == "" {
		t.Fatal("createdAt 应自动生成")
	}
	if first.Mode != ModeDryRun {
		t.Fatalf("mode 默认应为 dryRun，得到 %q", first.Mode)
	}
	if first.Input.Watch != WatchManual {
		t.Fatalf("watch 默认应为 manual，得到 %q", first.Input.Watch)
	}
	// 稳定 id：再写一次空 id 应得到不同的新 id
	second := mustPut(t, s, Zone{Name: "另一个", Input: Input{Type: InputManual}})
	if second.ID == first.ID {
		t.Fatal("空 id 每次应生成新 id")
	}

	// 覆盖同 id，篡改 createdAt 也必须保留原值
	updated := mustPut(t, s, Zone{
		ID:        first.ID,
		Name:      "实收核对（改名）",
		Input:     Input{Type: InputFolder, Watch: WatchFS},
		Mode:      ModeApply,
		CreatedAt: "1999-01-01T00:00:00Z",
	})
	if updated.CreatedAt != first.CreatedAt {
		t.Fatalf("createdAt 未被保留：%q != %q", updated.CreatedAt, first.CreatedAt)
	}
	if updated.Mode != ModeApply {
		t.Fatalf("mode=%q", updated.Mode)
	}

	// 落盘且可重开
	s2, err := Open(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.List()) != 2 {
		t.Fatalf("重开后 Zone 数=%d", len(s2.List()))
	}
}

func TestPutIdempotentOverwrite(t *testing.T) {
	s, _ := open(t)
	z := mustPut(t, s, Zone{ID: "z_aaaaaaaa", Name: "一", Input: Input{Type: InputFolder}})
	z.Name = "二"
	mustPut(t, s, z)
	z.Name = "三"
	mustPut(t, s, z)
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("同 id 覆盖后应只有 1 条，得到 %d", len(list))
	}
	if list[0].Name != "三" {
		t.Fatalf("name=%q", list[0].Name)
	}
}

func TestListStableOrderByCreatedAtThenID(t *testing.T) {
	s, _ := open(t)
	mustPut(t, s, Zone{ID: "z_2", Name: "b", Input: Input{Type: InputFolder}, CreatedAt: "2026-01-02T00:00:00Z"})
	mustPut(t, s, Zone{ID: "z_1", Name: "a", Input: Input{Type: InputFolder}, CreatedAt: "2026-01-02T00:00:00Z"})
	mustPut(t, s, Zone{ID: "z_0", Name: "c", Input: Input{Type: InputFolder}, CreatedAt: "2026-01-01T00:00:00Z"})
	list := s.List()
	got := []string{list[0].ID, list[1].ID, list[2].ID}
	want := []string{"z_0", "z_1", "z_2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("排序=%v，期望 %v", got, want)
	}
}

func TestPutRejectsInvalid(t *testing.T) {
	s, _ := open(t)
	cases := []struct {
		name string
		z    Zone
	}{
		{"空名称", Zone{Input: Input{Type: InputFolder}}},
		{"名称只有空格", Zone{Name: "  ", Input: Input{Type: InputFolder}}},
		{"空类型", Zone{Name: "x"}},
		{"未知类型", Zone{Name: "x", Input: Input{Type: "ftp"}}},
		{"非法 mode", Zone{Name: "x", Input: Input{Type: InputFolder}, Mode: "auto"}},
		{"非法 watch", Zone{Name: "x", Input: Input{Type: InputFolder, Watch: "hourly"}}},
		{"文件带斜杠", Zone{Name: "x", Input: Input{Type: InputFolder}, Target: Target{File: "sub/台账.xlsx"}}},
		{"文件带反斜杠", Zone{Name: "x", Input: Input{Type: InputFolder}, Target: Target{File: `sub\台账.xlsx`}}},
		{"文件是绝对路径", Zone{Name: "x", Input: Input{Type: InputFolder}, Target: Target{File: "/tmp/台账.xlsx"}}},
	}
	for _, c := range cases {
		if _, err := s.Put(c.z); err == nil {
			t.Fatalf("%s：应当报错", c.name)
		}
	}
	if len(s.List()) != 0 {
		t.Fatal("被拒的 Put 不该留下记录")
	}
}

func TestUnknownTypePreservedOnRead(t *testing.T) {
	dir := t.TempDir()
	raw := `{"version":1,"zones":[
		{"id":"z_old","name":"老配置","input":{"type":"webdav"},"mode":"dryRun","createdAt":"2025-01-01T00:00:00Z"}
	],"runs":[]}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 || list[0].Input.Type != "webdav" {
		t.Fatalf("未知类型不该丢记录：%+v", list)
	}
	// 但改它（写入）必须被拒
	if _, err := s.Put(list[0]); err == nil {
		t.Fatal("未知类型不该允许写入")
	}
}

func TestVersionZeroTreatedAsOne(t *testing.T) {
	dir := t.TempDir()
	raw := `{"zones":[{"id":"z_1","name":"a","input":{"type":"folder"},"mode":"dryRun","createdAt":"2025-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 {
		t.Fatal("version=0 的文件应能读")
	}
	mustPut(t, s, Zone{ID: "z_2", Name: "b", Input: Input{Type: InputFolder}})
	b, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != Version {
		t.Fatalf("version=%d，期望 %d", f.Version, Version)
	}
}

func TestDeleteAndErrNotFound(t *testing.T) {
	s, _ := open(t)
	z := mustPut(t, s, Zone{Name: "a", Input: Input{Type: InputFolder}})
	if err := s.Delete(z.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(z.ID); ok {
		t.Fatal("删除后不该还能 Get 到")
	}
	if err := s.Delete(z.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v，期望 ErrNotFound", err)
	}
	if err := s.Delete("z_不存在"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v，期望 ErrNotFound", err)
	}
}

func TestMarkStatus(t *testing.T) {
	s, _ := open(t)
	z := mustPut(t, s, Zone{Name: "a", Input: Input{Type: InputFolder}})
	if err := s.MarkStatus(z.ID, "error", "表头变了"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(z.ID)
	if got.Status != "error" || got.LastError != "表头变了" {
		t.Fatalf("status=%q lastError=%q", got.Status, got.LastError)
	}
	if err := s.MarkStatus("z_不存在", "ok", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v，期望 ErrNotFound", err)
	}
}

func TestRecordRunTrimTo50(t *testing.T) {
	s, _ := open(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		s.RecordRun(Run{
			ZoneID:  "z_1",
			Started: base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			OK:      true,
			Applied: i,
		})
	}
	runs := s.Runs("", 0)
	if len(runs) != maxRuns {
		t.Fatalf("runs=%d，期望裁剪到 %d", len(runs), maxRuns)
	}
	// 留下的是最近 50 条：最旧应为第 10 分钟那条（applied=10）
	if runs[len(runs)-1].Applied != 10 {
		t.Fatalf("最旧一条 applied=%d，期望 10", runs[len(runs)-1].Applied)
	}
	if runs[0].Applied != 59 {
		t.Fatalf("最新一条 applied=%d，期望 59", runs[0].Applied)
	}

	// 落盘后再开仍只有 50 条
	s2, err := Open(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Runs("", 0)) != maxRuns {
		t.Fatalf("落盘后 runs=%d", len(s2.Runs("", 0)))
	}
}

func TestRunsFilterAndOrder(t *testing.T) {
	s, _ := open(t)
	s.RecordRun(Run{ZoneID: "z_a", Started: "2026-01-01T00:00:00Z", Summary: "a1"})
	s.RecordRun(Run{ZoneID: "z_b", Started: "2026-01-03T00:00:00Z", Summary: "b1"})
	s.RecordRun(Run{ZoneID: "z_a", Started: "2026-01-02T00:00:00Z", Summary: "a2"})

	all := s.Runs("", 0)
	if len(all) != 3 {
		t.Fatalf("全部=%d", len(all))
	}
	if all[0].Summary != "b1" || all[1].Summary != "a2" || all[2].Summary != "a1" {
		t.Fatalf("倒序失败：%v", []string{all[0].Summary, all[1].Summary, all[2].Summary})
	}

	a := s.Runs("z_a", 0)
	if len(a) != 2 || a[0].Summary != "a2" || a[1].Summary != "a1" {
		t.Fatalf("按 zone 过滤/排序失败：%+v", a)
	}
	if got := s.Runs("z_a", 1); len(got) != 1 || got[0].Summary != "a2" {
		t.Fatalf("limit 失效：%+v", got)
	}
	if got := s.Runs("z_没有", 0); len(got) != 0 {
		t.Fatalf("不存在的 zone 应返回空：%+v", got)
	}
}

func TestCorruptJSONRecovers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, []byte(`{"zones": [ 这不是 JSON`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("损坏文件不该让 Open 失败：%v", err)
	}
	if len(s.List()) != 0 {
		t.Fatal("损坏文件应从空开始")
	}
	if _, err := os.Stat(p + ".bak"); err != nil {
		t.Fatalf("应留下 .bak：%v", err)
	}
	// 从空开始仍可正常写
	z := mustPut(t, s, Zone{Name: "恢复后", Input: Input{Type: InputFolder}})
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("重写后文件仍不是合法 JSON: %v", err)
	}
	if len(f.Zones) != 1 || f.Zones[0].ID != z.ID {
		t.Fatalf("写回内容不对：%+v", f.Zones)
	}
	// 再 Open 一次应读到它
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get(z.ID); !ok {
		t.Fatal("重开后应能读到恢复后的记录")
	}
}

func TestAtomicWriteKeepsOldFileOnDisk(t *testing.T) {
	s, dir := open(t)
	z := mustPut(t, s, Zone{Name: "a", Input: Input{Type: InputFolder}})
	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("文件不该为空")
	}
	// 不该留下临时文件
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+FileName+".tmp-") {
			t.Fatalf("残留临时文件 %s", e.Name())
		}
	}
	if _, ok := s.Get(z.ID); !ok {
		t.Fatal("内存副本丢了")
	}
}

func TestConcurrentPutNoLostUpdate(t *testing.T) {
	s, dir := open(t)
	const n = 60
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Put(Zone{
				ID:    fmt.Sprintf("z_%08d", i),
				Name:  fmt.Sprintf("作业区-%d", i),
				Input: Input{Type: InputFolder},
			})
			if err != nil {
				errs <- err
			}
			s.RecordRun(Run{ZoneID: fmt.Sprintf("z_%08d", i), Started: "2026-01-01T00:00:00Z"})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发 Put 出错：%v", err)
	}

	if got := len(s.List()); got != n {
		t.Fatalf("并发后 Zone 数=%d，期望 %d（丢数据）", got, n)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s2.List()); got != n {
		t.Fatalf("落盘后 Zone 数=%d，期望 %d", got, n)
	}
	// 并发 RecordRun 也应落盘（超过 50 条时按上限裁剪）
	wantRuns := n
	if wantRuns > maxRuns {
		wantRuns = maxRuns
	}
	if got := len(s2.Runs("", 0)); got != wantRuns {
		t.Fatalf("run 数=%d，期望 %d", got, wantRuns)
	}
}
