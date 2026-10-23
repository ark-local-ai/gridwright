// Package zoneingest 负责“作业区输入目录 → 工作区 inbox”的取件环节。
//
// 一次取件只做三件事：**列出、去重、复制**。它不解析表格、不调用模型、
// 不写账目——那些是既有 agent 在 inbox 里取到文件之后的事。把边界卡在这里，
// 是为了让“点了运行到底会动哪些文件”变成一个可以单独测试、可以演习
// （dryRun）的纯文件操作。
//
// 为什么要去重，而且要看 inbox/done：
// 发票/系统导出这类文件是**会被反复导出**的——同一张发票可能今天导一次、
// 明天整理时又导一次，文件名甚至内容完全一样。如果只比对 inbox，那么
// “已经处理完、被 agent 移进 inbox/done 的那一份”就看不见了：inbox 空着，
// 于是又投一遍，agent 再记一次账，就是重复入账。真正能证明“这份已经处理过”
// 的证据在 done 里，所以去重同时看三处：
//
//	a) 目标 inbox/<name> 已存在      —— 还没处理，别覆盖
//	b) done 下有同名或 N-<name>      —— 处理过同名文件
//	c) done 下有 size + 摘要相同的文件 —— 处理过同一内容（用户改过名）
//
// 其中 (b) 的 N-<name> 对应 workspace.MoveToDone 的重名编号规则（1-、2-…）；
// (c) 只对不超过 50MB 的文件算 SHA-256，取摘要前 16 字节做指纹，
// 超大文件退回“只按同名去重”，避免为了去重把整个大目录读一遍。
//
// 安全约束：所有路径先转绝对路径；源目录必须存在且是目录；拒绝源目录
// 等于工作区根或 inbox/done 自身——否则“从 inbox 取件投进 inbox”会自己
// 喂自己，形成死循环。
package zoneingest

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Candidate 是输入目录里的一个候选项。
//
// Path 始终是**源文件**路径；Duplicate 为 true 时 DuplicateOf 指向判定重复的
// 参照文件（inbox 或 done 里的那个），Reason 说明理由。投递成功后
// DuplicateOf 会被改写成**投递目标路径**（结构体没有单独的 Target 字段），
// Reason 变成“已投递”，便于调用方展示“这份去哪了”。
type Candidate struct {
	Name        string
	Path        string
	Size        int64
	ModTime     string
	Duplicate   bool
	DuplicateOf string
	Reason      string
}

// Options 是一次扫描/投递的参数。
//
// Pattern 为空表示接受 .xlsx/.xlsm/.csv/.xls；否则按基名 glob 匹配
// （path/filepath.Match，大小写不敏感）。InboxDir 必填，DoneDir 为空时
// 默认取 <InboxDir>/done。MaxFiles <= 0 时默认 200。
type Options struct {
	SourceDir string
	Pattern   string
	InboxDir  string
	DoneDir   string
	MaxFiles  int
}

// Result 是一次 Run 的统计。Scanned 是实际读到的目录项数（受 maxDirEntries
// 上限约束），Matched 是过滤后命中的文件数（截断前），Duplicates 是返回的
// 候选中被判为重复的数量，Delivered 是真正复制成功的数量（dryRun 时恒为 0）。
type Result struct {
	Scanned    int
	Matched    int
	Duplicates int
	Delivered  int
	Items      []Candidate
}

const (
	// defaultMaxFiles 是单次返回的候选项默认上限。
	defaultMaxFiles = 200
	// maxHashSize 是参与内容去重的文件大小上限（50MB）。
	maxHashSize int64 = 50 << 20
)

// maxDirEntries 是单次扫描读入的目录项硬上限，防超大目录把内存打爆。
// 声明为变量是为了让测试能把上限调小（正常情况下应当只读）。
var maxDirEntries = 2000

// defaultExts 是 Pattern 为空时接受的后缀（小写、含点）。
var defaultExts = map[string]bool{
	".xlsx": true,
	".xlsm": true,
	".csv":  true,
	".xls":  true,
}

// tempExts 是常见的“还没下完/还没写完”的临时后缀（小写、含点）。
var tempExts = map[string]bool{
	".tmp":        true,
	".crdownload": true,
	".part":       true,
}

// scanStats 是 Scan 内部需要、但 []Candidate 装不下的计数。
type scanStats struct {
	scanned int
	matched int
}

// Scan 列出 SourceDir 一层内匹配 Pattern 的文件，并标记重复项。
// 结果按 Name 排序，最多返回 MaxFiles 个；被截断的数量不体现在返回值里，
// 调用方可以调大 MaxFiles 或分批处理（Result.Matched 会给出截断前的命中数）。
func Scan(opt Options) ([]Candidate, error) {
	items, _, err := scanDir(opt)
	return items, err
}

