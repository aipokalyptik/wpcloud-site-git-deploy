#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
binary="${WPCLOUD_SITE_GIT_DEPLOY_BINARY:-$repo_root/dist/wpcloud-site-git-deploy-linux-amd64}"

if [[ "$(uname -s)" != "Linux" ]]; then
  printf 'go conformance requires Linux; run it in Linux CI, a Linux VM/container, or a disposable WP Cloud/Pressable site.\n'
  exit 0
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

json_report_check="$tmpdir/json-report-check.go"
cat >"$json_report_check" <<'EOF'
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	data, err := os.ReadFile(os.Getenv("REPORT_PATH"))
	if err != nil {
		panic(err)
	}
	var record struct {
		SchemaVersion int    `json:"schema_version"`
		Status        string `json:"status"`
		Phases        []struct {
			Name       string `json:"name"`
			DurationMS int64  `json:"duration_ms"`
		} `json:"phases"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		lines := bytes.Split(data, []byte("\n"))
		for i := len(lines) - 1; i >= 0; i-- {
			if len(bytes.TrimSpace(lines[i])) > 0 {
				data = lines[i]
				break
			}
		}
		if err := json.Unmarshal(data, &record); err != nil {
			panic(err)
		}
	}
	if record.SchemaVersion != 1 {
		panic(fmt.Sprintf("unexpected schema version %d", record.SchemaVersion))
	}
	if record.Status != os.Getenv("EXPECTED_STATUS") {
		panic(fmt.Sprintf("unexpected status %q", record.Status))
	}
	if len(record.Phases) == 0 {
		panic("expected at least one phase")
	}
}
EOF

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local needle="$1"
  local file="$2"

  if ! grep -Fq -- "$needle" "$file"; then
    printf '%s\n' "--- $file ---" >&2
    cat "$file" >&2
    printf '%s\n' "--- end $file ---" >&2
    fail "expected $file to contain: $needle"
  fi
}

assert_not_contains() {
  local needle="$1"
  local file="$2"

  if grep -Fq -- "$needle" "$file"; then
    fail "expected $file not to contain: $needle"
  fi
}

assert_fails_contains() {
  local expected="$1"
  local output_file="$2"
  shift 2

  if "$@" >"$output_file" 2>&1; then
    fail "expected command to fail: $*"
  fi
  assert_contains "$expected" "$output_file"
}

inode_of() {
  stat -c '%i' "$1"
}

run_git() {
  local repo="$1"
  shift

  git -C "$repo" "$@"
}

commit_all() {
  local repo="$1"
  local message="$2"

  run_git "$repo" add -A
  run_git "$repo" commit -m "$message" >/dev/null
  run_git "$repo" rev-parse HEAD
}

release_count() {
  local docroot="$1"
  local deployment_id="$2"
  local releases_dir="$docroot/.wpcloud-site-git-deploy/deployments/$deployment_id/releases"

  if [[ ! -d "$releases_dir" ]]; then
    printf '0\n'
    return
  fi
  find "$releases_dir" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' '
}

report_path_from_output() {
  local file="$1"
  awk '
    {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^report=/) {
          sub(/^report=/, "", $i)
          print $i
          exit
        }
      }
    }
  ' "$file"
}

assert_json_report() {
  local path="$1"
  local expected_status="$2"

  test -n "$path" || fail "report path was not printed"
  test -f "$path" || fail "report path does not exist: $path"
  REPORT_PATH="$path" EXPECTED_STATUS="$expected_status" go run "$json_report_check"
}

if [[ ! -x "$binary" ]]; then
  mkdir -p "$(dirname "$binary")"
  version="$(git -C "$repo_root" describe --tags --dirty --always 2>/dev/null || printf dev)"
  GOCACHE="${GOCACHE:-$repo_root/.cache/go-build}" \
  GOMODCACHE="${GOMODCACHE:-$repo_root/.cache/go-mod}" \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
      -ldflags "-X github.com/aipokalyptik/wpcloud-site-git-deploy/internal/cli.Version=$version" \
      -o "$binary" ./cmd/wpcloud-site-git-deploy
fi

"$binary" --help >"$tmpdir/help.txt"
assert_contains "wpcloud-site-git-deploy" "$tmpdir/help.txt"
assert_fails_contains "unexpected argument: site" "$tmpdir/legacy-positional.txt" "$binary" deploy site
assert_fails_contains "unknown command: update" "$tmpdir/legacy-update.txt" "$binary" update --name site

home_dir="$tmpdir/home"
docroot="$tmpdir/docroot"
repo="$tmpdir/source"
mkdir -p "$home_dir" "$docroot" "$repo"

git -C "$repo" init -b main >/dev/null
git -C "$repo" config user.name "WP Cloud Deploy Test"
git -C "$repo" config user.email "wpcloud-deploy-test@example.invalid"
mkdir -p "$repo/assets" "$repo/public/wp-content/themes/demo" "$repo/.github/workflows" "$repo/.ssh" "$repo/.aws" "$repo/wp-content/uploads/static" "$repo/wp-content/blogs.dir/1/files"
printf 'hello from main\n' >"$repo/index.html"
printf 'asset v1\n' >"$repo/assets/app.txt"
printf 'root only\n' >"$repo/README.md"
printf 'deploy root content\n' >"$repo/public/index.html"
printf 'theme file\n' >"$repo/public/wp-content/themes/demo/style.css"
printf 'upload logo\n' >"$repo/wp-content/uploads/static/logo.txt"
printf 'blogs logo\n' >"$repo/wp-content/blogs.dir/1/files/logo.txt"
printf 'secret\n' >"$repo/.env"
printf 'workflow\n' >"$repo/.github/workflows/test.yml"
printf 'mac metadata\n' >"$repo/.DS_Store"
printf 'ignore\n' >"$repo/.gitignore"
printf '*.txt text\n' >"$repo/.gitattributes"
printf '[submodule "vendor/example"]\n\tpath = vendor/example\n\turl = https://example.invalid/example.git\n' >"$repo/.gitmodules"
main_commit="$(commit_all "$repo" initial)"
git -C "$repo" tag v1

printf 'hello from feature\n' >"$repo/index.html"
feature_commit="$(commit_all "$repo" feature)"
git -C "$repo" branch feature "$feature_commit"

HOME="$home_dir" "$binary" init \
  --name site \
  --repo "$repo" \
  --docroot "$docroot" \
  --deployment-id site \
  --default-ref main \
  --keep-releases 5 >/dev/null

HOME="$home_dir" "$binary" status --name site >"$tmpdir/status.txt"
assert_contains "name=site" "$tmpdir/status.txt"
assert_contains "keep_releases=5" "$tmpdir/status.txt"

HOME="$home_dir" "$binary" list >"$tmpdir/list.txt"
assert_contains "site" "$tmpdir/list.txt"

HOME="$home_dir" "$binary" deploy --name site --tag v1 >"$tmpdir/deploy-tag.txt"
assert_json_report "$(report_path_from_output "$tmpdir/deploy-tag.txt")" success
grep -Fx 'hello from main' "$docroot/index.html" >/dev/null || fail "tag deploy did not publish main content"
test ! -e "$docroot/.env" || fail ".env should be excluded"
test ! -e "$docroot/.github" || fail ".github should be excluded"
test ! -e "$docroot/.DS_Store" || fail ".DS_Store should be excluded"
test ! -e "$docroot/.gitignore" || fail ".gitignore should be excluded"
test ! -e "$docroot/.gitattributes" || fail ".gitattributes should be excluded"
test ! -e "$docroot/.gitmodules" || fail ".gitmodules should be excluded"
grep -Fx 'upload logo' "$docroot/wp-content/uploads/static/logo.txt" >/dev/null || fail "shared uploads leaf should deploy"
grep -Fx 'blogs logo' "$docroot/wp-content/blogs.dir/1/files/logo.txt" >/dev/null || fail "shared blogs.dir leaf should deploy"
tag_release="$(sed -E 's/^release_id=([^ ]+) .*/\1/' "$tmpdir/deploy-tag.txt")"

HOME="$home_dir" "$binary" deploy --name site --branch feature >"$tmpdir/deploy-branch.txt"
grep -Fx 'hello from feature' "$docroot/index.html" >/dev/null || fail "branch deploy did not publish feature content"
branch_release="$(sed -E 's/^release_id=([^ ]+) .*/\1/' "$tmpdir/deploy-branch.txt")"
if [[ "$(inode_of "$docroot/.wpcloud-site-git-deploy/deployments/site/releases/$tag_release/assets/app.txt")" != "$(inode_of "$docroot/.wpcloud-site-git-deploy/deployments/site/releases/$branch_release/assets/app.txt")" ]]; then
  fail "unchanged asset should be hardlinked across releases"
fi

HOME="$home_dir" "$binary" deploy --name site --commit "$main_commit" >/dev/null
grep -Fx 'hello from main' "$docroot/index.html" >/dev/null || fail "commit deploy did not publish main content"

printf 'hello from late main\n' >"$repo/index.html"
late_commit="$(commit_all "$repo" late-main)"
git -C "$repo" tag v2 "$late_commit"
git -C "$repo" branch late-branch "$late_commit"

HOME="$home_dir" "$binary" branches --name site >"$tmpdir/branches-cached.txt"
assert_not_contains "late-branch" "$tmpdir/branches-cached.txt"
HOME="$home_dir" "$binary" branches --name site --fetch >"$tmpdir/branches-fetch.txt"
assert_contains "late-branch" "$tmpdir/branches-fetch.txt"
HOME="$home_dir" "$binary" tags --name site --fetch --limit 5 >"$tmpdir/tags-fetch.txt"
assert_contains "v2" "$tmpdir/tags-fetch.txt"
HOME="$home_dir" "$binary" commits --name site --fetch --limit 3 >"$tmpdir/commits-fetch.txt"
assert_contains "${late_commit:0:7}" "$tmpdir/commits-fetch.txt"

HOME="$home_dir" "$binary" deploy --name site >"$tmpdir/deploy-default.txt"
grep -Fx 'hello from late main' "$docroot/index.html" >/dev/null || fail "default deploy did not publish latest main"
before_noop="$(release_count "$docroot" site)"
HOME="$home_dir" "$binary" deploy --name site >"$tmpdir/deploy-noop.txt"
assert_json_report "$(report_path_from_output "$tmpdir/deploy-noop.txt")" no_op
after_noop="$(release_count "$docroot" site)"
[[ "$before_noop" == "$after_noop" ]] || fail "no-op deploy should not create a new release"
assert_contains "no_op=true" "$tmpdir/deploy-noop.txt"
HOME="$home_dir" "$binary" deploy --name site --force >"$tmpdir/deploy-force.txt"
after_force="$(release_count "$docroot" site)"
[[ "$after_force" -gt "$after_noop" ]] || fail "force deploy should create a new release"

HOME="$home_dir" "$binary" config --name site --set deploy_root=public >/dev/null
HOME="$home_dir" "$binary" deploy --name site --force >/dev/null
grep -Fx 'deploy root content' "$docroot/index.html" >/dev/null || fail "deploy root should publish subfolder index at docroot root"
test ! -e "$docroot/public/index.html" || fail "deploy root should not publish subfolder name"
grep -Fx 'theme file' "$docroot/wp-content/themes/demo/style.css" >/dev/null || fail "deploy root should preserve paths inside subfolder"
HOME="$home_dir" "$binary" config --name site --set deploy_root=missing >/dev/null
assert_fails_contains "deploy root does not exist" "$tmpdir/deploy-root-missing.txt" env HOME="$home_dir" "$binary" deploy --name site --force
HOME="$home_dir" "$binary" config --name site --unset deploy_root >/dev/null

post_marker="$tmpdir/post-marker.txt"
hook="$tmpdir/post-deploy.sh"
cat >"$hook" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .maintenance
grep -F '\$upgrading = ' .maintenance >/dev/null
printf 'hook:%s:%s\n' "\$PWD" "\$(cat index.html)" >>"$post_marker"
SH
chmod +x "$hook"
HOME="$home_dir" "$binary" config --name site --set post_deploy="$hook" >/dev/null
HOME="$home_dir" "$binary" deploy --name site --force >/dev/null
assert_contains "hook:$docroot:" "$post_marker"
test ! -e "$docroot/.maintenance" || fail "successful deploy should remove maintenance marker"

failing_hook="$tmpdir/failing-post-deploy.sh"
cat >"$failing_hook" <<SH
#!/usr/bin/env bash
set -euo pipefail
test -f .maintenance
exit 23
SH
chmod +x "$failing_hook"
before_fail_current="$(readlink "$docroot/.wpcloud-site-git-deploy/deployments/site/current")"
assert_fails_contains "post-deploy failed" "$tmpdir/failing-hook.txt" env HOME="$home_dir" "$binary" deploy --name site --force --post-deploy "$failing_hook"
after_fail_current="$(readlink "$docroot/.wpcloud-site-git-deploy/deployments/site/current")"
[[ "$after_fail_current" != "$before_fail_current" ]] || fail "failing post-deploy should leave newly promoted release current"
test ! -e "$docroot/.maintenance" || fail "failing post-deploy should remove owned maintenance marker"
HOME="$home_dir" "$binary" config --name site --unset post_deploy >/dev/null
HOME="$home_dir" "$binary" config --name site --set maintenance=false >/dev/null
no_maintenance_hook="$tmpdir/no-maintenance.sh"
cat >"$no_maintenance_hook" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
test ! -e .maintenance
SH
chmod +x "$no_maintenance_hook"
HOME="$home_dir" "$binary" deploy --name site --force --post-deploy "$no_maintenance_hook" >/dev/null
test ! -e "$docroot/.maintenance" || fail "disabled maintenance should not create marker"
HOME="$home_dir" "$binary" config --name site --set maintenance=true >/dev/null

mkdir -p "$repo/wp-content/cache"
printf 'cache\n' >"$repo/wp-content/cache/object-cache.bin"
commit_all "$repo" shared-cache >/dev/null
assert_fails_contains "shared path cannot be deployed" "$tmpdir/shared-cache.txt" env HOME="$home_dir" "$binary" deploy --name site
rm -rf "$repo/wp-content/cache"
printf 'recovered\n' >"$repo/index.html"
commit_all "$repo" shared-cache-recovery >/dev/null
HOME="$home_dir" "$binary" deploy --name site >"$tmpdir/shared-cache-recovery.txt" 2>&1 || {
  cat "$tmpdir/shared-cache-recovery.txt" >&2
  fail "deploy should recover after shared path rejection"
}
grep -Fx 'recovered' "$docroot/index.html" >/dev/null || fail "deploy should recover after shared path rejection"

rm -f "$repo/wp-content/uploads/static/logo.txt" "$repo/wp-content/blogs.dir/1/files/logo.txt"
commit_all "$repo" remove-shared-media >/dev/null
HOME="$home_dir" "$binary" deploy --name site >/dev/null
test -d "$docroot/wp-content/uploads/static" || fail "shared media parent should remain after leaf removal"
test ! -e "$docroot/wp-content/uploads/static/logo.txt" || fail "removed shared media leaf symlink should be gone"

mkdir -p "$repo/wp-content/uploads"
ln -s target "$repo/wp-content/uploads/static-link"
commit_all "$repo" shared-media-symlink >/dev/null
assert_fails_contains "shared container symlink cannot be deployed" "$tmpdir/shared-symlink.txt" env HOME="$home_dir" "$binary" deploy --name site
rm -f "$repo/wp-content/uploads/static-link"
printf 'recovered again\n' >"$repo/index.html"
commit_all "$repo" shared-symlink-recovery >/dev/null
HOME="$home_dir" "$binary" deploy --name site >/dev/null

HOME="$home_dir" "$binary" releases --name site >"$tmpdir/releases.txt"
rollback_target="$(awk '/ current$/ {next} {print $1; exit}' "$tmpdir/releases.txt")"
[[ -n "$rollback_target" ]] || fail "expected rollback target"
HOME="$home_dir" "$binary" rollback --name site --to "$rollback_target" >"$tmpdir/rollback.txt"
assert_contains "rolled_back=$rollback_target" "$tmpdir/rollback.txt"
assert_fails_contains "rollback release does not exist" "$tmpdir/rollback-missing.txt" env HOME="$home_dir" "$binary" rollback --name site --to missing-release

bad_link="$docroot/bad-link"
ln -s "$docroot/.wpcloud-site-git-deploy/deployments/site/current/index.html" "$bad_link"
assert_fails_contains "public symlink target is absolute" "$tmpdir/audit-bad-link.txt" env HOME="$home_dir" "$binary" doctor --name site --assert-public-symlinks --offline
rm -f "$bad_link"
HOME="$home_dir" "$binary" doctor --name site --assert-public-symlinks --offline >"$tmpdir/doctor-audit.txt"
assert_contains "OK public-symlinks valid" "$tmpdir/doctor-audit.txt"

HOME="$home_dir" "$binary" destroy --name site --confirm-destroy=site >/dev/null
test ! -d "$home_dir/.wpcloud-site-git-deploy/deployments/site" || fail "destroy should remove deployment config"
test -e "$docroot/index.html" || fail "destroy should not unpublish docroot content"

auth_home="$tmpdir/auth-home"
auth_docroot="$tmpdir/auth-docroot"
fake_bin="$tmpdir/fake-bin"
mkdir -p "$auth_home" "$auth_docroot" "$fake_bin"
cat >"$fake_bin/git" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf 'git %s GIT_SSH_COMMAND=%s\n' "$*" "${GIT_SSH_COMMAND:-}" >>"${WPCLOUD_TEST_GIT_LOG:?}"
exit 0
SH
cat >"$fake_bin/ssh-keygen" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
derive=0
out=""
while (($#)); do
  case "$1" in
    -y) derive=1; shift ;;
    -f) out="$2"; shift 2 ;;
    -t|-N) shift 2 ;;
    *) shift ;;
  esac
done
if ((derive)); then
  if grep -q INVALID "$out"; then
    exit 255
  fi
  printf 'ssh-ed25519 DERIVED-%s wpcloud-test\n' "$(basename "$out")"
  exit 0
fi
printf 'PRIVATE KEY\n' >"$out"
printf 'ssh-ed25519 PUBLICKEY wpcloud-test\n' >"$out.pub"
chmod 600 "$out"
chmod 644 "$out.pub"
SH
cat >"$fake_bin/ssh" <<'SH'
#!/usr/bin/env bash
exit 0
SH
cat >"$fake_bin/rsync" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "$fake_bin"/*
export WPCLOUD_TEST_GIT_LOG="$tmpdir/git-auth.log"
PATH="$fake_bin:$PATH" HOME="$auth_home" "$binary" init --name authsite --repo https://git.example.com/team/site.git --docroot "$auth_docroot" --deployment-id authsite --default-ref main >/dev/null
PATH="$fake_bin:$PATH" HOME="$auth_home" "$binary" auth --name authsite --verify >"$tmpdir/auth-generate.txt"
assert_contains "ssh-ed25519" "$tmpdir/auth-generate.txt"
assert_contains "git ls-remote --heads git@git.example.com:team/site.git GIT_SSH_COMMAND=ssh -i" "$tmpdir/git-auth.log"
external_key="$tmpdir/external_ed25519"
printf 'EXTERNAL PRIVATE KEY\n' >"$external_key"
chmod 600 "$external_key"
PATH="$fake_bin:$PATH" HOME="$auth_home" "$binary" auth --name authsite --use-key "$external_key" >/dev/null
PATH="$fake_bin:$PATH" HOME="$auth_home" "$binary" auth --name authsite --remove --purge-key >/dev/null
test -f "$external_key" || fail "purge should not delete external --use-key file"
import_key="$tmpdir/import_ed25519"
printf 'IMPORTED PRIVATE KEY\n' >"$import_key"
chmod 600 "$import_key"
PATH="$fake_bin:$PATH" HOME="$auth_home" "$binary" auth --name authsite --import-key "$import_key" --force-new-key >/dev/null
test -f "$auth_home/.wpcloud-site-git-deploy/keys/authsite_ed25519" || fail "import should create managed key"
PATH="$fake_bin:$PATH" HOME="$auth_home" "$binary" doctor --name authsite --offline >"$tmpdir/doctor-auth.txt"
assert_contains "OK ssh-key usable" "$tmpdir/doctor-auth.txt"

printf 'go conformance harness: passed\n'
