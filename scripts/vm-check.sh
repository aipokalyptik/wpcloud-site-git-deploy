#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
vm_name="${WPCLOUD_SITE_GIT_DEPLOY_LIMA_VM:-agc-ubuntu-amd64}"
remote_root="${WPCLOUD_SITE_GIT_DEPLOY_VM_REMOTE_ROOT:-/tmp/wpcloud-site-git-deploy-go-current}"

"$repo_root/scripts/ensure-lima-go.sh" "$vm_name"

COPYFILE_DISABLE=1 tar --no-xattrs --exclude tmp --exclude .cache -czf - -C "$repo_root" . |
  limactl shell "$vm_name" -- bash --noprofile --norc -c "
    set -euo pipefail
    rm -rf '$remote_root'
    mkdir -p '$remote_root'
    tar -xzf - -C '$remote_root'
    cd '$remote_root'
    export PATH=/usr/local/go/bin:\$PATH
    go test ./...
    go vet ./...
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/wpcloud-site-git-deploy
    bash -n scripts/install.sh scripts/live-e2e.sh tests/*.sh
    tests/go_conformance.sh
    git diff --check
  "
