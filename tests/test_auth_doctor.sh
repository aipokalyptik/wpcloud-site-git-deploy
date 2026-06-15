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
home_dir="$tmpdir/home"
docroot="$tmpdir/docroot"
mkdir -p "$fake_bin" "$home_dir" "$docroot"

cat >"$fake_bin/ssh-keygen" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
out=""
while (($#)); do
  case "$1" in
    -f) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
[[ -n "$out" ]] || exit 64
printf 'PRIVATE KEY\n' >"$out"
printf 'ssh-ed25519 PUBLICKEY wpcloud-test\n' >"$out.pub"
chmod 600 "$out"
chmod 644 "$out.pub"
SH
cat >"$fake_bin/git" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf 'git %s GIT_SSH_COMMAND=%s\n' "$*" "${GIT_SSH_COMMAND:-}" >>"${WPCLOUD_TEST_GIT_LOG:?}"
case "$*" in
  ls-remote*) exit "${WPCLOUD_TEST_GIT_LS_REMOTE_STATUS:-0}" ;;
  *) exit 0 ;;
esac
SH
cat >"$fake_bin/ssh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf 'ssh %s\n' "$*" >>"${WPCLOUD_TEST_SSH_LOG:?}"
exit "${WPCLOUD_TEST_SSH_STATUS:-0}"
SH
cat >"$fake_bin/rsync" <<'SH'
#!/usr/bin/env bash
exit 0
SH
cat >"$fake_bin/flock" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "$fake_bin"/*

export PATH="$fake_bin:$PATH"
export WPCLOUD_TEST_GIT_LOG="$tmpdir/git.log"
export WPCLOUD_TEST_SSH_LOG="$tmpdir/ssh.log"

[[ "$("$cli" --version)" == "1.0.8" ]] || fail "--version should report current release line"

HOME="$home_dir" "$cli" init site \
  --repo https://github.com/example/private-site.git \
  --docroot "$docroot" \
  --deployment-id site \
  --default-ref main >/dev/null

HOME="$home_dir" "$cli" auth site >"$tmpdir/auth-github.txt"
key_path="$home_dir/.wpcloud-site-git-deploy/keys/site_ed25519"
config_file="$home_dir/.wpcloud-site-git-deploy/deployments/site.env"
[[ -f "$key_path" ]] || fail "auth should create private key"
[[ -f "$key_path.pub" ]] || fail "auth should create public key"
assert_contains "ssh_key_path=$key_path" "$config_file"
assert_contains "repo_url=git@github.com:example/private-site.git" "$config_file"
assert_contains "Add this public key as a read-only GitHub deploy key" "$tmpdir/auth-github.txt"
assert_contains "ssh-ed25519 PUBLICKEY wpcloud-test" "$tmpdir/auth-github.txt"

mtime_before="$(stat -f '%m' "$key_path" 2>/dev/null || stat -c '%Y' "$key_path")"
HOME="$home_dir" "$cli" auth site >"$tmpdir/auth-reuse.txt"
mtime_after="$(stat -f '%m' "$key_path" 2>/dev/null || stat -c '%Y' "$key_path")"
[[ "$mtime_before" == "$mtime_after" ]] || fail "auth should reuse existing key by default"
assert_contains "Reusing existing deploy key" "$tmpdir/auth-reuse.txt"

sleep 1
HOME="$home_dir" "$cli" auth site --force-new-key >"$tmpdir/auth-force.txt"
mtime_forced="$(stat -f '%m' "$key_path" 2>/dev/null || stat -c '%Y' "$key_path")"
[[ "$mtime_forced" != "$mtime_after" ]] || fail "auth --force-new-key should replace existing key"

HOME="$home_dir" "$cli" doctor site >"$tmpdir/doctor-ok.txt"
assert_contains "OK config: deployment loaded" "$tmpdir/doctor-ok.txt"
assert_contains "OK ssh-key: private key is readable" "$tmpdir/doctor-ok.txt"
assert_contains "OK git-remote: remote access succeeded" "$tmpdir/doctor-ok.txt"
assert_contains "GIT_SSH_COMMAND=ssh -i $key_path -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new" "$tmpdir/git.log"

HOME="$home_dir" "$cli" auth site --verify >"$tmpdir/auth-verify.txt"
assert_contains "Verified remote access for git@github.com:example/private-site.git" "$tmpdir/auth-verify.txt"

if HOME="$home_dir" "$cli" auth site --remove --verify >"$tmpdir/auth-remove-bad.txt" 2>&1; then
  fail "auth --remove --verify should fail"
fi
assert_contains "--remove cannot be combined with --verify or --force-new-key" "$tmpdir/auth-remove-bad.txt"

HOME="$home_dir" "$cli" auth site --remove >"$tmpdir/auth-remove.txt"
assert_contains "Removed deploy key configuration for site" "$tmpdir/auth-remove.txt"
assert_not_contains "ssh_key_path=" "$config_file"
[[ -f "$key_path" ]] || fail "auth --remove should preserve private key by default"
[[ -f "$key_path.pub" ]] || fail "auth --remove should preserve public key by default"
>"$tmpdir/git.log"
HOME="$home_dir" "$cli" doctor site --offline >"$tmpdir/doctor-no-key.txt" || true
assert_contains "FAIL ssh-key: no deploy key configured" "$tmpdir/doctor-no-key.txt"
HOME="$home_dir" "$cli" doctor site >"$tmpdir/doctor-no-key-remote.txt" || true
assert_contains "git ls-remote git@github.com:example/private-site.git HEAD GIT_SSH_COMMAND=" "$tmpdir/git.log"

HOME="$home_dir" "$cli" auth site >/dev/null
HOME="$home_dir" "$cli" auth site --remove --purge-key >"$tmpdir/auth-purge.txt"
assert_contains "Deleted deploy key files" "$tmpdir/auth-purge.txt"
[[ ! -e "$key_path" ]] || fail "auth --remove --purge-key should delete private key"
[[ ! -e "$key_path.pub" ]] || fail "auth --remove --purge-key should delete public key"
assert_not_contains "ssh_key_path=" "$config_file"

HOME="$home_dir" "$cli" auth site >/dev/null

chmod 666 "$key_path"
if HOME="$home_dir" "$cli" doctor site --offline >"$tmpdir/doctor-bad-key.txt"; then
  fail "doctor should fail for permissive private key"
fi
assert_contains "FAIL ssh-key: private key permissions are too open" "$tmpdir/doctor-bad-key.txt"
for allowed_mode in 400 600 700; do
  chmod "$allowed_mode" "$key_path"
  HOME="$home_dir" "$cli" doctor site --offline >"$tmpdir/doctor-key-$allowed_mode.txt" || fail "doctor should pass for owner-only private key mode $allowed_mode"
  assert_contains "OK ssh-key: private key is readable" "$tmpdir/doctor-key-$allowed_mode.txt"
done
for bad_mode in 640 644 660 666; do
  chmod "$bad_mode" "$key_path"
  if HOME="$home_dir" "$cli" doctor site --offline >"$tmpdir/doctor-key-$bad_mode.txt"; then
    fail "doctor should fail for private key mode $bad_mode"
  fi
  assert_contains "FAIL ssh-key: private key permissions are too open" "$tmpdir/doctor-key-$bad_mode.txt"
done
chmod 600 "$key_path"

rm -f "$key_path.pub"
if HOME="$home_dir" "$cli" doctor site --offline >"$tmpdir/doctor-missing-pub.txt"; then
  fail "doctor should fail when public key is missing"
fi
assert_contains "FAIL ssh-key: public key is missing" "$tmpdir/doctor-missing-pub.txt"
printf 'ssh-ed25519 PUBLICKEY wpcloud-test\n' >"$key_path.pub"

WPCLOUD_TEST_GIT_LS_REMOTE_STATUS=7 HOME="$home_dir" "$cli" doctor site >"$tmpdir/doctor-remote-fail.txt" && fail "doctor should fail when remote access fails"
assert_contains "FAIL git-remote: remote access failed" "$tmpdir/doctor-remote-fail.txt"

generic_home="$tmpdir/generic-home"
generic_docroot="$tmpdir/generic-docroot"
mkdir -p "$generic_home" "$generic_docroot"
HOME="$generic_home" "$cli" init generic \
  --repo git@git.example.com:team/site.git \
  --docroot "$generic_docroot" \
  --deployment-id generic \
  --default-ref main >/dev/null
HOME="$generic_home" "$cli" auth generic >"$tmpdir/auth-generic.txt"
assert_contains "Add this public key to the repository host for git.example.com" "$tmpdir/auth-generic.txt"

bad_home="$tmpdir/bad-home"
bad_docroot="$tmpdir/bad-docroot"
mkdir -p "$bad_home" "$bad_docroot"
HOME="$bad_home" "$cli" init bad \
  --repo https://git.example.com/team/site.git \
  --docroot "$bad_docroot" \
  --deployment-id bad \
  --default-ref main >/dev/null
if HOME="$bad_home" "$cli" auth bad >"$tmpdir/auth-bad.txt" 2>&1; then
  fail "auth should reject generic HTTPS remotes"
fi
assert_contains "generic HTTPS repository URLs cannot be configured for deploy keys automatically" "$tmpdir/auth-bad.txt"

missing_home="$tmpdir/missing-home"
missing_docroot="$tmpdir/missing-docroot"
mkdir -p "$missing_home" "$missing_docroot"
HOME="$missing_home" "$cli" init missing \
  --repo git@github.com:example/missing.git \
  --docroot "$missing_docroot" \
  --deployment-id missing \
  --default-ref main >/dev/null
if HOME="$missing_home" PATH="/usr/bin:/bin" "$cli" doctor missing --offline >"$tmpdir/doctor-missing.txt"; then
  fail "doctor should fail when auth has not configured an ssh key"
fi
assert_contains "FAIL ssh-key: no deploy key configured" "$tmpdir/doctor-missing.txt"
