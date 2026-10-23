// Package zones 管理工作区内的“作业区（Zone）”。
//
// Zone 不是工作区：工作区仍然只是一个文件夹（用户选中的那个台账目录），
// Zone 只是这个工作区内部的一个**派单作用域 / 筛选视图**——圈定“从哪取数
// （input）、改到哪张表（target）、按哪些规则（rules）”，以及这次改动是
// 演习还是真改（mode）。它不复制文件、不另开目录，所以不存在“切错工作区”
// 这一层风险。
//
// 数据落在工作区根的 zones.json（与 ledger.csv / rules.yaml / sheet_notes.json
// 同级，沿用“内部文件放工作区根”的既有约定）。runs 只保留最近 50 条，
// 用于回答“上次跑成了没、被拒了几条”。
//
// dryRun 是安全默认：新建 Zone 没写 mode 时一律按演习处理，宁可多看一眼，
// 也不要第一次跑就把台账改了。真正的写盘由调用方（走确认制）决定。
//
// 包内不依赖 workspace 包：调用方传 layout.Root，路径白名单由调用方的
// pathsafe 负责，这里只把 input.path 当字符串保存。
package zones

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileName 是 Zone 数据文件在工作区根下的名字。
const FileName = "zones.json"

// Version 是当前文件格式版本。读到的 version 为 0 视为 1。
const Version = 1

// maxRuns 是 runs 的保留条数（只留最近 50 条）。
const maxRuns = 50

// 输入来源类型。
const (
	InputFolder = "folder" // 工作区里的某个子目录
	InputSystem = "system" // 外部系统（配合 connector）
	InputManual = "manual" // 人手拖进来的文件
)

// 触发方式。
const (
	WatchFS     = "fs"
	WatchCron   = "cron"
	WatchManual = "manual"
)

// 运行模式。dryRun 是默认值。
const (
	ModeDryRun = "dryRun"
	ModeApply  = "apply"
)

// ErrNotFound 表示指定 id 的 Zone 不存在。
var ErrNotFound = errors.New("作业区不存在")

// Input 描述“数据从哪来”。
type Input struct {
	Type      string `json:"type"`                // folder | system | manual
	Path      string `json:"path,omitempty"`      // 原样保存，路径白名单由调用方负责
	Connector string `json:"connector,omitempty"` // system 来源的适配器名
	Watch     string `json:"watch,omitempty"`     // fs | cron | manual（空→manual）
	Pattern   string `json:"pattern,omitempty"`   // 文件名通配/正则，按来源解释
}

// Target 描述“改到哪张表”。
type Target struct {
	File     string `json:"file,omitempty"`     // 工作区内的文件名，不含路径分隔符
	Sheet    string `json:"sheet,omitempty"`    // 工作表名
	NewTable string `json:"newTable,omitempty"` // 新建表名（可选）
}

