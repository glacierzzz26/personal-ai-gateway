#!/usr/bin/env bash
# =====================================================================
# claude-fallback.sh — 网关故障时,一键把 Claude Code 切到「备用端点」
#                      (绕过本网关),网关恢复后一键切回。
#
# 为什么需要:
#   本机 Claude Code 经 ~/.claude/settings.json 的 env 指向自建网关
#   (ANTHROPIC_BASE_URL=https://gatewayapi.5home.online)。网关一挂,Claude 也断,
#   连排查工具都没了 —— 这正是本脚本要消灭的「自举死锁」。
#   它用一份预先备好的「备用配置」临时替换 live 配置,让 Claude 复活;
#   网关恢复后一键还原。**它不依赖网关、不依赖 Claude 本身**。
#
# 一次性准备:
#   创建 ~/.claude/settings.fallback.json —— 与 settings.json 同结构,但把
#   ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN(以及 ANTHROPIC_MODEL /
#   ANTHROPIC_DEFAULT_*_MODEL / CLAUDE_CODE_SUBAGENT_MODEL)换成**不经本网关**的
#   端点(官方 API,或第二台独立中转)。示例见本脚本末尾注释。
#   该文件含密钥,且位于 ~/.claude(不在本仓库内),勿提交。
#
# 用法:
#   claude-fallback.sh on      切到备用端点(先把当前网关配置备份为 settings.gateway.json)
#   claude-fallback.sh off     切回网关(还原 settings.gateway.json)
#   claude-fallback.sh status  显示当前指向与备用配置是否就绪
#
# 注意:切换后需**重启 Claude Code**(env 在启动时读取),新会话才生效。
# =====================================================================
set -euo pipefail

CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
LIVE="$CLAUDE_DIR/settings.json"
FALLBACK="$CLAUDE_DIR/settings.fallback.json"
SAVED="$CLAUDE_DIR/settings.gateway.json"   # 切走前的网关配置备份

usage() { sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

base_url_of() { [[ -f "$1" ]] && python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('env',{}).get('ANTHROPIC_BASE_URL','(未设)'))" "$1" 2>/dev/null || echo "(无文件)"; }

case "${1:-}" in
  on)
    [[ -f "$FALLBACK" ]] || { echo "✗ 缺少备用配置:$FALLBACK —— 请先按脚本注释创建" >&2; exit 1; }
    # 若 live 已是备用(重复执行),不覆盖已备份的网关配置。
    if [[ -f "$SAVED" ]] && diff -q "$LIVE" "$FALLBACK" >/dev/null 2>&1; then
      echo "已是备用端点,无需再切"; exit 0
    fi
    cp -p "$LIVE" "$SAVED"
    cp -p "$FALLBACK" "$LIVE"
    echo "✓ 已切到备用端点:$(base_url_of "$LIVE")"
    echo "  (网关配置已备份到 $SAVED;**重启 Claude Code ** 后生效)"
    ;;
  off)
    [[ -f "$SAVED" ]] || { echo "✗ 无网关配置备份 $SAVED(未切过或已还原)" >&2; exit 1; }
    cp -p "$SAVED" "$LIVE"
    rm -f "$SAVED"
    echo "✓ 已切回网关:$(base_url_of "$LIVE")(**重启 Claude Code** 后生效)"
    ;;
  status)
    echo "live      : $LIVE  → $(base_url_of "$LIVE")"
    echo "备用就绪   : $([[ -f "$FALLBACK" ]] && echo "是 → $(base_url_of "$FALLBACK")" || echo "否(缺 $FALLBACK)")"
    echo "网关已备份 : $([[ -f "$SAVED" ]] && echo 是 || echo 否)"
    ;;
  *) usage ;;
esac

# ---------------------------------------------------------------------
# ~/.claude/settings.fallback.json 示例(换成你自己的官方 key / 第二端点):
# {
#   "env": {
#     "ANTHROPIC_BASE_URL": "https://api.anthropic.com",
#     "ANTHROPIC_AUTH_TOKEN": "sk-ant-xxxxxxxx",
#     "ANTHROPIC_MODEL": "claude-sonnet-5",
#     "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-5",
#     "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5",
#     "ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku-4-5-20251001",
#     "CLAUDE_CODE_SUBAGENT_MODEL": "claude-sonnet-5",
#     "ANTHROPIC_DANGEROUSLY_BYPASS_TOOL_CAPABILITY_CHECK": "true",
#     "DISABLE_AUTOUPDATER": "1"
#   },
#   "model": "sonnet",
#   "hasCompletedOnboarding": true
# }
# ---------------------------------------------------------------------
