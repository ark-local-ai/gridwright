// Package ledger 负责账目流水（spec §5）：append-only 的 CSV，
// 每格改动一条，old 列保留旧值 → 可回滚 + 审计 + 产品卖点"过程样板化"。
package ledger

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var headerCols = []string{"ts", "table", "cell", "op", "old", "new", "reason", "source", "rule", "model", "status"}

// Ledger 是线程安全的账目追加器。
type Ledger struct {
	path string
	mu   sync.Mutex
}

// Open 打开（不存在则创建并写表头）一个 ledger.csv。
func Open(path string) (*Ledger, error) {
	l := &Ledger{path: path}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.Create(path)
		if err != nil {
			return nil, fmt.Errorf("创建账目: %w", err)
		}
		w := csv.NewWriter(f)
		if err := w.Write(headerCols); err != nil {
			f.Close()
			return nil, err
		}
		w.Flush()
		f.Close()
	}
	return l, nil
}

// Append 追加一条账目。old/new 以字符串形式落账（数值/文本统一转字符串）。
func (l *Ledger) Append(ts time.Time, table, cell, op string, old, new, reason, source, rule, model, status string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	rec := []string{
		ts.Format("2006-01-02 15:04"), table, cell, op,
		old, new, reason, source, rule, model, status,
	}
	if err := w.Write(rec); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

// Recent 读取账目，返回最近 n 条（n<=0 表示全部），用于发给脑的"记忆压缩"（spec §4/§5.3）。
func (l *Ledger) Recent(n int) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) > 0 && records[0][0] == "ts" {
		records = records[1:] // 去表头
	}
	if n > 0 && len(records) > n {
		records = records[len(records)-n:]
	}
	out := make([]string, 0, len(records))
	for _, rec := range records {
		out = append(out, strings.Join(rec, ","))
	}
	return out, nil
}

// Path 返回账目文件路径。
func (l *Ledger) Path() string { return l.path }
