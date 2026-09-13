// Package jobs 是自动化任务：把"每天下班前体检一次"这类话变成真的定时任务
// （见 docs/agent-architecture/27-对话与自动化任务.md、7-对话与自动化任务.md）。
//
// 此前对话能听懂并产出 cron 提案，但**没有任何地方读取它**——说了等于没说。
// 本包补上：任务存储（工作区里 jobs.json）+ 极简 cron 解析 + 调度器 + 运行历史。
//
// 为什么自己写 cron 而不引库：只需要"每天几点/每 N 分钟/每周几"这几档，
// 引库要评估依赖与许可证，而这几档的解析不到百行。**不引入依赖**。
package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind 是任务做什么。
const (
	KindScan = "scan" // 只读体检（默认、最安全）
	// 后续可扩：run_inbox（处理 inbox）、generate（出清单）
)

// Job 是一条自动化任务。
type Job struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Schedule string `json:"schedule"` // cron：分 时 日 月 周（支持 */N、列表、区间）
	Kind     string `json:"kind"`
	Enabled  bool   `json:"enabled"`
	// Source 说明这条任务怎么来的（对话提案 / 手工 / 界面）
	Source  string `json:"source,omitempty"`
	Created string `json:"created"`
	// 运行状态（由调度器维护）
	LastRun  string `json:"lastRun,omitempty"`
	LastOK   bool   `json:"lastOk"`
	LastNote string `json:"lastNote,omitempty"`
	NextRun  string `json:"nextRun,omitempty"`
}

// Run 是一次运行记录。
type Run struct {
	JobID   string `json:"jobId"`
	Name    string `json:"name"`
	Started string `json:"started"`
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
	Errors  int    `json:"errors"`
	Warns   int    `json:"warns"`
}

// File 是 jobs.json 的形状。
type File struct {
	Jobs []Job `json:"jobs"`
	Runs []Run `json:"runs"` // 最近若干条
}

// Store 是某工作区的任务存储。
type Store struct {
	mu   sync.Mutex
	path string
	f    File
}

const maxRunsKept = 50

// Open 打开（或创建）工作区的 jobs.json。
func Open(workspaceRoot string) (*Store, error) {
	p := filepath.Join(workspaceRoot, "jobs.json")
	s := &Store{path: p}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &s.f)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取任务: %w", err)
	}
	return s, nil
}

// Path 返回存储路径。
func (s *Store) Path() string { return s.path }

// List 返回任务（按创建时间降序）。
func (s *Store) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Job(nil), s.f.Jobs...)
	if out == nil {
		out = []Job{}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}

// RecentRuns 最近运行记录（倒序）。
func (s *Store) RecentRuns(n int) []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Run(nil), s.f.Runs...)
	if out == nil {
		out = []Run{}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started > out[j].Started })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Add 新增一条任务（会校验 cron 表达式，避免存进去一条永远不跑的）。
func (s *Store) Add(j Job) (Job, error) {
	if strings.TrimSpace(j.Name) == "" {
		return Job{}, fmt.Errorf("任务要有名字")
	}
	sched := strings.TrimSpace(j.Schedule)
	if sched == "" {
		sched = "30 17 * * *" // 默认每天 17:30（用户指定的下班前）
	}
	if _, err := ParseCron(sched); err != nil {
		return Job{}, fmt.Errorf("时间表达式不合法：%w", err)
	}
	if j.Kind == "" {
		j.Kind = KindScan
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j.ID = fmt.Sprintf("job-%d", time.Now().UnixNano())
	j.Schedule = sched
	j.Created = time.Now().Format("2006-01-02 15:04")
	if j.NextRun == "" {
		j.NextRun = nextAfter(sched, time.Now())
	}
	s.f.Jobs = append(s.f.Jobs, j)
	if err := s.saveLocked(); err != nil {
		return Job{}, err
	}
	return j, nil
}

// Update 改一条任务（启停 / 改时间）。
func (s *Store) Update(id string, fn func(*Job)) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.f.Jobs {
		if s.f.Jobs[i].ID != id {
			continue
		}
		fn(&s.f.Jobs[i])
		if _, err := ParseCron(s.f.Jobs[i].Schedule); err != nil {
			return Job{}, fmt.Errorf("时间表达式不合法：%w", err)
		}
		s.f.Jobs[i].NextRun = nextAfter(s.f.Jobs[i].Schedule, time.Now())
		if err := s.saveLocked(); err != nil {
			return Job{}, err
		}
		return s.f.Jobs[i], nil
	}
	return Job{}, fmt.Errorf("任务不存在：%s", id)
}

// Delete 删一条任务。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.f.Jobs[:0]
	for _, j := range s.f.Jobs {
		if j.ID != id {
			out = append(out, j)
		}
	}
	s.f.Jobs = out
	return s.saveLocked()
}

