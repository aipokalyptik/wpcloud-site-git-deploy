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
  stat -c '%i' "$1"
}

supports_rsync_link_dest() {
  local dir="$1"
  local probe="$dir/.rsync-link-dest-probe.$$"
  local source="$probe/source"
  local basis="$probe/basis"
  local dest="$probe/dest"
  local result=1

  rm -rf "$probe"
  mkdir -p "$source" "$basis" "$dest"
  printf 'probe\n' >"$source/file.txt"
  cp "$source/file.txt" "$basis/file.txt"
  if rsync -a --checksum --no-times --link-dest="$basis" "$source/" "$dest/" >/dev/null 2>&1 &&
    [[ "$(inode_of "$basis/file.txt")" == "$(inode_of "$dest/file.txt")" ]]; then
    result=0
  fi
  rm -rf "$probe"
  return "$result"
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cli="$repo_root/bin/wpcloud-site-git-deploy"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

"$cli" --help >"$tmpdir/help.txt"
assert_contains "wpcloud-site-git-deploy deploys a Git repository" "$tmpdir/help.txt"
assert_contains "[--keep-releases N]" "$tmpdir/help.txt"
assert_contains "--use-key PATH" "$tmpdir/help.txt"
assert_contains "--import-key PATH" "$tmpdir/help.txt"
assert_contains "--force" "$tmpdir/help.txt"
assert_contains "--post-deploy PATH" "$tmpdir/help.txt"
assert_contains "--maintenance-file PATH|none" "$tmpdir/help.txt"
assert_contains "--help" "$tmpdir/help.txt"
assert_contains "--version" "$tmpdir/help.txt"
assert_contains "wpcloud-site-git-deploy auth site --use-key" "$repo_root/README.md"
assert_contains "wpcloud-site-git-deploy auth site --import-key" "$repo_root/README.md"
assert_contains "wpcloud-site-git-deploy update site --force" "$repo_root/README.md"
assert_contains "wpcloud-site-git-deploy config site --post-deploy" "$repo_root/README.md"
assert_contains "wpcloud-site-git-deploy config site --maintenance-file none" "$repo_root/README.md"

assert_not_contains "metadata_unquote()" "$cli"

fake_bin="$tmpdir/bin"
source_repo="$tmpdir/source"
home_dir="$tmpdir/home"
plain_home_dir="$tmpdir/plain-home"
docroot="$tmpdir/docroot"
plain_docroot="$tmpdir/plain-docroot"
mkdir -p "$fake_bin" "$source_repo" "$home_dir" "$plain_home_dir" "$docroot" "$plain_docroot"
touch "$tmpdir/gitconfig"
export GIT_CONFIG_GLOBAL="$tmpdir/gitconfig"
system_git="$(command -v git)"
system_flock="$(command -v flock)"
git_log="$tmpdir/git.log"
ssh_env_log="$tmpdir/ssh-env.log"

cat >"$fake_bin/git" <<SH
#!/usr/bin/env bash
set -euo pipefail
printf '%s|%s\n' "\$*" "\${GIT_SSH_COMMAND:-}" >>"$ssh_env_log"
if [[ "\${1:-}" == "-C" && "\${3:-}" == "gc" && "\${4:-}" == "--auto" ]]; then
  printf 'gc-auto %s\n' "\$2" >>"$git_log"
fi
exec "$system_git" "\$@"
SH
cat >"$fake_bin/flock" <<SH
#!/usr/bin/env bash
exec "$system_flock" "\$@"
SH
chmod +x "$fake_bin/flock"
chmod +x "$fake_bin/git"
export PATH="$fake_bin:$PATH"

git -C "$source_repo" init -b main >/dev/null
git -C "$source_repo" config user.name "WP Cloud Deploy Test"
git -C "$source_repo" config user.email "wpcloud-deploy-test@example.invalid"
mkdir -p "$source_repo/assets"
printf 'hello from main\n' >"$source_repo/index.html"
printf 'asset v1\n' >"$source_repo/assets/app.txt"
printf 'mac metadata\n' >"$source_repo/.DS_Store"
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

HOME="$plain_home_dir" "$cli" init plain \
  --repo "$source_repo" \
  --docroot "$plain_docroot" \
  --deployment-id plain \
  --default-ref main >/dev/null
HOME="$plain_home_dir" "$cli" branches plain >/dev/null
plain_repo_cache="$plain_home_dir/.wpcloud-site-git-deploy/repos/plain"
assert_contains "clone $source_repo $plain_repo_cache|" "$ssh_env_log"

mkdir -p "$home_dir/.wpcloud-site-git-deploy/keys"
printf 'PRIVATE KEY\n' >"$home_dir/.wpcloud-site-git-deploy/keys/site_ed25519"
printf 'ssh-ed25519 PUBLICKEY site\n' >"$home_dir/.wpcloud-site-git-deploy/keys/site_ed25519.pub"
chmod 600 "$home_dir/.wpcloud-site-git-deploy/keys/site_ed25519"
{
  printf '%s\n' "$home_dir/.wpcloud-site-git-deploy/keys/site_ed25519"
} >"$home_dir/.wpcloud-site-git-deploy/deployments/site/cfg-ssh_key_path"

first_deploy="$(HOME="$home_dir" "$cli" deploy site --tag v1)"
first_release="${first_deploy%% *}"
first_metadata_dir="$docroot/.wpcloud-site-git-deploy/deployments/site/metadata/$first_release"
[[ -d "$first_metadata_dir" ]] || fail "release metadata should be stored as a directory"
[[ ! -e "$docroot/.wpcloud-site-git-deploy/deployments/site/metadata/$first_release.env" ]] || fail "release metadata should not use .env files"
[[ "$(cat "$first_metadata_dir/cfg-release_id")" == "$first_release" ]] || fail "metadata should store release id as a value file"
[[ "$(cat "$first_metadata_dir/cfg-ref_mode")" == "tag" ]] || fail "metadata should store ref mode as a value file"
[[ "$(cat "$first_metadata_dir/cfg-ref_value")" == "v1" ]] || fail "metadata should store ref value as a value file"
[[ "$(cat "$first_metadata_dir/cfg-commit")" == "$main_commit" ]] || fail "metadata should store commit as a value file"
[[ -f "$first_metadata_dir/cfg-deployed_at" ]] || fail "metadata should store deployed_at as a value file"
grep -Fx 'hello from main' "$docroot/index.html" >/dev/null || fail "tag deploy did not publish v1 content"
[[ ! -e "$docroot/.gitignore" ]] || fail ".gitignore should be excluded by default"
[[ ! -e "$docroot/.gitattributes" ]] || fail ".gitattributes should be excluded by default"
[[ ! -e "$docroot/.gitmodules" ]] || fail ".gitmodules should be excluded by default"
[[ ! -e "$docroot/.DS_Store" ]] || fail ".DS_Store should be excluded by default"
[[ -L "$docroot/app-link.txt" ]] || fail "repo symlink should be deployed through public claim"
[[ -L "$docroot/index.html" ]] || fail "published index should be a symlink"
index_target="$(readlink "$docroot/index.html")"
[[ "$index_target" == .wpcloud-site-git-deploy/deployments/site/current/index.html ]] || fail "unexpected public symlink target: $index_target"
printf '%s\n' "$index_target" >"$tmpdir/index-target.txt"
assert_not_contains "$home_dir" "$tmpdir/index-target.txt"

conflict_home_dir="$tmpdir/conflict-home"
mkdir -p "$conflict_home_dir"
HOME="$conflict_home_dir" "$cli" init conflict \
  --repo "$source_repo" \
  --docroot "$docroot" \
  --deployment-id conflict \
  --default-ref main >/dev/null
if HOME="$conflict_home_dir" "$cli" deploy conflict --tag v1 >"$tmpdir/conflict-deploy.txt" 2>&1; then
  fail "foreign-claim promotion should fail"
fi
assert_contains "claim owned by another deployment:" "$tmpdir/conflict-deploy.txt"
[[ ! -d "$conflict_home_dir/.wpcloud-site-git-deploy/tmp/conflict" ]] || fail "failed promotion should remove temp worktree"
if [[ -d "$docroot/.wpcloud-site-git-deploy/deployments/conflict/incoming" ]]; then
  conflict_incoming_count="$(find "$docroot/.wpcloud-site-git-deploy/deployments/conflict/incoming" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
  [[ "$conflict_incoming_count" == "0" ]] || fail "failed promotion should remove incoming release"
fi
conflict_repo_cache="$conflict_home_dir/.wpcloud-site-git-deploy/repos/conflict"
if git -C "$conflict_repo_cache" worktree list --porcelain | grep -Fq "$conflict_home_dir/.wpcloud-site-git-deploy/tmp/conflict/"; then
  fail "failed promotion should remove temp worktree from git registry"
fi

second_deploy="$(HOME="$home_dir" "$cli" deploy site --branch feature)"
second_release="${second_deploy%% *}"
grep -Fx 'hello from feature' "$docroot/index.html" >/dev/null || fail "branch deploy did not publish feature content"
first_asset="$docroot/.wpcloud-site-git-deploy/deployments/site/releases/$first_release/assets/app.txt"
second_asset="$docroot/.wpcloud-site-git-deploy/deployments/site/releases/$second_release/assets/app.txt"
if supports_rsync_link_dest "$docroot"; then
  [[ "$(inode_of "$first_asset")" == "$(inode_of "$second_asset")" ]] || fail "unchanged asset should be hardlinked across releases"
else
  echo "rsync --link-dest hardlink reuse not supported in test docroot; skipping hardlink inode assertion" >&2
fi
first_index="$docroot/.wpcloud-site-git-deploy/deployments/site/releases/$first_release/index.html"
second_index="$docroot/.wpcloud-site-git-deploy/deployments/site/releases/$second_release/index.html"
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

cat >"$docroot/.maintenance" <<'EOF'
wpcloud-site-git-deploy maintenance
deployment_id=site
EOF
HOME="$home_dir" "$cli" rollback site >/dev/null
grep -Fx 'hello from main' "$docroot/index.html" >/dev/null || fail "rollback did not restore prior release"
[[ ! -e "$docroot/.maintenance" ]] || fail "rollback should remove stale tool-owned maintenance file"
if HOME="$home_dir" "$cli" rollback site --to missing-release >"$tmpdir/rollback-missing.txt" 2>&1; then
  fail "rollback to missing release should fail"
fi
assert_contains "rollback release does not exist" "$tmpdir/rollback-missing.txt"
assert_not_contains "rolled back to missing-release" "$tmpdir/rollback-missing.txt"
HOME="$home_dir" "$cli" status site >"$tmpdir/status-after-failed-rollback.txt"
assert_contains "name=site" "$tmpdir/status-after-failed-rollback.txt"
assert_contains "keep_releases=3" "$tmpdir/status-after-failed-rollback.txt"

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
assert_contains "clone $source_repo $repo_cache|ssh -i $home_dir/.wpcloud-site-git-deploy/keys/site_ed25519 -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new" "$ssh_env_log"
assert_contains "-C $repo_cache fetch --tags --prune origin|ssh -i $home_dir/.wpcloud-site-git-deploy/keys/site_ed25519 -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new" "$ssh_env_log"
assert_contains "submodule update --init --recursive|ssh -i $home_dir/.wpcloud-site-git-deploy/keys/site_ed25519 -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new" "$ssh_env_log"

late_deploy="$(HOME="$home_dir" "$cli" update site)"
late_release="${late_deploy%% *}"
grep -Fx 'hello from late main' "$docroot/index.html" >/dev/null || fail "update should deploy late main content before no-op"
noop_before_count="$(find "$docroot/.wpcloud-site-git-deploy/deployments/site/releases" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
noop_output="$(HOME="$home_dir" "$cli" update site)"
noop_after_count="$(find "$docroot/.wpcloud-site-git-deploy/deployments/site/releases" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
[[ "$noop_before_count" == "$noop_after_count" ]] || fail "no-op update should not create a new release"
case "$noop_output" in
  "no-op $late_release branch $late_commit") ;;
  *) fail "unexpected no-op output: $noop_output" ;;
