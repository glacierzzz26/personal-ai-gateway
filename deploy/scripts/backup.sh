#!/usr/bin/env bash
# =====================================================================
# backup.sh — 生产 PostgreSQL 快照(pg_dump 自定义格式)。
#
#   经 compose 在 db 容器内跑 `pg_dump -Fc`(自定义格式,供 pg_restore),stdout 落宿主;
#   **在线一致快照,无需停容器**(PostgreSQL 的多版本并发控制保证 dump 期间一致性)。
#   快照落在 data/backups/,只保留最近 KEEP 份。产物立即校验非空 —— 空快照宁可报错
#   也不留(回滚时才发现空快照比没快照更危险)。
#
#   依赖:宿主只需 docker + compose v2(或已装 pg_dump 且能连库)。不依赖 sqlite3。
#
# 使用/环境变量:
#   目标主机本地:    REMOTE_DIR=/opt/ai-gateway-v2 bash backup.sh
#   从本地经 ssh 触发:ssh <host> 'REMOTE_DIR=/opt/ai-gateway-v2 bash -s' < deploy/scripts/backup.sh
#   每日 cron(在目标主机):0 4 * * * REMOTE_DIR=/opt/ai-gateway-v2 bash /opt/ai-gateway-v2/backup.sh >> $HOME/gw-backup.log 2>&1
#   GW_DB_SERVICE(db 容器服务名,默认 db)/ GW_PG_USER(默认 gw)/ GW_PG_DB(默认 gateway)
# =====================================================================
set -euo pipefail

REMOTE_DIR="${REMOTE_DIR:-$HOME/ai-gateway}"
DEST="${DEST:-$REMOTE_DIR/data/backups}"
KEEP="${KEEP:-7}"
DB_SERVICE="${GW_DB_SERVICE:-db}"
PG_USER="${GW_PG_USER:-gw}"
PG_DB="${GW_PG_DB:-gateway}"

mkdir -p "$DEST"
# 时间戳带纳秒(取 %N),避免同一秒内两次备份撞名 —— 撞名会让后写的覆盖先写的,
# 且 make backup 与 upgrade.sh 的「升级前备份」可能在同一秒前后脚触发。
stamp="$(date +%Y%m%d-%H%M%S)-$(printf '%06d' "$(( $(date +%N) / 1000 ))")"
out="$DEST/gateway-$stamp.dump"

# 经 compose 在 db 容器内 dump;若失败 stdout 可能半截,故先写临时文件再原子改名,
# 且失败即删临时文件(绝不把半截 dump 当成一份可用快照)。
tmp="$out.part"
trap 'rm -f "$tmp"' EXIT
( cd "$REMOTE_DIR" && docker compose exec -T "$DB_SERVICE" \
    pg_dump -U "$PG_USER" -Fc "$PG_DB" ) > "$tmp"
[ -s "$tmp" ] || { echo "✗ 快照产物为空:$tmp(dump 失败?)" >&2; exit 1; }
mv "$tmp" "$out"
trap - EXIT

# 只保留最近 KEEP 份(按修改时间)。
ls -1t "$DEST"/gateway-*.dump 2>/dev/null | tail -n +"$((KEEP + 1))" | while read -r f; do
  rm -f "$f"
done
echo "快照完成:$out"
echo "现有快照 $(ls -1 "$DEST"/gateway-*.dump 2>/dev/null | wc -l) 份(保留上限 $KEEP)"
