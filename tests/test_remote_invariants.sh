#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
remote=("$repo_root/bin/wpcloud-site-git-deploy" __remote-deploy)
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

fake_bin="$tmpdir/bin"
docroot="$tmpdir/docroot"
home_like="$tmpdir/home/user"
base="$docroot/.wpcloud-site-git-deploy/deployments/site"
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
cat >"$fake_bin/ln" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${WPCLOUD_SITE_GIT_DEPLOY_CORRUPT_LINK_TARGET:-}" != "" &&
      "${1:-}" == "-s" &&
      "${3:-}" == *".wpcloud-site-git-deploy."* ]]; then
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
chmod +x "$fake_bin/ln"
chmod +x "$fake_bin/find"
export PATH="$fake_bin:$PATH"
export WPCLOUD_SITE_GIT_DEPLOY_FIND_LOG="$find_log"
export WPCLOUD_SITE_GIT_DEPLOY_TEST_DOCROOT="$docroot"
export WPCLOUD_SITE_GIT_DEPLOY_BOUNDARIES_FILE="$empty_boundaries"
export WPCLOUD_SITE_GIT_DEPLOY_PROTECTED_ANCHORS_FILE="$empty_protected"

printf 'ok\n' >"$incoming/index.html"
"${remote[@]}" --docroot "$docroot" --deployment-id site --release-id release-one --keep-releases 2 >/dev/null
docroot_scans="$(grep -c '^docroot-symlink-scan$' "$find_log" 2>/dev/null || true)"
[[ "$docroot_scans" == "1" ]] || fail "deploy should scan docroot symlinks only for materialized claims, got $docroot_scans scans"

target="$(readlink "$docroot/index.html")"
[[ "$target" == ".wpcloud-site-git-deploy/deployments/site/current/index.html" ]] || fail "public symlink target should be docroot-relative, got: $target"
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
if "${remote[@]}" --docroot "$docroot" --deployment-id site --assert-public-symlinks >/dev/null 2>"$tmpdir/bad.log"; then
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
  local corrupt_base="$corrupt_docroot/.wpcloud-site-git-deploy/deployments/site"
  local corrupt_incoming="$corrupt_base/incoming/release-one"

  mkdir -p "$corrupt_incoming"
  printf 'ok\n' >"$corrupt_incoming/index.html"
  : >"$find_log"

  if HOME="$home_like" WPCLOUD_SITE_GIT_DEPLOY_TEST_DOCROOT="$corrupt_docroot" WPCLOUD_SITE_GIT_DEPLOY_CORRUPT_LINK_TARGET="$corrupt_target" \
    "${remote[@]}" --docroot "$corrupt_docroot" --deployment-id site --release-id release-one --keep-releases 2 >/dev/null 2>"$tmpdir/$name.log"; then
    fail "scoped assertion should reject $name target"
  fi

  grep -Fq -- "$expected" "$tmpdir/$name.log" || fail "unexpected scoped assertion message for $name"
}

assert_corrupt_claim_fails absolute "/outside-target" "public symlink target is absolute: index.html"
assert_corrupt_claim_fails home "../${home_like#/}/file" "public symlink target contains HOME: index.html"
assert_corrupt_claim_fails outside "../outside-target" "public symlink resolves outside docroot: index.html"

foreign_docroot="$tmpdir/foreign-docroot"
foreign_base="$foreign_docroot/.wpcloud-site-git-deploy/deployments/site"
foreign_incoming="$foreign_base/incoming/release-one"
mkdir -p "$foreign_incoming"
printf 'ok\n' >"$foreign_incoming/index.html"
mkdir -p "$foreign_docroot"
ln -s ".wpcloud-site-git-deploy/deployments/other/current/index.html" "$foreign_docroot/index.html"
if "${remote[@]}" --docroot "$foreign_docroot" --deployment-id site --release-id release-one --keep-releases 2 >/dev/null 2>"$tmpdir/foreign-owner.log"; then
  fail "namespace symlink owned by another deployment should be rejected"
fi
grep -Fq -- "claim owned by another deployment: index.html" "$tmpdir/foreign-owner.log" || fail "unexpected foreign-owner rejection"

