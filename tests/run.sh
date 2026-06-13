#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"$repo_root/tests/test_cli_git_deploy.sh"
"$repo_root/tests/test_remote_invariants.sh"

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "$repo_root"/bin/wpcloud-site-git-deploy "$repo_root"/tests/*.sh
else
  echo "shellcheck not found; skipping shell lint" >&2
fi
