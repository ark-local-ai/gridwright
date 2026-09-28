#!/usr/bin/env bash
# 把官网（apps/frontend 的静态产物）发布到你的服务器。
#
# 为什么要有这个脚本而不是手敲 rsync：手敲的那条命令没人记得住，
# 而"只发静态产物、绝不带上运行期数据"这件事必须每次都对——
# apps/frontend/dist 里可能混着 workspace/（本机跑出来的交付物），
# 一起推上去就是把你的数据传到了公网。
#
# 用法：
#   bash apps/frontend/scripts/deploy-site.sh <user@host> [远端目录]
#
# 例：
#   bash apps/frontend/scripts/deploy-site.sh root@1.2.3.4 /var/www/gridwright
#
# 先干跑看一眼要发什么（不连服务器、不需要私钥）：
#   DRY_RUN=1 bash apps/frontend/scripts/deploy-site.sh root@1.2.3.4
#
# 需要：本机能 ssh 到目标机（密钥或密码）。服务器上的 tar 就够了，不需要 rsync。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
FRONTEND="$ROOT/apps/frontend"

TARGET="${1:-}"
REMOTE_DIR="${2:-}"
# SSH 私钥：默认用 ~/.ssh/gw-server.pem，可用 SSH_KEY 环境变量覆盖。
SSH_KEY="${SSH_KEY:-$HOME/.ssh/gw-server.pem}"
# 干跑：只构建+打包+列举，不连服务器。给了它就不需要私钥。
DRY_RUN="${DRY_RUN:-}"

if [ -z "$TARGET" ]; then
  echo "用法：bash apps/frontend/scripts/deploy-site.sh <user@host> [远端目录]" >&2
  echo "例：  bash apps/frontend/scripts/deploy-site.sh ubuntu@1.2.3.4 /var/www/gridwright" >&2
  echo "" >&2
  echo "私钥默认读 \$HOME/.ssh/gw-server.pem，可覆盖：SSH_KEY=~/.ssh/other.pem ..." >&2
  echo "只想看看会发什么：DRY_RUN=1 bash apps/frontend/scripts/deploy-site.sh <user@host>" >&2
  exit 1
fi
if [ -z "$REMOTE_DIR" ]; then
  echo "未给远端目录，默认 /var/www/gridwright" >&2
  REMOTE_DIR="/var/www/gridwright"
fi
# 干跑不连接，因此不要求私钥存在——否则"先干看一眼"这道门槛就被私钥挡住了。
if [ -z "$DRY_RUN" ] && [ ! -f "$SSH_KEY" ]; then
  echo "找不到私钥：$SSH_KEY（用 SSH_KEY 指定）" >&2
  exit 1
fi

echo "==> 1/4 构建官网"
cd "$FRONTEND"
[ -d node_modules ] || npm install --silent
npm run build

echo "==> 2/4 校验产物"
# 产物里必须能查到当前版本号，否则可能是读到了旧 dist。
# 用原生 node 脚本读而不是 node -p：路径里有中文/反斜杠时，命令行内联的字符串
# 转义会炸（Windows 上实测 MODULE_NOT_FOUND），所以把逻辑写成独立文件。
VER="$(node "$FRONTEND/scripts/read-app-version.mjs")"
if ! grep -rq "$VER" dist 2>/dev/null; then
  echo "构建产物里找不到版本 $VER —— 拒绝发布（可能是旧 dist）" >&2
  exit 1
fi
echo "    产物版本 $VER 已确认"

# 运行期数据一律**拒绝发布**，而不是"顺手排除"。
# 理由：排除是静默的。真让业务文件出现在 dist 里，说明构建输入被污染了
# （多半是 public/ 里放了东西），这时候该停下来查清楚，而不是发一个
# "少了一部分文件"的站点上去——那种问题上线后极难发现。
FORBIDDEN_RE='\.(xlsx|xls|xlsm|csv|tsv|db|sqlite)$'
BAD="$(find dist -type f | grep -E "$FORBIDDEN_RE" || true)"
if [ -n "$BAD" ]; then
  echo "!! dist 里有表格/数据库文件，拒绝发布：" >&2
  printf '%s\n' "$BAD" | sed 's/^/     /' >&2
  echo "   多半是 apps/frontend/public/ 里放了业务文件，把它挪出仓库再重跑。" >&2
  exit 1