// RecordRun 记一次运行（同时更新任务的 lastRun/lastOk/nextRun）。
func (s *Store) RecordRun(r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.f.Runs = append(s.f.Runs, r)
	if len(s.f.Runs) > maxRunsKept {
		s.f.Runs = s.f.Runs[len(s.f.Runs)-maxRunsKept:]
	}
	for i := range s.f.Jobs {
		if s.f.Jobs[i].ID != r.JobID {
			continue
		}
		s.f.Jobs[i].LastRun = r.Started
		s.f.Jobs[i].LastOK = r.OK
		s.f.Jobs[i].LastNote = r.Summary
		s.f.Jobs[i].NextRun = nextAfter(s.f.Jobs[i].Schedule, time.Now())
	}
	return s.saveLocked()
}

// Due 返回"到点该跑了"的任务。
func (s *Store) Due(now time.Time) []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Job
	for _, j := range s.f.Jobs {
		if !j.Enabled {
			continue
		}
		m, err := ParseCron(j.Schedule)
		if err != nil {
			continue
		}
		if m.Match(now) && !ranThisMinute(j, now) {
			out = append(out, j)
		}
	}
	return out
}

// ranThisMinute 防止同一分钟内重复触发（调度器每秒 tick）。
func ranThisMinute(j Job, now time.Time) bool {
	if j.LastRun == "" {
		return false
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", j.LastRun, time.Local)
	if err != nil {
		return false
	}
	return t.Year() == now.Year() && t.YearDay() == now.YearDay() &&
		t.Hour() == now.Hour() && t.Minute() == now.Minute()
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

// nextAfter 算下一次触发时间（用于界面显示"下次 17:30"）。
func nextAfter(sched string, from time.Time) string {
	m, err := ParseCron(sched)
	if err != nil {
		return ""
	}
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if m.Match(t) {
			return t.Format("2006-01-02 15:04")
		}
		t = t.Add(time.Minute)
	}
	return ""
}

// ---------- cron 解析 ----------

// Cron 是一个已解析的 cron 表达式：分 时 日 月 周。
type Cron struct {
	minute, hour, dom, month, dow map[int]bool
}

// ParseCron 解析标准 5 段 cron：分 时 日 月 周。
// 支持 `*`、`*/N`、`a,b,c`、`a-b`。**不支持 `@daily` 之类别名**（用不到，且容易误解）。
func ParseCron(expr string) (*Cron, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("需要 5 段（分 时 日 月 周），得到 %d 段：%q", len(fields), expr)
	}
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	sets := make([]map[int]bool, 5)
	for i, f := range fields {
		s, err := parseField(f, ranges[i][0], ranges[i][1])
		if err != nil {
			return nil, fmt.Errorf("第 %d 段 %q：%w", i+1, f, err)
		}
		sets[i] = s
	}
	return &Cron{sets[0], sets[1], sets[2], sets[3], sets[4]}, nil
}

func parseField(f string, lo, hi int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(f, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("空项")
		}
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("步长不合法")
			}
			step = n
			part = part[:i]
		}
		start, end := lo, hi
		if part != "*" {
			if i := strings.Index(part, "-"); i >= 0 {
				a, err1 := strconv.Atoi(part[:i])
				b, err2 := strconv.Atoi(part[i+1:])
				if err1 != nil || err2 != nil {
					return nil, fmt.Errorf("区间不合法")
				}
				start, end = a, b
			} else {
				v, err := strconv.Atoi(part)
				if err != nil {
					return nil, fmt.Errorf("不是数字：%q", part)
				}
				start, end = v, v
			}
		}
		if start < lo || end > hi || start > end {
			return nil, fmt.Errorf("超出范围 %d-%d", lo, hi)
		}
		for v := start; v <= end; v += step {
			out[v] = true
		}
	}
	return out, nil
}

// Match 判断某时刻是否命中。
// 日与周：cron 惯例是"任一命中即可"（两个都写时是 OR），这里遵循惯例。
func (c *Cron) Match(t time.Time) bool {
	if !c.minute[t.Minute()] || !c.hour[t.Hour()] || !c.month[int(t.Month())] {
		return false
	}
	domHit := c.dom[t.Day()]
	dowHit := c.dow[int(t.Weekday())]
	// 两者都受限时按 OR；只写一个时按 AND（与标准 cron 一致）
	domAny, dowAny := len(c.dom) == 31, len(c.dow) == 7
	switch {
	case domAny && dowAny:
		return true
	case domAny:
		return dowHit
	case dowAny:
		return domHit
	default:
		return domHit || dowHit
	}
}

// Describe 把人话翻译出来（界面显示用）。
func Describe(expr string) string {
	m, err := ParseCron(expr)
	if err != nil {
		return "时间不合法"
	}
	// 常见形态给一句人话，其余回显表达式
	if m.minute[30] && m.hour[17] && len(m.dom) == 31 && len(m.month) == 12 && len(m.dow) == 7 {
		return "每天 17:30"
	}
	if len(m.dom) == 31 && len(m.month) == 12 && len(m.dow) == 7 {
		mm := firstKey(m.minute)
		hh := firstKey(m.hour)
		if len(m.minute) == 1 && len(m.hour) == 1 {
			return fmt.Sprintf("每天 %02d:%02d", hh, mm)
		}
	}
	return expr
}

func firstKey(s map[int]bool) int {
	best := -1
	for k := range s {
		if best < 0 || k < best {
			best = k
		}
	}
	if best < 0 {
		return 0
	}
	return best
}
