#!/usr/bin/env bash
# =====================================================================
# notify.sh — 飞书自定义机器人 webhook 告警(HA 决策 D4:无 SDK、POST JSON)。
#
#   notify.sh "标题" ["正文"] [级别]     级别 ∈ info|warn|error(默认 warn)
#
#   发送内容为**飞书交互卡片**(msg_type=interactive),观感优于纯文本:
#     · 顶部标题栏按级别着色(info 绿 / warn 橙 / error 红),含图标 + 固定"AI 网关 · HA 告警"抬头;
#     · 正文首行为加粗**事件标题**(自动去掉冗余的"HA "前缀),其下为正文(markdown);
#     · 页脚小字:来源 ai-gateway · 主机 · 时间。
#
#   设计要点:**告警失败绝不阻断主流程**。HA controller 在故障切换路径上调用本脚本,
#   若 webhook 未配/超时/回非 2xx,只打印警告并以 0 退出 —— 告警是旁路,不能因它让切换失败。
#
# 环境变量:
#   FEISHU_WEBHOOK    机器人 webhook(缺省从 ha.env 读);未设 → 跳过发送(仍打印)
#   FEISHU_SECRET     机器人**加签**密钥(可选)。设了则按飞书「签名校验」加 timestamp+sign;
#                     没设则发明文。⚠️ 若机器人开了加签而这里没设,飞书回 code 19021
#                     (sign match fail)会被拒 —— 自检时务必确认发得出去。
#   NOTIFY_HOST_LABEL 页脚里的主机名(可选;缺省取 `hostname -s`)。
#   NOTIFY_TIMEOUT    curl 超时秒(默认 10)
# =====================================================================
set -uo pipefail

TITLE="${1:-AI 网关告警}"
BODY="${2:-}"
LEVEL="${3:-warn}"
WEBHOOK="${FEISHU_WEBHOOK:-}"
SECRET="${FEISHU_SECRET:-}"
HOST_LABEL="${NOTIFY_HOST_LABEL:-$(hostname -s 2>/dev/null || echo '')}"
TIMEOUT="${NOTIFY_TIMEOUT:-10}"

# 图标与标题栏颜色按级别。
case "$LEVEL" in
  info)  ICON="🟢"; COLOR="green" ;;
  error) ICON="🔴"; COLOR="red" ;;
  *)     ICON="🟠"; COLOR="orange" ;;
esac

if [ -z "$WEBHOOK" ]; then
  printf '[notify] 未设 FEISHU_WEBHOOK,跳过发送:%s | %s\n' "$TITLE" "$BODY" >&2
  exit 0
fi

# 用 python3 构造交互卡片 JSON(正确转义引号/换行,避免手拼出错)。
# 设了 FEISHU_SECRET 时按飞书签名算法在报文顶层追加 timestamp + sign(加签机器人必需)。
payload="$(TITLE="$TITLE" BODY="$BODY" ICON="$ICON" COLOR="$COLOR" \
           SECRET="$SECRET" HOST_LABEL="$HOST_LABEL" \
python3 - <<'PY'
import json, os, re, time, hmac, hashlib, base64

title   = os.environ["TITLE"]
body    = os.environ["BODY"]
icon    = os.environ["ICON"]
color   = os.environ["COLOR"]
secret  = os.environ.get("SECRET") or ""
host    = os.environ.get("HOST_LABEL") or ""
stamp   = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime())

# 卡片抬头固定,事件标题进正文首行;去掉冗余的 "HA " 前缀(抬头已含 HA)。
event = re.sub(r'^HA[ \t]+', '', title).strip() or title

elements = []
md = f"**{event}**"
if body:
    md += "\n" + body
elements.append({"tag": "div", "text": {"tag": "lark_md", "content": md}})
elements.append({"tag": "hr"})
elements.append({"tag": "note", "elements": [
    {"tag": "plain_text", "content": " · ".join(x for x in ("ai-gateway", host, stamp) if x)},
]})

msg = {
    "msg_type": "interactive",
    "card": {
        "config": {"wide_screen_mode": True},
        "header": {"template": color,
                   "title": {"tag": "plain_text", "content": f"{icon} AI 网关 · HA 告警"}},
        "elements": elements,
    },
}
if secret:
    ts = str(int(time.time()))
    # 飞书加签:string_to_sign = "{timestamp}\n{secret}" 作 HMAC-SHA256 的 key,消息体为空。
    sign = base64.b64encode(
        hmac.new(f"{ts}\n{secret}".encode("utf-8"), digestmod=hashlib.sha256).digest()
    ).decode("utf-8")
    msg["timestamp"] = ts
    msg["sign"] = sign
print(json.dumps(msg))
PY
)" || { echo "[notify] 构造 payload 失败,跳过" >&2; exit 0; }

# 飞书对「收到但拒收」(webhook 失效/被限频/卡片非法/加签失败)也回 HTTP 200,
# 故不能只看 curl 退出码 —— 必须查响应体里的 "code":0。失败只告警不阻断(仍 exit 0)。
resp="$(curl -fsS -m "$TIMEOUT" -H 'Content-Type: application/json' -d "$payload" "$WEBHOOK" 2>/dev/null)" || resp=""
if printf '%s' "$resp" | grep -q '"code":0'; then
  echo "[notify] 已发送:$TITLE"
else
  echo "[notify] 发送失败(不影响主流程):$TITLE ${resp:+| 响应:$resp}" >&2
fi
exit 0
