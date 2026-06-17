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

github_deploy_key_titles=()

cleanup_github_deploy_keys() {
  local title

  for title in "${github_deploy_key_titles[@]:-}"; do
    while IFS= read -r key_id; do
      [[ -n "$key_id" ]] || continue
      gh api -X DELETE "repos/aipokalyptik/wpcloud-site-git-deploy-fixture/keys/$key_id" >/dev/null || true
    done < <(gh api --paginate repos/aipokalyptik/wpcloud-site-git-deploy-fixture/keys --jq ".[] | select(.title == \"$title\") | .id" 2>/dev/null || true)
  done
}

trap cleanup_github_deploy_keys EXIT

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

  sleep 1

  local attempt
  for attempt in 1 2; do
    if REMOTE_FILE="$remote_file" SSH_HOST="$ssh_host" SSH_PORT="$ssh_port" SSH_USER="$ssh_user" SSH_PASS="$ssh_pass" KNOWN_HOSTS="$known_hosts" expect <<'EXPECT'
set timeout 420
spawn ssh -p $env(SSH_PORT) -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$env(KNOWN_HOSTS) $env(SSH_USER)@$env(SSH_HOST) "bash $env(REMOTE_FILE)"
expect {
  -re "(?i)password:" { send "$env(SSH_PASS)\r"; exp_continue }
  eof
}
catch wait result
exit [lindex $result 3]
EXPECT
    then
      return 0
    fi
    if [[ "$attempt" -eq 1 ]]; then
      log "- retrying $label after remote execution failure"
      sleep 5
    fi
  done
  return 1
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

copy_from_remote() {
  local remote_file="$1"
  local local_file="$2"

  REMOTE_FILE="$remote_file" LOCAL_FILE="$local_file" SSH_HOST="$ssh_host" SSH_PORT="$ssh_port" SSH_USER="$ssh_user" SSH_PASS="$ssh_pass" KNOWN_HOSTS="$known_hosts" expect <<'EXPECT'
set timeout 180
spawn scp -P $env(SSH_PORT) -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$env(KNOWN_HOSTS) $env(SSH_USER)@$env(SSH_HOST):$env(REMOTE_FILE) $env(LOCAL_FILE)
expect {
  -re "(?i)password:" { send "$env(SSH_PASS)\r"; exp_continue }
  eof
}
catch wait result
exit [lindex $result 3]
EXPECT
}

add_fixture_deploy_key() {
  local title="$1"
  local public_key_file="$2"
  local key_id

  github_deploy_key_titles+=("$title")
  cleanup_github_deploy_keys
  key_id="$(gh api repos/aipokalyptik/wpcloud-site-git-deploy-fixture/keys \
    -f title="$title" \
    -f key="$(cat "$public_key_file")" \
    -F read_only=true \
    --jq '.id')"
  log "- added GitHub deploy key $title ($key_id)"
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
  -lname '*.wpcloud-site-git-deploy/deployments/cli-root-init/current*' -o \
  -lname '*.github-ssh-deploy/deployments/cli-test/current*' -o \
  -lname '*.github-ssh-deploy/deployments/cli-layer/current*' -o \
  -lname '*.github-ssh-deploy/deployments/cli-auth/current*' -o \
  -lname '*.github-ssh-deploy/deployments/cli-root-init/current*' \
\) -delete 2>/dev/null || true
rm -rf \
  /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test \
  /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-layer \
  /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-auth \
  /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-root-init \
  /srv/htdocs/.github-ssh-deploy/deployments/cli-test \
  /srv/htdocs/.github-ssh-deploy/deployments/cli-layer \
  /srv/htdocs/.github-ssh-deploy/deployments/cli-auth \
  /srv/htdocs/.github-ssh-deploy/deployments/cli-root-init \
  "$HOME/.wpcloud-site-git-deploy/deployments/cli-test.env" \
  "$HOME/.wpcloud-site-git-deploy/deployments/cli-layer.env" \
  "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env" \
  "$HOME/.wpcloud-site-git-deploy/deployments/cli-root-init.env" \
  "$HOME/.wpcloud-site-git-deploy/repos/cli-test" \
  "$HOME/.wpcloud-site-git-deploy/repos/cli-layer" \
  "$HOME/.wpcloud-site-git-deploy/repos/cli-auth" \
  "$HOME/.wpcloud-site-git-deploy/repos/cli-root-init" \
  "$HOME/.wpcloud-site-git-deploy/tmp/cli-test" \
  "$HOME/.wpcloud-site-git-deploy/tmp/cli-layer" \
  "$HOME/.wpcloud-site-git-deploy/tmp/cli-auth" \
  "$HOME/.wpcloud-site-git-deploy/tmp/cli-root-init"
