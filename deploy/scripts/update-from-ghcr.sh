#!/usr/bin/env bash
# =====================================================================
# update-from-ghcr.sh — lab 侧自更新:拉 ghcr :latest,digest 变了才重建。
#
#   lab 连不通 github.com、只能到 ghcr.io,所以云端 runner 无法主动推;这里
#   反过来由 lab 轮询 ghcr 的 :latest digest。没变则零成本退出,不变更任何东西。
#
#   幂等、可重复执行;任何一步失败都不破坏当前在跑的版本(先 pull 成功、拿到
#   新 digest 之后才动 compose)。
#
# 环境变量(缺省值即生产值):
#   GW_DIR        compose 工程目录          默认 $HOME/ai-gateway
#   GW_IMAGE      ghcr 全名                 默认 ghcr.io/glacierzzz26/personal-ai-gateway
#   GW_REF        ghcr tag                  默认 latest
#   GW_SERVICE    compose 服务名            默认 gateway
#   GW_COMPOSE    compose 命令              默认 "docker compose"
#
# 手动跑一次:bash deploy/scripts/update-from-ghcr.sh
# 退出码:0 = 已更新 / 无更新;1 = 失败(保持旧版本)
# =====================================================================
set -euo pipefail

GW_DIR="${GW_DIR:-$HOME/ai-gateway}"
GW_IMAGE="${GW_IMAGE:-ghcr.io/glacierzzz26/personal-ai-gateway}"
GW_REF="${GW_REF:-latest}"
GW_SERVICE="${GW_SERVICE:-gateway}"
GW_COMPOSE="${GW_COMPOSE:-docker compose}"

COMPOSE_FILE="$GW_DIR/docker-compose.yml"
ENV_FILE="$GW_DIR/.env"
STATE_FILE="$GW_DIR/.deployed-digest"   # 记录上次成功部署的 :latest digest

log() { printf '[gw-updater] %s\n' "$*"; }

[ -f "$COMPOSE_FILE" ] || { log "找不到 $COMPOSE_FILE,跳过"; exit 1; }

# flock:与手动 deploy.sh / 并发 timer 互斥(锁在 /tmp,不写工程目录)。
exec 9>/tmp/gw-updater.lock
flock -n 9 || { log "已有 updater 在跑,跳过本轮"; exit 0; }

# 拉 :latest,拿它的 digest。匿名拉取(镜像 public,lab 无需凭证)。
log "拉取 $GW_IMAGE:$GW_REF ..."
if ! docker pull -q "$GW_IMAGE:$GW_REF" >/dev/null; then
  log "docker pull 失败(网络抖动?),保持当前版本,下轮再试"
  exit 1
fi

NEW_DIGEST="$(docker image inspect --format '{{index .RepoDigests 0}}' "$GW_IMAGE:$GW_REF" 2>/dev/null || true)"
[ -n "$NEW_DIGEST" ] || { log "拿不到拉取后的 digest,放弃"; exit 1; }

OLD_DIGEST="$(cat "$STATE_FILE" 2>/dev/null || true)"
if [ "$NEW_DIGEST" = "$OLD_DIGEST" ]; then
  log "digest 未变(${NEW_DIGEST##*@}),无操作"
  exit 0
fi

# 用 CI 注入的 version 标签(短 sha,与 /healthz、git describe 同口径)做 tag;
# 取不到就退回 digest 短值。无论哪种,都补一个本地 tag 供 compose GW_IMAGE_TAG 引用。
IMAGE_TAG="$(docker image inspect \
  --format '{{index .Config.Labels "org.opencontainers.image.version"}}' \
  "$GW_IMAGE:$GW_REF" 2>/dev/null || true)"
case "$IMAGE_TAG" in
  ''|'<no value>'|'dev') IMAGE_TAG="sha-${NEW_DIGEST##*@sha256:}"; IMAGE_TAG="${IMAGE_TAG:0:16}" ;;
esac
docker tag "$GW_IMAGE:$GW_REF" "$GW_IMAGE:$IMAGE_TAG"

log "发现新版本 $IMAGE_TAG,重建 $GW_SERVICE ..."

# 更新 .env 的 GW_IMAGE_TAG(不存在则追加),其余行原样保留。
if [ -f "$ENV_FILE" ]; then
  if grep -q '^GW_IMAGE_TAG=' "$ENV_FILE"; then
    sed -i "s|^GW_IMAGE_TAG=.*|GW_IMAGE_TAG=$IMAGE_TAG|" "$ENV_FILE"
  else
    printf '\nGW_IMAGE_TAG=%s\n' "$IMAGE_TAG" >> "$ENV_FILE"
  fi
else
  printf 'GW_IMAGE_TAG=%s\n' "$IMAGE_TAG" > "$ENV_FILE"
fi

cd "$GW_DIR"
# GW_IMAGE 不显式传 → compose 用缺省 ghcr 全名(与 deploy.sh 的本地短名链路区分)。
if $GW_COMPOSE up -d "$GW_SERVICE"; then
  printf '%s\n' "$NEW_DIGEST" > "$STATE_FILE"
  log "已更新到 $IMAGE_TAG"
else
  log "compose up 失败,当前容器未被替换?(.env 已指向 $IMAGE_TAG,可手动回退)"
  exit 1
fi

# 清掉不再使用的镜像层(仅 dangling;旧 tag 镜像保留,便于 GW_IMAGE_TAG 回滚)。
docker image prune -f >/dev/null 2>&1 || true
