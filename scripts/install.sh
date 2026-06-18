#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_root="${WPCLOUD_SITE_GIT_DEPLOY_HOME:-$HOME/.wpcloud-site-git-deploy}"
install_bin="$install_root/bin"
binary_name="wpcloud-site-git-deploy"

mkdir -p "$install_bin"

if [[ -x "$repo_root/dist/$binary_name-linux-amd64" ]]; then
  cp "$repo_root/dist/$binary_name-linux-amd64" "$install_bin/$binary_name"
elif [[ -x "$repo_root/$binary_name" ]]; then
  cp "$repo_root/$binary_name" "$install_bin/$binary_name"
elif command -v go >/dev/null 2>&1; then
  (
    cd "$repo_root"
    version="$(git describe --tags --dirty --always 2>/dev/null || printf dev)"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
      -ldflags "-X github.com/aipokalyptik/wpcloud-site-git-deploy/internal/cli.Version=$version" \
      -o "$install_bin/$binary_name" ./cmd/wpcloud-site-git-deploy
  )
else
  printf 'wpcloud-site-git-deploy: no bundled binary found and go is not installed\n' >&2
  printf 'expected %s or %s\n' "$repo_root/dist/$binary_name-linux-amd64" "$repo_root/$binary_name" >&2
  exit 1
fi

chmod 755 "$install_bin/$binary_name"
printf 'installed %s\n' "$install_bin/$binary_name"