rm -f /srv/htdocs/.maintenance /srv/htdocs/.custom-maintenance /srv/htdocs/.init-maintenance
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
wpcloud-site-git-deploy init cli-root-init --repo https://github.com/aipokalyptik/wpcloud-site-git-deploy-fixture.git --docroot /srv/htdocs --deployment-id cli-root-init --default-ref main --keep-releases 2 --deploy-root init-only-root --maintenance-file .init-maintenance
REMOTE
log "## install-current"
log "- installed current checkout on throwaway site"

reset_fixture_tree
mkdir -p "$fixture/wp-content/plugins/cli-fixture" "$fixture/wp-content/themes/cli-theme" "$fixture/wp-content/mu-plugins/cli-mu" "$fixture/assets" "$fixture/.well-known" "$fixture/.github/workflows" "$fixture/public-root/wp-content/themes/root-theme" "$fixture/public-root/nested" "$fixture/init-only-root"
mkdir -p "$fixture/.aws" "$fixture/.ssh"
printf 'baseline\n' >"$fixture/index.html"
printf 'plugin baseline\n' >"$fixture/wp-content/plugins/cli-fixture/plugin.php"
printf 'theme baseline\n' >"$fixture/wp-content/themes/cli-theme/style.css"
printf 'mu baseline\n' >"$fixture/wp-content/mu-plugins/cli-mu/cli-mu.php"
printf 'asset baseline\n' >"$fixture/assets/app.txt"
printf 'deny from all\n' >"$fixture/.htaccess"
printf 'security baseline\n' >"$fixture/.well-known/security.txt"
printf '.env\n' >"$fixture/.gitignore"
printf 'attrs should not deploy\n' >"$fixture/.gitattributes"
: >"$fixture/.gitmodules"
printf 'secret should not deploy\n' >"$fixture/.env"
printf 'workflow should not deploy\n' >"$fixture/.github/workflows/ci.yml"
printf 'finder metadata should not deploy\n' >"$fixture/.DS_Store"
printf 'aws should not deploy\n' >"$fixture/.aws/credentials"
printf 'ssh should not deploy\n' >"$fixture/.ssh/config"
printf 'npm should not deploy\n' >"$fixture/.npmrc"
printf 'pypi should not deploy\n' >"$fixture/.pypirc"
printf 'netrc should not deploy\n' >"$fixture/.netrc"
printf 'root feature v1\n' >"$fixture/public-root/root-feature.txt"
printf 'root theme v1\n' >"$fixture/public-root/wp-content/themes/root-theme/style.css"
printf 'root nested v1\n' >"$fixture/public-root/nested/file.txt"
printf 'init root v1\n' >"$fixture/init-only-root/init-root.txt"
printf 'repo root visible\n' >"$fixture/root-only.txt"
git -C "$fixture" add -f .env .DS_Store .gitignore .gitattributes .gitmodules .github/workflows/ci.yml .aws/credentials .ssh/config .npmrc .pypirc .netrc
sha="$(commit_fixture "E2E 01 baseline deployable tree")"
git -C "$fixture" tag -f e2e-live-tag "$sha" >/dev/null
git -C "$fixture" push origin refs/tags/e2e-live-tag --force >/dev/null
deploy_update e2e-01-baseline "grep -Fx 'baseline' /srv/htdocs/index.html; grep -Fx 'plugin baseline' /srv/htdocs/wp-content/plugins/cli-fixture/plugin.php; grep -Fx 'theme baseline' /srv/htdocs/wp-content/themes/cli-theme/style.css; grep -Fx 'mu baseline' /srv/htdocs/wp-content/mu-plugins/cli-mu/cli-mu.php; test ! -e /srv/htdocs/.env; test ! -e /srv/htdocs/.github; test ! -e /srv/htdocs/.DS_Store; test ! -e /srv/htdocs/.gitignore; test ! -e /srv/htdocs/.gitattributes; test ! -e /srv/htdocs/.gitmodules; test ! -e /srv/htdocs/.aws; test ! -e /srv/htdocs/.ssh; test ! -e /srv/htdocs/.npmrc; test ! -e /srv/htdocs/.pypirc; test ! -e /srv/htdocs/.netrc"
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
release_dir=/srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test/releases
first_inode=$(stat -c '%d:%i' "$release_dir/$forced_update_current/index.html")
second_inode=$(stat -c '%d:%i' "$release_dir/$forced_deploy_current/index.html")
test "$first_inode" = "$second_inode"
test ! -e /srv/htdocs/.maintenance
REMOTE
log "## e2e-02-noop-and-force"
log "- no-op update preserved current release; update --force and deploy --force promoted new hardlinked releases"

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

