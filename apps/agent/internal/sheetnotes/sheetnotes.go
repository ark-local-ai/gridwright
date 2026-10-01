// Package sheetnotes 缓存"工作表大概描述"（见 docs/agent-architecture/19-界面设计.md 与
// C:\Users\19566\.peakcode\workspace\gridwright-工作表详述-plan.md）。
//
// 为什么是缓存文件而不是每次现问模型：描述是低频变化的长效信息，而模型调用有
// 秒级延迟与 token 成本。存成工作区根下的 sheet_notes.json（与 ledger.csv /
// rules.yaml / state.yaml 同级，沿用"内部文件放工作区根"的既有约定）。
//
// DataFiles() 只列 *.xlsx，所以这个文件不会被当成一张表。
package sheetnotes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileName 是缓存文件在工作区根下的名字。
const FileName = "sheet_notes.json"

// Note 是某张工作表的一条描述。
type Note struct {
	Description string `json:"description"`
	At          string `json:"at"`    // 生成时间（YYYY-MM-DD HH:MM）
	Model       string `json:"model"` // 用哪个模型生成的
	// HeaderHash 是生成时的表头指纹。表头变了（列增删/移动）→ 描述可能过期，
	// 下次体检会重算。空值表示没记录（老数据），视为过期。
	HeaderHash string `json:"headerHash,omitempty"`
}

// Store 是 sheet_notes.json 的读写器，线程安全（与 ledger 一样）。
type Store struct {
	path string
	mu   sync.Mutex
	data fileData
}

type fileData struct {
	Version int             `json:"version"`
	Notes   map[string]Note `json:"notes"`
}

// Open 打开（不存在则视为空，不立即建文件；首次 Put 才写盘）。
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: fileData{Version: 1, Notes: map[string]Note{}}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		// 文件坏了不该连累只读功能：当作空，下次 Put 会覆盖成好的。
		return s, nil
	}
	if s.data.Notes == nil {
		s.data.Notes = map[string]Note{}
	}
	if s.data.Version == 0 {
		s.data.Version = 1
	}
	return s, nil
}

// Path 返回缓存文件路径。
func (s *Store) Path() string { return s.path }

// Get 取一条描述，不存在返回 false。
func (s *Store) Get(key string) (Note, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.data.Notes[key]
	return n, ok
}

// Put 写入一条描述并落盘。空描述不入库（描述必须有内容才有意义）。
func (s *Store) Put(key string, n Note) error {
	if n.Description == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Notes == nil {
		s.data.Notes = map[string]Note{}
	}
	s.data.Notes[key] = n
	return s.saveLocked()
}

// All 返回全部描述的快照（拷贝，调用方改不到内部状态）。
func (s *Store) All() map[string]Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Note, len(s.data.Notes))
	for k, v := range s.data.Notes {
		out[k] = v
	}
	return out
}

// saveLocked 原子落盘：先写临时文件再 rename，避免写一半的坏 JSON 留在原地。
func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".sheet_notes-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
