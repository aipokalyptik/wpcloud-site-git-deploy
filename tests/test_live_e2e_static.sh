#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
live_e2e="$repo_root/scripts/live-e2e.sh"

require_literal() {
  local literal="$1"

  if ! grep -Fq "$literal" "$live_e2e"; then
    printf 'live E2E script is missing expected text: %s\n' "$literal" >&2
    exit 1
  fi
}

reject_literal() {
  local literal="$1"

  if grep -Fq "$literal" "$live_e2e"; then
    printf 'live E2E script still contains obsolete text: %s\n' "$literal" >&2
    exit 1
  fi
}

require_literal "git_lfs_version=\"\${WPCLOUD_SITE_GIT_DEPLOY_GIT_LFS_VERSION:-3.7.1}\""
require_literal "git_lfs_linux_amd64_sha256=\"\${WPCLOUD_SITE_GIT_DEPLOY_GIT_LFS_LINUX_AMD64_SHA256:-1c0b6ee5200ca708c5cebebb18fdeb0e1c98f1af5c1a9cba205a4c0ab5a5ec08}\""
require_literal "https://github.com/git-lfs/git-lfs/releases/download/v\\\${version}/git-lfs-linux-amd64-v\\\${version}.tar.gz"
require_literal "sha256sum -c -"
require_literal "ensure_remote_git_lfs"
require_literal "remote_deploy_default e2e-11-lfs"
reject_literal "e2e-11-lfs-missing-tool"
reject_literal "git-lfs is required"
