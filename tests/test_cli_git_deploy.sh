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

inode_of() {
  if stat -f '%i' "$1" >/dev/null 2>&1; then
    stat -f '%i' "$1"
  else
    stat -c '%i' "$1"
  fi
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cli="${WPCLOUD_SITE_GIT_DEPLOY_CLI:-$repo_root/bin/wpcloud-site-git-deploy}"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

fake_bin="$tmpdir/bin"
source_repo="$tmpdir/source"
home_dir="$tmpdir/home"
docroot="$tmpdir/docroot"
mkdir -p "$fake_bin" "$source_repo" "$home_dir" "$docroot"
touch "$tmpdir/gitconfig"
export GIT_CONFIG_GLOBAL="$tmpdir/gitconfig"
system_git="$(command -v git)"
git_log="$tmpdir/git.log"

cat >"$fake_bin/git" <<SH
#!/usr/bin/env bash
set -euo pipefail
if [[ "\${1:-}" == "-C" && "\${3:-}" == "gc" && "\${4:-}" == "--auto" ]]; then
  printf 'gc-auto %s\n' "\$2" >>"$git_log"
fi
exec "$system_git" "\$@"
SH
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
chmod +x "$fake_bin/git"
export PATH="$fake_bin:$PATH"

git -C "$source_repo" init -b main >/dev/null
git -C "$source_repo" config user.name "WP Cloud Deploy Test"
git -C "$source_repo" config user.email "wpcloud-deploy-test@example.invalid"
mkdir -p "$source_repo/assets"
printf 'hello from main\n' >"$source_repo/index.html"
printf 'asset v1\n' >"$source_repo/assets/app.txt"
printf 'repo bookkeeping\n' >"$source_repo/.gitignore"
printf '*.bin filter=lfs diff=lfs merge=lfs -text\n' >"$source_repo/.gitattributes"
printf '[submodule "vendor/example"]\n\tpath = vendor/example\n\turl = https://example.invalid/example.git\n' >"$source_repo/.gitmodules"
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

first_deploy="$(HOME="$home_dir" "$cli" deploy site --tag v1)"
first_release="${first_deploy%% *}"
grep -Fx 'hello from main' "$docroot/index.html" >/dev/null || fail "tag deploy did not publish v1 content"
[[ ! -e "$docroot/.gitignore" ]] || fail ".gitignore should be excluded by default"
[[ ! -e "$docroot/.gitattributes" ]] || fail ".gitattributes should be excluded by default"
[[ ! -e "$docroot/.gitmodules" ]] || fail ".gitmodules should be excluded by default"
[[ -L "$docroot/app-link.txt" ]] || fail "repo symlink should be deployed through public claim"
[[ -L "$docroot/index.html" ]] || fail "published index should be a symlink"
index_target="$(readlink "$docroot/index.html")"
[[ "$index_target" == .github-ssh-deploy/deployments/site/current/index.html ]] || fail "unexpected public symlink target: $index_target"
assert_not_contains "$home_dir" <(printf '%s\n' "$index_target")

second_deploy="$(HOME="$home_dir" "$cli" deploy site --branch feature)"
second_release="${second_deploy%% *}"
grep -Fx 'hello from feature' "$docroot/index.html" >/dev/null || fail "branch deploy did not publish feature content"
first_asset="$docroot/.github-ssh-deploy/deployments/site/releases/$first_release/assets/app.txt"
second_asset="$docroot/.github-ssh-deploy/deployments/site/releases/$second_release/assets/app.txt"
[[ "$(inode_of "$first_asset")" == "$(inode_of "$second_asset")" ]] || fail "unchanged asset should be hardlinked across releases"
first_index="$docroot/.github-ssh-deploy/deployments/site/releases/$first_release/index.html"
second_index="$docroot/.github-ssh-deploy/deployments/site/releases/$second_release/index.html"
[[ "$(inode_of "$first_index")" != "$(inode_of "$second_index")" ]] || fail "changed file should not be hardlinked across releases"

HOME="$home_dir" "$cli" deploy site --commit "$main_commit" >/dev/null
grep -Fx 'hello from main' "$docroot/index.html" >/dev/null || fail "commit deploy did not publish main commit content"

HOME="$home_dir" "$cli" update site >/dev/null
grep -Fx 'hello from feature' "$docroot/index.html" >/dev/null || fail "update did not deploy latest default ref"
repo_cache="$home_dir/.wpcloud-site-git-deploy/repos/site"
if git -C "$repo_cache" worktree list --porcelain | grep -Fq "$home_dir/.wpcloud-site-git-deploy/tmp/site/"; then
  fail "deploy worktree should be removed from git worktree registry"
fi
[[ ! -d "$home_dir/.wpcloud-site-git-deploy/tmp/site" ]] || fail "deploy worktree temp directory should be cleaned up"

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

printf 'hello from late main\n' >"$source_repo/index.html"
git -C "$source_repo" add index.html
git -C "$source_repo" commit -m "late main content" >/dev/null
late_commit="$(git -C "$source_repo" rev-parse HEAD)"
git -C "$source_repo" branch late-branch "$late_commit"
git -C "$source_repo" tag v2 "$late_commit"

HOME="$home_dir" "$cli" branches site >"$tmpdir/branches-cached.txt"
assert_not_contains "late-branch" "$tmpdir/branches-cached.txt"

HOME="$home_dir" "$cli" tags site >"$tmpdir/tags-cached.txt"
assert_not_contains "v2" "$tmpdir/tags-cached.txt"

HOME="$home_dir" "$cli" commits site --limit 1 >"$tmpdir/commits-cached.txt"
assert_not_contains "$late_commit" "$tmpdir/commits-cached.txt"

HOME="$home_dir" "$cli" branches site --fetch >"$tmpdir/branches-fetched.txt"
assert_contains "late-branch" "$tmpdir/branches-fetched.txt"
HOME="$home_dir" "$cli" tags site --fetch >"$tmpdir/tags-fetched.txt"
assert_contains "v2" "$tmpdir/tags-fetched.txt"
HOME="$home_dir" "$cli" commits site --fetch --limit 1 >"$tmpdir/commits-fetched.txt"
assert_contains "$late_commit" "$tmpdir/commits-fetched.txt"
assert_contains "gc-auto $repo_cache" "$git_log"

lfs_source_repo="$tmpdir/lfs-source"
lfs_home_dir="$tmpdir/lfs-home"
lfs_docroot="$tmpdir/lfs-docroot"
mkdir -p "$lfs_source_repo" "$lfs_home_dir" "$lfs_docroot"

cat >"$fake_bin/git-lfs" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  install)
    exit 0
    ;;
  pull)
    printf 'hydrated lfs content\n' >media.bin
    exit 0
    ;;
  *)
    echo "unexpected git-lfs command: $*" >&2
    exit 1
    ;;
