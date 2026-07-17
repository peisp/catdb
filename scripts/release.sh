#!/usr/bin/env bash
# 交互式发布脚本：↑/↓ 选择发布类型（正式/预发布）→ 显示该类型最近的 tag →
# ↑/↓ 选择 tag 要打的 commit（该类型上次 tag 之后的提交）→ 输入新版本号 →
# 确认后打 tag 并推送，触发 .github/workflows/release.yml。
# 兼容 macOS 自带 bash 3.2；无终端时菜单退化为数字选择。
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m%s\033[0m\n' "$*"; }
die()  { printf '\033[31m%s\033[0m\n' "$*" >&2; exit 1; }

# 有可交互终端吗？决定菜单用方向键还是数字输入。
TTY_OK=""
if ( : </dev/tty ) 2>/dev/null; then TTY_OK=1; fi

# select_menu "标题" "选项..." — ↑/↓ 移动、回车确认，结果下标写入 MENU_INDEX。
select_menu() {
  local title=$1; shift
  local options=("$@")
  local count=${#options[@]}
  local idx=0 i key seq

  if [[ -z "$TTY_OK" ]]; then
    bold "$title"
    for ((i = 0; i < count; i++)); do
      printf '  %d) %s\n' $((i + 1)) "${options[$i]}"
    done
    read -rp "请选择 [1-${count}]: " key
    if ! [[ "$key" =~ ^[0-9]+$ ]] || ((key < 1 || key > count)); then
      die "无效选择：${key}"
    fi
    MENU_INDEX=$((key - 1))
    return
  fi

  bold "${title}（↑/↓ 选择，回车确认）" >/dev/tty
  while true; do
    for ((i = 0; i < count; i++)); do
      printf '\033[2K' >/dev/tty
      if ((i == idx)); then
        printf '\033[7m❯ %s\033[0m\n' "${options[$i]}" >/dev/tty
      else
        printf '  %s\n' "${options[$i]}" >/dev/tty
      fi
    done
    IFS= read -rsn1 key </dev/tty
    if [[ "$key" == $'\x1b' ]]; then
      seq=""
      read -rsn2 -t 1 seq </dev/tty || true
      if [[ "$seq" == "[A" ]] && ((idx > 0)); then
        idx=$((idx - 1))
      elif [[ "$seq" == "[B" ]] && ((idx < count - 1)); then
        idx=$((idx + 1))
      fi
    elif [[ -z "$key" ]]; then # 回车
      break
    fi
    printf '\033[%dA' "$count" >/dev/tty # 光标移回列表顶部重绘
  done
  MENU_INDEX=$idx
}

# ── 前置检查 ──────────────────────────────────────────────────────────
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [[ "$BRANCH" != "main" ]]; then
  warn "当前分支是 ${BRANCH}（不是 main）。"
  read -rp "仍要继续吗？[y/N] " go_on
  [[ "$go_on" == "y" || "$go_on" == "Y" ]] || exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
  warn "工作区有未提交的改动（tag 只指向已提交内容）："
  git status --short | head -10
  read -rp "仍要继续吗？[y/N] " go_on
  [[ "$go_on" == "y" || "$go_on" == "Y" ]] || exit 1
fi

echo "同步远端 tag..."
git fetch origin --tags --quiet

# ── 选择发布类型 ──────────────────────────────────────────────────────
select_menu "发布类型" \
  "正式版 (stable)   — tag 形如 v0.2.0，所有用户收到更新" \
  "预发布 (beta/rc)  — tag 形如 v0.2.0-beta.1，GitHub 标记为 Pre-release"
if [[ "$MENU_INDEX" == 0 ]]; then TYPE=stable; else TYPE=beta; fi

# ── 显示该类型最近的 tag ──────────────────────────────────────────────
if [[ "$TYPE" == "stable" ]]; then
  LAST=$(git tag --sort=-version:refname | grep -v -- '-' | head -1 || true)
else
  LAST=$(git tag --sort=-version:refname | grep -- '-' | head -1 || true)
fi
LAST_ANY=$(git tag --sort=-version:refname | head -1 || true)

echo
bold "最近的${TYPE}标签: ${LAST:-（无）}"
if [[ -n "$LAST_ANY" && "$LAST_ANY" != "$LAST" ]]; then
  echo "（所有类型中最新: ${LAST_ANY}）"
fi

# ── 选择 tag 要打的 commit（该类型上次 tag 之后的提交，最多列 30 条）──
COMMIT_LIMIT=30
if [[ -n "$LAST" ]]; then
  RANGE="${LAST}..HEAD"
else
  RANGE="HEAD"
fi
COMMITS=()
while IFS= read -r line; do
  COMMITS+=("$line")
done < <(git log --oneline -${COMMIT_LIMIT} "$RANGE")

if ((${#COMMITS[@]} == 0)); then
  warn "${LAST} 之后没有新提交，tag 只能打在当前 HEAD 上。"
  COMMITS=("$(git log --oneline -1 HEAD)")
fi
COMMITS[0]="${COMMITS[0]}   ← HEAD"
if ((${#COMMITS[@]} == COMMIT_LIMIT)); then
  echo "（提交较多，只列出最近 ${COMMIT_LIMIT} 条）"
fi

echo
select_menu "tag 打在哪个 commit 上" "${COMMITS[@]}"
COMMIT_HASH=${COMMITS[$MENU_INDEX]%% *}
COMMIT_SUBJECT=$(git log -1 --pretty=%s "$COMMIT_HASH")

# ── 输入并校验新版本号 ────────────────────────────────────────────────
if [[ "$TYPE" == "stable" ]]; then
  PATTERN='^v?[0-9]+\.[0-9]+\.[0-9]+$'
  HINT="X.Y.Z，例如 0.2.0"
else
  PATTERN='^v?[0-9]+\.[0-9]+\.[0-9]+-(beta|rc)\.[0-9]+$'
  HINT="X.Y.Z-beta.N 或 X.Y.Z-rc.N，例如 0.2.0-beta.1"
fi

echo
read -rp "输入新版本号（${HINT}）: " INPUT
[[ "$INPUT" =~ $PATTERN ]] || die "版本号格式不对，应为：${HINT}"

TAG="v${INPUT#v}"

if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
  die "tag ${TAG} 已存在。"
fi

# ── 确认并推送 ────────────────────────────────────────────────────────
echo
bold "即将执行："
echo "  git tag ${TAG} ${COMMIT_HASH}        (${COMMIT_SUBJECT})"
echo "  git push origin ${TAG}"
if [[ "$TYPE" == "beta" ]]; then
  echo "  → CI 将发布为 GitHub Pre-release，仅测试版通道用户收到更新"
else
  echo "  → CI 将发布为正式 Release，所有用户收到更新"
fi
read -rp "确认？[y/N] " ok
[[ "$ok" == "y" || "$ok" == "Y" ]] || { echo "已取消。"; exit 0; }

git tag "$TAG" "$COMMIT_HASH"
git push origin "$TAG"

echo
bold "已推送 ${TAG}。构建进度: https://github.com/peisp/catdb/actions"