fi
if [ -d dist/workspace ]; then
  echo "!! dist/workspace 存在（那是本机跑出来的交付物），拒绝发布。" >&2
  echo "   确认无用后：rm -rf apps/frontend/dist && 重跑本脚本。" >&2
  exit 1
fi
echo "    未发现运行期数据"

echo "==> 3/4 打包"
TMP_TGZ="$(mktemp -t gw-site-XXXXXX).tgz"
# 打**整个 dist**，而不是硬列文件名。硬列那时漏了 favicon.ico——它在 public/ 里
# 一直等着被发布，却从来没进过包；而且以后往 public/ 加文件还得记得回来改这里。
# 排除 sourcemap：体积大，且把源码结构暴露在公网。
tar czf "$TMP_TGZ" --exclude=workspace --exclude='*.map' -C dist .
# 打完再验一遍包内条目。tar 的 --exclude 是模式匹配，不是白名单——
# 不校验就等于假设"排除规则恰好覆盖了所有情况"。
if tar tzf "$TMP_TGZ" | grep -Eq "$FORBIDDEN_RE"; then
  echo "!! 打包结果里仍含表格/数据库文件，中止：" >&2
  tar tzf "$TMP_TGZ" | grep -E "$FORBIDDEN_RE" | sed 's/^/     /' >&2
  rm -f "$TMP_TGZ"
  exit 1
fi
echo "    包大小 $(du -h "$TMP_TGZ" | cut -f1)，$(tar tzf "$TMP_TGZ" | wc -l) 个条目"

if [ -n "$DRY_RUN" ]; then
  echo "==> 4/4 干跑：不上传。包留在 $TMP_TGZ，内容如下"
  tar tzf "$TMP_TGZ" | sed 's/^/    /'
  echo
  echo "（干跑结束。去掉 DRY_RUN 即真正发布到 $TARGET:$REMOTE_DIR）"
  exit 0
fi

echo "==> 4/4 上传到 $TARGET:$REMOTE_DIR"
# 不用 rsync：Windows（Git Bash）上没有这个命令，而本项目的开发机就是 Windows。
# 先打包成 tar 再 scp，两端都不需要额外依赖（服务器上的 tar 一定有）。
REMOTE_TGZ="/tmp/gw-site-$$.tgz"
scp -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$TMP_TGZ" "$TARGET:$REMOTE_TGZ"
rm -f "$TMP_TGZ"

# 在服务器上解包。用 sudo：web 根通常归 root（Caddy 以 root 读），
# 而 tar 不会自己清理旧文件——前端资源名带内容哈希，不清就只会越堆越多。
# 解完把属主改回登录用户，这样以后不带 sudo 也能清理（除非要求 root 属主）。
ssh -i "$SSH_KEY" "$TARGET" "
  set -e
  sudo mkdir -p '$REMOTE_DIR'
  sudo find '$REMOTE_DIR' -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  sudo tar xzf '$REMOTE_TGZ' -C '$REMOTE_DIR'
  sudo chown -R \$(id -u):\$(id -g) '$REMOTE_DIR'
  rm -f '$REMOTE_TGZ'
  echo '    远端已更新，文件数：'\$(find '$REMOTE_DIR' -type f | wc -l)
"

cat <<EOF

发布完成：$TARGET:$REMOTE_DIR

如果 Web 服务器还没指向该目录（示例：Caddy，本机服务器当前用的就是它）：

  :80 {
      root * $REMOTE_DIR
      file_server
      try_files {path} /index.html
  }

改完 reload 即可（不停机）：sudo systemctl reload caddy

EOF
