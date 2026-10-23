package pathsafe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

// requireWindows：本包的判定语义就是 Windows 的（大小写不敏感、盘符、UNC、
// 尾部空格/点截断），在别的平台上这些断言没有意义。
func requireWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("pathsafe 的判定语义仅针对 Windows")
	}
}

// newStore 在临时目录里新建一个空清单。
func newStore(t *testing.T) *Store {
	t.Helper()
	requireWindows(t)
	s, err := Open(filepath.Join(t.TempDir(), "grants.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// newRoot 建一个临时目录并返回它的路径（作为"授权根"用）。
func newRoot(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// mustMkdir 建目录，失败即终止。
func mustMkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", p, err)
	}
	return p
}

// wantOutside 断言错误可被 errors.Is 判为 ErrOutside。
func wantOutside(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 期望拒绝（ErrOutside），实际放行", label)
	}
	if !errors.Is(err, ErrOutside) {
		t.Fatalf("%s: 期望 ErrOutside，实际 %v", label, err)
	}
}

// wantOK 断言放行。
func wantOK(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 期望放行，实际 %v", label, err)
	}
}

// ---------------------------------------------------------------------------
// 路径判定
// ---------------------------------------------------------------------------

func TestCheckRejectsDotDotEscape(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	root := mustMkdir(t, filepath.Join(base, "data"))
	if _, err := s.Allow(root, "作业区"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	wantOK(t, "根内文件", s.Check(filepath.Join(root, "a.xlsx"), ModeRead))
	wantOutside(t, "data\\..\\secret", s.Check(filepath.Join(root, "..", "secret.txt"), ModeRead))
	wantOutside(t, "上跳到温区根", s.Check(filepath.Join(root, ".."), ModeRead))
	// 混用分隔符的上跳也必须被 Clean 掉，而不是靠字符串前缀混过去。
	wantOutside(t, "混合分隔符上跳", s.Check(root+`/../..`, ModeRead))
	wantOutside(t, "上跳到系统目录", s.Check(filepath.Join(root, "..", "..", "..", "Windows", "system.ini"), ModeRead))
}

func TestCheckSiblingWithSharedPrefix(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	data := mustMkdir(t, filepath.Join(base, "data"))
	data2 := mustMkdir(t, filepath.Join(base, "data2"))
	if _, err := s.Allow(data, "作业区"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	wantOK(t, "data 本身", s.Check(data, ModeRead))
	wantOK(t, "data 之下", s.Check(filepath.Join(data, "x.txt"), ModeRead))
	// 关键回归：字符串前缀相等，但组件不同。
	wantOutside(t, "data2 不是 data 的子路径", s.Check(filepath.Join(data2, "x.txt"), ModeRead))
	wantOutside(t, "data2 本身", s.Check(data2, ModeRead))
}

func TestCheckCaseInsensitive(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	root := mustMkdir(t, filepath.Join(base, "CaseDir"))
	if _, err := s.Allow(root, "作业区"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	// 不存在的尾部不会被 EvalSymlinks 归一化大小写，所以这条真正考察的是
	// "按组件 + 大小写不敏感"比较，而不是文件系统返回的规范大小写。
	wantOK(t, "全大写", s.Check(strings.ToUpper(root)+`\NEW\FILE.XLSX`, ModeRead))
	wantOK(t, "全小写", s.Check(strings.ToLower(root)+`\new\file.xlsx`, ModeRead))

	// 盘符大小写：卷比较同样不敏感。
	driveLower := strings.ToLower(root[:1]) + root[1:]
	if driveLower == root {
		t.Skip("临时目录盘符已小写，跳过盘符大小写断言")
	}
	wantOK(t, "盘符小写", s.Check(driveLower, ModeRead))
}

func TestCheckRejectsDriveRelativeAndDevices(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	if _, err := s.Allow(base, "作业区"); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	drive := base[:2] // "C:"

	cases := []struct{ label, p string }{
		{"空路径", ""},
		{"纯空白", "   "},
		{"NUL", base + "\x00x"},
		{"控制字符", base + "\\a\x01b.txt"},
		{"盘符相对", drive + "foo"},
		{"裸盘符", drive},
		{"扩展长度前缀", `\\?\` + base},
		{"设备前缀", `\\.\` + base},
		{"设备前缀 UNC 形式", `\\?\UNC\server\share\x`},
		{"组件内冒号", filepath.Join(base, "a::$DATA")},
		{"冒号中间", filepath.Join(base, "a:b.txt")},
		{"UNC 缺共享名", `\\server`},
		{"UNC 空共享名", `\\server\\x`},
	}
	for _, c := range cases {
		wantOutside(t, c.label, s.Check(c.p, ModeRead))
	}
}

func TestCheckTrimsTrailingSpacesAndDots(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	data := mustMkdir(t, filepath.Join(base, "data"))
	if err := os.WriteFile(filepath.Join(data, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := s.Allow(data, "作业区"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	// Win32 会静默截断，所以这些写法其实都指向 data\file.txt。
	wantOK(t, "文件尾部加点", s.Check(filepath.Join(data, "file.txt.")+".", ModeRead))
	wantOK(t, "目录尾部加空格", s.Check(base+`\data \file.txt`, ModeRead))
	wantOK(t, "目录尾部加点", s.Check(base+`\data.\file.txt`, ModeRead))
	wantOK(t, "不存在的文件加点", s.Check(data+`\future.xlsx.`, ModeRead))

	// 反向：截断后仍然在根之外，不能因为"多打了空格"就放行。
	wantOutside(t, "兄弟目录加空格", s.Check(base+`\data2 \file.txt`, ModeRead))
}

func TestCheckSymlinkEscape(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	root := mustMkdir(t, filepath.Join(base, "work"))
	outside := mustMkdir(t, filepath.Join(base, "outside"))
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		// 普通用户默认没有创建符号链接的权限，退而用目录联接（junction）：
		// 它同样是重解析点，EvalSymlinks 一样会跟过去，覆盖面等价。
		if jerr := makeJunction(link, outside); jerr != nil {
			t.Skipf("当前环境无法创建符号链接/目录联接: %v / %v", err, jerr)
		}
	}
	if _, err := s.Allow(root, "作业区"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	wantOK(t, "根内普通文件", s.Check(filepath.Join(root, "a.txt"), ModeRead))
	// 链接本体已经指向根外，因此它自己也不在根内。
	wantOutside(t, "链接本体", s.Check(link, ModeRead))
	wantOutside(t, "经链接越界", s.Check(filepath.Join(link, "secret.txt"), ModeRead))

	// 授权根本身是链接时，应记录并解析成真实目录：两条写法等价。
	s2 := newStore(t)
	if _, err := s2.Allow(link, "作业区"); err != nil {
		t.Fatalf("Allow(link): %v", err)
	}
	wantOK(t, "链接根的真实路径", s2.Check(filepath.Join(outside, "x.txt"), ModeRead))
	wantOK(t, "链接根的链接写法", s2.Check(filepath.Join(link, "x.txt"), ModeRead))
}

func TestUNCContainment(t *testing.T) {
	// 纯规范化层面的判定：不碰网络，快且确定。
	rootCanon, err := canonicalize(`\\server\share`)
	if err != nil {
		t.Fatalf("canonicalize UNC: %v", err)
	}
	inside, err := canonicalize(`\\SERVER\SHARE\dir\a.txt`)
	if err != nil {
		t.Fatalf("canonicalize UNC 内: %v", err)
	}
	otherShare, err := canonicalize(`\\server\share2\a.txt`)
	if err != nil {
		t.Fatalf("canonicalize UNC 兄弟共享: %v", err)
	}
	otherServer, err := canonicalize(`\\other\share\a.txt`)
	if err != nil {
		t.Fatalf("canonicalize UNC 其它服务器: %v", err)
	}
	if !rootCanon.contains(inside) {
		t.Fatalf("同一共享内的路径应被包含: %s", inside.path)
	}
	if rootCanon.contains(otherShare) {
		t.Fatalf("share2 不应被 share 包含: %s", otherShare.path)
	}
	if rootCanon.contains(otherServer) {
		t.Fatalf("其它服务器不应被包含: %s", otherServer.path)
	}
	if rootCanon.vol != `\\server\share` {
		t.Fatalf("UNC 卷应取 \\\\server\\share，实际 %q", rootCanon.vol)
	}

	// 端到端（用 127.0.0.1，避免域名解析等待；共享不存在也不影响判定，
	// 因为"不存在的尾部"按字面拼回去比较）。
	if runtime.GOOS != "windows" {
		return
	}
	s, err := Open(filepath.Join(t.TempDir(), "grants.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	uncRoot := `\\127.0.0.1\share`
	if _, err := s.Allow(uncRoot, "系统导出"); err != nil {
		t.Fatalf("Allow UNC: %v", err)
	}
	wantOK(t, "UNC 内", s.Check(uncRoot+`\2024\a.xlsx`, ModeRead))
	wantOK(t, "UNC 大小写", s.Check(`\\127.0.0.1\SHARE\2024\a.xlsx`, ModeRead))
	wantOutside(t, "UNC 兄弟共享", s.Check(`\\127.0.0.1\share2\a.xlsx`, ModeRead))
	wantOutside(t, "UNC 同主机其它共享", s.Check(`\\127.0.0.1\other\a.xlsx`, ModeRead))
	// 共享是 UNC 的卷根，`..` 上跳不出它（Clean 会停住），因此仍算共享内。
	wantOK(t, "UNC 共享内上跳", s.Check(`\\127.0.0.1\share\..\..\x`, ModeRead))
}

func TestCreatePrecheckNonExistentPath(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	root := mustMkdir(t, filepath.Join(base, "work"))
	if _, err := s.Allow(root, "作业区"); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	wantOK(t, "不存在的深层文件", s.Check(filepath.Join(root, "a", "b", "c.xlsx"), ModeRW))
	wantOutside(t, "不存在的根外文件", s.Check(filepath.Join(base, "a", "b", "c.xlsx"), ModeRW))

	// 授权一个还不存在的目录也应允许（用户先授权、目录后建）。
	s2 := newStore(t)
	future := filepath.Join(base, "future")
	if _, err := s2.Allow(future, "发票目录"); err != nil {
		t.Fatalf("Allow(不存在): %v", err)
	}
	mustMkdir(t, future)
	wantOK(t, "先授权后建目录", s2.Check(filepath.Join(future, "inv.xlsx"), ModeRW))
}

// ---------------------------------------------------------------------------
// 权限模式
// ---------------------------------------------------------------------------

func TestReadOnlyVsReadWrite(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	ro := mustMkdir(t, filepath.Join(base, "inbox"))
	rw := mustMkdir(t, filepath.Join(base, "outbox"))
	if _, err := s.AllowWithMode(ro, "发票目录", ModeRead); err != nil {
		t.Fatalf("AllowWithMode(ro): %v", err)
	}
	if _, err := s.AllowWithMode(rw, "作业区", ModeRW); err != nil {
		t.Fatalf("AllowWithMode(rw): %v", err)
	}

	wantOK(t, "只读根读", s.Check(filepath.Join(ro, "a.xlsx"), ModeRead))
	err := s.Check(filepath.Join(ro, "a.xlsx"), ModeRW)
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("只读根写: 期望 ErrReadOnly，实际 %v", err)
	}
	if errors.Is(err, ErrOutside) {
		t.Fatalf("ErrReadOnly 不应同时被判定为 ErrOutside: %v", err)
	}
	wantOK(t, "读写根读", s.Check(filepath.Join(rw, "a.xlsx"), ModeRead))
	wantOK(t, "读写根写", s.Check(filepath.Join(rw, "a.xlsx"), ModeRW))

	// 默认 Allow = 读写。
	s2 := newStore(t)
	if g, err := s2.Allow(base, "作业区"); err != nil || g.Mode != ModeRW {
		t.Fatalf("Allow 默认模式应为 rw，实际 %+v, err=%v", g, err)
	}
}

// ---------------------------------------------------------------------------
// 清单管理
// ---------------------------------------------------------------------------

func TestAllowIdempotent(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	root := mustMkdir(t, filepath.Join(base, "work"))

	g1, err := s.Allow(root, "作业区")
	if err != nil {
		t.Fatalf("Allow 1: %v", err)
	}
	g2, err := s.Allow(root, "作业区")
	if err != nil {
		t.Fatalf("Allow 2: %v", err)
	}
	if len(s.List()) != 1 {
		t.Fatalf("重复授权应只有一条记录，实际 %d", len(s.List()))
	}
	if !g1.Added.Equal(g2.Added) {
		t.Fatalf("重复授权不应刷新 Added：%v vs %v", g1.Added, g2.Added)
	}
	// 大小写不同、尾部多一个点，仍视为同一目录。
	if _, err := s.Allow(strings.ToUpper(root)+`.`, "作业区"); err != nil {
		t.Fatalf("Allow 大小写变体: %v", err)
	}
	if len(s.List()) != 1 {
		t.Fatalf("大小写变体不应新增记录，实际 %d", len(s.List()))
	}
	if g := s.List()[0]; g.Root != root || !filepath.IsAbs(g.Resolved) {
		t.Fatalf("Grant 字段异常: %+v", g)
	}

	// 先只读、再读写 → 升级，而不是新增。
	s2 := newStore(t)
	if _, err := s2.AllowWithMode(root, "作业区", ModeRead); err != nil {
		t.Fatalf("AllowWithMode(ro): %v", err)
	}
	if _, err := s2.Allow(root, "作业区"); err != nil {
		t.Fatalf("Allow(rw): %v", err)
	}
	if l := s2.List(); len(l) != 1 || l[0].Mode != ModeRW {
		t.Fatalf("应升级为单条 rw，实际 %+v", l)
	}
}

func TestRevoke(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	a := mustMkdir(t, filepath.Join(base, "a"))
	b := mustMkdir(t, filepath.Join(base, "b"))
	if _, err := s.Allow(a, "作业区"); err != nil {
		t.Fatalf("Allow a: %v", err)
	}
	if _, err := s.Allow(b, "发票目录"); err != nil {
		t.Fatalf("Allow b: %v", err)
	}

	if err := s.Revoke(a); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	wantOutside(t, "撤销后不可用", s.Check(filepath.Join(a, "x"), ModeRead))
	wantOK(t, "其它授权不受影响", s.Check(filepath.Join(b, "x"), ModeRead))
	if l := s.List(); len(l) != 1 || l[0].Resolved != b {
		t.Fatalf("撤销后清单异常: %+v", l)
	}

	if err := s.Revoke(a); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("重复撤销应返回 ErrNoGrant，实际 %v", err)
	}
	if err := s.Revoke(""); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("空路径应返回 ErrInvalidPath，实际 %v", err)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	requireWindows(t)
	path := filepath.Join(dir, "grants.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	root := mustMkdir(t, filepath.Join(newRoot(t), "work"))
	if _, err := s.Allow(root, "作业区"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var doc document
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("落盘内容不是合法 JSON: %v\n%s", err, b)
	}
	if doc.Version != currentVersion || len(doc.Grants) != 1 || doc.Grants[0].Source != "作业区" {
		t.Fatalf("落盘内容异常: %+v", doc)
	}
	// 临时文件不应残留。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	wantOK(t, "重启后仍授权", s2.Check(filepath.Join(root, "x.xlsx"), ModeRW))
	if len(s2.List()) != 1 {
		t.Fatalf("重启后清单条数异常: %d", len(s2.List()))
	}
}

func TestCorruptFileRecovery(t *testing.T) {
	dir := t.TempDir()
	requireWindows(t)
	path := filepath.Join(dir, "grants.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"grants":[{"root":`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("损坏文件不应让 Open 报错: %v", err)
	}
	if l := s.List(); len(l) != 0 {
		t.Fatalf("损坏文件应从空清单开始，实际 %+v", l)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("损坏文件应被重命名为 .bak: %v", err)
	}
	// 损坏 + 空文件 / 只剩空白，也都能开。
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if s, err := Open(path); err != nil || len(s.List()) != 0 {
		t.Fatalf("空白文件应视为空清单，实际 err=%v", err)
	}

	// 损坏恢复后仍可正常写入并再次读取。
	root := mustMkdir(t, filepath.Join(newRoot(t), "work"))
	if _, err := s.Allow(root, "作业区"); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	wantOK(t, "恢复后可用", s2.Check(root, ModeRW))
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatalf("空存储路径应报错")
	}
}

func TestAllowRejectsNonDirectory(t *testing.T) {
	s := newStore(t)
	base := newRoot(t)
	file := filepath.Join(base, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := s.Allow(file, "作业区"); err == nil {
		t.Fatalf("授权根是文件时应报错")
	}
	if _, err := s.Allow(filepath.Join(base, "a\x01b"), "作业区"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("非法路径应返回 ErrInvalidPath，实际 %v", err)
	}
}
