#!/usr/bin/env bash
# =====================================================================
# local-deploy.sh — 本地构建升级(绕开 CI / ghcr),并临时让开 gw-updater。
#
# 为什么需要它:装了 gw-updater 后,它每 60s 轮询 ghcr :latest 并把 .env 改回
# ghcr 的版本,会把本地推上去的镜像顶掉。本脚本:
#
#   1) 停 gw-updater.timer,并等正在跑的那轮 service 收尾(不与它抢 compose)
#   2) 跑 deploy.sh —— 本地构建 ai-gateway:<ver> → docker save|ssh load → compose up
#   3) 成功后把 updater 的对账基线 .deployed-digest 对齐到 ghcr 当前 :latest,
#      于是 timer 恢复后不会立刻把你顶回 ghcr,而是让这个本地版本一直跑到
#      ghcr 出下一个新版本为止
#   4) 恢复 timer(成功/失败都恢复,由 trap 兜住)
#
# 用法:
#   deploy/scripts/local-deploy.sh [GW_HOST]        默认 rguo@192.168.0.202
#     KEEP_STOPPED=1  部署后不恢复 timer(长时间钉本地版;恢复命令见 README)
#     NO_ALIGN=1      跳过第 3 步对账对齐
#     REMOTE_DIR=…    覆盖远端目录(默认 <远端家目录>/ai-gateway)
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HOST="${1:-${GW_HOST:-rguo@192.168.0.202}}"
KEEP_STOPPED="${KEEP_STOPPED:-0}"
NO_ALIGN="${NO_ALIGN:-0}"
IMAGE_LOCAL="${IMAGE_LOCAL:-ai-gateway}"                                  # deploy.sh 用的本地短名
IMAGE_GHCR="${IMAGE_GHCR:-ghcr.io/glacierzzz26/personal-ai-gateway}"      # updater 用的 ghcr 全名

SSH_BASE=(ssh -o BatchMode=yes -o ConnectTimeout=5)
pssh() { "${SSH_BASE[@]}" "$HOST" "$1"; }

RHOME="$(pssh 'printf %s "$HOME"')"
REMOTE_DIR="${REMOTE_DIR:-$RHOME/ai-gateway}"
STATE_FILE="$REMOTE_DIR/.deployed-digest"

# 单元状态:active / inactive / failed / not-installed。is-active 未激活时返回非零,
# 故用 `|| true` 取出它的 stdout,不当成错误。
unit_state() {
  pssh "if systemctl --user cat '$1' >/dev/null 2>&1; then systemctl --user is-active '$1' 2>/dev/null || true; else echo not-installed; fi"
}

TIMER_STATE="$(unit_state gw-updater.timer)"
WAS_ACTIVE=0
case "$TIMER_STATE" in
  active|activating|reloading) WAS_ACTIVE=1 ;;
  not-installed) echo "==> lab 上未安装 gw-updater,跳过停/恢复,直接本地部署" ;;
  *) echo "==> gw-updater.timer 当前为 $TIMER_STATE,无需停" ;;
esac

restore_timer() {
  [ "$WAS_ACTIVE" = 1 ] || return 0
  [ "$RESTORED" = 1 ] && return 0
  RESTORED=1
  if [ "$KEEP_STOPPED" = 1 ]; then
    echo "==> 按 KEEP_STOPPED=1 保持 timer 停止(恢复:ssh $HOST 'systemctl --user start gw-updater.timer')"
    return 0
  fi
  echo "==> 恢复 gw-updater.timer"
  pssh 'systemctl --user start gw-updater.timer' \
    || echo "  警告:恢复失败,请手动 ssh $HOST 'systemctl --user start gw-updater.timer'" >&2
}
RESTORED=0
trap restore_timer EXIT

if [ "$WAS_ACTIVE" = 1 ]; then
  echo "==> 停 gw-updater.timer"
  pssh 'systemctl --user stop gw-updater.timer'
  # 等正在跑的那轮 service 收尾(它可能正在 pull/compose up)。最多等 60s。
  echo -n "    等待在途的 updater 运行收尾"
  for _ in $(seq 1 30); do
    [ "$(unit_state gw-updater.service)" = active ] || break
    echo -n "."
    sleep 2
  done
  echo
  if [ "$(unit_state gw-updater.service)" = active ]; then
    echo "错误:gw-updater.service 仍在运行;放弃本次部署,以免和它抢 compose" >&2
    exit 1
  fi
fi

echo "==> 本地构建并部署(host=$HOST, dir=$REMOTE_DIR)"
REMOTE_DIR="$REMOTE_DIR" bash "$SCRIPT_DIR/deploy.sh" "$HOST"

# 对齐对账基线:让 updater 恢复后认为「ghcr :latest 已部署过」,从而不把你的
# 本地版本顶回去。只有 ghcr 真出了下一个新 digest,它才会接手。
if [ "$NO_ALIGN" = 0 ] && [ "$TIMER_STATE" != "not-installed" ]; then
  echo "==> 对齐 updater 对账基线(钉住本地版本,直到 ghcr 出下一个新版本)"
  if pssh "docker pull -q '$IMAGE_GHCR:latest' >/dev/null 2>&1 && docker image inspect --format '{{index .RepoDigests 0}}' '$IMAGE_GHCR:latest' > '$STATE_FILE'"; then
    echo "    基线已对齐:$(pssh "cat '$STATE_FILE'" 2>/dev/null || echo '?')"
  else
    echo "    警告:ghcr 不可达或尚无 :latest,基线未对齐;" >&2
    echo "          若下次轮询 ghcr 可达且已有更新的 :latest,会被切回 ghcr(届时手动再部署即可)" >&2
  fi
fi

echo
echo "本地部署完成(镜像 $IMAGE_LOCAL:<ver>)。"
restore_timer
[ "$WAS_ACTIVE" = 1 ] && [ "$KEEP_STOPPED" = 0 ] && echo "gw-updater 已恢复。" || true
exit 0
