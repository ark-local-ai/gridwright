//go:build windows

package proc

import (
	"log"
	"os"

	"golang.org/x/sys/windows"
)

// WatchParent 监视父进程（桌面壳）是否还活着；父进程没了就让当前进程退出。
//
// 为什么需要：引擎是桌面壳用 sidecar 拉起的子进程。若壳被强杀/崩溃，
// 子进程不会被回收，会一直占着 7700 端口，下次打开应用就连不上自己的引擎。
// 这里等父进程句柄收到信号即退出——比"壳退出时清理"更可靠（壳来不及清理也不留孤儿）。
//
// parentPID<=0 表示不监视（例如手动命令行运行）。
func WatchParent(parentPID int, onExit func()) {
	if parentPID <= 0 {
		return
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(parentPID))
	if err != nil {
		log.Printf("无法监视父进程 %d（%v），跳过", parentPID, err)
		return
	}
	go func() {
		defer windows.CloseHandle(h)
		// 等父进程结束（没有超时，永久等待）
		if _, err := windows.WaitForSingleObject(h, windows.INFINITE); err == nil {
			log.Printf("父进程 %d 已退出，引擎随之退出", parentPID)
			if onExit != nil {
				onExit()
			}
			os.Exit(0)
		}
	}()
}