esac
SH
chmod +x "$fake_bin/git-lfs"

git -C "$lfs_source_repo" init -b main >/dev/null
git -C "$lfs_source_repo" config user.name "WP Cloud Deploy Test"
git -C "$lfs_source_repo" config user.email "wpcloud-deploy-test@example.invalid"
printf '*.bin filter=lfs diff=lfs merge=lfs -text\n' >"$lfs_source_repo/.gitattributes"
{
  printf 'version https://git-lfs.github.com/spec/v1\n'
  printf 'oid sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n'
  printf 'size 1\n'
} >"$lfs_source_repo/media.bin"
{
  printf 'version https://git-lfs.github.com/spec/v1\n'
  printf 'this normal text file intentionally looks like an LFS pointer header\n'
} >"$lfs_source_repo/notes.txt"
git -C "$lfs_source_repo" add .
git -C "$lfs_source_repo" commit -m "lfs fixture" >/dev/null

HOME="$lfs_home_dir" "$cli" init lfs-site \
  --repo "$lfs_source_repo" \
  --docroot "$lfs_docroot" \
  --deployment-id lfs-site \
  --default-ref main >/dev/null
HOME="$lfs_home_dir" "$cli" update lfs-site >/dev/null
grep -Fx 'hydrated lfs content' "$lfs_docroot/media.bin" >/dev/null || fail "LFS file should be hydrated by git-lfs pull"
grep -Fx 'version https://git-lfs.github.com/spec/v1' "$lfs_docroot/notes.txt" >/dev/null || fail "non-LFS pointer-shaped file should not fail deploy"
