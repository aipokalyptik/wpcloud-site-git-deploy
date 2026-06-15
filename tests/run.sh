#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

go test "$repo_root/..."
go vet "$repo_root/..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$tmpdir/wpcloud-site-git-deploy-linux-amd64" "$repo_root/cmd/wpcloud-site-git-deploy"
go build -o "$tmpdir/wpcloud-site-git-deploy" "$repo_root/cmd/wpcloud-site-git-deploy"
export WPCLOUD_SITE_GIT_DEPLOY_CLI="$tmpdir/wpcloud-site-git-deploy"

"$repo_root/tests/test_cli_git_deploy.sh"
"$repo_root/tests/test_remote_invariants.sh"

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "$repo_root"/scripts/*.sh "$repo_root"/tests/*.sh
else
  echo "shellcheck not found; skipping shell lint" >&2
fi
