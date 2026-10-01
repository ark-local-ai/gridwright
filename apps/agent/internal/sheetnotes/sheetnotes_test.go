package sheetnotes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	// 文件不存在 → 打开为空，不报错、也先不建文件
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("a.xlsx!Sheet1"); ok {
		t.Fatal("空库不应有记录")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Open 不该立刻建文件")
	}

	if err := s.Put("a.xlsx!Sheet1", Note{Description: "销售明细台账", At: "2026-10-01 16:40", Model: "deepseek-chat", HeaderHash: "abc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Put 后应落盘：%v", err)
	}

	// 重新打开要读得回来（真落盘，不是内存）
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := s2.Get("a.xlsx!Sheet1")
	if !ok || n.Description != "销售明细台账" || n.HeaderHash != "abc" {
		t.Fatalf("读回不对：%+v ok=%v", n, ok)
	}
	if len(s2.All()) != 1 {
		t.Fatalf("All 应有 1 条，得到 %d", len(s2.All()))
	}
}

func TestPutEmptyDescriptionIsNoop(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put("k", Note{Description: ""}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("空描述不应入库")
	}
}

func TestOpenBrokenFileDegradesToEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("{ 这不是 json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("坏文件不该让 Open 失败：%v", err)
	}
	if len(s.All()) != 0 {
		t.Fatal("坏文件应降级为空库")
	}
}
