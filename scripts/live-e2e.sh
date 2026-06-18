#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="${WPCLOUD_SITE_GIT_DEPLOY_FIXTURE:-$repo_root-fixture}"
layer_fixture="${WPCLOUD_SITE_GIT_DEPLOY_LAYER_FIXTURE:-$repo_root-layer-fixture}"
evidence="${WPCLOUD_SITE_GIT_DEPLOY_E2E_EVIDENCE:-$repo_root/tmp/go-live-e2e-evidence.md}"
known_hosts="${WPCLOUD_SITE_GIT_DEPLOY_KNOWN_HOSTS:-/tmp/wpcloud-site-git-deploy-known-hosts}"
bundle="${WPCLOUD_SITE_GIT_DEPLOY_E2E_BUNDLE:-$repo_root/tmp/go-live-e2e-source.tar.gz}"
binary="$repo_root/dist/wpcloud-site-git-deploy-linux-amd64"
git_lfs_version="${WPCLOUD_SITE_GIT_DEPLOY_GIT_LFS_VERSION:-3.7.1}"
git_lfs_linux_amd64_sha256="${WPCLOUD_SITE_GIT_DEPLOY_GIT_LFS_LINUX_AMD64_SHA256:-1c0b6ee5200ca708c5cebebb18fdeb0e1c98f1af5c1a9cba205a4c0ab5a5ec08}"

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

github_deploy_key_titles=()

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'required command not found: %s\n' "$1" >&2
    exit 1
  }
}

log() {
  printf '%s\n' "$*"
  printf '%s\n' "$*" >>"$evidence"
}

cleanup_github_deploy_keys() {
  local title
  local key_id

  for title in "${github_deploy_key_titles[@]:-}"; do
    while IFS= read -r key_id; do
      [[ -n "$key_id" ]] || continue
      gh api -X DELETE "repos/aipokalyptik/wpcloud-site-git-deploy-fixture/keys/$key_id" >/dev/null || true
    done < <(gh api --paginate repos/aipokalyptik/wpcloud-site-git-deploy-fixture/keys --jq ".[] | select(.title == \"$title\") | .id" 2>/dev/null || true)
  done
}

remote_script() {
  local label="$1"
  local script_file="$repo_root/tmp/go-live-e2e-remote-$label.sh"
  local remote_file="/tmp/wpcloud-site-git-deploy-go-$label.sh"

  cat >"$script_file"
  chmod 700 "$script_file"

  SCRIPT_FILE="$script_file" REMOTE_FILE="$remote_file" SSH_HOST="$ssh_host" SSH_PORT="$ssh_port" SSH_USER="$ssh_user" SSH_PASS="$ssh_pass" KNOWN_HOSTS="$known_hosts" expect <<'EXPECT'
set timeout 240
spawn scp -P $env(SSH_PORT) -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$env(KNOWN_HOSTS) $env(SCRIPT_FILE) $env(SSH_USER)@$env(SSH_HOST):$env(REMOTE_FILE)
expect {
  -re "(?i)password:" { send "$env(SSH_PASS)\r"; exp_continue }
  eof
}
catch wait result
exit [lindex $result 3]
EXPECT

  REMOTE_FILE="$remote_file" SSH_HOST="$ssh_host" SSH_PORT="$ssh_port" SSH_USER="$ssh_user" SSH_PASS="$ssh_pass" KNOWN_HOSTS="$known_hosts" expect <<'EXPECT'
set timeout 600
spawn ssh -p $env(SSH_PORT) -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$env(KNOWN_HOSTS) $env(SSH_USER)@$env(SSH_HOST) "bash $env(REMOTE_FILE)"
expect {
  -re "(?i)password:" { send "$env(SSH_PASS)\r"; exp_continue }
  eof
}
catch wait result
exit [lindex $result 3]
EXPECT
}

