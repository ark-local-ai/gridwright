package pathsafe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// document 是磁盘上的 JSON 结构。
type document struct {
	Version int     `json:"version"`
	Grants  []Grant `json:"grants"`
}

// Store 是一份授权清单，背后是一个 JSON 文件。
//
// 并发安全：内部用 RWMutex 保护，Check/List 只取读锁，
// 写操作（Allow/Revoke）在锁内完成"改内存 + 原子落盘"，
// 保证两个 goroutine 不会写出互相覆盖的文件（本机单进程，够用）。
type Store struct {
	path string
	mu   sync.RWMutex
	doc  document
}

// Open 打开（必要时创建内存中的空）授权清单。
//
// 文件不存在 → 空清单、无错误（首次启动）。
// 文件损坏 → 重命名为 `<path>.bak` 后从空开始，不报错、不 panic：
// 白名单读不出来时**失败即拒绝**才是安全的默认，但不能因此让引擎起不来；
// 旧文件留在 .bak 里，用户可以手工找回之前的授权。
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("pathsafe: 存储路径为空")
	}
	s := &Store{path: path, doc: document{Version: currentVersion}}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("pathsafe: 读取授权清单: %w", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return s, nil
	}

	var d document
	if err := json.Unmarshal(b, &d); err != nil {
		_ = os.Rename(path, path+".bak")
		return s, nil
	}
	if d.Version == 0 {
		d.Version = currentVersion
	}
	if d.Grants == nil {
		d.Grants = []Grant{}
	}
	s.doc = d
	return s, nil
}

// Path 返回底层 JSON 文件路径。
func (s *Store) Path() string { return s.path }

// Allow 授权一个根目录（读写）。等价于 AllowWithMode(root, source, ModeRW)。
func (s *Store) Allow(root, source string) (Grant, error) {
	return s.AllowWithMode(root, source, ModeRW)
}

// AllowWithMode 授权一个根目录（指定模式），幂等。
//
// 幂等：同一目录（按规范化后的卷 + 组件比较，大小写不敏感）重复授权
// 不会产生第二条记录，也不会刷新 Added。若之前是只读、新请求是读写，
// 则**升级**为读写（用户的意图只会越来越明确）。
func (s *Store) AllowWithMode(root, source string, mode Mode) (Grant, error) {
	c, err := canonicalize(root)
	if err != nil {
		return Grant{}, err
	}
	switch mode {
	case ModeRead, ModeRW:
	case "":
		mode = ModeRead
	default:
		return Grant{}, fmt.Errorf("pathsafe: 未知权限模式 %q", string(mode))
	}
	// 已存在但不是目录 → 授权无意义，早点报错（不存在的路径允许：
	// 作业区可能还没建，用户先授权是合理的）。
	if fi, err := os.Stat(c.path); err == nil && !fi.IsDir() {
		return Grant{}, fmt.Errorf("pathsafe: 授权根不是目录: %s", c.path)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.doc.Grants {
		if !grantMatches(s.doc.Grants[i], c) {
			continue
		}
		g := s.doc.Grants[i]
		if mode == ModeRW && g.Mode != ModeRW {
			g.Mode = ModeRW
			s.doc.Grants[i] = g
			if err := s.saveLocked(); err != nil {
				return Grant{}, err
			}
		}
		return g, nil
	}

	g := Grant{
		Root:     root,
		Source:   strings.TrimSpace(source),
		Mode:     mode,
		Added:    time.Now().UTC(),
		Resolved: c.path,
	}
	s.doc.Grants = append(s.doc.Grants, g)
	if err := s.saveLocked(); err != nil {
		// 落盘失败就回滚内存，避免"看起来授权了、重启后消失"。
		s.doc.Grants = s.doc.Grants[:len(s.doc.Grants)-1]
		return Grant{}, err
	}
	return g, nil
}

// Check 判断 p 是否在某个已授权根目录内，且该授权满足 want。
//
// 返回的 err 一定可用 errors.Is 判定：ErrOutside（含非法路径）/ ErrReadOnly。
// p 不需要存在（create 预检）。
func (s *Store) Check(p string, want Mode) error {
	c, err := canonicalize(p)
	if err != nil {
		// 无法证明安全 → 一律按越界处理，把原因一并带回。
		return fmt.Errorf("%w: %w", ErrOutside, err)
	}
	// 未知请求按最严格解释：当作要写。
	if want != ModeRead {
		want = ModeRW
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, g := range s.doc.Grants {
		gc, gerr := canonicalize(g.Resolved)
		if gerr != nil {
			// 旧数据无法规范化时退化为整串大小写不敏感比较（宁可收紧）。
			if !strings.EqualFold(g.Resolved, c.path) {
				continue
			}
		} else if !gc.contains(c) {
			continue
		}
		if !g.Mode.Allows(want) {
			return fmt.Errorf("%w: %s 位于只读授权 %s 内", ErrReadOnly, c.path, g.Resolved)
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrOutside, c.path)
}

// List 返回当前所有授权的副本（调用方随便改，不影响内部状态）。
func (s *Store) List() []Grant {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Grant, len(s.doc.Grants))
	copy(out, s.doc.Grants)
	return out
}

// Revoke 撤销一个根目录的授权。无该授权时返回 ErrNoGrant。
func (s *Store) Revoke(root string) error {
	c, err := canonicalize(root)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := make([]Grant, 0, len(s.doc.Grants))
	found := false
	for _, g := range s.doc.Grants {
		if grantMatches(g, c) {
			found = true
			continue
		}
		kept = append(kept, g)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrNoGrant, c.path)
	}
	old := s.doc.Grants
	s.doc.Grants = kept
	if err := s.saveLocked(); err != nil {
		s.doc.Grants = old
		return err
	}
	return nil
}

// grantMatches 判断一条已存授权是否就是 c 这个根目录。
func grantMatches(g Grant, c canon) bool {
	gc, err := canonicalize(g.Resolved)
	if err != nil {
		return strings.EqualFold(g.Resolved, c.path)
	}
	return gc.equal(c)
}

// saveLocked 原子落盘：临时文件 + rename，权限 0600。
// 直接覆写原文件在断电/崩溃时会留下半截 JSON，于是白名单会被判为损坏并清空——
// 那等于所有自动行为突然被拒。rename 在同一目录内是原子的，能避免这个窗口。
func (s *Store) saveLocked() error {
	if s.doc.Version == 0 {
		s.doc.Version = currentVersion
	}
	if s.doc.Grants == nil {
		s.doc.Grants = []Grant{}
	}
	b, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return fmt.Errorf("pathsafe: 序列化授权清单: %w", err)
	}
	b = append(b, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("pathsafe: 创建目录 %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".pathsafe-*.tmp")
	if err != nil {
		return fmt.Errorf("pathsafe: 创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	// CreateTemp 已是 0600，这里显式再设一次（Windows 上权限位有限，尽力而为）。
	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(b); err != nil {
		cleanup()
		return fmt.Errorf("pathsafe: 写入临时文件: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("pathsafe: 同步临时文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("pathsafe: 关闭临时文件: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("pathsafe: 替换授权清单: %w", err)
	}
	_ = os.Chmod(s.path, 0o600)
	return nil
}
