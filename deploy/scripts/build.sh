#!/usr/bin/env bash
# =====================================================================
# build.sh — 本地构建生产镜像:前端构建 + 网关交叉编译 + docker build(纯组装)。
#
#   产物镜像:$IMAGE:$VER 与 $IMAGE:latest(可用于 compose 回滚)。
#   VER 由 deploy.sh 经环境传入;单独运行则取 git describe。CI(release.yml)
#   复用本脚本,只需传 IMAGE=ghcr.io/<owner>/<repo>。
#
# 用法:build.sh                    → 本地短名 ai-gateway,版本取自 git describe
#       VER=v0.0.1 build.sh         → 指定版本
#       IMAGE=ghcr.io/o/r build.sh  → 改用全名(CI 用)
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
VER="${VER:-$(cd "$REPO" && git describe --tags --always --dirty)}"
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
docker build --build-arg VERSION="$VER" -t "$IMAGE:$VER" -t "$IMAGE:latest" "$CTX"

echo "镜像就绪:$IMAGE:$VER"
