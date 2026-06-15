#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$repo_root/dist"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$repo_root/dist/wpcloud-site-git-deploy" "$repo_root/cmd/wpcloud-site-git-deploy"
printf '%s\n' "$repo_root/dist/wpcloud-site-git-deploy"