ensure_remote_git_lfs() {
  log "## ensure-git-lfs"

  remote_script ensure-git-lfs <<REMOTE
set -euo pipefail
export PATH="\$HOME/.wpcloud-site-git-deploy/bin:\$PATH"
if command -v git-lfs >/dev/null 2>&1; then
  git-lfs version
  exit 0
fi

version="$git_lfs_version"
expected_sha="$git_lfs_linux_amd64_sha256"
url="https://github.com/git-lfs/git-lfs/releases/download/v\${version}/git-lfs-linux-amd64-v\${version}.tar.gz"
work="\$(mktemp -d "\${TMPDIR:-/tmp}/git-lfs.XXXXXX")"
archive="\$work/git-lfs.tar.gz"
trap 'rm -rf "\$work"' EXIT

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "\$url" -o "\$archive"
elif command -v wget >/dev/null 2>&1; then
  wget -q "\$url" -O "\$archive"
else
  printf 'curl or wget is required to download git-lfs\n' >&2
  exit 1
fi

printf '%s  %s\n' "\$expected_sha" "\$archive" | sha256sum -c -
tar -xzf "\$archive" -C "\$work"
install -m 755 "\$work/git-lfs-\$version/git-lfs" "\$HOME/.wpcloud-site-git-deploy/bin/git-lfs"
git-lfs version
REMOTE
}

copy_bundle() {
  BUNDLE="$bundle" SSH_HOST="$ssh_host" SSH_PORT="$ssh_port" SSH_USER="$ssh_user" SSH_PASS="$ssh_pass" KNOWN_HOSTS="$known_hosts" expect <<'EXPECT'
set timeout 240
spawn scp -P $env(SSH_PORT) -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$env(KNOWN_HOSTS) $env(BUNDLE) $env(SSH_USER)@$env(SSH_HOST):/tmp/wpcloud-site-git-deploy-go-source.tar.gz
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
set timeout 240
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

reset_fixture_tree() {
  git -C "$fixture" switch main >/dev/null
  git -C "$fixture" fetch origin main >/dev/null
  git -C "$fixture" reset --hard origin/main >/dev/null
  git -C "$fixture" clean -ffdx >/dev/null
  git -C "$fixture" config user.name "WP Cloud CLI Fixture"
  git -C "$fixture" config user.email "wpcloud-cli-fixture@example.invalid"
  find "$fixture" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
}

commit_fixture() {
  local message="$1"

  git -C "$fixture" add -A
  git -C "$fixture" commit -m "$message" >/dev/null
  git -C "$fixture" push origin main >/dev/null
  git -C "$fixture" rev-parse HEAD
}

reset_layer_fixture_tree() {
  git -C "$layer_fixture" switch main >/dev/null
  git -C "$layer_fixture" fetch origin main >/dev/null
  git -C "$layer_fixture" reset --hard origin/main >/dev/null
  git -C "$layer_fixture" clean -ffdx >/dev/null
  git -C "$layer_fixture" config user.name "WP Cloud CLI Layer Fixture"
  git -C "$layer_fixture" config user.email "wpcloud-cli-layer-fixture@example.invalid"
  find "$layer_fixture" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
}

commit_layer_fixture() {
  local message="$1"

  git -C "$layer_fixture" add -A
  git -C "$layer_fixture" commit -m "$message" >/dev/null
  git -C "$layer_fixture" push origin main >/dev/null
  git -C "$layer_fixture" rev-parse HEAD
}

remote_deploy_default() {
  local label="$1"
  local assertions="$2"

  log "## $label"
  remote_script "$label" <<REMOTE
set -euo pipefail
export PATH="\$HOME/.wpcloud-site-git-deploy/bin:\$PATH"
wpcloud-site-git-deploy deploy --name cli-test
$assertions
test ! -e /srv/htdocs/.maintenance
REMOTE
  log "- deployed $(git -C "$fixture" rev-parse --short HEAD)"
}

expect_remote_failure() {
  local label="$1"
  local command="$2"
  local expected="$3"
  local assertions="$4"

  log "## $label"
  remote_script "$label" <<REMOTE
set -euo pipefail
export PATH="\$HOME/.wpcloud-site-git-deploy/bin:\$PATH"
set +e
$command >/tmp/wpcloud-site-git-deploy-go-fail.out 2>/tmp/wpcloud-site-git-deploy-go-fail.err
status=\$?
set -e
test "\$status" -ne 0
grep -q "$expected" /tmp/wpcloud-site-git-deploy-go-fail.err
$assertions
test ! -e /srv/htdocs/.maintenance
REMOTE
}

require_command expect
require_command git
require_command git-lfs
require_command tar
require_command gh

trap cleanup_github_deploy_keys EXIT

mkdir -p "$(dirname "$evidence")" "$(dirname "$bundle")" "$repo_root/dist"
printf '# Go Live E2E Evidence\n\n' >"$evidence"

log "## Build and upload"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$binary" ./cmd/wpcloud-site-git-deploy
  tar --exclude .git --exclude tmp --exclude .cache -czf "$bundle" .
)
copy_bundle
remote_script install-current <<'REMOTE'
set -euo pipefail
rm -rf /tmp/wpcloud-site-git-deploy-go-source
mkdir -p /tmp/wpcloud-site-git-deploy-go-source
tar -xzf /tmp/wpcloud-site-git-deploy-go-source.tar.gz -C /tmp/wpcloud-site-git-deploy-go-source
bash /tmp/wpcloud-site-git-deploy-go-source/scripts/install.sh
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy --version
rm -rf "$HOME/.wpcloud-site-git-deploy/deployments/cli-test" \
       "$HOME/.wpcloud-site-git-deploy/deployments/cli-layer" \
       "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth" \
       "$HOME/.wpcloud-site-git-deploy/repos/cli-test" \
       "$HOME/.wpcloud-site-git-deploy/repos/cli-layer" \
       "$HOME/.wpcloud-site-git-deploy/repos/cli-auth" \
       "$HOME/.wpcloud-site-git-deploy/tmp/cli-test" \
       "$HOME/.wpcloud-site-git-deploy/tmp/cli-layer" \
       "$HOME/.wpcloud-site-git-deploy/tmp/cli-auth"
