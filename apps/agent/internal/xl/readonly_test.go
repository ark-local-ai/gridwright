package xl

import (
	"os"
	"path/filepath"
	"testing"
)

// 微信等渠道收到的文件常带只读属性，excelize 直接 SaveAs 会 Access is denied。
// EnsureWritable 应能清掉只读位；已可写的文件不动它。
func TestEnsureWritableClearsReadOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.xlsx")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(p); info.Mode().Perm()&0o200 != 0 {
		t.Fatalf("前置条件不成立：文件应为只读，实际 %v", info.Mode().Perm())
	}
	if err := EnsureWritable(p); err != nil {
		t.Fatalf("解除只读失败: %v", err)
	}
	if info, _ := os.Stat(p); info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("解除后应可写，实际 %v", info.Mode().Perm())
	}
	// 已可写：再来一次应无副作用地通过
	if err := EnsureWritable(p); err != nil {
		t.Fatalf("可写文件不该报错: %v", err)
	}
}

func TestEnsureWritableMissingFile(t *testing.T) {
	if err := EnsureWritable(filepath.Join(t.TempDir(), "nope.xlsx")); err == nil {
		t.Fatal("文件不存在应报错")
	}
}