remote_script e2e-03b-init-deploy-root-maintenance <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
marker=/tmp/live-init-root-post-deploy.sh
cat >"$marker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
test -f .init-maintenance
grep -F '$upgrading = ' .init-maintenance >/dev/null
grep -Fx 'init root v1' init-root.txt
test ! -e root-only.txt
SH
chmod +x "$marker"
wpcloud-site-git-deploy status cli-root-init | grep -Fx 'deploy_root=init-only-root'
wpcloud-site-git-deploy status cli-root-init | grep -Fx 'maintenance_file=.init-maintenance'
wpcloud-site-git-deploy update cli-root-init --post-deploy "$marker"
grep -Fx 'init root v1' /srv/htdocs/init-root.txt
test ! -e /srv/htdocs/init-only-root/init-root.txt
test ! -e /srv/htdocs/root-only.txt
test ! -e /srv/htdocs/.init-maintenance
REMOTE
log "## e2e-03b-init-deploy-root-maintenance"
log "- init-time deploy-root and custom maintenance-file verified"

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
custom_maintenance=/tmp/live-custom-maintenance-post-deploy.sh
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
cat >"$custom_maintenance" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .custom-maintenance
grep -F '\$upgrading = ' .custom-maintenance >/dev/null
test ! -e .maintenance
printf 'custom:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$marker"
SH
chmod +x "$configured" "$override" "$failing" "$no_maintenance" "$custom_maintenance"
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
wpcloud-site-git-deploy config cli-test --maintenance-file .custom-maintenance
wpcloud-site-git-deploy status cli-test | grep -Fx 'maintenance_file=.custom-maintenance'
wpcloud-site-git-deploy update cli-test --force --post-deploy "$custom_maintenance" >/dev/null
grep -F 'custom:/srv/htdocs:changed' "$marker"
test ! -e /srv/htdocs/.custom-maintenance
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
log "- post-deploy hooks, failure behavior, tool-owned maintenance cleanup, non-owned preservation, rollback cleanup, custom maintenance-file, and maintenance-file none verified"

remote_script e2e-05b-concurrent-deploy-rejection <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
hook=/tmp/live-blocking-post-deploy.sh
ready=/tmp/live-blocking-post-deploy-ready
release=/tmp/live-blocking-post-deploy-release
marker=/tmp/live-blocking-post-deploy-marker
rm -f "$ready" "$release" "$marker" /tmp/live-blocking-update.status
cat >"$hook" <<SH
#!/usr/bin/env bash
set -euo pipefail
printf 'ready\n' >"$ready"
while [[ ! -e "$release" ]]; do
  sleep 0.1
done
printf 'done:%s:%s\n' "\$PWD" "\$(cat index.html)" >"$marker"
SH
chmod +x "$hook"
(
  set +e
  wpcloud-site-git-deploy update cli-test --force --post-deploy "$hook" >/tmp/live-blocking-update.out 2>/tmp/live-blocking-update.err
  printf '%s\n' "$?" >/tmp/live-blocking-update.status
) &
blocking_pid="$!"
for _ in {1..100}; do
  [[ -e "$ready" ]] && break
  sleep 0.1
done
if [[ ! -e "$ready" ]]; then
  touch "$release"
  wait "$blocking_pid" || true
  echo "blocking live deploy did not start" >&2
  exit 1
