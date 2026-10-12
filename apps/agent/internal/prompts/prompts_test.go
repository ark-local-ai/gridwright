package prompts

import (
	"path/filepath"
	"testing"
)

func TestExtractSlotsOrderedUnique(t *testing.T) {
	got := ExtractSlots("记一笔：{铺位} 收到 {月份} 租金 {金额}，{铺位} 下月再看")
	want := []string{"铺位", "月份", "金额"}
	if len(got) != len(want) {
		t.Fatalf("槽位数 = %v，想要 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("槽位顺序 = %v，想要 %v", got, want)
		}
	}
}

func TestExtractSlotsIgnoresEmpty(t *testing.T) {
	got := ExtractSlots("{} {}   {a} {}")
	want := []string{"a"}
	if len(got) != len(want) || got[0] != "a" {
		t.Fatalf("槽位 = %v，想要 %v", got, want)
	}
}

func TestRenderKeepsMissingSlots(t *testing.T) {
	got := Render("记一笔：{铺位} 收到 {金额}", map[string]string{"铺位": "B31", "金额": "  "})
	want := "记一笔：B31 收到 {金额}"
	if got != want {
		t.Fatalf("Render = %q，想要 %q", got, want)
	}
}

func TestSaveDerivesSlotsAndUpserts(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Prompt{Name: "收租录账", Template: "记一笔：{铺位} 收到 {月份} 租金 {金额}"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || len(p.Slots) != 3 {
		t.Fatalf("保存后应有 id 与 3 个槽位，得到 %+v", p)
	}
	// 同 id 更新：Slots 必须随 template 重算
	p.Template = "记一笔：{铺位} 收到 {金额}"
	if _, err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	all := s.All()
	if len(all) != 1 {
		t.Fatalf("同 id 应该更新而非新增，得到 %d 条", len(all))
	}
	if len(all[0].Slots) != 2 {
		t.Fatalf("槽位应随模板重算为 2，得到 %v", all[0].Slots)
	}
}

func TestSaveRejectsMissingFields(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if _, err := s.Save(Prompt{Template: "x"}); err == nil {
		t.Fatal("缺 name 应报错")
	}
	if _, err := s.Save(Prompt{Name: "x"}); err == nil {
		t.Fatal("缺 template 应报错")
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	p, _ := s.Save(Prompt{Name: "A", Template: "做 A"})
	if err := s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.All()) != 0 {
		t.Fatal("删除后应为空")
	}
}

func TestPersistenceAcrossOpen(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if _, err := s.Save(Prompt{Name: "A", Template: "做 {谁}"}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	all := s2.All()
	if len(all) != 1 || filepath.Base(s2.Path()) != "prompts.yaml" {
		t.Fatalf("应能从 prompts.yaml 读回，得到 %+v @ %s", all, s2.Path())
	}
}

func TestBuiltinsHaveSlots(t *testing.T) {
	bs := Builtins()
	if len(bs) < 5 {
		t.Fatalf("内置模板应至少 5 个，得到 %d", len(bs))
	}
	for _, b := range bs {
		if b.Name == "" || b.Template == "" {
			t.Fatalf("内置模板缺名字/模板：%+v", b)
		}
		if b.Source != "builtin" {
			t.Errorf("%q 的 source 应为 builtin，得到 %q", b.Name, b.Source)
		}
		if len(b.Slots) == 0 {
			t.Errorf("内置模板 %q 应有槽位，得到 %v", b.Name, b.Slots)
		}
	}
}