esac
force_update_output="$(HOME="$home_dir" "$cli" update site --force)"
force_update_release="${force_update_output%% *}"
[[ "$force_update_release" != "$late_release" ]] || fail "update --force should create a new release id"
[[ -d "$docroot/.wpcloud-site-git-deploy/deployments/site/releases/$force_update_release" ]] || fail "update --force should create a promoted release"
case "$force_update_output" in
  "$force_update_release branch $late_commit") ;;
  *) fail "unexpected force update output: $force_update_output" ;;
esac
force_deploy_output="$(HOME="$home_dir" "$cli" deploy site --branch main --force)"
force_deploy_release="${force_deploy_output%% *}"
[[ "$force_deploy_release" != "$force_update_release" ]] || fail "deploy --force should create a new release id"
case "$force_deploy_output" in
  "$force_deploy_release branch $late_commit") ;;
  *) fail "unexpected force deploy output: $force_deploy_output" ;;
esac

post_marker="$tmpdir/post-deploy-marker.txt"
configured_hook="$tmpdir/configured-post-deploy.sh"
override_hook="$tmpdir/override-post-deploy.sh"
failing_hook="$tmpdir/failing-post-deploy.sh"
no_maintenance_hook="$tmpdir/no-maintenance-post-deploy.sh"
cat >"$configured_hook" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .maintenance
grep -Fx '<?php' .maintenance >/dev/null
grep -F '\$upgrading = ' .maintenance >/dev/null
grep -Fx '// wpcloud-site-git-deploy maintenance' .maintenance >/dev/null
printf 'configured:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$post_marker"
SH
cat >"$override_hook" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .maintenance
grep -Fx '<?php' .maintenance >/dev/null
grep -F '\$upgrading = ' .maintenance >/dev/null
grep -Fx '// wpcloud-site-git-deploy maintenance' .maintenance >/dev/null
printf 'override:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$post_marker"
SH
cat >"$failing_hook" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .maintenance
grep -Fx '<?php' .maintenance >/dev/null
grep -F '\$upgrading = ' .maintenance >/dev/null
grep -Fx '// wpcloud-site-git-deploy maintenance' .maintenance >/dev/null
printf 'failing:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$post_marker"
exit 23
SH
cat >"$no_maintenance_hook" <<SH
#!/usr/bin/env bash
set -euo pipefail
test ! -e .maintenance
printf 'no-maintenance:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$post_marker"
SH
chmod +x "$configured_hook" "$override_hook" "$failing_hook" "$no_maintenance_hook"
if HOME="$home_dir" "$cli" config site --post-deploy "" >"$tmpdir/empty-post-deploy.out" 2>"$tmpdir/empty-post-deploy.err"; then
  fail "empty post-deploy config should fail"
