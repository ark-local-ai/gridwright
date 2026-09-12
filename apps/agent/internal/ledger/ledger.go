// Package ledger 负责账目流水（spec §5）：append-only 的 CSV，
// 每格改动一条，old 列保留旧值 → 可回滚 + 审计 + 产品卖点"过程样板化"。
package ledger

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"
)

var headerCols = []string{"ts", "table", "sheet", "cell", "op", "old", "new", "reason", "source", "rule", "model", "status"}

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
func (l *Ledger) Append(ts time.Time, table, sheet, cell, op string, old, new, reason, source, rule, model, status string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	rec := []string{
		ts.Format("2006-01-02 15:04"), table, sheet, cell, op,
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

// Entry 是一条账目的结构化形式（供界面 / API 展示，见 docs/agent-architecture/13-接口契约.md）。
type Entry struct {
	Ts     string `json:"ts"`
	Table  string `json:"table"`
	Sheet  string `json:"sheet"`
	Cell   string `json:"cell"`
	Op     string `json:"op"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Reason string `json:"reason"`
	Source string `json:"source"`
	Rule   string `json:"rule"`
	Model  string `json:"model"`
	Status string `json:"status"`
}

// Entries 读取账目并结构化返回，最近 n 条（n<=0 表示全部）。
func (l *Ledger) Entries(n int) ([]Entry, error) {
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
	// 兼容旧账目：早期版本没有 sheet 列（11 列）。按表头判断是否含 sheet，
	// 老文件按老下标读——**不因为加了列就读不了历史账目**。
	hasSheet := true
	if len(records) > 0 && len(records[0]) > 0 && records[0][0] == "ts" {
		hasSheet = false
		for _, h := range records[0] {
			if h == "sheet" {
				hasSheet = true
				break
			}
		}
		records = records[1:]
	}
	if n > 0 && len(records) > n {
		records = records[len(records)-n:]
	}
	out := make([]Entry, 0, len(records))
	for _, rec := range records {
		e := Entry{}
		get := func(i int) string {
			if i >= 0 && i < len(rec) {
				return rec[i]
			}
			return ""
		}
		if hasSheet {
			e.Ts, e.Table, e.Sheet, e.Cell, e.Op = get(0), get(1), get(2), get(3), get(4)
			e.Old, e.New, e.Reason, e.Source = get(5), get(6), get(7), get(8)
			e.Rule, e.Model, e.Status = get(9), get(10), get(11)
		} else {
			// 旧格式：无 sheet 列
			e.Ts, e.Table, e.Cell, e.Op = get(0), get(1), get(2), get(3)
			e.Old, e.New, e.Reason, e.Source = get(4), get(5), get(6), get(7)
			e.Rule, e.Model, e.Status = get(8), get(9), get(10)
		}
		out = append(out, e)
	}
	return out, nil
}

// Path 返回账目文件路径。
func (l *Ledger) Path() string { return l.path }

// ActivityByNode 统计近 windowDays 天各 文件!sheet 的改动次数（含指数衰减）。
//
// 用于权重里的"活跃度"（见 docs/agent-architecture/22-权重设计.md）。
// 用滑动窗口 + 衰减而非累积总数：**"最近谁在动"比"历史总共动了几次"更贴近当下重点**，
// 否则三年前的活跃会永远压着现在的重点。
//
// 返回：节点ID -> 加权次数（四舍五入到整数，便于展示与归一化）
func (l *Ledger) ActivityByNode(windowDays int) (map[string]int, error) {
	if windowDays <= 0 {
		windowDays = 30
	}
	entries, err := l.Entries(0)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := map[string]int{}
	for _, e := range entries {
		if e.Status != "ok" {
			continue // 只算真正落地的改动
		}
		t, ok := parseTs(e.Ts)
		if !ok {
			continue
		}
		age := now.Sub(t).Hours() / 24
		if age < 0 || age > float64(windowDays) {
			continue
		}
		// 指数衰减：越近权重越高（τ = 窗口/2）
		w := math.Exp(-age / (float64(windowDays) / 2))
		key := nodeKey(e.Table, e.Sheet)
		out[key] += int(math.Round(w * 10)) // 放大 10 倍再取整，避免全被四舍五入成 0
	}
	return out, nil
}

// nodeKey 拼 文件!sheet（与 graph.Node.ID() 一致；表名去扩展名）。
func nodeKey(table, sheet string) string {
	base := table
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".xlsx")
	if sheet == "" {
		return base
	}
	return base + "!" + sheet
}

// parseTs 解析账目里的本地时间戳。
// **必须按 Local 解析**：账目里存的是本地墙上时间（time.Now().Format），
// 若用 time.Parse（UTC）解析，再和本地 now 相减会得到负的"年龄"，
// 导致所有条目被窗口过滤掉——活跃度会静默失效（非 UTC 时区必现）。
func parseTs(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(s), time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
