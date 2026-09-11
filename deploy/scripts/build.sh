#!/usr/bin/env bash
# =====================================================================
# build.sh — 本地构建生产镜像:前端构建 + 网关交叉编译 + docker build(纯组装)。
#
#   产物镜像:ai-gateway:$VER 与 ai-gateway:latest(可用于 compose 回滚)。
#   VER 由 deploy.sh 经环境传入;单独运行则取 git describe。可原样搬进 CI
#   (CI 里把 docker build 换成 buildx + push 到 registry 即可)。
#
# 用法:build.sh            → 版本取自 git describe
#       VER=v0.0.1 build.sh → 指定版本
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
VER="${VER:-$(cd "$REPO" && git describe --tags --always --dirty)}"

echo "==> [1/4] 前端构建(npm run build)"
( cd "$REPO/web-v2" && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null )

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

echo "==> [4/4] docker build → ai-gateway:$VER"
docker build --build-arg VERSION="$VER" -t "ai-gateway:$VER" -t ai-gateway:latest "$CTX"

echo "镜像就绪:ai-gateway:$VER"
