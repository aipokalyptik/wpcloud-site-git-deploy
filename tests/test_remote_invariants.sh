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
find_log="$tmpdir/find.log"
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
cat >"$fake_bin/ln" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${WPCLOUD_SITE_GIT_DEPLOY_CORRUPT_LINK_TARGET:-}" != "" &&
      "${1:-}" == "-s" &&
      "${3:-}" == *".github-ssh-deploy."* ]]; then
  exec /bin/ln -s "$WPCLOUD_SITE_GIT_DEPLOY_CORRUPT_LINK_TARGET" "$3"
fi
exec /bin/ln "$@"
SH
cat >"$fake_bin/find" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "${WPCLOUD_SITE_GIT_DEPLOY_TEST_DOCROOT:-}" && "${2:-}" == "-path" ]]; then
  printf 'docroot-symlink-scan\n' >>"${WPCLOUD_SITE_GIT_DEPLOY_FIND_LOG:?}"
fi
exec /usr/bin/find "$@"
SH
chmod +x "$fake_bin/flock"
chmod +x "$fake_bin/mv"
chmod +x "$fake_bin/ln"
chmod +x "$fake_bin/find"
export PATH="$fake_bin:$PATH"
export WPCLOUD_SITE_GIT_DEPLOY_FIND_LOG="$find_log"
export WPCLOUD_SITE_GIT_DEPLOY_TEST_DOCROOT="$docroot"
export WPCLOUD_SITE_GIT_DEPLOY_SKIP_GNU_FIND_CHECK=1
export GITHUB_SSH_DEPLOY_BOUNDARIES_FILE="$empty_boundaries"
export GITHUB_SSH_DEPLOY_PROTECTED_ANCHORS_FILE="$empty_protected"

printf 'ok\n' >"$incoming/index.html"
"$remote" --docroot "$docroot" --deployment-id site --release-id release-one --keep-releases 2 >/dev/null
docroot_scans="$(grep -c '^docroot-symlink-scan$' "$find_log" 2>/dev/null || true)"
[[ "$docroot_scans" == "1" ]] || fail "deploy should scan docroot symlinks only for materialized claims, got $docroot_scans scans"

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
scan_count_before_audit="$(grep -c '^docroot-symlink-scan$' "$find_log" 2>/dev/null || true)"
if "$remote" --docroot "$docroot" --deployment-id site --assert-public-symlinks >/dev/null 2>"$tmpdir/bad.log"; then
  fail "assert-public-symlinks should reject HOME symlink"
fi
scan_count_after_audit="$(grep -c '^docroot-symlink-scan$' "$find_log" 2>/dev/null || true)"
[[ "$scan_count_after_audit" -gt "$scan_count_before_audit" ]] || fail "standalone audit should still scan the full docroot"
if ! grep -Eq "public symlink (target is absolute|resolves outside docroot)" "$tmpdir/bad.log"; then
  fail "unexpected outside-docroot assertion message"
fi

assert_corrupt_claim_fails() {
  local name="$1"
  local corrupt_target="$2"
  local expected="$3"
  local corrupt_docroot="$tmpdir/$name-docroot"
  local corrupt_base="$corrupt_docroot/.github-ssh-deploy/deployments/site"
  local corrupt_incoming="$corrupt_base/incoming/release-one"

  mkdir -p "$corrupt_incoming"
  printf 'ok\n' >"$corrupt_incoming/index.html"
  : >"$find_log"

  if HOME="$home_like" WPCLOUD_SITE_GIT_DEPLOY_TEST_DOCROOT="$corrupt_docroot" WPCLOUD_SITE_GIT_DEPLOY_CORRUPT_LINK_TARGET="$corrupt_target" \
    "$remote" --docroot "$corrupt_docroot" --deployment-id site --release-id release-one --keep-releases 2 >/dev/null 2>"$tmpdir/$name.log"; then
    fail "scoped assertion should reject $name target"
  fi

  grep -Fq -- "$expected" "$tmpdir/$name.log" || fail "unexpected scoped assertion message for $name"
}

assert_corrupt_claim_fails absolute "/outside-target" "public symlink target is absolute: index.html"
assert_corrupt_claim_fails home "../${home_like#/}/file" "public symlink target contains HOME: index.html"
assert_corrupt_claim_fails outside "../outside-target" "public symlink resolves outside docroot: index.html"
