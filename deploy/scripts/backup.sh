#!/usr/bin/env bash
# =====================================================================
# backup.sh — 生产 SQLite 快照(gateway-v2.db)。
#
#   优先 `sqlite3 .backup`(在线一致快照,WAL 安全);服务器无 sqlite3 时退化为
#   「短暂停 gateway 容器 → 复制 → 重启」(几秒停机,建议深夜 cron)。
#   快照落在 data/backups/,只保留最近 KEEP 份。
#
#   停机分支的正确性:容器 SIGTERM 优雅退出会 checkpoint WAL 并落盘(main.go 里
#   st.Close()),故「停容器后拷 .db」是自洽快照。仍**一并拷 -wal/-shm** —— 若停机
#   因故没落盘完全,带上这两个文件比丢掉已提交事务更接近真相(WAL 下数据可能只在
#   -wal 里)。产物立即校验非空,空快照宁可报错也不留。
#
# 用法:
#   目标主机本地:    REMOTE_DIR=/opt/ai-gateway-v2 bash backup.sh
#   从本地经 ssh 触发:ssh <host> 'REMOTE_DIR=/opt/ai-gateway-v2 bash -s' < deploy/scripts/backup.sh
#   每日 cron(在目标主机):0 4 * * * REMOTE_DIR=/opt/ai-gateway-v2 bash /opt/ai-gateway-v2/backup.sh >> $HOME/gw-backup.log 2>&1
# =====================================================================
set -euo pipefail

REMOTE_DIR="${REMOTE_DIR:-$HOME/ai-gateway}"
DB="${DB:-$REMOTE_DIR/data/gateway-v2.db}"
DEST="${DEST:-$REMOTE_DIR/data/backups}"
KEEP="${KEEP:-7}"
SERVICE="${GW_SERVICE:-gateway}"

[ -f "$DB" ] || { echo "DB 不存在: $DB" >&2; exit 1; }
mkdir -p "$DEST"
# 时间戳带纳秒(seq 取 %N),避免同一秒内两次备份撞名 —— 撞名会让后写的覆盖先写的,
# 且 make backup 与 upgrade.sh 的「升级前备份」可能在同一秒前后脚触发。
stamp="$(date +%Y%m%d-%H%M%S)-$(printf '%06d' "$(( $(date +%N) / 1000 ))")"
out="$DEST/gateway-v2-$stamp.db"

# 复制 DB 及其 WAL 伴生文件(存在才拷;.db 永远拷)。
copy_db_files() {
  cp -p "$DB" "$out"
  [[ -f "$DB-wal" ]] && cp -p "$DB-wal" "$out-wal" || true
  [[ -f "$DB-shm" ]] && cp -p "$DB-shm" "$out-shm" || true
}

# 停容器分支的恢复动作:无论正常结束、报错还是被 kill(SIGINT/TERM/HUP),都要把网关拉回来。
RESTORE_NEEDED=0
restore_gateway() {
  [[ "$RESTORE_NEEDED" == 1 ]] || return 0
  RESTORE_NEEDED=0
  ( cd "$REMOTE_DIR" && docker compose start "$SERVICE" >/dev/null 2>&1 ) || true
}
# EXIT 抓正常退出与显式 exit;INT/TERM/HUP 抓被中断 —— trap 里再 exit 会重入 EXIT,
# 故 restore_gateway 自带幂等(先清标志)。缺这几条信号 trap 是原实现的真隐患:
# 复制途中被 ssh 断线/超时打断,会把容器永远留在 stopped。
trap restore_gateway EXIT
trap 'exit 130' INT TERM HUP

if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "$DB" ".backup '$out'"
  echo "在线快照完成: $out"
else
  echo "⚠ 服务器无 sqlite3:短暂停容器期间网关**不可用**(数秒),复制完自动拉起…"
  RESTORE_NEEDED=1
  ( cd "$REMOTE_DIR" && docker compose stop "$SERVICE" >/dev/null )
  copy_db_files
  ( cd "$REMOTE_DIR" && docker compose start "$SERVICE" >/dev/null )
  RESTORE_NEEDED=0
  echo "停容器复制完成: $out"
fi

# 产物校验:空文件 / 目录不存在都判失败(空快照比没快照更危险 —— 回滚时才发现)。
if [[ ! -s "$out" ]]; then
  echo "✗ 快照产物为空或缺失: $out" >&2
  exit 1
fi

# 只保留最近 KEEP 份(按修改时间;.db 与伴生 -wal/-shm 同组丢弃)
ls -1t "$DEST"/gateway-v2-*.db 2>/dev/null | tail -n +"$((KEEP + 1))" | while read -r f; do
  rm -f "$f" "$f-wal" "$f-shm"
done
echo "现有快照 $(ls -1 "$DEST"/gateway-v2-*.db 2>/dev/null | wc -l) 份(保留上限 $KEEP):$out"