fi
if timeout 10 wpcloud-site-git-deploy update cli-test --force >/tmp/live-concurrent-update.out 2>/tmp/live-concurrent-update.err; then
  touch "$release"
  wait "$blocking_pid" || true
  echo "concurrent live deploy unexpectedly succeeded" >&2
  exit 1
fi
grep -F 'deployment already running' /tmp/live-concurrent-update.err
touch "$release"
wait "$blocking_pid"
test "$(cat /tmp/live-blocking-update.status)" = "0"
grep -F 'done:/srv/htdocs:' "$marker"
test ! -e /srv/htdocs/.maintenance
REMOTE
log "## e2e-05b-concurrent-deploy-rejection"
log "- overlapping deploy/update for one deployment id failed with deployment already running"

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

mkdir -p "$fixture/wp-content/uploads/static" "$fixture/wp-content/blogs.dir/1/files"
printf 'deploy-managed upload\n' >"$fixture/wp-content/uploads/static/logo.png"
printf 'deploy-managed multisite upload\n' >"$fixture/wp-content/blogs.dir/1/files/logo.png"
sha="$(commit_fixture "E2E 13 shared media leaf files")"
deploy_update e2e-13-shared-media-leaf-files "test -d /srv/htdocs/wp-content/uploads; test -d /srv/htdocs/wp-content/uploads/static; test -L /srv/htdocs/wp-content/uploads/static/logo.png; grep -Fx 'deploy-managed upload' /srv/htdocs/wp-content/uploads/static/logo.png; test -d /srv/htdocs/wp-content/blogs.dir/1/files; test -L /srv/htdocs/wp-content/blogs.dir/1/files/logo.png; grep -Fx 'deploy-managed multisite upload' /srv/htdocs/wp-content/blogs.dir/1/files/logo.png"
log "- commit $sha"

printf 'tracked maintenance should not deploy\n' >"$fixture/.maintenance"
sha="$(commit_fixture "E2E 14 shared maintenance rejection")"
expect_update_failure e2e-14-shared-maintenance "shared path cannot be deployed: .maintenance"
log "- commit $sha"

rm -f "$fixture/.maintenance"
mkdir -p "$fixture/wp-content/cache"
printf 'cache should not deploy\n' >"$fixture/wp-content/cache/object-cache.bin"
sha="$(commit_fixture "E2E 15 shared cache rejection")"
expect_update_failure e2e-15-shared-cache "shared path cannot be deployed: wp-content/cache"
log "- commit $sha"

rm -rf "$fixture/wp-content/cache"
mkdir -p "$fixture/wp-content/upgrade"
printf 'upgrade should not deploy\n' >"$fixture/wp-content/upgrade/package.tmp"
sha="$(commit_fixture "E2E 16 shared upgrade rejection")"
expect_update_failure e2e-16-shared-upgrade "shared path cannot be deployed: wp-content/upgrade"
log "- commit $sha"

rm -rf "$fixture/wp-content/upgrade" "$fixture/wp-content/uploads" "$fixture/wp-content/blogs.dir"
sha="$(commit_fixture "E2E 18 shared path cleanup")"
deploy_update e2e-18-shared-cleanup "grep -Fx 'changed' /srv/htdocs/index.html; test -d /srv/htdocs/wp-content/uploads/static; test ! -e /srv/htdocs/wp-content/uploads/static/logo.png; test -d /srv/htdocs/wp-content/blogs.dir/1/files; test ! -e /srv/htdocs/wp-content/blogs.dir/1/files/logo.png; test ! -e /srv/htdocs/.maintenance; test ! -e /srv/htdocs/wp-content/cache/object-cache.bin; test ! -e /srv/htdocs/wp-content/upgrade/package.tmp"
log "- commit $sha"

rm -rf "$fixture/vendor"
git -C "$fixture" submodule add https://github.com/aipokalyptik/jippity-submodule-fixture.git vendor/public-submodule >/dev/null
sha="$(commit_fixture "E2E 19 add nested public submodule")"
deploy_update e2e-19-submodule-add "grep -Fx 'Parent submodule fixture v1 for organic deploy validation.' /srv/htdocs/vendor/public-submodule/parent-fixture.txt; grep -Fx 'Nested submodule fixture v1 for organic deploy validation.' /srv/htdocs/vendor/public-submodule/nested-fixture/nested-fixture.txt"
log "- commit $sha"