// Run 执行一次取件。dryRun 为 true 时只扫描、绝不写任何文件。
// 单个文件投递失败不会中断整批：该 Candidate 的 Reason 记下失败原因，
// 其余文件继续投递。
func Run(opt Options, dryRun bool) (Result, error) {
	items, stats, err := scanDir(opt)
	if err != nil {
		return Result{}, err
	}
	_, inboxAbs, _, err := normalize(opt)
	if err != nil {
		return Result{}, err
	}

	res := Result{
		Scanned: stats.scanned,
		Matched: stats.matched,
		Items:   make([]Candidate, 0, len(items)),
	}
	for _, c := range items {
		if c.Duplicate {
			res.Duplicates++
			res.Items = append(res.Items, c)
			continue
		}
		if dryRun {
			res.Items = append(res.Items, c)
			continue
		}
		dst, derr := Deliver(c.Path, inboxAbs)
		if derr != nil {
			c.Reason = "投递失败：" + derr.Error()
			res.Items = append(res.Items, c)
			continue
		}
		c.Duplicate = false
		c.DuplicateOf = dst
		c.Reason = "已投递"
		res.Delivered++
		res.Items = append(res.Items, c)
	}
	return res, nil
}

// scanDir 是 Scan/Run 共用的实现，额外返回目录项/命中计数。
func scanDir(opt Options) ([]Candidate, scanStats, error) {
	var stats scanStats
	srcAbs, inboxAbs, doneAbs, err := normalize(opt)
	if err != nil {
		return nil, stats, err
	}
	pattern := opt.Pattern
	if pattern != "" {
		if _, err := filepath.Match(pattern, ""); err != nil {
			return nil, stats, fmt.Errorf("文件名匹配模式无效 %q: %w", pattern, err)
		}
	}

	entries, err := os.ReadDir(srcAbs)
	if err != nil {
		return nil, stats, fmt.Errorf("读取源目录 %s: %w", srcAbs, err)
	}
	done, err := listDone(doneAbs)
	if err != nil {
		return nil, stats, err
	}

	out := make([]Candidate, 0, len(entries))
	for _, e := range entries {
		if stats.scanned >= maxDirEntries {
			break
		}
		stats.scanned++
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !acceptName(name, pattern) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			// 列目录期间文件被移走/锁住：跳过，不让整批失败。
			continue
		}
		stats.matched++

		c := Candidate{
			Name:    name,
			Path:    filepath.Join(srcAbs, name),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		}
		markDuplicate(&c, inboxAbs, done)
		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool {
		ni, nj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if ni != nj {
			return ni < nj
		}
		return out[i].Name < out[j].Name
	})

	limit := opt.MaxFiles
	if limit <= 0 {
		limit = defaultMaxFiles
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, stats, nil
}

// normalize 把三个目录转成绝对路径并做安全校验。
// 返回顺序：源目录、inbox、done。
func normalize(opt Options) (string, string, string, error) {
	if strings.TrimSpace(opt.SourceDir) == "" {
		return "", "", "", errors.New("缺少源目录 SourceDir")
	}
	if strings.TrimSpace(opt.InboxDir) == "" {
		return "", "", "", errors.New("缺少收件箱目录 InboxDir")
	}

	srcAbs, err := filepath.Abs(opt.SourceDir)
	if err != nil {
		return "", "", "", fmt.Errorf("解析源目录路径: %w", err)
	}
	info, err := os.Stat(srcAbs)
	if err != nil {
		return "", "", "", fmt.Errorf("源目录不可用 %s: %w", srcAbs, err)
	}
	if !info.IsDir() {
		return "", "", "", fmt.Errorf("源路径不是目录: %s", srcAbs)
	}

	inboxAbs, err := filepath.Abs(opt.InboxDir)
	if err != nil {
		return "", "", "", fmt.Errorf("解析收件箱路径: %w", err)
	}
	doneAbs := opt.DoneDir
	if strings.TrimSpace(doneAbs) == "" {
		doneAbs = filepath.Join(inboxAbs, "done")
	} else {
		doneAbs, err = filepath.Abs(doneAbs)
		if err != nil {
			return "", "", "", fmt.Errorf("解析 done 路径: %w", err)
		}
	}

	// 源目录 = 工作区根（inbox 的父目录）或 inbox/done 自身时，
	// 取件会把刚投进去的文件再当成输入，越跑越多。直接拒绝。
	rootAbs := filepath.Dir(inboxAbs)
	switch {
	case samePath(srcAbs, inboxAbs):
		return "", "", "", fmt.Errorf("源目录不能是 inbox 自身: %s", srcAbs)
	case samePath(srcAbs, doneAbs):
		return "", "", "", fmt.Errorf("源目录不能是 done 自身: %s", srcAbs)
	case samePath(srcAbs, rootAbs):
		return "", "", "", fmt.Errorf("源目录不能是工作区根（会把自己的 inbox 当输入）: %s", srcAbs)
	}
	return srcAbs, inboxAbs, doneAbs, nil
}

