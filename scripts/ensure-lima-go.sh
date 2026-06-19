#!/usr/bin/env bash
set -euo pipefail

vm_name="${1:-${WPCLOUD_SITE_GIT_DEPLOY_LIMA_VM:-agc-ubuntu-amd64}}"
go_version="${WPCLOUD_SITE_GIT_DEPLOY_GO_VERSION:-1.26.3}"
go_minor="${go_version%.*}"
go_sha256="${WPCLOUD_SITE_GIT_DEPLOY_GO_LINUX_AMD64_SHA256:-}"
archive_name="go${go_version}.linux-amd64.tar.gz"
download_url="https://go.dev/dl/$archive_name"

remote_script="$(cat <<'REMOTE'
set -euo pipefail

go_version="${WPCLOUD_SITE_GIT_DEPLOY_GO_VERSION:?}"
go_minor="${go_version%.*}"
go_sha256="${WPCLOUD_SITE_GIT_DEPLOY_GO_LINUX_AMD64_SHA256:-}"
archive_name="go${go_version}.linux-amd64.tar.gz"
download_url="https://go.dev/dl/$archive_name"

export PATH="/usr/local/go/bin:$PATH"
if command -v go >/dev/null 2>&1; then
  current="$(go version)"
  if [[ "$current" == "go version go$go_minor."* ]]; then
    printf '%s\n' "$current"
    exit 0
  fi
fi

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'required VM command not found: %s\n' "$1" >&2
    exit 1
  fi
}

require_command tar
require_command sha256sum
require_command sudo
if command -v curl >/dev/null 2>&1; then
  downloader=(curl -fsSL)
elif command -v wget >/dev/null 2>&1; then
  downloader=(wget -qO-)
else
  printf 'curl or wget is required to download Go in the VM\n' >&2
  exit 1
fi

if [[ -z "$go_sha256" ]]; then
  checksum_url="https://go.dev/dl/?mode=json&include=all"
  set +e
  go_sha256="$("${downloader[@]}" "$checksum_url" 2>/dev/null |
    grep -A6 -F "\"filename\": \"$archive_name\"" |
    awk -F'"' '/"sha256"/ { print $4; exit }')"
  status=$?
  set -e
  if [[ "$status" -ne 0 || -z "$go_sha256" ]]; then
    printf 'could not find Go checksum for %s from %s; set WPCLOUD_SITE_GIT_DEPLOY_GO_LINUX_AMD64_SHA256\n' "$archive_name" "$checksum_url" >&2
    exit 1
  fi
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/wpcloud-go.XXXXXX")"
trap 'rm -rf "$work"' EXIT
archive="$work/$archive_name"
"${downloader[@]}" "$download_url" >"$archive"
printf '%s  %s\n' "$go_sha256" "$archive" | sha256sum -c -
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "$archive"
/usr/local/go/bin/go version
REMOTE
)"

limactl shell "$vm_name" -- env \
  "WPCLOUD_SITE_GIT_DEPLOY_GO_VERSION=$go_version" \
  "WPCLOUD_SITE_GIT_DEPLOY_GO_LINUX_AMD64_SHA256=$go_sha256" \
  bash --noprofile --norc -c "$remote_script"