git -C "$fixture" submodule deinit -f vendor/public-submodule >/dev/null
git -C "$fixture" rm -f vendor/public-submodule >/dev/null
rm -rf "$fixture/.git/modules/vendor/public-submodule"
sha="$(commit_fixture "E2E 20 remove public submodule")"
deploy_update e2e-20-submodule-remove "test ! -e /srv/htdocs/vendor/public-submodule; grep -Fx 'changed' /srv/htdocs/index.html"
log "- commit $sha"

git -C "$fixture" submodule add https://github.com/aipokalyptik/jippity-private-submodule-fixture.git vendor/private-submodule >/dev/null
sha="$(commit_fixture "E2E 21 add private submodule without site credentials")"
expect_update_failure e2e-21-private-submodule "could not read Username"
log "- commit $sha"

git -C "$fixture" submodule deinit -f vendor/private-submodule >/dev/null || true
git -C "$fixture" rm -f vendor/private-submodule >/dev/null
rm -rf "$fixture/.git/modules/vendor/private-submodule"
sha="$(commit_fixture "E2E 22 remove private submodule")"
deploy_update e2e-22-private-submodule-remove "test ! -e /srv/htdocs/vendor/private-submodule; grep -Fx 'changed' /srv/htdocs/index.html"
log "- commit $sha"

printf 'protected overwrite attempt\n' >"$fixture/wp-load.php"
sha="$(commit_fixture "E2E 23 protected anchor violation")"
expect_update_failure e2e-23-protected "protected path: wp-load.php"
log "- commit $sha"

rm -f "$fixture/wp-load.php"
sha="$(commit_fixture "E2E 24 protected anchor fix")"
deploy_update e2e-24-protected-fix "grep -Fx 'changed' /srv/htdocs/index.html; target=\$(readlink /srv/htdocs/wp-load.php || true); case \"\$target\" in *'.wpcloud-site-git-deploy/deployments/cli-test'*) echo 'wp-load.php was claimed by cli-test' >&2; exit 1;; esac"
log "- commit $sha"

current_commit="$sha"
remote_script e2e-25-tag-and-commit-deploy <<REMOTE
set -euo pipefail
export PATH="\$HOME/.local/bin:\$HOME/.wpcloud-site-git-deploy/bin:\$PATH"
tag_output=\$(wpcloud-site-git-deploy deploy cli-test --tag e2e-live-tag --force)
printf '%s\n' "\$tag_output" | grep -F ' tag '
wpcloud-site-git-deploy releases cli-test | grep -F 'tag:e2e-live-tag'
grep -Fx 'baseline' /srv/htdocs/index.html
commit_output=\$(wpcloud-site-git-deploy deploy cli-test --commit "$current_commit" --force)
printf '%s\n' "\$commit_output" | grep -F " commit $current_commit"
grep -Fx 'changed' /srv/htdocs/index.html
test ! -e /srv/htdocs/.maintenance
REMOTE
log "## e2e-25-tag-and-commit-deploy"
log "- tag and commit deploy paths verified"

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
remote_script e2e-26-layer-deploy <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy init cli-layer --repo https://github.com/aipokalyptik/wpcloud-site-git-deploy-layer-fixture.git --docroot /srv/htdocs --deployment-id cli-layer --default-ref main --keep-releases 3
wpcloud-site-git-deploy update cli-layer
grep -Fx 'layer content' /srv/htdocs/layer-owned/file.txt
test ! -e /srv/htdocs/.maintenance
REMOTE
log "## e2e-26-layer-deploy"
log "- deployed layer fixture $(git -C "$layer_fixture" rev-parse HEAD)"

mkdir -p "$fixture/layer-owned"
printf 'main should not engulf layer\n' >"$fixture/layer-owned/file.txt"
sha="$(commit_fixture "E2E 27 foreign layer overlap violation")"
expect_update_failure e2e-27-foreign-layer "claim owned by another deployment: layer-owned"
log "- commit $sha"

rm -rf "$fixture/layer-owned"
sha="$(commit_fixture "E2E 28 remove foreign layer overlap")"
deploy_update e2e-28-foreign-layer-fix "grep -Fx 'layer content' /srv/htdocs/layer-owned/file.txt; grep -Fx 'changed' /srv/htdocs/index.html"
log "- commit $sha"