rm -rf /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test \
       /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-layer \
       /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-auth
rm -rf /srv/htdocs/index.html \
       /srv/htdocs/hook.sh \
       /srv/htdocs/post-deploy-marker.txt \
       /srv/htdocs/layer-owned \
       /srv/htdocs/modules \
       /srv/htdocs/wp-content/uploads/static/logo.txt \
       /srv/htdocs/wp-content/blogs.dir/1/files/logo.txt
wpcloud-site-git-deploy init --name cli-test --repo https://github.com/aipokalyptik/wpcloud-site-git-deploy-fixture.git --docroot /srv/htdocs --deployment-id cli-test --default-ref main --keep-releases 5
wpcloud-site-git-deploy init --name cli-auth --repo https://github.com/aipokalyptik/wpcloud-site-git-deploy-fixture.git --docroot /srv/htdocs --deployment-id cli-auth --default-ref main --keep-releases 2
wpcloud-site-git-deploy doctor --name cli-test --offline
REMOTE
ensure_remote_git_lfs

reset_fixture_tree
cat >"$fixture/index.html" <<'EOF'
baseline
EOF
cat >"$fixture/.env" <<'EOF'
should-not-deploy
EOF
mkdir -p "$fixture/.github/workflows" "$fixture/.ssh" "$fixture/.aws" "$fixture/wp-content/uploads/static" "$fixture/wp-content/blogs.dir/1/files"
cat >"$fixture/.github/workflows/test.yml" <<'EOF'
should-not-deploy
EOF
cat >"$fixture/.DS_Store" <<'EOF'
should-not-deploy
EOF
cat >"$fixture/wp-content/uploads/static/logo.txt" <<'EOF'
upload-logo
EOF
cat >"$fixture/wp-content/blogs.dir/1/files/logo.txt" <<'EOF'
blogs-logo
EOF
baseline_commit="$(commit_fixture go-live-baseline)"
tag_name="go-live-tag-$(date +%Y%m%d%H%M%S)"
git -C "$fixture" tag "$tag_name" "$baseline_commit" >/dev/null
git -C "$fixture" push origin "$tag_name" >/dev/null

remote_deploy_default e2e-01-baseline '
grep -q baseline /srv/htdocs/index.html
grep -q upload-logo /srv/htdocs/wp-content/uploads/static/logo.txt
grep -q blogs-logo /srv/htdocs/wp-content/blogs.dir/1/files/logo.txt
test ! -e /srv/htdocs/.env
test ! -e /srv/htdocs/.github
test ! -e /srv/htdocs/.DS_Store
wpcloud-site-git-deploy doctor --name cli-test --offline
wpcloud-site-git-deploy doctor --name cli-test --assert-public-symlinks --offline
'

