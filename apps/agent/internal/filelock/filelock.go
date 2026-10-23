// Package filelock 提供“同一张表同一时刻只允许一个写回”的进程内锁。
//
// 为什么需要：一次 apply、一次回滚、inbox 自动处理都可能命同一张 xlsx。
// excelize 是“整本读入 → 改内存 → 整本 SaveAs”，两个写回交错会互相覆盖
// （后保存的把先保存的改动抹掉），这不是理论问题——多个作业区并行时会必现。
//
// 边界：只解决**同一引擎进程内**的并发。桌面版只有一个 sidecar，够用；
// 用户手动开两个引擎仍可能冲突（Excel 自己的锁文件能挡住一部分）。
// 跨进程锁留给后续（复用 agent 的 O_EXCL claim 思路），不要在这里假装解决了。
package filelock

import (
	"path/filepath"
	"strings"
	"sync"
)

type refMutex struct {
	mu   sync.Mutex
	refs int
}

var (
	mu    sync.Mutex
	locks = map[string]*refMutex{}
)

// Lock 锁定一个文件路径，返回解锁函数。必须 defer 调用。
//
// 键用规范化 + 小写路径：Windows 上 C:\a.xlsx 与 c:\A.XLSX 是同一个文件。
func Lock(path string) func() {
	key := strings.ToLower(filepath.Clean(path))
	mu.Lock()
	m := locks[key]
	if m == nil {
		m = &refMutex{}
		locks[key] = m
	}
	m.refs++
	mu.Unlock()

	m.mu.Lock()
	return func() {
		m.mu.Unlock()
		mu.Lock()
		m.refs--
		if m.refs == 0 {
			delete(locks, key)
		}
		mu.Unlock()
	}
}
