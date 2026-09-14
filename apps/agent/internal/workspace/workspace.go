// Package workspace 处理"工作区 = 一个文件夹"的约定（spec §3）：
// 盯住工作区、从 inbox 取新数据、处理完移入 inbox/done、检测 Excel 锁。
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Layout 是工作区的目录结构。
type Layout struct {
	Root    string // 工作区根目录
	Inbox   string // 新数据入口
	Done    string // 处理完的文件归档
	Rules   string // rules.yaml
	State   string // state.yaml
	Ledger  string // ledger.csv
	Archive string
}

// LayoutOf 由根目录推导约定路径，并确保 inbox/done/archive 存在。
func LayoutOf(root string) (*Layout, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "inbox", "done"), 0o755); err != nil {
		return nil, fmt.Errorf("创建 inbox 目录: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(abs, "archive"), 0o755); err != nil {
		return nil, fmt.Errorf("创建 archive 目录: %w", err)
	}
	return &Layout{
		Root:    abs,
		Inbox:   filepath.Join(abs, "inbox"),
		Done:    filepath.Join(abs, "inbox", "done"),
		Rules:   filepath.Join(abs, "rules.yaml"),
		State:   filepath.Join(abs, "state.yaml"),
		Ledger:  filepath.Join(abs, "ledger.csv"),
		Archive: filepath.Join(abs, "archive"),
	}, nil
}

// DataFiles 列出工作区根目录下的被看管表（*.xlsx，排除锁文件与隐藏文件）。
func (l *Layout) DataFiles() ([]string, error) {
	entries, err := os.ReadDir(l.Root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "~$") || strings.HasPrefix(name, ".") {
			continue // Excel 锁文件 / 隐藏文件
		}
		if strings.EqualFold(filepath.Ext(name), ".xlsx") {
			out = append(out, filepath.Join(l.Root, name))
		}
	}
	return out, nil
}

// InboxFiles 列出 inbox 里待处理的文件（csv / xlsx），不含 done 子目录。
func (l *Layout) InboxFiles() ([]string, error) {
	entries, err := os.ReadDir(l.Inbox)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".csv" || ext == ".xlsx" {
			out = append(out, filepath.Join(l.Inbox, name))
		}
	}
	return out, nil
}

// Locked 判断某张 xlsx 是否被 Excel 占用（存在同名 ~$ 锁文件，spec §4）。
func Locked(path string) bool {
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	lock := filepath.Join(dir, "~$"+name)
	_, err := os.Stat(lock)
	return err == nil
}

// MoveToDone 把处理完的文件移入 inbox/done（重名自动加序号）。
func (l *Layout) MoveToDone(src string) error {
	base := filepath.Base(src)
	dst := filepath.Join(l.Done, base)
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(l.Done, fmt.Sprintf("%d-%s", i, base))
	}
	if err := os.Rename(src, dst); err != nil {
		if os.IsNotExist(err) {
			// 已经被移走（并发下的正常情况）：不是错误，别刷噪音日志。
			return nil
		}
		return err
	}
	return nil
}