fi
assert_contains "post-deploy path must not be empty" "$tmpdir/empty-post-deploy.err"
if HOME="$home_dir" "$cli" config site --maintenance-file "" >"$tmpdir/empty-maintenance-file.out" 2>"$tmpdir/empty-maintenance-file.err"; then
  fail "empty maintenance-file config should fail"
fi
assert_contains "maintenance-file must be a safe relative path or none" "$tmpdir/empty-maintenance-file.err"
HOME="$home_dir" "$cli" config site --post-deploy "$configured_hook" >/dev/null
HOME="$home_dir" "$cli" status site >"$tmpdir/status-post-deploy.txt"
assert_contains "post_deploy=$configured_hook" "$tmpdir/status-post-deploy.txt"
assert_contains "maintenance_file=.maintenance" "$tmpdir/status-post-deploy.txt"
printf 'post deploy content\n' >"$source_repo/index.html"
git -C "$source_repo" add index.html
git -C "$source_repo" commit -m "post deploy content" >/dev/null
HOME="$home_dir" "$cli" update site >/dev/null
assert_contains "configured:$docroot:post deploy content" "$post_marker"
[[ ! -e "$docroot/.maintenance" ]] || fail "successful post-deploy should remove maintenance file"
configured_runs_before="$(grep -c '^configured:' "$post_marker")"
HOME="$home_dir" "$cli" update site --force >/dev/null
configured_runs_after="$(grep -c '^configured:' "$post_marker")"
[[ "$configured_runs_after" -eq $((configured_runs_before + 1)) ]] || fail "configured post-deploy should run again on update --force"
[[ ! -e "$docroot/.maintenance" ]] || fail "forced update should remove maintenance file"
HOME="$home_dir" "$cli" update site --force --post-deploy "$override_hook" >/dev/null
assert_contains "override:$docroot:post deploy content" "$post_marker"
[[ ! -e "$docroot/.maintenance" ]] || fail "post-deploy override should remove maintenance file"
override_runs="$(grep -c '^override:' "$post_marker")"
configured_runs_after_override="$(grep -c '^configured:' "$post_marker")"
[[ "$override_runs" == "1" ]] || fail "--post-deploy override should run exactly once"
[[ "$configured_runs_after_override" == "$configured_runs_after" ]] || fail "--post-deploy override should not also run configured hook"
before_failing_current="$(HOME="$home_dir" "$cli" status site | awk -F= '/^current=/{print $2}')"
if HOME="$home_dir" "$cli" update site --force --post-deploy "$failing_hook" >"$tmpdir/failing-post-deploy.out" 2>"$tmpdir/failing-post-deploy.err"; then
  fail "failing post-deploy should make update exit nonzero"
