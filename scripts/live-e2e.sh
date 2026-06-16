#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="${WPCLOUD_SITE_GIT_DEPLOY_FIXTURE:-$repo_root-fixture}"
layer_fixture="${WPCLOUD_SITE_GIT_DEPLOY_LAYER_FIXTURE:-$repo_root-layer-fixture}"
evidence="${WPCLOUD_SITE_GIT_DEPLOY_E2E_EVIDENCE:-$repo_root/tmp/live-e2e-evidence.md}"
known_hosts="${WPCLOUD_SITE_GIT_DEPLOY_KNOWN_HOSTS:-/tmp/wpcloud-site-git-deploy-known-hosts}"
bundle="${WPCLOUD_SITE_GIT_DEPLOY_E2E_BUNDLE:-$repo_root/tmp/live-e2e-source.tar.gz}"

if [[ -f "$repo_root/.env.local" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$repo_root/.env.local"
  set +a
fi

: "${WPCLOUD_CLI_SSH_HOST:?set WPCLOUD_CLI_SSH_HOST in .env.local or the environment}"
: "${WPCLOUD_CLI_SSH_PORT:?set WPCLOUD_CLI_SSH_PORT in .env.local or the environment}"
: "${WPCLOUD_CLI_SSH_USERNAME:?set WPCLOUD_CLI_SSH_USERNAME in .env.local or the environment}"
: "${WPCLOUD_CLI_SSH_PASSWORD:?set WPCLOUD_CLI_SSH_PASSWORD in .env.local or the environment}"

ssh_host="$WPCLOUD_CLI_SSH_HOST"
ssh_port="$WPCLOUD_CLI_SSH_PORT"
ssh_user="$WPCLOUD_CLI_SSH_USERNAME"
ssh_pass="$WPCLOUD_CLI_SSH_PASSWORD"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'required command not found: %s\n' "$1" >&2
    exit 1
  }
}

require_command expect
require_command git
require_command tar
require_command gh

mkdir -p "$(dirname "$evidence")" "$(dirname "$bundle")"
printf '# Live E2E Evidence\n\n' >"$evidence"

log() {
  printf '%s\n' "$*"
  printf '%s\n' "$*" >>"$evidence"
}

remote_script() {
  local label="$1"
  local script_file="$repo_root/tmp/live-e2e-remote-$label.sh"
  local remote_file="/tmp/wpcloud-site-git-deploy-live-$label.sh"
  cat >"$script_file"
  chmod 700 "$script_file"

  SCRIPT_FILE="$script_file" REMOTE_FILE="$remote_file" SSH_HOST="$ssh_host" SSH_PORT="$ssh_port" SSH_USER="$ssh_user" SSH_PASS="$ssh_pass" KNOWN_HOSTS="$known_hosts" expect <<'EXPECT'
set timeout 180
spawn scp -P $env(SSH_PORT) -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$env(KNOWN_HOSTS) $env(SCRIPT_FILE) $env(SSH_USER)@$env(SSH_HOST):$env(REMOTE_FILE)
expect {
  -re "(?i)password:" { send "$env(SSH_PASS)\r"; exp_continue }
  eof
}
catch wait result
exit [lindex $result 3]
EXPECT

  REMOTE_FILE="$remote_file" SSH_HOST="$ssh_host" SSH_PORT="$ssh_port" SSH_USER="$ssh_user" SSH_PASS="$ssh_pass" KNOWN_HOSTS="$known_hosts" expect <<'EXPECT'
set timeout 420
spawn ssh -p $env(SSH_PORT) -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$env(KNOWN_HOSTS) $env(SSH_USER)@$env(SSH_HOST) "bash $env(REMOTE_FILE)"
expect {
  -re "(?i)password:" { send "$env(SSH_PASS)\r"; exp_continue }
  eof
}
catch wait result
exit [lindex $result 3]
EXPECT
}

copy_bundle() {
  BUNDLE="$bundle" SSH_HOST="$ssh_host" SSH_PORT="$ssh_port" SSH_USER="$ssh_user" SSH_PASS="$ssh_pass" KNOWN_HOSTS="$known_hosts" expect <<'EXPECT'
set timeout 180
spawn scp -P $env(SSH_PORT) -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$env(KNOWN_HOSTS) $env(BUNDLE) $env(SSH_USER)@$env(SSH_HOST):/tmp/wpcloud-site-git-deploy-live-source.tar.gz
expect {
  -re "(?i)password:" { send "$env(SSH_PASS)\r"; exp_continue }
  eof
}
catch wait result
exit [lindex $result 3]
EXPECT
}

