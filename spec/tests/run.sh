#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "tests/run.sh requires Linux/GNU tooling." >&2
  echo "Run it on the site host, Linux CI, a Linux container/VM, or use the throwaway WP Cloud/Pressable E2E site." >&2
  exit 64
fi

"$repo_root/tests/test_cli_git_deploy.sh"
"$repo_root/tests/test_auth_doctor.sh"
"$repo_root/tests/test_remote_invariants.sh"

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "$repo_root"/bin/wpcloud-site-git-deploy "$repo_root"/scripts/*.sh "$repo_root"/tests/*.sh
else
  echo "shellcheck not found; skipping shell lint" >&2
fi
