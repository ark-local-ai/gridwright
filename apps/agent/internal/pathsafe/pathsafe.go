// Package pathsafe 维护"用户显式授权的根目录"白名单，并据此判定
// 某个路径是否允许被自动监听 / 读取 / 写入。
//
// 背景：gridwright 会替用户盯着作业区、发票目录、系统导出等位置。
// "盯着"意味着程序自己发起读写，不需要用户每次点确认。所以必须有一条硬边界：
// **只有在用户亲手授权过的根目录之内，自动行为才被允许**。这个包就是那条边界。
//
// # 为什么不用 strings.HasPrefix
//
// HasPrefix 是**字符串**关系，而"在目录之内"是**路径**关系，两者不等价：
//   - `C:\data2` 以 `C:\data` 为前缀，但它不是 `C:\data` 的子路径（同级兄弟）；
//   - `C:\DATA\x` 与 `c:\data\x` 是同一个文件，HasPrefix 判为不在其内；
//   - `C:\data\..\Windows` 以 `C:\data` 为前缀，实际却是系统目录；
//   - `C:\data\a::$DATA`、`C:\data\ `（尾部空格被 Windows 静默截断）等写法
//     都能让字符串更长、判定通过，落到别的文件上。
//
// 所以这里的判定是：先规范化，再拆成**卷 + 路径组件**，卷比较（UNC 的
// `\\server\share` 也算一个卷）+ 组件逐个比较，且大小写不敏感（Windows 语义）。
//
// # 为什么不只靠 filepath.EvalSymlinks
//
// EvalSymlinks 只解决"链接指向哪里"，解决不了上面那些字符串/命名层面的把戏，
// 而且它对**不存在的路径**直接报错——但 create 预检恰恰要判断一个还不存在的
// 文件将来落在哪里。于是这里的做法是：
//  1. 自己先做词法规范化（分隔符统一、组件校验、尾部空格/点截断）；
//  2. 再对"最深的已存在祖先"做 EvalSymlinks，把不存在的尾部原样拼回去；
//  3. 最后若结果本身是符号链接，再解析一次。
//
// 两步都不能省：先规范化才能保证 EvalSymlinks 看到的是真实路径，
// 先解析链接才能保证 `C:\data\link\to\outside` 不被当成 `C:\data` 之内。
//
// 包内不依赖 config：存储文件路径由调用方传入，便于测试与嵌入式使用。
package pathsafe

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// 可判定的错误。调用方用 errors.Is 判断，不要比较错误字符串。
var (
	// ErrOutside 表示路径不在任何已授权的根目录内（也用于非法路径：无法证明安全即视为越界）。
	ErrOutside = errors.New("路径不在已授权的根目录内")
	// ErrReadOnly 表示路径在授权范围内，但该根目录只授予了只读权限。
	ErrReadOnly = errors.New("授权为只读，不允许写入")
	// ErrInvalidPath 表示路径本身不可信（空、NUL、设备前缀、盘符相对、ADS 等）。
	ErrInvalidPath = errors.New("非法路径")
	// ErrNoGrant 表示该根目录当前没有被授权（Revoke 时用）。
	ErrNoGrant = errors.New("该根目录未被授权")
)

// Mode 是授权模式。
type Mode string

const (
	// ModeRead 只允许读取 / 监听（不允许创建、修改、删除）。
	ModeRead Mode = "read"
	// ModeRW 允许读写。
	ModeRW Mode = "rw"
)

// Allows 判断当前模式能否满足 want 请求。
func (m Mode) Allows(want Mode) bool {
	if m != ModeRead && m != ModeRW {
		return false
	}
	switch want {
	case ModeRead:
		return true
	case ModeRW:
		return m == ModeRW
	default:
		// 未知请求按最严格的解释处理（失败即拒绝）。
		return false
	}
}

// Grant 是一条用户授权。
type Grant struct {
	// Root 是调用方传入的原始写法（仅用于展示 / 让用户认出自己选了哪个目录）。
	Root string `json:"root"`
	// Source 记录授权来源（例如 "作业区" / "发票目录" / "系统导出"），纯审计用途。
	Source string `json:"source"`
	// Mode 是权限模式。
	Mode Mode `json:"mode"`
	// Added 是首次授权时间（UTC）。重复授权不会刷新它。
	Added time.Time `json:"added"`
	// Resolved 是规范化 + 符号链接解析后的绝对路径，判定实际用的是它。
	Resolved string `json:"resolved"`
}

// currentVersion 是当前存储格式版本。
const currentVersion = 1

// errPath 生成非法路径错误（同时包装 ErrInvalidPath）。
func errPath(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidPath}, args...)...)
}

// firstControl 返回第一个控制字符（C0 区与 DEL）。
func firstControl(s string) (rune, bool) {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return r, true
		}
	}
	return 0, false
}

// isDriveLetter 判断是否为盘符字母。
func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// hasDevicePrefix 判断（已统一为反斜杠的）路径是否带 Windows 设备 / 扩展长度前缀。
// 这类路径绕开 Win32 的常规解析（`\\.\` 直通设备，`\\?\` 关闭规范化），
// 白名单判定在它们上面没有可依赖的语义，一律拒绝。
func hasDevicePrefix(p string) bool {
	low := strings.ToLower(p)
	return strings.HasPrefix(low, `\\?\`) || strings.HasPrefix(low, `\\.\`) ||
		strings.Contains(low, `\\?\`) || strings.Contains(low, `\\.\`)
}

// stripDevicePrefix 去掉**我们自己**（EvalSymlinks 在某些情况下）返回的设备前缀。
// 只用于内部结果，不用于用户输入。
func stripDevicePrefix(p string) string {
	low := strings.ToLower(p)
	if strings.HasPrefix(low, `\\?\unc\`) {
		return `\\` + p[len(`\\?\unc\`):]
	}
	if strings.HasPrefix(low, `\\?\`) {
		return p[len(`\\?\`):]
	}
	if strings.HasPrefix(low, `\\.\`) {
		return p[len(`\\.\`):]
	}
	return p
}
