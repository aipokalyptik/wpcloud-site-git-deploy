#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
remote="$repo_root/lib/remote-deploy.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

fake_bin="$tmpdir/bin"
docroot="$tmpdir/docroot"
home_like="$tmpdir/home/user"
base="$docroot/.github-ssh-deploy/deployments/site"
incoming="$base/incoming/release-one"
empty_boundaries="$tmpdir/empty-boundaries"
empty_protected="$tmpdir/empty-protected"
mkdir -p "$fake_bin" "$incoming" "$home_like"
docroot_real="$(cd "$docroot" && pwd -P)"
: >"$empty_boundaries"
: >"$empty_protected"

cat >"$fake_bin/flock" <<'SH'
#!/usr/bin/env bash
exit 0
SH
cat >"$fake_bin/mv" <<'SH'
#!/usr/bin/env bash
if [[ "${1:-}" == "-T" ]]; then
  shift
  if [[ "${1:-}" == "--" ]]; then
    shift
  fi
  python3 - "$1" "$2" <<'PY'
import os
import sys
os.rename(sys.argv[1], sys.argv[2])
PY
  exit 0
fi
exec /bin/mv "$@"
SH
chmod +x "$fake_bin/flock"
chmod +x "$fake_bin/mv"
export PATH="$fake_bin:$PATH"
export WPCLOUD_SITE_GIT_DEPLOY_SKIP_GNU_FIND_CHECK=1
export GITHUB_SSH_DEPLOY_BOUNDARIES_FILE="$empty_boundaries"
export GITHUB_SSH_DEPLOY_PROTECTED_ANCHORS_FILE="$empty_protected"

printf 'ok\n' >"$incoming/index.html"
"$remote" --docroot "$docroot" --deployment-id site --release-id release-one --keep-releases 2 >/dev/null

target="$(readlink "$docroot/index.html")"
[[ "$target" == ".github-ssh-deploy/deployments/site/current/index.html" ]] || fail "public symlink target should be docroot-relative, got: $target"
[[ "$target" != /* ]] || fail "public symlink target must not be absolute"
[[ "$target" != *"$home_like"* ]] || fail "public symlink target must not include HOME"

resolved="$(cd "$(dirname "$docroot/index.html")" && pwd -P)/$target"
resolved="$(cd "$(dirname "$resolved")" && pwd -P)/$(basename "$resolved")"
case "$resolved" in
  "$docroot_real"/*) ;;
  *) fail "public symlink resolves outside docroot: $resolved" ;;
esac

bad="$docroot/bad-link"
ln -s "$home_like/file" "$bad"
if "$remote" --docroot "$docroot" --deployment-id site --assert-public-symlinks >/dev/null 2>"$tmpdir/bad.log"; then
  fail "assert-public-symlinks should reject HOME symlink"
fi
if ! grep -Eq "public symlink (target is absolute|resolves outside docroot)" "$tmpdir/bad.log"; then
  fail "unexpected outside-docroot assertion message"
fi
