#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_root="${WPCLOUD_SITE_GIT_DEPLOY_HOME:-$HOME/.wpcloud-site-git-deploy}"
bin_dir="$install_root/bin"
binary_source="${WPCLOUD_SITE_GIT_DEPLOY_BINARY:-$repo_root/dist/wpcloud-site-git-deploy}"

mkdir -p "$bin_dir" "$install_root/deployments" "$install_root/repos" "$install_root/tmp"

if [[ -x "$binary_source" ]]; then
  cp "$binary_source" "$bin_dir/wpcloud-site-git-deploy"
elif command -v go >/dev/null 2>&1; then
  (cd "$repo_root" && CGO_ENABLED=0 go build -o "$bin_dir/wpcloud-site-git-deploy" ./cmd/wpcloud-site-git-deploy)
else
  echo "no wpcloud-site-git-deploy binary found and go is not available" >&2
  echo "set WPCLOUD_SITE_GIT_DEPLOY_BINARY to a release binary path or install Go to build from source" >&2
  exit 1
fi

chmod 755 "$bin_dir/wpcloud-site-git-deploy"

printf 'installed to %s\n' "$install_root"
printf 'add this to PATH: %s\n' "$bin_dir"
