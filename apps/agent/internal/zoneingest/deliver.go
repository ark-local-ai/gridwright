package zoneingest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Deliver 把 src 复制进 inboxDir，返回目标绝对路径。
//
// 语义是**不覆盖**：目标同名已存在时返回错误，让调用方决定是跳过还是改名，
// 而不是悄悄盖掉收件箱里还没处理的那一份。实现分两步：
//
//  1. 用 O_CREATE|O_EXCL 先占住目标名——这是并发下唯一可靠的“抢名”手段，
//     在 Windows 上尤其重要（os.Rename 默认会替换已存在文件）；
//  2. 复制到同目录的临时文件，写完 Sync/Close、保留源文件权限与 mtime，
//     再 rename 覆盖刚才占位的空文件。rename 是同卷原子的，所以 inbox
//     里不会出现“只写了一半的表格”。
//
// 任一步失败都会清掉临时文件和占位文件，不留半成品。
func Deliver(src, inboxDir string) (string, error) {
	if strings.TrimSpace(src) == "" {
		return "", errors.New("缺少源文件路径")
	}
	if strings.TrimSpace(inboxDir) == "" {
		return "", errors.New("缺少收件箱目录")
	}

	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return "", fmt.Errorf("解析源文件路径: %w", err)
	}
	info, err := os.Stat(srcAbs)
	if err != nil {
		return "", fmt.Errorf("源文件不可用 %s: %w", srcAbs, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("源路径不是文件: %s", srcAbs)
	}

	inboxAbs, err := filepath.Abs(inboxDir)
	if err != nil {
		return "", fmt.Errorf("解析收件箱路径: %w", err)
	}
	if err := os.MkdirAll(inboxAbs, 0o755); err != nil {
		return "", fmt.Errorf("创建收件箱目录: %w", err)
	}

	dst := filepath.Join(inboxAbs, filepath.Base(srcAbs))
	if samePath(srcAbs, dst) {
		return "", fmt.Errorf("源文件已经就在收件箱里: %s", srcAbs)
	}

	// 第一步：抢名。已存在即失败，绝不覆盖。
	placeholder, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("收件箱里已有同名文件，不覆盖: %s", dst)
		}
		return "", fmt.Errorf("占用目标文件名 %s: %w", dst, err)
	}
	if cerr := placeholder.Close(); cerr != nil {
		_ = os.Remove(dst)
		return "", fmt.Errorf("关闭占位文件: %w", cerr)
	}

	ok := false
	var tmpName string
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
		if !ok {
			// 占位失败/复制失败：把空壳也清掉，别让下一次被它挡住。
			_ = os.Remove(dst)
		}
	}()

	tmp, err := os.CreateTemp(inboxAbs, ".zoneingest-*.tmp")
	if err != nil {
		return "", fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName = tmp.Name()

	in, err := os.Open(srcAbs)
	if err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("打开源文件: %w", err)
	}
	_, copyErr := io.Copy(tmp, in)
	closeInErr := in.Close()
	if copyErr != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("复制 %s: %w", srcAbs, copyErr)
	}
	if closeInErr != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("关闭源文件: %w", closeInErr)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("落盘临时文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("关闭临时文件: %w", err)
	}

	// 保留权限与修改时间，让 inbox 里的文件仍能按原始时间排序。
	_ = os.Chmod(tmpName, info.Mode().Perm())
	if err := os.Chtimes(tmpName, info.ModTime(), info.ModTime()); err != nil {
		return "", fmt.Errorf("保留修改时间: %w", err)
	}

	if err := os.Rename(tmpName, dst); err != nil {
		return "", fmt.Errorf("移动临时文件到 %s: %w", dst, err)
	}
	tmpName = "" // rename 之后临时名已不存在
	ok = true
	return dst, nil
}
