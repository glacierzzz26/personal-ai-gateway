#!/usr/bin/env bash
# =====================================================================
# notify-feishu.sh — 往「飞书自定义机器人」webhook 发一条文本消息。
#
# 用于节点不可达 / 健康校验失败 / 备份失败 / 发生切换 等事件的告警
# (见 deploy/HA.md §5.8)。零依赖:只用 curl + python3(宿主已有)。
#
# 用法:
#   FEISHU_WEBHOOK='https://open.feishu.cn/open-apis/bot/v2/hook/xxx' \
#     notify-feishu.sh "ai-gateway 告警:aliyun 健康校验失败"
#
# 退出码:0 发送成功;1 未设 webhook 或发送失败。
# =====================================================================
set -euo pipefail

WH="${FEISHU_WEBHOOK:-}"
MSG="${1:-}"
[[ -n "$WH" ]]  || { echo "✗ 未设 FEISHU_WEBHOOK" >&2; exit 1; }
[[ -n "$MSG" ]] || { echo "✗ 用法:notify-feishu.sh \"消息\"" >&2; exit 1; }

body="$(python3 -c 'import json,sys;print(json.dumps({"msg_type":"text","content":{"text":sys.argv[1]}}))' "$MSG")"
resp="$(curl -sS -m 10 -X POST "$WH" -H 'Content-Type: application/json' -d "$body")"
# 飞书成功回 {"code":0,...};非 0 即失败(webhook 失效/被限频等)。
if printf '%s' "$resp" | grep -q '"code":0'; then
  echo "✓ 已发送"
else
  echo "✗ 发送失败:$resp" >&2; exit 1
fi
