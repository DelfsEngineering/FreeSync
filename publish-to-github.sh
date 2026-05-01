#!/usr/bin/env bash
# One-shot: log into GitHub CLI once, then run this script again.
set -euo pipefail
export PATH="$HOME/bin:$HOME/miniconda3/bin:$PATH"

cd "$(dirname "$0")"

if ! gh api user >/dev/null 2>&1; then
  cat <<'EOF'
GitHub CLI is not logged in on this Mac yet.

Do this ONCE in Terminal (same window), then run this script again:

  export PATH="$HOME/bin:$HOME/miniconda3/bin:$PATH"
  gh auth login -p https -h github.com -w

A browser/device page opens — approve it before it times out (~15 min).

TOKEN OPTION (if the browser flow fails):
  Create a classic PAT: https://github.com/settings/tokens
  Scopes: repo, read:org, gist
  Then:
    export GH_TOKEN='paste_your_token_here'
    gh auth login --with-token <<< "$GH_TOKEN"

EOF
  exit 1
fi

LOGIN="$(gh api user -q .login)"
echo "Logged in to GitHub as: $LOGIN"

if git remote get-url origin >/dev/null 2>&1; then
  echo "Remote 'origin' already set. Pushing main..."
  git push -u origin main
  echo "Done: $(git remote get-url origin)"
  exit 0
fi

REPO_NAME="FreeSync"
FULL_REPO="${LOGIN}/${REPO_NAME}"

if gh repo view "${FULL_REPO}" >/dev/null 2>&1; then
  echo "Repo https://github.com/${FULL_REPO} already exists. Adding origin and pushing..."
  git remote add origin "https://github.com/${FULL_REPO}.git"
  git push -u origin main
else
  echo "Creating repo ${FULL_REPO} and pushing..."
  gh repo create "${REPO_NAME}" --public --source=. --remote=origin --push
fi

echo "Open: https://github.com/${FULL_REPO}"