commit_fixture() {
  local message="$1"
  git -C "$fixture" add -A
  git -C "$fixture" commit -m "$message" >/dev/null
  git -C "$fixture" push origin main >/dev/null
  git -C "$fixture" rev-parse HEAD
}

reset_fixture_tree() {
  git -C "$fixture" switch main >/dev/null
  git -C "$fixture" pull --ff-only origin main >/dev/null
  git -C "$fixture" config user.name "WP Cloud CLI Fixture"
  git -C "$fixture" config user.email "wpcloud-cli-fixture@example.invalid"
  find "$fixture" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
}

deploy_update() {
  local label="$1"
  shift
  local assertions="$*"
  log "## $label"
  remote_script "$label" <<REMOTE
set -euo pipefail
export PATH="\$HOME/.local/bin:\$HOME/.wpcloud-site-git-deploy/bin:\$PATH"
wpcloud-site-git-deploy update cli-test
$assertions
test ! -e /srv/htdocs/.maintenance
REMOTE
  log "- deployed $(git -C "$fixture" rev-parse --short HEAD)"
}

expect_update_failure() {
  local label="$1"
  local expected="$2"
  log "## $label"
  remote_script "$label" <<REMOTE
set -euo pipefail
export PATH="\$HOME/.local/bin:\$HOME/.wpcloud-site-git-deploy/bin:\$PATH"
before=\$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print \$2}')
if wpcloud-site-git-deploy update cli-test > /tmp/$label.out 2> /tmp/$label.err; then
  cat /tmp/$label.out
  cat /tmp/$label.err >&2
  echo "expected update failure for $label" >&2
  exit 1
fi
grep -F "$expected" /tmp/$label.err
after=\$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print \$2}')
test "\$before" = "\$after"
test ! -e /srv/htdocs/.maintenance
REMOTE
  log "- failed as expected: $expected"
}