fi
after_failing_current="$(HOME="$home_dir" "$cli" status site | awk -F= '/^current=/{print $2}')"
[[ -n "$after_failing_current" && "$after_failing_current" != "$before_failing_current" ]] || fail "failing post-deploy should leave newly promoted release current"
assert_contains "failing:$docroot:post deploy content" "$post_marker"
assert_contains "post-deploy failed: $failing_hook" "$tmpdir/failing-post-deploy.err"
[[ ! -e "$docroot/.maintenance" ]] || fail "failing post-deploy should remove tool-owned maintenance file"
HOME="$home_dir" "$cli" config site --maintenance-file none >/dev/null
HOME="$home_dir" "$cli" status site >"$tmpdir/status-maintenance-none.txt"
assert_contains "maintenance_file=none" "$tmpdir/status-maintenance-none.txt"
HOME="$home_dir" "$cli" update site --force --post-deploy "$no_maintenance_hook" >/dev/null
assert_contains "no-maintenance:$docroot:post deploy content" "$post_marker"
[[ ! -e "$docroot/.maintenance" ]] || fail "maintenance-file none should not create maintenance file"
HOME="$home_dir" "$cli" config site --clear-post-deploy >/dev/null
HOME="$home_dir" "$cli" status site >"$tmpdir/status-post-deploy-cleared.txt"
assert_contains "post_deploy=" "$tmpdir/status-post-deploy-cleared.txt"
cleared_runs_before="$(wc -l <"$post_marker" | tr -d ' ')"
HOME="$home_dir" "$cli" update site --force >/dev/null
cleared_runs_after="$(wc -l <"$post_marker" | tr -d ' ')"
[[ "$cleared_runs_after" == "$cleared_runs_before" ]] || fail "cleared post-deploy config should stop automatic hook execution"
HOME="$home_dir" "$cli" config site --maintenance-file .maintenance >/dev/null

