#!/usr/bin/env bash
# =====================================================================
# build.sh — 本地构建生产镜像:前端构建 + 网关交叉编译 + docker build(纯组装)。
#
#   产物镜像:$IMAGE:$VER 与 $IMAGE:latest(可用于 compose 回滚)。
#   VER 默认由 lib-version.sh 算(tag 上为 <tag>-<sha7>,否则 v0.0.0-<sha7>);
#   可由环境覆盖。CI(release.yml)复用本脚本,只需传 IMAGE=ghcr.io/<owner>/<repo>。
#
# 用法:build.sh                    → 本地短名 ai-gateway,版本取自 lib-version.sh
#       VER=v0.0.1 build.sh         → 指定版本
#       IMAGE=ghcr.io/o/r build.sh  → 改用全名(CI 用)
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"

# 版本标识单一来源:口径见 lib-version.sh(gw_version / gw_git_short / gw_schema_head)。
# shellcheck source=lib-version.sh
. "$SCRIPT_DIR/lib-version.sh"

VER="${VER:-$(gw_version "$REPO")}"
GIT_SHORT="${GIT_SHORT:-$(gw_git_short "$REPO")}"
SCHEMA_HEAD="${SCHEMA_HEAD:-$(gw_schema_head "$REPO")}"
IMAGE="${IMAGE:-ai-gateway}"

echo "==> [1/4] 前端构建(npm run build)"
# GETFRONT=0:由调用方(CI)自行完成 npm ci + build,此处只复用已有 dist。
if [ "${GETFRONT:-1}" = "1" ]; then
  ( cd "$REPO/web-v2" && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null )
else
  echo "    GETFRONT=0,跳过 npm;复用现有 web-v2/dist"
  [ -d "$REPO/web-v2/dist" ] || { echo "错误:web-v2/dist 不存在,无法跳过前端构建" >&2; exit 1; }
fi

echo "==> [2/4] 网关交叉编译(linux/amd64, version=$VER)"
tmpbin="$(mktemp /tmp/gw-build-bin.XXXXXX)"
CTX="$(mktemp -d /tmp/gw-build-ctx.XXXXXX)"
trap 'rm -rf "$tmpbin" "$CTX"' EXIT

( cd "$REPO" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
    -ldflags "-X main.version=$VER" -o "$tmpbin" ./cmd/gateway )

echo "==> [3/4] 组装构建上下文"
mkdir -p "$CTX/web-v2"
cp "$REPO/deploy/Dockerfile.gateway" "$CTX/Dockerfile"
cp "$REPO/deploy/config.prod.yaml"   "$CTX/config.yaml"
cp "$tmpbin"                          "$CTX/gateway-bin"
cp -r "$REPO/web-v2/dist"             "$CTX/web-v2/dist"

echo "==> [4/4] docker build → $IMAGE:$VER"
# 三个标识一并烘焙进镜像:APP_VERSION/GIT_SHORT(展示与核对)、SCHEMA_HEAD(升级脚本判降级)。
docker build \
  --build-arg VERSION="$VER" \
  --build-arg GIT_SHORT="$GIT_SHORT" \
  --build-arg SCHEMA_HEAD="$SCHEMA_HEAD" \
  -t "$IMAGE:$VER" -t "$IMAGE:latest" "$CTX"

echo "镜像就绪:$IMAGE:$VER (git=$GIT_SHORT schema=$SCHEMA_HEAD)"
