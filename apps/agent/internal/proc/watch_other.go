//go:build !windows

package proc

// WatchParent 在非 Windows 平台为空实现（桌面壳目前只发 Windows）。
// 其他平台若需要，可用 os.Getppid + 定期探测实现。
func WatchParent(parentPID int, onExit func()) {}