blocking_hook="$tmpdir/blocking-post-deploy.sh"
blocking_ready="$tmpdir/blocking-ready"
blocking_release="$tmpdir/blocking-release"
blocking_marker="$tmpdir/blocking-marker"
cat >"$blocking_hook" <<SH
#!/usr/bin/env bash
set -euo pipefail
printf 'ready\n' >"$blocking_ready"
while [[ ! -e "$blocking_release" ]]; do
  sleep 0.1
done
printf 'blocked:%s:%s\n' "\$PWD" "\$(cat index.html)" >"$blocking_marker"
SH
chmod +x "$blocking_hook"
printf 'concurrent deploy content\n' >"$source_repo/index.html"
git -C "$source_repo" add index.html
git -C "$source_repo" commit -m "concurrent deploy content" >/dev/null
(
  set +e
  HOME="$home_dir" "$cli" update site --force --post-deploy "$blocking_hook" >"$tmpdir/blocking-update.out" 2>"$tmpdir/blocking-update.err"
  printf '%s\n' "$?" >"$tmpdir/blocking-update.status"
) &
blocking_pid="$!"
for _ in {1..100}; do
  [[ -e "$blocking_ready" ]] && break
  sleep 0.1
done
if [[ ! -e "$blocking_ready" ]]; then
  touch "$blocking_release"
  wait "$blocking_pid" || true
  fail "blocking post-deploy did not start"
