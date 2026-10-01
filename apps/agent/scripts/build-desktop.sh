#!/usr/bin/env bash
# 打包 gridwright Windows 桌面客户端（Tauri 安装包）
#
# 产物：apps/desktop/src-tauri/target/release/bundle/nsis/*.exe
#       —— 双击安装、有窗口、像正常软件的安装包。
#
# 它做什么：
#   1) 编译前端（工作台界面）
#   2) 把前端产物同步进 Go 源码树（sidecar 用 go:embed 把界面编进二进制）
#   3) 编译 Go 引擎，放到 Tauri 的 sidecar 目录（应用启动时随包拉起）
#   4) 重画安装界面品牌图（颜色跟着品牌令牌走，避免手工图悄悄过期）
#   5) cargo tauri build → NSIS 安装包（把上面的引擎一起装进去）
#
# 顺序要紧：界面必须在**编 Go 之前**就位。倒过来的话 sidecar 内嵌的是上一轮的界面。
#
# 用法：
#   bash apps/agent/scripts/build-desktop.sh
set -euo pipefail

# Go 用 1.21 工具链（保持与本仓库既有产物一致；Win10 上任何版本都能跑）
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.21.13}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
AGENT="$ROOT/apps/agent"
DESKTOP="$ROOT/apps/desktop"
SIDECAR_DIR="$DESKTOP/src-tauri/binaries"
# Tauri 按「名字-目标三元组」找 sidecar，名字必须与 tauri.conf.json 的
# externalBin: ["binaries/gridwright"] 对应。
TARGET_TRIPLE="x86_64-pc-windows-msvc"

echo "==> 1/5 编译前端（界面）"
cd "$DESKTOP"
if [ ! -d node_modules ]; then
  echo "    npm install ..."
  npm install --silent
fi
npm run build
if [ ! -f "$DESKTOP/dist/index.html" ]; then
  echo "!! 前端没有产出 dist/index.html，中止（再往下就会把旧界面编进 sidecar）" >&2
  exit 1
fi

echo "==> 2/5 同步前端产物到 Go 源码树（sidecar 内嵌界面走这里）"
# 为什么必须有这一步：sidecar 用 go:embed 把界面**编进二进制**（internal/webui/dist）。
# 不刷这一步，桌面版自己看不出来（它的界面走 Tauri 前端协议，读 apps/desktop/dist），
# 但 sidecar 内嵌的是上一次刷过的界面 —— 用户直接用浏览器开 127.0.0.1:7700 时，
# 看到的是一个与当前版本不符的旧界面。build-single.sh 一直在刷，桌面脚本漏了，
# 于是同一份 Go 代码存在两个界面版本。
rm -rf "$AGENT/internal/webui/dist"
mkdir -p "$AGENT/internal/webui/dist"
cp -r "$DESKTOP/dist/." "$AGENT/internal/webui/dist/"
# go:embed 要求目录非空；.gitkeep 保证“清空后忘了重编”也不会编译失败。
touch "$AGENT/internal/webui/dist/.gitkeep"
ls -1 "$AGENT/internal/webui/dist/assets" | head -5

echo "==> 3/5 编译 Go 引擎 → sidecar"
cd "$AGENT"
mkdir -p "$SIDECAR_DIR"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" \
  -o "$SIDECAR_DIR/gridwright-$TARGET_TRIPLE.exe" ./cmd/gridwright
ls -lh "$SIDECAR_DIR/gridwright-$TARGET_TRIPLE.exe"

echo "==> 4/5 应用图标与安装界面品牌图"
# 应用图标：**只有缺失时才画**。
#
# 为什么不再无条件重画：仓库里已提交的图标是当前设计（人改过、经评审），
# 而这个生成脚本的图源较旧——无条件重画会把它**静静覆盖回旧设计**（实测踩到，
# 用户装完发现图标变回去了）。图标不是每次都该变的东西。
if [ -f "$DESKTOP/src-tauri/icons/icon.ico" ]; then
  echo "    图标已存在，沿用仓库里已提交的那份（不重画）"
elif command -v python >/dev/null 2>&1 && python -c "import PIL" >/dev/null 2>&1; then
  echo "    图标缺失，用脚本生成"
  python "$DESKTOP/scripts/gen_app_icon.py"
else
  echo "    !! 图标缺失且没有 python + Pillow，构建可能失败" >&2
fi

# 安装界面品牌图：颜色跟着品牌令牌走，重画才能跟上调色。
if command -v python >/dev/null 2>&1 && python -c "import PIL" >/dev/null 2>&1; then
  python "$DESKTOP/scripts/gen_installer_art.py"
else
  echo "    跳过（需要 python + Pillow）；沿用仓库里已提交的图"
fi

echo "==> 5/5 cargo tauri build（NSIS 安装包）"
cd "$DESKTOP"
npx tauri build --bundles nsis

echo
echo "==> 完成，产物："
find "$DESKTOP/src-tauri/target/release/bundle" -name "*.exe" -o -name "*.msi" 2>/dev/null | while read -r f; do
  printf '  %s  (%s)\n' "$f" "$(du -h "$f" | cut -f1)"
done

cat <<'EOF'

安装包用法（在 Win10/11 上）：
  1) 双击安装包 → 一路下一步（无需管理员，默认装到用户目录）
  2) 开始菜单/桌面会有 gridwright
  3) 双击打开 → 应用会自动拉起内置引擎（不用另装任何东西）
  4) 首次会让你选一个装表的文件夹

说明：
  - 引擎随安装包一起装，用户不需要单独准备
  - 配置放在 %APPDATA%\gridwright\，工作区是你自己选的文件夹——
    卸载/升级都不会动它们
  - 离线可用：看表/体检/联动图/账目不需要联网；改表要配模型（设置页里填）
EOF
