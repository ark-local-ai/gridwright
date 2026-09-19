#!/usr/bin/env bash
# 打包 gridwright Windows 桌面客户端（Tauri 安装包）
#
# 产物：apps/desktop/src-tauri/target/release/bundle/nsis/*.exe
#       —— 双击安装、有窗口、像正常软件的安装包。
#
# 它做什么：
#   1) 编译 Go 引擎，放到 Tauri 的 sidecar 目录（应用启动时随包拉起）
#   2) 重画安装界面品牌图（颜色跟着品牌令牌走，避免手工图悄悄过期）
#   3) 编译前端（工作台界面）
#   4) cargo tauri build → NSIS 安装包（把上面的引擎一起装进去）
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

echo "==> 1/4 编译 Go 引擎 → sidecar"
cd "$AGENT"
mkdir -p "$SIDECAR_DIR"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" \
  -o "$SIDECAR_DIR/gridwright-$TARGET_TRIPLE.exe" ./cmd/gridwright
ls -lh "$SIDECAR_DIR/gridwright-$TARGET_TRIPLE.exe"

echo "==> 2/4 重画应用图标与安装界面品牌图"
# 由脚本产出而非手工放图：颜色是品牌令牌的副本，重画才跟得上调色。
# 缺 Python/Pillow 时跳过（已提交的图仍在，安装包照出）。
if command -v python >/dev/null 2>&1 && python -c "import PIL" >/dev/null 2>&1; then
  python "$DESKTOP/scripts/gen_app_icon.py"
  python "$DESKTOP/scripts/gen_installer_art.py"
else
  echo "    跳过（需要 python + Pillow）；沿用仓库里已提交的图"
fi

echo "==> 3/4 编译前端（界面）"
cd "$DESKTOP"
if [ ! -d node_modules ]; then
  echo "    npm install ..."
  npm install --silent
fi
npm run build

echo "==> 4/4 cargo tauri build（NSIS 安装包）"
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