fi
if HOME="$home_dir" timeout 5 "$cli" update site --force >"$tmpdir/concurrent-update.out" 2>"$tmpdir/concurrent-update.err"; then
  touch "$blocking_release"
  wait "$blocking_pid" || true
  fail "concurrent deployment should fail while another deployment is running"
fi
assert_contains "deployment already running" "$tmpdir/concurrent-update.err"
[[ -e "$docroot/.maintenance" ]] || fail "concurrent failed deployment should not remove active maintenance file"
touch "$blocking_release"
wait "$blocking_pid"
blocking_status="$(cat "$tmpdir/blocking-update.status")"
if [[ "$blocking_status" != "0" ]]; then
  cat "$tmpdir/blocking-update.err" >&2
  fail "blocking deployment should complete after release"
fi
assert_contains "blocked:$docroot:concurrent deploy content" "$blocking_marker"
[[ ! -e "$docroot/.maintenance" ]] || fail "blocking deployment should remove maintenance file"

tampered_release="$(HOME="$home_dir" "$cli" status site | awk -F= '/^current=/{print $2}')"
tampered_commit="$(HOME="$home_dir" "$cli" releases site | awk '$1 == "'"$tampered_release"'" { if ($2 == "current") print $3; else print $2 }')"
metadata_dir="$docroot/.wpcloud-site-git-deploy/deployments/site/metadata/$tampered_release"
tamper_marker="$tmpdir/metadata-executed"
printf '%s\n' "$tampered_commit" >"$metadata_dir/cfg-commit"
printf '%s\n' branch >"$metadata_dir/cfg-ref_mode"
printf '%s\n' main >"$metadata_dir/cfg-ref_value"
: >"$metadata_dir/cfg-deploy_root"
printf 'touch %q\n' "$tamper_marker" >"$metadata_dir/cfg-extra"
HOME="$home_dir" "$cli" update site >"$tmpdir/noop-tampered.txt"
assert_contains "no-op $tampered_release branch $tampered_commit" "$tmpdir/noop-tampered.txt"
[[ ! -e "$tamper_marker" ]] || fail "metadata parser should not execute shell from no-op path"
HOME="$home_dir" "$cli" releases site >"$tmpdir/releases-tampered.txt"
assert_contains "$tampered_commit branch:main" "$tmpdir/releases-tampered.txt"
[[ ! -e "$tamper_marker" ]] || fail "metadata parser should not execute shell from releases path"

root_source_repo="$tmpdir/root-source"
root_home_dir="$tmpdir/root-home"
root_docroot="$tmpdir/root-docroot"
mkdir -p "$root_source_repo/public/wp-content/themes/demo" "$root_home_dir" "$root_docroot"
git -C "$root_source_repo" init -b main >/dev/null
git -C "$root_source_repo" config user.name "WP Cloud Deploy Test"
git -C "$root_source_repo" config user.email "wpcloud-deploy-test@example.invalid"
printf 'root only\n' >"$root_source_repo/README.md"
printf 'from public\n' >"$root_source_repo/public/index.html"
printf 'theme file\n' >"$root_source_repo/public/wp-content/themes/demo/style.css"
git -C "$root_source_repo" add .
git -C "$root_source_repo" commit -m "subfolder root" >/dev/null
root_commit="$(git -C "$root_source_repo" rev-parse HEAD)"

HOME="$root_home_dir" "$cli" init root-site \
  --repo "$root_source_repo" \
  --docroot "$root_docroot" \
  --deployment-id root-site \
  --default-ref main \
  --deploy-root public >/dev/null
