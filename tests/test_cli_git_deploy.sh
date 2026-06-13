#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local needle="$1"
  local file="$2"
  grep -Fq -- "$needle" "$file" || fail "expected $file to contain: $needle"
}

assert_not_contains() {
  local needle="$1"
  local file="$2"
  if grep -Fq -- "$needle" "$file"; then
    fail "expected $file not to contain: $needle"
  fi
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cli="$repo_root/bin/wpcloud-site-git-deploy"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

fake_bin="$tmpdir/bin"
source_repo="$tmpdir/source"
home_dir="$tmpdir/home"
docroot="$tmpdir/docroot"
mkdir -p "$fake_bin" "$source_repo" "$home_dir" "$docroot"

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

git -C "$source_repo" init -b main >/dev/null
git -C "$source_repo" config user.name "WP Cloud Deploy Test"
git -C "$source_repo" config user.email "wpcloud-deploy-test@example.invalid"
mkdir -p "$source_repo/assets"
printf 'hello from main\n' >"$source_repo/index.html"
printf 'asset v1\n' >"$source_repo/assets/app.txt"
printf 'repo bookkeeping\n' >"$source_repo/.gitignore"
ln -s assets/app.txt "$source_repo/app-link.txt"
git -C "$source_repo" add .
git -C "$source_repo" commit -m "initial" >/dev/null
main_commit="$(git -C "$source_repo" rev-parse HEAD)"
git -C "$source_repo" tag v1

printf 'hello from feature\n' >"$source_repo/index.html"
git -C "$source_repo" add index.html
git -C "$source_repo" commit -m "feature content" >/dev/null
feature_commit="$(git -C "$source_repo" rev-parse HEAD)"
git -C "$source_repo" branch feature "$feature_commit"

HOME="$home_dir" "$cli" init site \
  --repo "$source_repo" \
  --docroot "$docroot" \
  --deployment-id site \
  --default-ref main >/dev/null

HOME="$home_dir" "$cli" deploy site --tag v1 >/dev/null
grep -Fx 'hello from main' "$docroot/index.html" >/dev/null || fail "tag deploy did not publish v1 content"
[[ ! -e "$docroot/.gitignore" ]] || fail ".gitignore should be excluded by default"
[[ -L "$docroot/app-link.txt" ]] || fail "repo symlink should be deployed through public claim"
[[ -L "$docroot/index.html" ]] || fail "published index should be a symlink"
index_target="$(readlink "$docroot/index.html")"
[[ "$index_target" == .github-ssh-deploy/deployments/site/current/index.html ]] || fail "unexpected public symlink target: $index_target"
assert_not_contains "$home_dir" <(printf '%s\n' "$index_target")

HOME="$home_dir" "$cli" deploy site --branch feature >/dev/null
grep -Fx 'hello from feature' "$docroot/index.html" >/dev/null || fail "branch deploy did not publish feature content"

HOME="$home_dir" "$cli" deploy site --commit "$main_commit" >/dev/null
grep -Fx 'hello from main' "$docroot/index.html" >/dev/null || fail "commit deploy did not publish main commit content"

HOME="$home_dir" "$cli" update site >/dev/null
grep -Fx 'hello from feature' "$docroot/index.html" >/dev/null || fail "update did not deploy latest default ref"

HOME="$home_dir" "$cli" releases site >"$tmpdir/releases.txt"
assert_contains "$feature_commit" "$tmpdir/releases.txt"

HOME="$home_dir" "$cli" rollback site >/dev/null
grep -Fx 'hello from main' "$docroot/index.html" >/dev/null || fail "rollback did not restore prior release"

HOME="$home_dir" "$cli" branches site >"$tmpdir/branches.txt"
assert_contains "feature" "$tmpdir/branches.txt"

HOME="$home_dir" "$cli" tags site >"$tmpdir/tags.txt"
assert_contains "v1" "$tmpdir/tags.txt"

HOME="$home_dir" "$cli" commits site --limit 2 >"$tmpdir/commits.txt"
assert_contains "$feature_commit" "$tmpdir/commits.txt"