assert_shared_path_fails() {
  local name="$1"
  local shared_path="$2"
  local expected_path="$3"
  local shared_docroot="$tmpdir/$name-docroot"
  local shared_base="$shared_docroot/.wpcloud-site-git-deploy/deployments/site"
  local initial_incoming="$shared_base/incoming/release-one"
  local bad_incoming="$shared_base/incoming/release-two"

  mkdir -p "$initial_incoming"
  printf 'ok\n' >"$initial_incoming/index.html"
  "${remote[@]}" --docroot "$shared_docroot" --deployment-id site --release-id release-one --keep-releases 2 >/dev/null

  mkdir -p "$bad_incoming/$(dirname "$shared_path")"
  printf 'bad\n' >"$bad_incoming/$shared_path"
  if "${remote[@]}" --docroot "$shared_docroot" --deployment-id site --release-id release-two --keep-releases 2 >/dev/null 2>"$tmpdir/$name-shared.log"; then
    fail "shared path $shared_path should be rejected"
  fi
  grep -Fq -- "shared path cannot be deployed: $expected_path" "$tmpdir/$name-shared.log" || fail "unexpected shared path rejection for $shared_path"
  [[ "$(readlink "$shared_base/current")" == "releases/release-one" ]] || fail "shared path rejection should leave current unchanged"
}

assert_shared_path_fails uploads "wp-content/uploads/file.jpg" "wp-content/uploads"
assert_shared_path_fails maintenance ".maintenance" ".maintenance"

maintenance_docroot="$tmpdir/tool-maintenance-docroot"
maintenance_base="$maintenance_docroot/.wpcloud-site-git-deploy/deployments/site"
maintenance_incoming="$maintenance_base/incoming/release-one"
maintenance_hook="$tmpdir/maintenance-hook.sh"
mkdir -p "$maintenance_incoming"
printf 'ok\n' >"$maintenance_incoming/index.html"
cat >"$maintenance_hook" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .maintenance
grep -Fx 'wpcloud-site-git-deploy maintenance' .maintenance >/dev/null
grep -Fx 'deployment_id=site' .maintenance >/dev/null
SH
chmod +x "$maintenance_hook"
"${remote[@]}" --docroot "$maintenance_docroot" --deployment-id site --release-id release-one --keep-releases 2 --maintenance-file .maintenance --post-deploy-file "$maintenance_hook" >/dev/null
[[ ! -e "$maintenance_docroot/.maintenance" ]] || fail "successful deploy should remove tool-owned maintenance file"

nonowned_docroot="$tmpdir/nonowned-maintenance-docroot"
nonowned_base="$nonowned_docroot/.wpcloud-site-git-deploy/deployments/site"
nonowned_incoming="$nonowned_base/incoming/release-one"
mkdir -p "$nonowned_incoming" "$nonowned_docroot"
printf 'manual maintenance\n' >"$nonowned_docroot/.maintenance"
printf 'ok\n' >"$nonowned_incoming/index.html"
"${remote[@]}" --docroot "$nonowned_docroot" --deployment-id site --release-id release-one --keep-releases 2 --maintenance-file .maintenance >/dev/null
grep -Fx 'manual maintenance' "$nonowned_docroot/.maintenance" >/dev/null || fail "non-owned maintenance file should be preserved"

rollback_missing_docroot="$tmpdir/rollback-missing-maintenance-docroot"
rollback_missing_base="$rollback_missing_docroot/.wpcloud-site-git-deploy/deployments/site"
mkdir -p "$rollback_missing_base/releases" "$rollback_missing_docroot"
cat >"$rollback_missing_docroot/.maintenance" <<'EOF'
wpcloud-site-git-deploy maintenance
deployment_id=site
EOF
if "${remote[@]}" --docroot "$rollback_missing_docroot" --deployment-id site --rollback-to missing-release --maintenance-file .maintenance >/dev/null 2>"$tmpdir/rollback-missing-maintenance.log"; then
  fail "rollback to missing release should fail"
fi
grep -Fq -- "rollback release does not exist" "$tmpdir/rollback-missing-maintenance.log" || fail "unexpected rollback missing maintenance failure"
[[ ! -e "$rollback_missing_docroot/.maintenance" ]] || fail "failed rollback should remove stale tool-owned maintenance file"