// Zone 是一个作业区。
type Zone struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Icon      string   `json:"icon,omitempty"`
	Input     Input    `json:"input"`
	Target    Target   `json:"target"`
	Rules     []string `json:"rules,omitempty"`
	Mode      string   `json:"mode"` // dryRun | apply
	Status    string   `json:"status,omitempty"`
	LastRun   string   `json:"lastRun,omitempty"`
	LastError string   `json:"lastError,omitempty"`
	Outputs   []string `json:"outputs,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

// Run 是一次运行记录（只留摘要，不留明细）。
type Run struct {
	ZoneID   string `json:"zoneId"`
	Started  string `json:"started"`
	OK       bool   `json:"ok"`
	Applied  int    `json:"applied"`
	Rejected int    `json:"rejected"`
	Summary  string `json:"summary,omitempty"`
	Err      string `json:"err,omitempty"`
}

// File 是 zones.json 的磁盘结构。
type File struct {
	Version int    `json:"version"`
	Zones   []Zone `json:"zones"`
	Runs    []Run  `json:"runs"`
}

// Store 是 zones.json 的读写器，线程安全：内存副本 + 每次写后整份原子落盘。
type Store struct {
	root string
	path string
	mu   sync.Mutex
	data File
}

// Open 打开（不存在则视为空，不立即建文件；首次写才落盘）。
//
// 文件损坏时把它重命名成 zones.json.bak 并从空开始，不 panic、不阻塞启动：
// 作业区配置坏掉不该让整个管家打不开。
func Open(root string) (*Store, error) {
	p := filepath.Join(root, FileName)
	s := &Store{
		root: root,
		path: p,
		data: File{Version: Version, Zones: []Zone{}, Runs: []Run{}},
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("读取 %s: %w", p, err)
	}
	if len(b) == 0 {
		return s, nil
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		// 坏文件留证据，但当前进程从空开始；下一次写会生成干净的文件。
		_ = os.Remove(p + ".bak")
		_ = os.Rename(p, p+".bak")
		return s, nil
	}
	if f.Version == 0 {
		f.Version = Version
	}
	if f.Zones == nil {
		f.Zones = []Zone{}
	}
	if f.Runs == nil {
		f.Runs = []Run{}
	}
	s.data = f
	return s, nil
}

// Path 返回数据文件路径。
func (s *Store) Path() string { return s.path }

// Root 返回工作区根目录。
func (s *Store) Root() string { return s.root }

// List 返回全部 Zone。稳定排序：createdAt，其次 id。
func (s *Store) List() []Zone {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Zone, 0, len(s.data.Zones))
	for _, z := range s.data.Zones {
		out = append(out, cloneZone(z))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get 取一个 Zone，不存在返回 false。
func (s *Store) Get(id string) (Zone, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, z := range s.data.Zones {
		if z.ID == id {
			return cloneZone(z), true
		}
	}
	return Zone{}, false
}

// Put 新建或覆盖一个 Zone，返回落库后的副本。
//
// id 为空时生成稳定的 z_ + 8 hex；同 id 覆盖。createdAt 保留：已有记录沿用
// 原值，新记录空则用当前时间。name 必填；mode 空→dryRun；input.watch 空→manual；
// input.type 必须是 folder/system/manual（未知类型拒绝写入，但读取时会原样保留）。
func (s *Store) Put(z Zone) (Zone, error) {
	if err := validate(&z); err != nil {
		return Zone{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Format(time.RFC3339)
	idx := -1
	for i := range s.data.Zones {
		if z.ID != "" && s.data.Zones[i].ID == z.ID {
			idx = i
			break
		}
	}

	if z.ID == "" {
		z.ID = s.newIDLocked()
	}
	if idx >= 0 {
		// 覆盖：身份证时间以库里为准，调用方传错也不改历史。
		z.CreatedAt = s.data.Zones[idx].CreatedAt
	} else if z.CreatedAt == "" {
		z.CreatedAt = now
	}

	if idx >= 0 {
		s.data.Zones[idx] = cloneZone(z)
	} else {
		s.data.Zones = append(s.data.Zones, cloneZone(z))
	}
	if err := s.saveLocked(); err != nil {
		return Zone{}, err
	}
	return cloneZone(z), nil
}

// Delete 删除一个 Zone，不存在返回 ErrNotFound。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Zones {
		if s.data.Zones[i].ID == id {
			s.data.Zones = append(s.data.Zones[:i], s.data.Zones[i+1:]...)
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

// RecordRun 追加一条运行记录并把 runs 裁剪到最近 50 条。
//
// 它不修改 Zone 的 status/lastRun：状态由调用方用 MarkStatus 显式表达，
// 一次运行记录不该偷偷改配置。
func (s *Store) RecordRun(r Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Runs = append(s.data.Runs, r)
	sort.SliceStable(s.data.Runs, func(i, j int) bool {
		return s.data.Runs[i].Started > s.data.Runs[j].Started
	})
	if len(s.data.Runs) > maxRuns {
		s.data.Runs = s.data.Runs[:maxRuns]
	}
	_ = s.saveLocked() // 记录运行日志失败不该打断调用方主流程
}

// Runs 返回运行记录。zoneID 为空=全部；按 started 倒序；limit<=0=不限条数。
func (s *Store) Runs(zoneID string, limit int) []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Run, 0, len(s.data.Runs))
	for _, r := range s.data.Runs {
		if zoneID != "" && r.ZoneID != zoneID {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started > out[j].Started })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// MarkStatus 更新一个 Zone 的状态与最近错误，不存在返回 ErrNotFound。
func (s *Store) MarkStatus(id, status, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Zones {
		if s.data.Zones[i].ID != id {
			continue
		}
		s.data.Zones[i].Status = status
		s.data.Zones[i].LastError = lastError
		return s.saveLocked()
	}
	return ErrNotFound
}

// validate 做写入前的严格校验，并补齐默认值。
func validate(z *Zone) error {
	if strings.TrimSpace(z.Name) == "" {
		return errors.New("作业区名称不能为空")
	}
	switch z.Input.Type {
	case InputFolder, InputSystem, InputManual:
		// ok
	case "":
		return errors.New("输入类型不能为空")
	default:
		return fmt.Errorf("未知输入类型 %q（只支持 folder/system/manual）", z.Input.Type)
	}

	if z.Mode == "" {
		z.Mode = ModeDryRun
	}
	switch z.Mode {
	case ModeDryRun, ModeApply:
	default:
		return fmt.Errorf("未知运行模式 %q（只支持 dryRun/apply）", z.Mode)
	}

	if z.Input.Watch == "" {
		z.Input.Watch = WatchManual
	}
	switch z.Input.Watch {
	case WatchFS, WatchCron, WatchManual:
	default:
		return fmt.Errorf("未知触发方式 %q（只支持 fs/cron/manual）", z.Input.Watch)
	}

	if strings.ContainsAny(z.Target.File, `/\`) {
		return fmt.Errorf("目标文件名 %q 不能包含路径分隔符（只允许工作区内的文件名）", z.Target.File)
	}
	return nil
}

// newIDLocked 生成不与现有记录冲突的 z_ + 8 hex。调用方需持锁。
func (s *Store) newIDLocked() string {
	for i := 0; i < 100; i++ {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			// 极端情况下退回时间戳，保证 id 仍然稳定唯一。
			return fmt.Sprintf("z_%08x", time.Now().UnixNano()&0xffffffff)
		}
		id := "z_" + hex.EncodeToString(b[:])
		taken := false
		for j := range s.data.Zones {
			if s.data.Zones[j].ID == id {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
	}
	return fmt.Sprintf("z_%08x", time.Now().UnixNano()&0xffffffff)
}

// saveLocked 整份落盘：临时文件 + rename，权限 0600。调用方需持锁。
func (s *Store) saveLocked() error {
	s.data.Version = Version
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 %s: %w", s.path, err)
	}
	b = append(b, '\n')

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, "."+FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("写入临时文件: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("同步临时文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		cleanup()
		return fmt.Errorf("设置权限: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		cleanup()
		return fmt.Errorf("替换 %s: %w", s.path, err)
	}
	return nil
}

// cloneZone 复制切片字段，避免调用方改返回值时污染内存里的副本。
func cloneZone(z Zone) Zone {
	if z.Rules != nil {
		z.Rules = append([]string(nil), z.Rules...)
	}
	if z.Outputs != nil {
		z.Outputs = append([]string(nil), z.Outputs...)
	}
	return z
}
