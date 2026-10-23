package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 体检与联动图都按「文件签名」复用结果：同一批文件连续请求只扫一次。
func TestScanAndGraphCacheReuse(t *testing.T) {
	s := newTestServer(t)

	rep1, err := s.runScan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rep2, err := s.runScan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep1 != rep2 {
		t.Fatal("文件没变时应复用同一份体检结果（不应重新扫描）")
	}

	g1, err := s.ensureGraph(s.workspaceSig())
	if err != nil {
		t.Fatal(err)
	}
	g2, err := s.ensureGraph(s.workspaceSig())
	if err != nil {
		t.Fatal(err)
	}
	if g1 != g2 {
		t.Fatal("文件没变时应复用同一份联动图")
	}
}

// 文件内容/规则一变，签名必须变，下一次请求不能拿到旧结果。
func TestCacheInvalidatesOnChange(t *testing.T) {
	s := newTestServer(t)
	_, layout, _, _ := s.cur()

	rep1, err := s.runScan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ensureGraph(s.workspaceSig()); err != nil {
		t.Fatal(err)
	}
	sig1 := s.workspaceSig()

	// 改 rules.yaml（联动图的声明边来源）。内容长度不同，size 必变。
	rules := []byte("links:\n  - from: 测试表.xlsx!Sheet1\n    to: 测试表.xlsx!Sheet1\n")
	if err := os.WriteFile(filepath.Join(layout.Root, "rules.yaml"), rules, 0o644); err != nil {
		t.Fatal(err)
	}
	sig2 := s.workspaceSig()
	if sig1 == sig2 {
		t.Fatal("rules.yaml 改动后签名应变化")
	}

	// 体检缓存也该按新签名重建（这里只用指针不同来证明没有复用旧结果）。
	rep2, err := s.runScan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep1 == rep2 {
		t.Fatal("签名变化后不应返回旧体检结果")
	}
}