verify_owned_symlinks() {
  remote_script verify-owned-symlinks <<'REMOTE'
set -euo pipefail
links_file=/tmp/cli-test-owned-links.$$.txt
find /srv/htdocs -type l -printf '%p|%l\n' | grep '.wpcloud-site-git-deploy/deployments/cli-test/current' > "$links_file"
test -s "$links_file"
while IFS='|' read -r path target; do
  case "$target" in
    /*)
      echo "absolute target: $path -> $target" >&2
      exit 1
      ;;
  esac
  case "$target" in
    *"$HOME"*)
      echo "HOME target: $path -> $target" >&2
      exit 1
      ;;
  esac
  resolved=$(cd "$(dirname "$path")" && readlink -f "$target")
  case "$resolved" in
    /srv/htdocs/*)
      ;;
    *)
      echo "outside docroot: $path -> $resolved" >&2
      exit 1
      ;;
  esac
done < "$links_file"
rm -f "$links_file"
REMOTE
}

log "## bundle"
COPYFILE_DISABLE=1 tar \
  --exclude='.git' \
  --exclude='.env.local' \
  --exclude='tmp' \
  -czf "$bundle" \
  -C "$repo_root" \
  .
copy_bundle
log "- copied current checkout bundle to throwaway site"

remote_script install-current <<'REMOTE'
set -euo pipefail
rm -rf /tmp/wpcloud-site-git-deploy-live-source
mkdir -p /tmp/wpcloud-site-git-deploy-live-source
tar -xzf /tmp/wpcloud-site-git-deploy-live-source.tar.gz -C /tmp/wpcloud-site-git-deploy-live-source
/tmp/wpcloud-site-git-deploy-live-source/scripts/install.sh >/tmp/wpcloud-site-git-deploy-live-install.log
find /srv/htdocs -type l \( \
  -lname '*.wpcloud-site-git-deploy/deployments/cli-test/current*' -o \
  -lname '*.wpcloud-site-git-deploy/deployments/cli-layer/current*' -o \
  -lname '*.wpcloud-site-git-deploy/deployments/cli-auth/current*' -o \
  -lname '*.github-ssh-deploy/deployments/cli-test/current*' -o \
  -lname '*.github-ssh-deploy/deployments/cli-layer/current*' -o \
  -lname '*.github-ssh-deploy/deployments/cli-auth/current*' \
\) -delete 2>/dev/null || true
rm -rf \
  /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test \
  /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-layer \
  /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-auth \
  /srv/htdocs/.github-ssh-deploy/deployments/cli-test \
  /srv/htdocs/.github-ssh-deploy/deployments/cli-layer \
  /srv/htdocs/.github-ssh-deploy/deployments/cli-auth \
  "$HOME/.wpcloud-site-git-deploy/deployments/cli-test.env" \
  "$HOME/.wpcloud-site-git-deploy/deployments/cli-layer.env" \
  "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env" \
  "$HOME/.wpcloud-site-git-deploy/repos/cli-test" \
  "$HOME/.wpcloud-site-git-deploy/repos/cli-layer" \
  "$HOME/.wpcloud-site-git-deploy/repos/cli-auth" \
  "$HOME/.wpcloud-site-git-deploy/tmp/cli-test" \
  "$HOME/.wpcloud-site-git-deploy/tmp/cli-layer" \
  "$HOME/.wpcloud-site-git-deploy/tmp/cli-auth"
rm -f /srv/htdocs/.maintenance
mkdir -p "$HOME/.local/bin"
if ! command -v git-lfs >/dev/null 2>&1; then
  tmp=/tmp/git-lfs-install.$$
  rm -rf "$tmp"
  mkdir -p "$tmp"
  cd "$tmp"
  curl -fsSL https://github.com/git-lfs/git-lfs/releases/download/v3.7.1/git-lfs-linux-amd64-v3.7.1.tar.gz -o git-lfs.tar.gz
  tar -xzf git-lfs.tar.gz
  cp git-lfs-3.7.1/git-lfs "$HOME/.local/bin/git-lfs"
  chmod 755 "$HOME/.local/bin/git-lfs"
  rm -rf "$tmp"
fi
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy --version
wpcloud-site-git-deploy init cli-test --repo https://github.com/aipokalyptik/wpcloud-site-git-deploy-fixture.git --docroot /srv/htdocs --deployment-id cli-test --default-ref main --keep-releases 5
wpcloud-site-git-deploy init cli-auth --repo https://github.com/aipokalyptik/wpcloud-site-git-deploy-fixture.git --docroot /srv/htdocs --deployment-id cli-auth --default-ref main --keep-releases 2
REMOTE
log "## install-current"
log "- installed current checkout on throwaway site"

reset_fixture_tree
mkdir -p "$fixture/wp-content/plugins/cli-fixture" "$fixture/wp-content/themes/cli-theme" "$fixture/wp-content/mu-plugins/cli-mu" "$fixture/assets" "$fixture/.well-known" "$fixture/.github/workflows" "$fixture/public-root/wp-content/themes/root-theme" "$fixture/public-root/nested"
printf 'baseline\n' >"$fixture/index.html"
printf 'plugin baseline\n' >"$fixture/wp-content/plugins/cli-fixture/plugin.php"
printf 'theme baseline\n' >"$fixture/wp-content/themes/cli-theme/style.css"
printf 'mu baseline\n' >"$fixture/wp-content/mu-plugins/cli-mu/cli-mu.php"
printf 'asset baseline\n' >"$fixture/assets/app.txt"
printf 'deny from all\n' >"$fixture/.htaccess"
printf 'security baseline\n' >"$fixture/.well-known/security.txt"
printf '.env\n' >"$fixture/.gitignore"
printf 'secret should not deploy\n' >"$fixture/.env"
printf 'workflow should not deploy\n' >"$fixture/.github/workflows/ci.yml"
printf 'root feature v1\n' >"$fixture/public-root/root-feature.txt"
printf 'root theme v1\n' >"$fixture/public-root/wp-content/themes/root-theme/style.css"
printf 'root nested v1\n' >"$fixture/public-root/nested/file.txt"
printf 'repo root visible\n' >"$fixture/root-only.txt"
sha="$(commit_fixture "E2E 01 baseline deployable tree")"
deploy_update e2e-01-baseline "grep -Fx 'baseline' /srv/htdocs/index.html; grep -Fx 'plugin baseline' /srv/htdocs/wp-content/plugins/cli-fixture/plugin.php; grep -Fx 'theme baseline' /srv/htdocs/wp-content/themes/cli-theme/style.css; grep -Fx 'mu baseline' /srv/htdocs/wp-content/mu-plugins/cli-mu/cli-mu.php; test ! -e /srv/htdocs/.env; test ! -e /srv/htdocs/.github"
log "- commit $sha"

remote_script e2e-02-noop-and-force <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
before_count=$(find /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test/releases -mindepth 1 -maxdepth 1 -type d | wc -l)
before_current=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
noop=$(wpcloud-site-git-deploy update cli-test)
after_count=$(find /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test/releases -mindepth 1 -maxdepth 1 -type d | wc -l)
after_current=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
case "$noop" in
  "no-op $before_current branch "*)
    ;;
  *)
    echo "unexpected no-op output: $noop" >&2
    exit 1
    ;;
esac
test "$before_count" = "$after_count"
test "$before_current" = "$after_current"
forced_update=$(wpcloud-site-git-deploy update cli-test --force)
forced_update_current=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
test "$forced_update_current" != "$after_current"
case "$forced_update" in
  no-op*)
    echo "update --force unexpectedly no-oped: $forced_update" >&2
    exit 1
    ;;
esac
forced_deploy=$(wpcloud-site-git-deploy deploy cli-test --branch main --force)
forced_deploy_current=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
test "$forced_deploy_current" != "$forced_update_current"
case "$forced_deploy" in
  no-op*)
    echo "deploy --force unexpectedly no-oped: $forced_deploy" >&2
    exit 1
    ;;
esac
test ! -e /srv/htdocs/.maintenance
REMOTE
log "## e2e-02-noop-and-force"
log "- no-op update preserved current release; update --force and deploy --force promoted new releases"

remote_script e2e-03-deploy-root <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy config cli-test --deploy-root public-root
wpcloud-site-git-deploy status cli-test | grep -Fx 'deploy_root=public-root'
wpcloud-site-git-deploy update cli-test
grep -Fx 'root feature v1' /srv/htdocs/root-feature.txt
grep -Fx 'root theme v1' /srv/htdocs/wp-content/themes/root-theme/style.css
grep -Fx 'root nested v1' /srv/htdocs/nested/file.txt
test ! -e /srv/htdocs/public-root/root-feature.txt
test ! -e /srv/htdocs/root-only.txt
wpcloud-site-git-deploy releases cli-test | grep 'deploy-root:public-root'
if wpcloud-site-git-deploy config cli-test --deploy-root ../outside > /tmp/live-bad-root.out 2>&1; then
  cat /tmp/live-bad-root.out
  echo "unsafe deploy-root was accepted" >&2
  exit 1
fi
grep -F 'deploy-root must be a safe relative path' /tmp/live-bad-root.out
wpcloud-site-git-deploy config cli-test --deploy-root missing-root
before_current=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
if wpcloud-site-git-deploy update cli-test > /tmp/live-missing-root.out 2>&1; then
  cat /tmp/live-missing-root.out
  echo "missing deploy-root update succeeded" >&2
  exit 1
fi
grep -F 'deploy root does not exist or is not a directory: missing-root' /tmp/live-missing-root.out
after_current=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
test "$before_current" = "$after_current"
wpcloud-site-git-deploy config cli-test --clear-deploy-root
wpcloud-site-git-deploy update cli-test
grep -Fx 'repo root visible' /srv/htdocs/root-only.txt
grep -Fx 'root feature v1' /srv/htdocs/public-root/root-feature.txt
wpcloud-site-git-deploy config cli-test --deploy-root public-root
wpcloud-site-git-deploy update cli-test
grep -Fx 'root feature v1' /srv/htdocs/root-feature.txt
test ! -e /srv/htdocs/public-root/root-feature.txt
test ! -e /srv/htdocs/root-only.txt
wpcloud-site-git-deploy config cli-test --clear-deploy-root
REMOTE
log "## e2e-03-deploy-root"
log "- deploy-root positive, invalid, missing, clear, and restore paths verified"

printf 'changed\n' >"$fixture/index.html"
printf 'plugin changed\n' >"$fixture/wp-content/plugins/cli-fixture/plugin.php"
printf 'theme changed\n' >"$fixture/wp-content/themes/cli-theme/style.css"
printf 'mu changed\n' >"$fixture/wp-content/mu-plugins/cli-mu/cli-mu.php"
sha="$(commit_fixture "E2E 04 change existing files")"
deploy_update e2e-04-change "grep -Fx 'changed' /srv/htdocs/index.html; grep -Fx 'plugin changed' /srv/htdocs/wp-content/plugins/cli-fixture/plugin.php; grep -Fx 'theme changed' /srv/htdocs/wp-content/themes/cli-theme/style.css; grep -Fx 'mu changed' /srv/htdocs/wp-content/mu-plugins/cli-mu/cli-mu.php"
log "- commit $sha"

remote_script e2e-05-post-deploy-maintenance <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
marker=/tmp/live-post-deploy-marker.txt
configured=/tmp/live-configured-post-deploy.sh
override=/tmp/live-override-post-deploy.sh
failing=/tmp/live-failing-post-deploy.sh
no_maintenance=/tmp/live-no-maintenance-post-deploy.sh
rm -f "$marker"
cat >"$configured" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .maintenance
grep -Fx '<?php' .maintenance >/dev/null
grep -F '\$upgrading = ' .maintenance >/dev/null
grep -Fx '// wpcloud-site-git-deploy maintenance' .maintenance >/dev/null
grep -Fx '// deployment_id=cli-test' .maintenance >/dev/null
printf 'configured:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$marker"
SH
cat >"$override" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .maintenance
grep -F '\$upgrading = ' .maintenance >/dev/null
printf 'override:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$marker"
SH
cat >"$failing" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .maintenance
grep -F '\$upgrading = ' .maintenance >/dev/null
printf 'failing:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$marker"
exit 42
SH
cat >"$no_maintenance" <<SH
#!/usr/bin/env bash
set -euo pipefail
test ! -e .maintenance
printf 'none:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$marker"
SH
chmod +x "$configured" "$override" "$failing" "$no_maintenance"
wpcloud-site-git-deploy config cli-test --post-deploy "$configured"
wpcloud-site-git-deploy status cli-test | grep -Fx "post_deploy=$configured"
wpcloud-site-git-deploy status cli-test | grep -Fx 'maintenance_file=.maintenance'
wpcloud-site-git-deploy update cli-test --force >/dev/null
grep -F 'configured:/srv/htdocs:changed' "$marker"
test ! -e /srv/htdocs/.maintenance
before_override_count=$(grep -c '^configured:' "$marker")
wpcloud-site-git-deploy update cli-test --force --post-deploy "$override" >/dev/null
after_override_count=$(grep -c '^configured:' "$marker")
test "$before_override_count" = "$after_override_count"
grep -F 'override:/srv/htdocs:changed' "$marker"
test ! -e /srv/htdocs/.maintenance
before_failure_current=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
if wpcloud-site-git-deploy update cli-test --force --post-deploy "$failing" > /tmp/live-failing-post-deploy.out 2> /tmp/live-failing-post-deploy.err; then
  cat /tmp/live-failing-post-deploy.out
  cat /tmp/live-failing-post-deploy.err >&2
  echo "failing post-deploy unexpectedly succeeded" >&2
  exit 1
fi
after_failure_current=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
test "$after_failure_current" != "$before_failure_current"
grep -F "post-deploy failed: $failing" /tmp/live-failing-post-deploy.err
grep -F 'failing:/srv/htdocs:changed' "$marker"
test ! -e /srv/htdocs/.maintenance
wpcloud-site-git-deploy config cli-test --clear-post-deploy
printf 'manual maintenance\n' > /srv/htdocs/.maintenance
wpcloud-site-git-deploy update cli-test --force >/dev/null
grep -Fx 'manual maintenance' /srv/htdocs/.maintenance
rm -f /srv/htdocs/.maintenance
cat > /srv/htdocs/.maintenance <<EOF
<?php
\$upgrading = 1234567890;
// wpcloud-site-git-deploy maintenance
// deployment_id=cli-test
EOF
wpcloud-site-git-deploy rollback cli-test >/dev/null
test ! -e /srv/htdocs/.maintenance
wpcloud-site-git-deploy config cli-test --maintenance-file none
wpcloud-site-git-deploy status cli-test | grep -Fx 'maintenance_file=none'
wpcloud-site-git-deploy update cli-test --force --post-deploy "$no_maintenance" >/dev/null
grep -F 'none:/srv/htdocs:changed' "$marker"
test ! -e /srv/htdocs/.maintenance
wpcloud-site-git-deploy config cli-test --maintenance-file .maintenance
wpcloud-site-git-deploy status cli-test | grep -Fx 'post_deploy='
REMOTE
log "## e2e-05-post-deploy-maintenance"
log "- post-deploy hooks, failure behavior, tool-owned maintenance cleanup, non-owned preservation, rollback cleanup, and maintenance-file none verified"

mkdir -p "$fixture/assets/deep/nested" "$fixture/space dir" "$fixture/punctuation !@#" "$fixture/unicode-雪"
printf 'deep\n' >"$fixture/assets/deep/nested/file.txt"
printf 'space\n' >"$fixture/space dir/file with spaces.txt"
printf 'punctuation\n' >"$fixture/punctuation !@#/file.txt"
printf 'unicode\n' >"$fixture/unicode-雪/☃.txt"
sha="$(commit_fixture "E2E 06 add complex paths")"
deploy_update e2e-06-complex "grep -Fx 'deep' /srv/htdocs/assets/deep/nested/file.txt; grep -Fx 'space' '/srv/htdocs/space dir/file with spaces.txt'; grep -Fx 'punctuation' '/srv/htdocs/punctuation !@#/file.txt'; grep -Fx 'unicode' '/srv/htdocs/unicode-雪/☃.txt'"
log "- commit $sha"

rm -rf "$fixture/punctuation !@#" "$fixture/assets/deep"
sha="$(commit_fixture "E2E 07 remove files and directories")"
deploy_update e2e-07-remove "test ! -e '/srv/htdocs/punctuation !@#'; test ! -e /srv/htdocs/assets/deep; grep -Fx 'space' '/srv/htdocs/space dir/file with spaces.txt'"
log "- commit $sha"

printf 'file version\n' >"$fixture/swap-file-dir"
mkdir -p "$fixture/swap-dir-file"
printf 'dir version\n' >"$fixture/swap-dir-file/value.txt"
sha="$(commit_fixture "E2E 08 add swap fixtures")"
deploy_update e2e-08-swap-add "grep -Fx 'file version' /srv/htdocs/swap-file-dir; grep -Fx 'dir version' /srv/htdocs/swap-dir-file/value.txt"
log "- commit $sha"

rm -f "$fixture/swap-file-dir"
mkdir -p "$fixture/swap-file-dir"
printf 'directory after file\n' >"$fixture/swap-file-dir/value.txt"
rm -rf "$fixture/swap-dir-file"
printf 'file after directory\n' >"$fixture/swap-dir-file"
sha="$(commit_fixture "E2E 09 replace file and directory")"
deploy_update e2e-09-swap-replace "grep -Fx 'directory after file' /srv/htdocs/swap-file-dir/value.txt; grep -Fx 'file after directory' /srv/htdocs/swap-dir-file"
log "- commit $sha"

ln -sf assets/app.txt "$fixture/app-link.txt"
sha="$(commit_fixture "E2E 10 add repo symlink")"
deploy_update e2e-10-symlink "test -L /srv/htdocs/app-link.txt; grep -Fx 'asset baseline' /srv/htdocs/app-link.txt"
log "- commit $sha"

git -C "$fixture" lfs install --local >/dev/null
git -C "$fixture" lfs track 'lfs/*.bin' >/dev/null
mkdir -p "$fixture/lfs"
printf 'real lfs content\n' >"$fixture/lfs/data.bin"
sha="$(commit_fixture "E2E 11 add Git LFS content")"
deploy_update e2e-11-lfs-add "grep -Fx 'real lfs content' /srv/htdocs/lfs/data.bin; ! grep -F 'git-lfs.github.com/spec' /srv/htdocs/lfs/data.bin"
log "- commit $sha"

git -C "$fixture" lfs untrack 'lfs/*.bin' >/dev/null
git -C "$fixture" rm --cached lfs/data.bin >/dev/null
printf 'plain file after lfs removal\n' >"$fixture/lfs/data.bin"
sha="$(commit_fixture "E2E 12 remove Git LFS tracking")"
deploy_update e2e-12-lfs-remove "grep -Fx 'plain file after lfs removal' /srv/htdocs/lfs/data.bin"
log "- commit $sha"

mkdir -p "$fixture/wp-content/uploads"
printf 'user upload should not deploy\n' >"$fixture/wp-content/uploads/file.jpg"
sha="$(commit_fixture "E2E 13 shared uploads rejection")"
expect_update_failure e2e-13-shared-uploads "shared path cannot be deployed: wp-content/uploads"
log "- commit $sha"

rm -rf "$fixture/wp-content/uploads"
printf 'tracked maintenance should not deploy\n' >"$fixture/.maintenance"
sha="$(commit_fixture "E2E 14 shared maintenance rejection")"
expect_update_failure e2e-14-shared-maintenance "shared path cannot be deployed: .maintenance"
log "- commit $sha"

rm -f "$fixture/.maintenance"
sha="$(commit_fixture "E2E 15 shared path cleanup")"
deploy_update e2e-15-shared-cleanup "grep -Fx 'changed' /srv/htdocs/index.html; test ! -e /srv/htdocs/wp-content/uploads/file.jpg; test ! -e /srv/htdocs/.maintenance"
log "- commit $sha"

rm -rf "$fixture/vendor"
git -C "$fixture" submodule add https://github.com/aipokalyptik/jippity-submodule-fixture.git vendor/public-submodule >/dev/null
sha="$(commit_fixture "E2E 16 add nested public submodule")"
deploy_update e2e-16-submodule-add "grep -Fx 'Parent submodule fixture v1 for organic deploy validation.' /srv/htdocs/vendor/public-submodule/parent-fixture.txt; grep -Fx 'Nested submodule fixture v1 for organic deploy validation.' /srv/htdocs/vendor/public-submodule/nested-fixture/nested-fixture.txt"
log "- commit $sha"

git -C "$fixture" submodule deinit -f vendor/public-submodule >/dev/null
git -C "$fixture" rm -f vendor/public-submodule >/dev/null
rm -rf "$fixture/.git/modules/vendor/public-submodule"
sha="$(commit_fixture "E2E 17 remove public submodule")"
deploy_update e2e-17-submodule-remove "test ! -e /srv/htdocs/vendor/public-submodule; grep -Fx 'changed' /srv/htdocs/index.html"
log "- commit $sha"

git -C "$fixture" submodule add https://github.com/aipokalyptik/jippity-private-submodule-fixture.git vendor/private-submodule >/dev/null
sha="$(commit_fixture "E2E 18 add private submodule without site credentials")"
expect_update_failure e2e-18-private-submodule "could not read Username"
log "- commit $sha"

git -C "$fixture" submodule deinit -f vendor/private-submodule >/dev/null || true
git -C "$fixture" rm -f vendor/private-submodule >/dev/null
rm -rf "$fixture/.git/modules/vendor/private-submodule"
sha="$(commit_fixture "E2E 19 remove private submodule")"
deploy_update e2e-19-private-submodule-remove "test ! -e /srv/htdocs/vendor/private-submodule; grep -Fx 'changed' /srv/htdocs/index.html"
log "- commit $sha"

printf 'protected overwrite attempt\n' >"$fixture/wp-load.php"
sha="$(commit_fixture "E2E 20 protected anchor violation")"
expect_update_failure e2e-20-protected "protected path: wp-load.php"
log "- commit $sha"

rm -f "$fixture/wp-load.php"
sha="$(commit_fixture "E2E 21 protected anchor fix")"
deploy_update e2e-21-protected-fix "grep -Fx 'changed' /srv/htdocs/index.html; target=\$(readlink /srv/htdocs/wp-load.php || true); case \"\$target\" in *'.wpcloud-site-git-deploy/deployments/cli-test'*) echo 'wp-load.php was claimed by cli-test' >&2; exit 1;; esac"
log "- commit $sha"

rm -rf "$layer_fixture"
mkdir -p "$layer_fixture/layer-owned"
git -C "$layer_fixture" init -b main >/dev/null
git -C "$layer_fixture" config user.name "WP Cloud CLI Layer Fixture"
git -C "$layer_fixture" config user.email "wpcloud-cli-layer@example.invalid"
printf 'layer content\n' >"$layer_fixture/layer-owned/file.txt"
git -C "$layer_fixture" add .
git -C "$layer_fixture" commit -m "E2E layer baseline" >/dev/null
if ! gh repo view aipokalyptik/wpcloud-site-git-deploy-layer-fixture >/dev/null 2>&1; then
  gh repo create aipokalyptik/wpcloud-site-git-deploy-layer-fixture --public --source="$layer_fixture" --remote=origin >/dev/null
else
  git -C "$layer_fixture" remote add origin https://github.com/aipokalyptik/wpcloud-site-git-deploy-layer-fixture.git 2>/dev/null || git -C "$layer_fixture" remote set-url origin https://github.com/aipokalyptik/wpcloud-site-git-deploy-layer-fixture.git
fi
git -C "$layer_fixture" push -u origin main --force >/dev/null
remote_script e2e-22-layer-deploy <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy init cli-layer --repo https://github.com/aipokalyptik/wpcloud-site-git-deploy-layer-fixture.git --docroot /srv/htdocs --deployment-id cli-layer --default-ref main --keep-releases 3
wpcloud-site-git-deploy update cli-layer
grep -Fx 'layer content' /srv/htdocs/layer-owned/file.txt
test ! -e /srv/htdocs/.maintenance
REMOTE
log "## e2e-22-layer-deploy"
log "- deployed layer fixture $(git -C "$layer_fixture" rev-parse HEAD)"

mkdir -p "$fixture/layer-owned"
printf 'main should not engulf layer\n' >"$fixture/layer-owned/file.txt"
sha="$(commit_fixture "E2E 23 foreign layer overlap violation")"
expect_update_failure e2e-23-foreign-layer "claim owned by another deployment: layer-owned"
log "- commit $sha"

rm -rf "$fixture/layer-owned"
sha="$(commit_fixture "E2E 24 remove foreign layer overlap")"
deploy_update e2e-24-foreign-layer-fix "grep -Fx 'layer content' /srv/htdocs/layer-owned/file.txt; grep -Fx 'changed' /srv/htdocs/index.html"
log "- commit $sha"

remote_script e2e-25-auth-remove <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy auth cli-auth > /tmp/live-auth-create.out
key_path=$(awk -F= '/^ssh_key_path=/{print $2}' "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env")
test -n "$key_path"
test -f "$key_path"
test -f "$key_path.pub"
if wpcloud-site-git-deploy auth cli-auth --remove --verify > /tmp/live-auth-bad.out 2>&1; then
  cat /tmp/live-auth-bad.out
  echo "auth --remove --verify unexpectedly succeeded" >&2
  exit 1
fi
grep -F -- '--remove cannot be combined with --verify, --force-new-key, --use-key, or --import-key' /tmp/live-auth-bad.out
wpcloud-site-git-deploy auth cli-auth --remove > /tmp/live-auth-remove.out
grep -F 'Removed deploy key configuration for cli-auth' /tmp/live-auth-remove.out
! grep -E '^ssh_key_path=' "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env"
test -f "$key_path"
test -f "$key_path.pub"
wpcloud-site-git-deploy auth cli-auth > /tmp/live-auth-recreate.out
key_path=$(awk -F= '/^ssh_key_path=/{print $2}' "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env")
wpcloud-site-git-deploy auth cli-auth --remove --purge-key > /tmp/live-auth-purge.out
grep -F 'Deleted deploy key files for cli-auth' /tmp/live-auth-purge.out
! grep -E '^ssh_key_path=' "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env"
test ! -e "$key_path"
test ! -e "$key_path.pub"
REMOTE
log "## e2e-25-auth-remove"
log "- auth removal, invalid option combination, and purge behavior verified"

remote_script e2e-26-rollback-and-lists <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
before=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
wpcloud-site-git-deploy rollback cli-test
after=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
test "$before" != "$after"
test ! -e /srv/htdocs/.maintenance
wpcloud-site-git-deploy releases cli-test | tee /tmp/cli-test-releases.txt
wpcloud-site-git-deploy branches cli-test | grep -Fx main
wpcloud-site-git-deploy tags cli-test | grep -Fx v1
wpcloud-site-git-deploy commits cli-test --limit 5 | grep 'E2E'
count=$(find /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test/releases -mindepth 1 -maxdepth 1 -type d | wc -l)
test "$count" -le 5
REMOTE
log "## e2e-26-rollback-and-lists"
log "- rollback/list/retention verified"

verify_owned_symlinks
log "## symlink invariant"
log "- all cli-test public symlinks are relative and resolve under /srv/htdocs"

log
log "Completed at $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