HOME="$root_home_dir" "$cli" update root-site >/dev/null
grep -Fx 'from public' "$root_docroot/index.html" >/dev/null || fail "deploy-root should publish subfolder index at docroot root"
grep -Fx 'theme file' "$root_docroot/wp-content/themes/demo/style.css" >/dev/null || fail "deploy-root should preserve paths under subfolder"
[[ ! -e "$root_docroot/public/index.html" ]] || fail "deploy-root should not publish subfolder name"
[[ ! -e "$root_docroot/README.md" ]] || fail "deploy-root should not publish repo root files"
HOME="$root_home_dir" "$cli" releases root-site >"$tmpdir/root-releases.txt"
assert_contains "$root_commit" "$tmpdir/root-releases.txt"
assert_contains "deploy-root:public" "$tmpdir/root-releases.txt"
HOME="$root_home_dir" "$cli" status root-site >"$tmpdir/root-status-public.txt"
assert_contains "deploy_root=public" "$tmpdir/root-status-public.txt"

HOME="$root_home_dir" "$cli" config root-site --deploy-root missing >/dev/null
if HOME="$root_home_dir" "$cli" update root-site >"$tmpdir/missing-root.txt" 2>&1; then
  fail "missing deploy root should fail"
fi
assert_contains "deploy root does not exist or is not a directory: missing" "$tmpdir/missing-root.txt"
if HOME="$root_home_dir" "$cli" config root-site --deploy-root ../outside >"$tmpdir/bad-root.txt" 2>&1; then
  fail "unsafe deploy root should fail"
fi
assert_contains "deploy-root must be a safe relative path" "$tmpdir/bad-root.txt"
HOME="$root_home_dir" "$cli" config root-site --clear-deploy-root >/dev/null
HOME="$root_home_dir" "$cli" status root-site >"$tmpdir/root-status-cleared.txt"
assert_contains "deploy_root=" "$tmpdir/root-status-cleared.txt"
root_count_before_clear_deploy="$(find "$root_docroot/.wpcloud-site-git-deploy/deployments/root-site/releases" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
HOME="$root_home_dir" "$cli" update root-site >/dev/null
root_count_after_clear_deploy="$(find "$root_docroot/.wpcloud-site-git-deploy/deployments/root-site/releases" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
[[ "$root_count_after_clear_deploy" -gt "$root_count_before_clear_deploy" ]] || fail "changing deploy root should deploy the same commit again"
grep -Fx 'root only' "$root_docroot/README.md" >/dev/null || fail "clearing deploy-root should publish repo root files"

lfs_source_repo="$tmpdir/lfs-source"
lfs_home_dir="$tmpdir/lfs-home"
lfs_docroot="$tmpdir/lfs-docroot"
mkdir -p "$lfs_source_repo" "$lfs_home_dir" "$lfs_docroot"

cat >"$fake_bin/git-lfs" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf 'git-lfs %s GIT_SSH_COMMAND=%s\n' "$*" "${GIT_SSH_COMMAND:-}" >>"${WPCLOUD_TEST_LFS_LOG:?}"
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
export WPCLOUD_TEST_LFS_LOG="$tmpdir/git-lfs.log"

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
mkdir -p "$lfs_home_dir/.wpcloud-site-git-deploy/keys"
printf 'PRIVATE KEY\n' >"$lfs_home_dir/.wpcloud-site-git-deploy/keys/lfs-site_ed25519"
printf 'ssh-ed25519 PUBLICKEY lfs\n' >"$lfs_home_dir/.wpcloud-site-git-deploy/keys/lfs-site_ed25519.pub"
chmod 600 "$lfs_home_dir/.wpcloud-site-git-deploy/keys/lfs-site_ed25519"
{
  printf '%s\n' "$lfs_home_dir/.wpcloud-site-git-deploy/keys/lfs-site_ed25519"
} >"$lfs_home_dir/.wpcloud-site-git-deploy/deployments/lfs-site/cfg-ssh_key_path"
HOME="$lfs_home_dir" "$cli" update lfs-site >/dev/null
grep -Fx 'hydrated lfs content' "$lfs_docroot/media.bin" >/dev/null || fail "LFS file should be hydrated by git-lfs pull"
grep -Fx 'version https://git-lfs.github.com/spec/v1' "$lfs_docroot/notes.txt" >/dev/null || fail "non-LFS pointer-shaped file should not fail deploy"
assert_contains "git-lfs pull GIT_SSH_COMMAND=ssh -i $lfs_home_dir/.wpcloud-site-git-deploy/keys/lfs-site_ed25519 -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new" "$tmpdir/git-lfs.log"
