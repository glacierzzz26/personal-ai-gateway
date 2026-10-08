#!/usr/bin/env bash
# =====================================================================
# notify.sh — 飞书自定义机器人 webhook 告警(HA 决策 D4:无 SDK、POST JSON)。
#
#   notify.sh "标题" ["正文"] [级别]     级别 ∈ info|warn|error(默认 warn)
#
#   设计要点:**告警失败绝不阻断主流程**。HA controller 在故障切换路径上调用本脚本,
#   若 webhook 未配/超时/回非 2xx,只打印警告并以 0 退出 —— 告警是旁路,不能因它让切换失败。
#
# 环境变量:
#   FEISHU_WEBHOOK   机器人 webhook(缺省从 ha.env 读);未设 → 跳过发送(仍打印)
#   NOTIFY_TIMEOUT   curl 超时秒(默认 10)
# =====================================================================
set -uo pipefail

TITLE="${1:-AI 网关告警}"
BODY="${2:-}"
LEVEL="${3:-warn}"
WEBHOOK="${FEISHU_WEBHOOK:-}"
TIMEOUT="${NOTIFY_TIMEOUT:-10}"

case "$LEVEL" in
  info)  ICON="🟢" ;;
  error) ICON="🔴" ;;
  *)     ICON="🟠" ;;
esac

if [ -z "$WEBHOOK" ]; then
  printf '[notify] 未设 FEISHU_WEBHOOK,跳过发送:%s | %s\n' "$TITLE" "$BODY" >&2
  exit 0
fi

# 用 python3 构造 JSON(正确转义标题/正文里的引号与换行,避免手拼 JSON 出错)。
payload="$(TITLE="$TITLE" BODY="$BODY" ICON="$ICON" python3 - <<'PY'
import json, os
text = f"{os.environ['ICON']} {os.environ['TITLE']}"
if os.environ.get("BODY"):
    text += "\n" + os.environ["BODY"]
print(json.dumps({"msg_type": "text", "content": {"text": text}}))
PY
)" || { echo "[notify] 构造 payload 失败,跳过" >&2; exit 0; }

if curl -fsS -m "$TIMEOUT" -H 'Content-Type: application/json' -d "$payload" "$WEBHOOK" >/dev/null 2>&1; then
  echo "[notify] 已发送:$TITLE"
else
  echo "[notify] 发送失败(不影响主流程):$TITLE" >&2
fi
exit 0
