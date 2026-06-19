#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ensure_go="$repo_root/scripts/ensure-lima-go.sh"
vm_check="$repo_root/scripts/vm-check.sh"
makefile="$repo_root/Makefile"

require_file() {
  local file="$1"

  if [[ ! -f "$file" ]]; then
    printf 'expected file is missing: %s\n' "$file" >&2
    exit 1
  fi
}

require_literal() {
  local literal="$1"
  local file="$2"

  if ! grep -Fq -- "$literal" "$file"; then
    printf '%s is missing expected text: %s\n' "$file" "$literal" >&2
    exit 1
  fi
}

require_file "$ensure_go"
require_file "$vm_check"

require_literal "limactl shell" "$ensure_go"
require_literal "go\${go_version}.linux-amd64.tar.gz" "$ensure_go"
require_literal "\"go version go\$go_minor.\"*" "$ensure_go"
require_literal "WPCLOUD_SITE_GIT_DEPLOY_GO_VERSION" "$ensure_go"
require_literal "https://go.dev/dl/?mode=json&include=all" "$ensure_go"

require_literal "scripts/ensure-lima-go.sh" "$vm_check"
require_literal "--no-xattrs" "$vm_check"
require_literal "go test ./..." "$vm_check"
require_literal "go vet ./..." "$vm_check"
require_literal "tests/go_conformance.sh" "$vm_check"

require_literal "vm-go:" "$makefile"
require_literal "vm-check:" "$makefile"