// samePath 比较两个已绝对化、已 Clean 的路径是否指向同一位置。
// Windows 文件系统大小写不敏感，所以只在 Windows 上折叠大小写。
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// acceptName 判断目录项是否是候选：跳过隐藏文件、Office 锁文件、临时文件，
// 再按 Pattern 或默认后缀过滤。
func acceptName(name, pattern string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~$") {
		return false
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, "~") {
		return false
	}
	if tempExts[filepath.Ext(lower)] {
		return false
	}
	if pattern != "" {
		ok, err := filepath.Match(strings.ToLower(pattern), lower)
		return err == nil && ok
	}
	return defaultExts[filepath.Ext(lower)]
}

// doneFile 是 done 目录里参与比对的普通文件。
type doneFile struct {
	name string
	path string
	size int64
}

// listDone 列出 done 下的普通文件；done 不存在视为“还没处理过任何文件”。
func listDone(doneAbs string) ([]doneFile, error) {
	entries, err := os.ReadDir(doneAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 done 目录 %s: %w", doneAbs, err)
	}
	out := make([]doneFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		out = append(out, doneFile{
			name: e.Name(),
			path: filepath.Join(doneAbs, e.Name()),
			size: info.Size(),
		})
	}
	return out, nil
}

// markDuplicate 按“inbox 同名 → done 同名/编号 → done 同内容”的顺序判定。
// 先便宜后昂贵：只有前面都没命中才去算摘要。
func markDuplicate(c *Candidate, inboxAbs string, done []doneFile) {
	if _, err := os.Stat(filepath.Join(inboxAbs, c.Name)); err == nil {
		c.Duplicate = true
		c.DuplicateOf = filepath.Join(inboxAbs, c.Name)
		c.Reason = "收件箱里已有同名文件"
		return
	}

	for _, d := range done {
		if doneNameMatches(d.name, c.Name) {
			c.Duplicate = true
			c.DuplicateOf = d.path
			c.Reason = "已处理过同名文件"
			return
		}
	}

	if c.Size > maxHashSize {
		// 超大文件不读内容：说明清楚，调用方知道这一条只做了同名去重。
		c.Reason = fmt.Sprintf("文件超过 %dMB，未做内容去重", maxHashSize>>20)
		return
	}

	fp, err := fingerprint(c.Path)
	if err != nil {
		c.Reason = "计算文件摘要失败，未做内容去重：" + err.Error()
		return
	}
	for _, d := range done {
		if d.size != c.Size || d.size > maxHashSize || d.name == c.Name {
			continue
		}
		dfp, err := fingerprint(d.path)
		if err != nil || dfp != fp {
			continue
		}
		c.Duplicate = true
		c.DuplicateOf = d.path
		c.Reason = fmt.Sprintf("已处理过相同内容（改名 %s）", d.name)
		return
	}
}

// doneNameMatches 判断 done 里的文件名是否就是 name，或 workspace.MoveToDone
// 生成的 N-name / N-name-M-name 形式（只认纯数字前缀）。
func doneNameMatches(doneName, name string) bool {
	if strings.EqualFold(doneName, name) {
		return true
	}
	if len(doneName) <= len(name)+1 || doneName[len(doneName)-len(name)-1] != '-' {
		return false
	}
	if !strings.EqualFold(doneName[len(doneName)-len(name):], name) {
		return false
	}
	prefix := doneName[:len(doneName)-len(name)-1]
	// 形如 1-name / 2-1-name：每一段都必须是数字。
	for _, seg := range strings.Split(prefix, "-") {
		if seg == "" {
			return false
		}
		if _, err := strconv.Atoi(seg); err != nil {
			return false
		}
	}
	return true
}

// fingerprint 返回 SHA-256 摘要的前 16 字节（128 位），足以避免改名误判。
func fingerprint(path string) ([16]byte, error) {
	var fp [16]byte
	f, err := os.Open(path)
	if err != nil {
		return fp, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fp, err
	}
	copy(fp[:], h.Sum(nil)[:16])
	return fp, nil
}