remote_script e2e-02-noop-force <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
before="$(find /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test/releases -mindepth 1 -maxdepth 1 -type d | wc -l)"
wpcloud-site-git-deploy deploy --name cli-test | tee /tmp/wpcloud-site-git-deploy-go-noop.out
grep -q 'no_op=true' /tmp/wpcloud-site-git-deploy-go-noop.out
after="$(find /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test/releases -mindepth 1 -maxdepth 1 -type d | wc -l)"
test "$before" = "$after"
wpcloud-site-git-deploy deploy --name cli-test --force
new_after="$(find /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test/releases -mindepth 1 -maxdepth 1 -type d | wc -l)"
test "$new_after" -gt "$after"
REMOTE

cat >"$fixture/index.html" <<'EOF'
deploy-root-root
EOF
mkdir -p "$fixture/public-root"
cat >"$fixture/public-root/index.html" <<'EOF'
deploy-root-only
EOF
commit_fixture go-live-deploy-root >/dev/null
remote_script e2e-03-deploy-root <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy config --name cli-test --set deploy_root=public-root
wpcloud-site-git-deploy deploy --name cli-test
grep -q deploy-root-only /srv/htdocs/index.html
set +e
wpcloud-site-git-deploy config --name cli-test --set deploy_root=../outside >/tmp/root.out 2>/tmp/root.err
status=$?
set -e
test "$status" -ne 0
wpcloud-site-git-deploy config --name cli-test --unset deploy_root
REMOTE

cat >"$fixture/index.html" <<'EOF'
post-deploy
EOF
cat >"$fixture/hook.sh" <<'EOF'
test -f .maintenance
grep -q '$upgrading' .maintenance
printf hook-ran > post-deploy-marker.txt
EOF
commit_fixture go-live-post-deploy >/dev/null
remote_script e2e-04-post-deploy-maintenance <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy config --name cli-test --set post_deploy=hook.sh
wpcloud-site-git-deploy deploy --name cli-test
grep -q hook-ran /srv/htdocs/post-deploy-marker.txt
test ! -e /srv/htdocs/.maintenance
wpcloud-site-git-deploy config --name cli-test --unset post_deploy
REMOTE

cat >"$fixture/index.html" <<'EOF'
shared-fail
EOF
mkdir -p "$fixture/wp-content/cache"
cat >"$fixture/wp-content/cache/object-cache.bin" <<'EOF'
cache
EOF
commit_fixture go-live-shared-cache >/dev/null
expect_remote_failure e2e-05-shared-cache 'wpcloud-site-git-deploy deploy --name cli-test' 'shared path cannot be deployed' 'grep -q post-deploy /srv/htdocs/index.html'
rm -rf "$fixture/wp-content/cache"
cat >"$fixture/index.html" <<'EOF'
after-shared-fix
EOF
commit_fixture go-live-shared-fix >/dev/null
remote_deploy_default e2e-06-shared-fix 'grep -q after-shared-fix /srv/htdocs/index.html'

remote_script e2e-07-tag-and-commit <<REMOTE
set -euo pipefail
export PATH="\$HOME/.wpcloud-site-git-deploy/bin:\$PATH"
wpcloud-site-git-deploy deploy --name cli-test --tag $tag_name --force
grep -q baseline /srv/htdocs/index.html
wpcloud-site-git-deploy deploy --name cli-test --commit $baseline_commit --force
grep -q baseline /srv/htdocs/index.html
REMOTE

reset_layer_fixture_tree
printf 'layer-%s\n' "$(date +%s)" >"$layer_fixture/layer-owned"
commit_layer_fixture go-live-layer >/dev/null
remote_script e2e-08-layer-and-foreign-ancestor <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy init --name cli-layer --repo https://github.com/aipokalyptik/wpcloud-site-git-deploy-layer-fixture.git --docroot /srv/htdocs --deployment-id cli-layer --default-ref main --keep-releases 2
wpcloud-site-git-deploy deploy --name cli-layer
grep -q layer /srv/htdocs/layer-owned
REMOTE
mkdir -p "$fixture/layer-owned"
cat >"$fixture/layer-owned/child.txt" <<'EOF'
engulf
EOF
commit_fixture go-live-foreign-ancestor >/dev/null
remote_deploy_default e2e-09-exact-foreign-takeover 'grep -q engulf /srv/htdocs/layer-owned/child.txt'
rm -rf "$fixture/layer-owned"
cat >"$fixture/index.html" <<'EOF'
foreign-fixed
EOF
commit_fixture go-live-foreign-fixed >/dev/null
remote_deploy_default e2e-10-foreign-fixed 'grep -q foreign-fixed /srv/htdocs/index.html'

