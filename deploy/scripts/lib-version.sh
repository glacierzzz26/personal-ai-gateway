#!/usr/bin/env bash
# =====================================================================
# lib-version.sh — 版本标识的**单一来源**(被 build.sh / deploy.sh / upgrade.sh source)。
#
# 发布纪律要求「版本号 + 短 hash 一并带上,光有短 hash 不够(hash 不表意)」。
# 本文件把这套口径收敛到一处,避免 CI(release.yml)与本地(build.sh)各算一份而漂。
#
# 口径(与发布纪律一致):
#   - HEAD 正好落在 v* tag 上 → <tag>-<sha7>      如 v0.9.0-4de1cde
#   - 否则(未发版)         → v0.0.0-<sha7>      如 v0.0.0-4de1cde
#   - 工作区有未提交改动    → 追加 -dirty          如 v0.0.0-4de1cde-dirty
#
# 提供:
#   gw_version   [repo]  版本串(如上)
#   gw_git_short [repo]  7 位短 hash
#   gw_schema_head [repo] 迁移条数(schema.go 的 migrations 长度;升级脚本据此判降级)
#
# 直接运行(非 source)时打印三者,便于人工核对:
#   bash deploy/scripts/lib-version.sh
# =====================================================================

# gw_git_short <repo> — HEAD 的 7 位短 hash;取不到时回 "unknown"(不静默)。
gw_git_short() {
  local repo="${1:-.}" sha
  sha="$(cd "$repo" 2>/dev/null && git rev-parse --short=7 HEAD 2>/dev/null)" || true
  printf '%s\n' "${sha:-unknown}"
}

# gw_version <repo> — 版本串,口径见文件头。
gw_version() {
  local repo="${1:-.}" sha tag ver
  sha="$(gw_git_short "$repo")"
  # 恰好停在 v* tag 上?--exact-match 只在 HEAD 就是该 tag 时命中。
  tag="$(cd "$repo" 2>/dev/null && git describe --tags --exact-match --match 'v*' 2>/dev/null)" || true
  if [ -n "$tag" ]; then
    ver="$tag-$sha"
  else
    ver="v0.0.0-$sha"
  fi
  # dirty:与 git describe --dirty 同口径(只算已跟踪文件的改动,不算 untracked)。
  if ! (cd "$repo" 2>/dev/null && git diff-index --quiet HEAD -- 2>/dev/null); then
    ver="$ver-dirty"
  fi
  printf '%s\n' "$ver"
}

# gw_schema_head <repo> — 迁移条数。数 schema.go 里 migrations 数组的 m00NN 条目。
gw_schema_head() {
  local repo="${1:-.}" n
  n="$(grep -cE '^[[:space:]]*m[0-9]{4}[A-Za-z]' "$repo/internal/store/schema.go" 2>/dev/null)" || true
  printf '%s\n' "${n:-0}"
}

# 直接运行(非 source):打印三项,便于本地核对。
if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
  echo "version=$(gw_version .)"
  echo "git_short=$(gw_git_short .)"
  echo "schema_head=$(gw_schema_head .)"
fi