remote_script e2e-29-auth-generate <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy auth cli-auth > /tmp/live-auth-create.out
key_path=$(awk -F= '/^ssh_key_path=/{print $2}' "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env")
test -n "$key_path"
test -f "$key_path"
test -f "$key_path.pub"
cp "$key_path.pub" /tmp/live-cli-auth-generated.pub
wpcloud-site-git-deploy doctor cli-auth --offline | tee /tmp/live-doctor-offline.out
grep -F 'OK ssh-key: public key exists' /tmp/live-doctor-offline.out
grep -F 'WARN git-remote: skipped remote access check because --offline was provided' /tmp/live-doctor-offline.out
REMOTE
copy_from_remote /tmp/live-cli-auth-generated.pub "$repo_root/tmp/live-cli-auth-generated.pub"
add_fixture_deploy_key "wpcloud-site-git-deploy live generated $(date -u '+%Y%m%d%H%M%S')" "$repo_root/tmp/live-cli-auth-generated.pub"
remote_script e2e-30-auth-verify-doctor <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy auth cli-auth --verify | tee /tmp/live-auth-verify.out
grep -F 'Verified remote access for git@github.com:aipokalyptik/wpcloud-site-git-deploy-fixture.git' /tmp/live-auth-verify.out
wpcloud-site-git-deploy doctor cli-auth | tee /tmp/live-doctor.out
grep -F 'OK git-remote: remote access succeeded' /tmp/live-doctor.out
REMOTE
log "## e2e-29-30-auth-generate-verify-doctor"
log "- generated deploy key, offline doctor, auth --verify, and online doctor verified"

remote_script e2e-31-auth-use-key-prepare <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
external=/tmp/live-external-ed25519
rm -f "$external" "$external.pub"
ssh-keygen -t ed25519 -N '' -C 'wpcloud-site-git-deploy-live-use-key' -f "$external" >/dev/null
chmod 600 "$external"
wpcloud-site-git-deploy auth cli-auth --use-key "$external" > /tmp/live-auth-use-key.out
grep -F "Using existing deploy key: $external" /tmp/live-auth-use-key.out
grep -Fx "ssh_key_path=$external" "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env"
cp "$external.pub" /tmp/live-cli-auth-use-key.pub
REMOTE
copy_from_remote /tmp/live-cli-auth-use-key.pub "$repo_root/tmp/live-cli-auth-use-key.pub"
add_fixture_deploy_key "wpcloud-site-git-deploy live use-key $(date -u '+%Y%m%d%H%M%S')" "$repo_root/tmp/live-cli-auth-use-key.pub"
remote_script e2e-32-auth-use-key-verify <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy auth cli-auth --use-key /tmp/live-external-ed25519 --verify | tee /tmp/live-auth-use-key-verify.out
grep -F 'Verified remote access for git@github.com:aipokalyptik/wpcloud-site-git-deploy-fixture.git' /tmp/live-auth-use-key-verify.out
wpcloud-site-git-deploy doctor cli-auth | grep -F 'OK git-remote: remote access succeeded'
REMOTE
log "## e2e-31-32-auth-use-key"
log "- external deploy key use and verification path verified"

