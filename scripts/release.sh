#!/usr/bin/env bash
# 交互式发布脚本：选择发布类型（正式/预发布）→ 显示该类型最近的 tag →
# 输入新版本号 → 确认后打 tag 并推送，触发 .github/workflows/release.yml。
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m%s\033[0m\n' "$*"; }
die()  { printf '\033[31m%s\033[0m\n' "$*" >&2; exit 1; }

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
bold "发布类型："
echo "  1) 正式版 (stable)      — tag 形如 v0.2.0"
echo "  2) 预发布 (beta/rc)     — tag 形如 v0.2.0-beta.1，GitHub 上标记为 Pre-release"
read -rp "请选择 [1/2]: " kind
case "$kind" in
  1) TYPE=stable ;;
  2) TYPE=beta ;;
  *) die "无效选择：$kind" ;;
esac

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
echo "  git tag $TAG        (commit: $(git rev-parse --short HEAD) — $(git log -1 --pretty=%s))"
echo "  git push origin $TAG"
if [[ "$TYPE" == "beta" ]]; then
  echo "  → CI 将发布为 GitHub Pre-release，仅测试版通道用户收到更新"
else
  echo "  → CI 将发布为正式 Release，所有用户收到更新"
fi
read -rp "确认？[y/N] " ok
[[ "$ok" == "y" || "$ok" == "Y" ]] || { echo "已取消。"; exit 0; }

git tag "$TAG"
git push origin "$TAG"

echo
bold "已推送 ${TAG}。构建进度: https://github.com/peisp/catdb/actions"
