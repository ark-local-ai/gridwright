#!/usr/bin/env bash
# 跑竞态检测测试（go test -race）。
#
# 为什么需要这个脚本：Go 的 -race 在 Windows/amd64 上要求 cgo，而 cgo 又要一个
# gcc 兼容编译器。这里自动去找 gcc（$CC 优先，其次本机 MSYS2 的 mingw64、
# winget 装的 WinLibs），找到就设好 CC/PATH 再跑。
#
# 本机的 gcc 来自 MSYS2（装在 D:\softWare\msys64）。一次性装法二选一：
#   MSYS2：pacman -S mingw-w64-x86_64-gcc   （→ D:\softWare\msys64\mingw64\bin\gcc.exe）
#   winget：winget install BrechtSanders.WinLibs.POSIX.UCRT
#
# 用法：
#   bash apps/agent/scripts/test-race.sh                      # 全部包
#   bash apps/agent/scripts/test-race.sh ./internal/api/...   # 只跑指定包
set -euo pipefail

cd "$(dirname "$0")/.." # -> apps/agent

find_cc() {
  if [ -n "${CC:-}" ] && command -v "$CC" >/dev/null 2>&1; then
    echo "$CC"
    return
  fi
  local c
  for c in \
    /d/softWare/msys64/mingw64/bin/gcc.exe \
    /c/msys64/mingw64/bin/gcc.exe \
    "$HOME"/AppData/Local/Microsoft/WinGet/Packages/*/mingw64/bin/gcc.exe \
    /c/ProgramData/mingw64/mingw64/bin/gcc.exe \
    "$(command -v gcc 2>/dev/null || true)"; do
    if [ -n "$c" ] && [ -x "$c" ]; then
      echo "$c"
      return
    fi
  done
}

CC_BIN="$(find_cc || true)"
if [ -z "$CC_BIN" ]; then
  echo "找不到 gcc：-race 需要 cgo + gcc。" >&2
  echo "本机是 MSYS2：/d/softWare/msys64/mingw64/bin/gcc.exe" >&2
  echo "装一个：pacman -S mingw-w64-x86_64-gcc（或 winget install BrechtSanders.WinLibs.POSIX.UCRT）" >&2
  echo "或用 CC=/path/to/gcc 指定已有编译器。" >&2
  exit 1
fi

export CC="$CC_BIN"
export PATH="$(dirname "$CC_BIN"):$PATH"
echo "CC=$CC"

# 无参数 = 跑全部包（go test 不带包参数只会跑当前目录，而 scripts/.. 下没有 Go 文件）
if [ "$#" -eq 0 ]; then
  set -- ./...
fi
CGO_ENABLED=1 go test -race -count=1 "$@"
