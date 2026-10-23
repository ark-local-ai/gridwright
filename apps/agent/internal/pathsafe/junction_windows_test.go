//go:build windows

package pathsafe

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 目录联接（junction）的重解析点常量。
//
// 为什么用 syscall 而不是 `cmd /c mklink /J`：
// 测试里不该为了造一个重解析点去拼 shell 命令（参数来自路径，
// 拼接方式一旦被改就变成命令注入面）；直接下发 FSCTL 更确定。
const (
	ioReparseTagMountPoint = 0xA0000003
	fsctlSetReparsePoint   = 0x000900A4
)

// makeJunction 在 link 处创建一个指向 target 的目录联接（不需要管理员权限）。
// link 不能预先存在。
func makeJunction(link, target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(link, 0o755); err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("打开目录: %w", err)
	}
	defer windows.CloseHandle(h)

	// 替换名用 NT 对象路径写法；打印名给普通 API 看。
	subst, err := windows.UTF16FromString(`\??\` + abs)
	if err != nil {
		return err
	}
	printName, err := windows.UTF16FromString(abs)
	if err != nil {
		return err
	}
	subBytes := (len(subst) - 1) * 2
	printBytes := (len(printName) - 1) * 2

	// REPARSE_DATA_BUFFER：8 字节头 + MountPointReparseBuffer 的 8 字节固定部分
	// （4 个 USHORT，注意**没有** Flags——那是符号链接才有的字段）+ 路径缓冲。
	//
	// 与 junction.exe / mklink 的实际字节布局对齐很重要：两个名字各自以 NUL
	// 结尾（长度字段都不含 NUL），且 PrintNameOffset 必须越过那个 NUL——
	// 否则内核直接回 ERROR_INVALID_REPARSE_DATA。
	const header = 8
	const mpFixed = 8
	printOff := subBytes + 2
	pathSpace := subBytes + 2 + printBytes + 2
	buf := make([]byte, header+mpFixed+pathSpace)
	put16 := func(off int, v uint16) { *(*uint16)(unsafe.Pointer(&buf[off])) = v }
	put32 := func(off int, v uint32) { *(*uint32)(unsafe.Pointer(&buf[off])) = v }

	put32(0, ioReparseTagMountPoint)
	put16(4, uint16(mpFixed+pathSpace)) // ReparseDataLength（不含 8 字节头）
	put16(6, 0)                         // Reserved
	put16(8, 0)                         // SubstituteNameOffset
	put16(10, uint16(subBytes))         // SubstituteNameLength
	put16(12, uint16(printOff))         // PrintNameOffset
	put16(14, uint16(printBytes))       // PrintNameLength

	n := header + mpFixed
	for _, u := range subst[:len(subst)-1] {
		put16(n, u)
		n += 2
	}
	n += 2 // 替换名后的 NUL
	for _, u := range printName[:len(printName)-1] {
		put16(n, u)
		n += 2
	}

	var returned uint32
	if err := windows.DeviceIoControl(h, fsctlSetReparsePoint,
		&buf[0], uint32(len(buf)), nil, 0, &returned, nil); err != nil {
		return fmt.Errorf("设置重解析点: %w", err)
	}
	return nil
}