git -C "$fixture" lfs install --local >/dev/null
git -C "$fixture" lfs track '*.bin' >/dev/null
cat >"$fixture/index.html" <<'EOF'
lfs-release
EOF
mkdir -p "$fixture/assets"
printf 'large-content-from-lfs\n' >"$fixture/assets/blob.bin"
commit_fixture go-live-lfs >/dev/null
remote_deploy_default e2e-11-lfs 'grep -q large-content-from-lfs /srv/htdocs/assets/blob.bin'

rm -rf "$fixture/modules" "$fixture/.git/modules/modules/layer"
git -C "$fixture" submodule add https://github.com/aipokalyptik/wpcloud-site-git-deploy-layer-fixture.git modules/layer >/dev/null
cat >"$fixture/index.html" <<'EOF'
submodule-release
EOF
commit_fixture go-live-submodule >/dev/null
remote_deploy_default e2e-12-submodule 'grep -q submodule-release /srv/htdocs/index.html && grep -q layer /srv/htdocs/modules/layer/layer-owned'

remote_script e2e-13-auth-generate <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy auth --name cli-auth > /tmp/go-cli-auth-generated.out
grep -q '^ssh-ed25519 ' /tmp/go-cli-auth-generated.out
cp "$HOME/.wpcloud-site-git-deploy/keys/cli-auth_ed25519.pub" /tmp/go-cli-auth-generated.pub
REMOTE
copy_from_remote /tmp/go-cli-auth-generated.pub "$repo_root/tmp/go-live-cli-auth-generated.pub"
add_fixture_deploy_key "wpcloud-site-git-deploy-go-live-generated" "$repo_root/tmp/go-live-cli-auth-generated.pub"
remote_script e2e-14-auth-verify-doctor <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy auth --name cli-auth --verify
wpcloud-site-git-deploy doctor --name cli-auth
wpcloud-site-git-deploy deploy --name cli-auth
REMOTE

remote_script e2e-15-auth-use-import-remove <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
rm -f /tmp/go-existing-ed25519 /tmp/go-existing-ed25519.pub
ssh-keygen -t ed25519 -N "" -f /tmp/go-existing-ed25519 >/dev/null
wpcloud-site-git-deploy auth --name cli-auth --use-key /tmp/go-existing-ed25519
grep -q /tmp/go-existing-ed25519 "$HOME/.wpcloud-site-git-deploy/deployments/cli-auth/config.json"
wpcloud-site-git-deploy auth --name cli-auth --import-key /tmp/go-existing-ed25519 --force-new-key
test -f "$HOME/.wpcloud-site-git-deploy/keys/cli-auth_ed25519"
wpcloud-site-git-deploy auth --name cli-auth --remove --purge-key
test ! -f "$HOME/.wpcloud-site-git-deploy/keys/cli-auth_ed25519"
REMOTE

remote_script e2e-16-rollback-lists <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy releases --name cli-test | tee /tmp/go-releases.out
rollback_to="$(awk '/ current$/ {next} {print $1; exit}' /tmp/go-releases.out)"
test -n "$rollback_to"
wpcloud-site-git-deploy rollback --name cli-test --to "$rollback_to"
wpcloud-site-git-deploy branches --name cli-test --fetch --limit 5
wpcloud-site-git-deploy tags --name cli-test --fetch --limit 5
wpcloud-site-git-deploy commits --name cli-test --fetch --limit 5
count="$(find /srv/htdocs/.wpcloud-site-git-deploy/deployments/cli-test/releases -mindepth 1 -maxdepth 1 -type d | wc -l)"
test "$count" -le 5
REMOTE

log "## Completed"
log "- evidence: $evidence"
