#!/usr/bin/env bash
# =====================================================================
# backup.sh — 生产 SQLite 快照(gateway-v2.db)。
#
#   优先 `sqlite3 .backup`(在线一致快照,WAL 安全);服务器无 sqlite3 时退化为
#   「短暂停 gateway 容器 → 复制 → 重启」(几秒停机,建议深夜 cron)。
#   快照落在 data/backups/,只保留最近 KEEP 份。
#
# 用法:
#   目标主机本地:    REMOTE_DIR=~/ai-gateway bash backup.sh
#   从本地经 ssh 触发:ssh rguo@192.168.0.202 'REMOTE_DIR=~/ai-gateway bash -s' < deploy/scripts/backup.sh
#   每日 cron(在目标主机):0 4 * * * REMOTE_DIR=$HOME/ai-gateway bash $HOME/ai-gateway/backup.sh >> $HOME/gw-backup.log 2>&1
# =====================================================================
set -euo pipefail

REMOTE_DIR="${REMOTE_DIR:-$HOME/ai-gateway}"
DB="${DB:-$REMOTE_DIR/data/gateway-v2.db}"
DEST="${DEST:-$REMOTE_DIR/data/backups}"
KEEP="${KEEP:-7}"

[ -f "$DB" ] || { echo "DB 不存在: $DB" >&2; exit 1; }
mkdir -p "$DEST"
out="$DEST/gateway-v2-$(date +%Y%m%d-%H%M%S).db"

if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "$DB" ".backup '$out'"
  echo "在线快照完成: $out"
else
  echo "服务器无 sqlite3,短暂停容器后复制(gateway 重启前不可用)…"
  cd "$REMOTE_DIR"
  docker compose stop gateway >/dev/null
  trap 'cd "$REMOTE_DIR" && docker compose start gateway >/dev/null' EXIT
  cp "$DB" "$out"
  docker compose start gateway >/dev/null
  trap - EXIT
  echo "停容器复制完成: $out"
fi

# 只保留最近 KEEP 份(按修改时间)
ls -1t "$DEST"/gateway-v2-*.db 2>/dev/null | tail -n +"$((KEEP + 1))" | xargs -r rm -f
echo "现有快照 $(ls -1 "$DEST"/gateway-v2-*.db 2>/dev/null | wc -l) 份(保留上限 $KEEP)"