remote_script e2e-33-auth-import-key-prepare <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
import_source=/tmp/live-import-ed25519
rm -f "$import_source" "$import_source.pub"
ssh-keygen -t ed25519 -N '' -C 'wpcloud-site-git-deploy-live-import-key' -f "$import_source" >/dev/null
chmod 600 "$import_source"
wpcloud-site-git-deploy auth cli-auth --import-key "$import_source" --force-new-key > /tmp/live-auth-import-key.out
managed="$HOME/.wpcloud-site-git-deploy/keys/cli-auth_ed25519"
grep -F "Imported deploy key: $managed" /tmp/live-auth-import-key.out
grep -Fx "ssh_key_path=$managed" "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env"
test -f "$managed"
test -f "$managed.pub"
test "$(stat -c '%a' "$managed")" = "600"
cp "$managed.pub" /tmp/live-cli-auth-import-key.pub
REMOTE
copy_from_remote /tmp/live-cli-auth-import-key.pub "$repo_root/tmp/live-cli-auth-import-key.pub"
add_fixture_deploy_key "wpcloud-site-git-deploy live import-key $(date -u '+%Y%m%d%H%M%S')" "$repo_root/tmp/live-cli-auth-import-key.pub"
remote_script e2e-34-auth-import-key-verify-rotate-remove <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy auth cli-auth --verify | tee /tmp/live-auth-import-key-verify.out
grep -F 'Verified remote access for git@github.com:aipokalyptik/wpcloud-site-git-deploy-fixture.git' /tmp/live-auth-import-key-verify.out
old_fingerprint=$(ssh-keygen -lf "$HOME/.wpcloud-site-git-deploy/keys/cli-auth_ed25519.pub" | awk '{print $2}')
wpcloud-site-git-deploy auth cli-auth --force-new-key > /tmp/live-auth-rotate.out
new_fingerprint=$(ssh-keygen -lf "$HOME/.wpcloud-site-git-deploy/keys/cli-auth_ed25519.pub" | awk '{print $2}')
test "$old_fingerprint" != "$new_fingerprint"
if wpcloud-site-git-deploy auth cli-auth --remove --verify > /tmp/live-auth-bad.out 2>&1; then
  cat /tmp/live-auth-bad.out
  echo "auth --remove --verify unexpectedly succeeded" >&2
  exit 1
fi
grep -F -- '--remove cannot be combined with --verify, --force-new-key, --use-key, or --import-key' /tmp/live-auth-bad.out
wpcloud-site-git-deploy auth cli-auth --remove > /tmp/live-auth-remove.out
grep -F 'Removed deploy key configuration for cli-auth' /tmp/live-auth-remove.out
! grep -E '^ssh_key_path=' "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth.env"
key_path="$HOME/.wpcloud-site-git-deploy/keys/cli-auth_ed25519"
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
log "## e2e-33-34-auth-import-rotate-remove"
log "- managed key import, generated-key rotation, removal, invalid option combination, and purge behavior verified"

remote_script e2e-35-rollback-and-lists <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
before=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
target="$before"
wpcloud-site-git-deploy rollback cli-test --to "$target"
after=$(wpcloud-site-git-deploy status cli-test | awk -F= '/^current=/{print $2}')
test "$after" = "$target"
test ! -e /srv/htdocs/.maintenance
if wpcloud-site-git-deploy rollback cli-test --to missing-release-id > /tmp/live-rollback-missing.out 2>&1; then
  cat /tmp/live-rollback-missing.out
  echo "missing rollback target unexpectedly succeeded" >&2
  exit 1
fi
grep -F 'rollback release does not exist:' /tmp/live-rollback-missing.out
wpcloud-site-git-deploy releases cli-test | tee /tmp/cli-test-releases.txt
wpcloud-site-git-deploy branches cli-test | grep -Fx main
wpcloud-site-git-deploy branches cli-test --fetch --limit 1 | grep -Fx main
wpcloud-site-git-deploy tags cli-test | grep -Fx v1
wpcloud-site-git-deploy tags cli-test --fetch --limit 50 | grep -Fx e2e-live-tag
wpcloud-site-git-deploy commits cli-test --limit 5 | grep 'E2E'
wpcloud-site-git-deploy commits cli-test --fetch --limit 5 | grep 'E2E'
count=$(find /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test/releases -mindepth 1 -maxdepth 1 -type d | wc -l)
test "$count" -le 5
REMOTE
log "## e2e-35-rollback-and-lists"
log "- explicit rollback target, missing rollback target, fetch/list, limit, and retention verified"

verify_owned_symlinks
remote_script e2e-36-full-symlink-audit <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
audit_root=/tmp/wpcloud-site-git-deploy-audit-docroot
rm -rf "$audit_root"
mkdir -p "$audit_root/releases/current" "$audit_root/public"
printf 'audit\n' > "$audit_root/releases/current/file.txt"
ln -s ../releases/current/file.txt "$audit_root/public/file.txt"
wpcloud-site-git-deploy __remote-deploy --docroot "$audit_root" --assert-public-symlinks
REMOTE
log "## symlink invariant"
log "- all cli-test public symlinks are relative and resolve under /srv/htdocs; full-docroot audit command passed on controlled docroot"

log
log "Completed at $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
