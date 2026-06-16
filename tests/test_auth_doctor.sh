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
derive=0
while (($#)); do
  case "$1" in
    -y) derive=1; shift ;;
    -f) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
[[ -n "$out" ]] || exit 64
if ((derive)); then
  if grep -Eq 'INVALID|ENCRYPTED' "$out"; then
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
cat >"$fake_bin/exchange-rename" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "$fake_bin"/*

export PATH="$fake_bin:$PATH"
export WPCLOUD_TEST_GIT_LOG="$tmpdir/git.log"
export WPCLOUD_TEST_SSH_LOG="$tmpdir/ssh.log"

[[ "$("$cli" --version)" == "1.1.3" ]] || fail "--version should report current release line"

HOME="$home_dir" "$cli" init site \
  --repo https://github.com/example/private-site.git \
  --docroot "$docroot" \
  --deployment-id site \
  --default-ref main >/dev/null
[[ ! -e "$home_dir/.wpcloud-site-git-deploy/bin/exchange-rename" ]] || fail "init should use exchange-rename from PATH when available"

HOME="$home_dir" "$cli" auth site >"$tmpdir/auth-github.txt"
key_path="$home_dir/.wpcloud-site-git-deploy/keys/site_ed25519"
config_file="$home_dir/.wpcloud-site-git-deploy/deployments/site.env"
[[ -f "$key_path" ]] || fail "auth should create private key"
[[ -f "$key_path.pub" ]] || fail "auth should create public key"
assert_contains "ssh_key_path=$key_path" "$config_file"
assert_contains "repo_url=git@github.com:example/private-site.git" "$config_file"
assert_contains "Add this public key as a read-only GitHub deploy key" "$tmpdir/auth-github.txt"
assert_contains "ssh-ed25519 PUBLICKEY wpcloud-test" "$tmpdir/auth-github.txt"

mtime_before="$(stat -c '%Y' "$key_path")"
HOME="$home_dir" "$cli" auth site >"$tmpdir/auth-reuse.txt"
mtime_after="$(stat -c '%Y' "$key_path")"
[[ "$mtime_before" == "$mtime_after" ]] || fail "auth should reuse existing key by default"
assert_contains "Reusing existing deploy key" "$tmpdir/auth-reuse.txt"

sleep 1
HOME="$home_dir" "$cli" auth site --force-new-key >"$tmpdir/auth-force.txt"
mtime_forced="$(stat -c '%Y' "$key_path")"
[[ "$mtime_forced" != "$mtime_after" ]] || fail "auth --force-new-key should replace existing key"

HOME="$home_dir" "$cli" doctor site >"$tmpdir/doctor-ok.txt"
assert_contains "OK config: deployment loaded" "$tmpdir/doctor-ok.txt"
assert_contains "OK helper: exchange-rename available: $fake_bin/exchange-rename" "$tmpdir/doctor-ok.txt"
assert_contains "OK ssh-key: private key is readable" "$tmpdir/doctor-ok.txt"
assert_contains "OK git-remote: remote access succeeded" "$tmpdir/doctor-ok.txt"
assert_contains "GIT_SSH_COMMAND=ssh -i $key_path -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new" "$tmpdir/git.log"

: >"$tmpdir/git.log"
HOME="$home_dir" "$cli" auth site --verify >"$tmpdir/auth-verify.txt"
assert_contains "Verified remote access for git@github.com:example/private-site.git" "$tmpdir/auth-verify.txt"
ls_remote_count="$(grep -c '^git ls-remote git@github.com:example/private-site.git HEAD ' "$tmpdir/git.log")"
[[ "$ls_remote_count" == "1" ]] || fail "auth --verify should run exactly one remote check; saw $ls_remote_count"

if HOME="$home_dir" "$cli" auth site --remove --verify >"$tmpdir/auth-remove-bad.txt" 2>&1; then
  fail "auth --remove --verify should fail"
fi
assert_contains "--remove cannot be combined with --verify, --force-new-key, --use-key, or --import-key" "$tmpdir/auth-remove-bad.txt"

HOME="$home_dir" "$cli" auth site --remove >"$tmpdir/auth-remove.txt"
assert_contains "Removed deploy key configuration for site" "$tmpdir/auth-remove.txt"
assert_not_contains "ssh_key_path=" "$config_file"
[[ -f "$key_path" ]] || fail "auth --remove should preserve private key by default"
[[ -f "$key_path.pub" ]] || fail "auth --remove should preserve public key by default"
: >"$tmpdir/git.log"
HOME="$home_dir" "$cli" doctor site --offline >"$tmpdir/doctor-no-key.txt"
assert_contains "WARN ssh-key: no deploy key configured" "$tmpdir/doctor-no-key.txt"
HOME="$home_dir" "$cli" doctor site >"$tmpdir/doctor-no-key-remote.txt"
assert_contains "OK git-remote: remote access succeeded" "$tmpdir/doctor-no-key-remote.txt"
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
HOME="$home_dir" "$cli" doctor site --offline >"$tmpdir/doctor-missing-pub.txt" || fail "doctor should accept a missing public key when it can derive one"
assert_contains "OK ssh-key: public key can be derived" "$tmpdir/doctor-missing-pub.txt"
printf 'ssh-ed25519 PUBLICKEY wpcloud-test\n' >"$key_path.pub"

external_key="$tmpdir/external_ed25519"
printf 'EXTERNAL PRIVATE KEY\n' >"$external_key"
chmod 600 "$external_key"
: >"$tmpdir/git.log"
HOME="$home_dir" "$cli" auth site --use-key "$external_key" --verify >"$tmpdir/auth-use-key.txt"
assert_contains "ssh_key_path=$external_key" "$config_file"
assert_contains "Using existing deploy key: $external_key" "$tmpdir/auth-use-key.txt"
assert_contains "ssh-ed25519 DERIVED-external_ed25519 wpcloud-test" "$tmpdir/auth-use-key.txt"
assert_contains "Verified remote access for git@github.com:example/private-site.git" "$tmpdir/auth-use-key.txt"
assert_contains "GIT_SSH_COMMAND=ssh -i $external_key -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new" "$tmpdir/git.log"
HOME="$home_dir" "$cli" auth site --remove --purge-key >"$tmpdir/auth-remove-external.txt"
assert_contains "Did not delete external key files: $external_key" "$tmpdir/auth-remove-external.txt"
[[ -f "$external_key" ]] || fail "auth --remove --purge-key must not delete external --use-key files"

import_source="$tmpdir/import_source_ed25519"
printf 'IMPORTED PRIVATE KEY\n' >"$import_source"
chmod 600 "$import_source"
HOME="$home_dir" "$cli" auth site --import-key "$import_source" --force-new-key >"$tmpdir/auth-import-key.txt"
assert_contains "Imported deploy key: $key_path" "$tmpdir/auth-import-key.txt"
assert_contains "ssh_key_path=$key_path" "$config_file"
assert_contains "ssh-ed25519 DERIVED-site_ed25519 wpcloud-test" "$tmpdir/auth-import-key.txt"
[[ -f "$key_path" ]] || fail "auth --import-key should create managed private key"
[[ -f "$key_path.pub" ]] || fail "auth --import-key should derive managed public key"
[[ "$(cat "$key_path")" == "IMPORTED PRIVATE KEY" ]] || fail "auth --import-key should copy private key content"
assert_contains "ssh-ed25519 DERIVED-site_ed25519 wpcloud-test" "$key_path.pub"
imported_mode="$(stat -c '%a' "$key_path")"
[[ "$imported_mode" == "600" ]] || fail "imported key should be chmod 600, got $imported_mode"
if HOME="$home_dir" "$cli" auth site --import-key "$import_source" >"$tmpdir/auth-import-overwrite.txt" 2>&1; then
  fail "auth --import-key should not overwrite a managed key without --force-new-key"
fi
assert_contains "managed deploy key already exists" "$tmpdir/auth-import-overwrite.txt"
printf 'REPLACEMENT PRIVATE KEY\n' >"$import_source"
HOME="$home_dir" "$cli" auth site --import-key "$import_source" --force-new-key >"$tmpdir/auth-import-force.txt"
[[ "$(cat "$key_path")" == "REPLACEMENT PRIVATE KEY" ]] || fail "auth --import-key --force-new-key should replace managed key"

missing_key="$tmpdir/missing_ed25519"
if HOME="$home_dir" "$cli" auth site --use-key "$missing_key" >"$tmpdir/auth-use-missing.txt" 2>&1; then
  fail "auth --use-key should fail for a missing key"
fi
assert_contains "private key is missing" "$tmpdir/auth-use-missing.txt"
unreadable_key="$tmpdir/unreadable_ed25519"
printf 'UNREADABLE PRIVATE KEY\n' >"$unreadable_key"
chmod 000 "$unreadable_key"
if HOME="$home_dir" "$cli" auth site --use-key "$unreadable_key" >"$tmpdir/auth-use-unreadable.txt" 2>&1; then
  fail "auth --use-key should fail for an unreadable key"
fi
assert_contains "private key is not readable" "$tmpdir/auth-use-unreadable.txt"
chmod 600 "$unreadable_key"
permissive_key="$tmpdir/permissive_ed25519"
printf 'PERMISSIVE PRIVATE KEY\n' >"$permissive_key"
chmod 644 "$permissive_key"
if HOME="$home_dir" "$cli" auth site --use-key "$permissive_key" >"$tmpdir/auth-use-permissive.txt" 2>&1; then
  fail "auth --use-key should fail for a permissive key"
fi
assert_contains "private key permissions are too open" "$tmpdir/auth-use-permissive.txt"
invalid_key="$tmpdir/invalid_ed25519"
printf 'INVALID PRIVATE KEY\n' >"$invalid_key"
chmod 600 "$invalid_key"
if HOME="$home_dir" "$cli" auth site --use-key "$invalid_key" >"$tmpdir/auth-use-invalid.txt" 2>&1; then
  fail "auth --use-key should fail for an invalid key"
fi
assert_contains "private key cannot be used without prompting" "$tmpdir/auth-use-invalid.txt"
encrypted_key="$tmpdir/encrypted_ed25519"
printf 'ENCRYPTED PRIVATE KEY\n' >"$encrypted_key"
chmod 600 "$encrypted_key"
if HOME="$home_dir" "$cli" auth site --import-key "$encrypted_key" --force-new-key >"$tmpdir/auth-import-encrypted.txt" 2>&1; then
  fail "auth --import-key should fail for a passphrase-protected key"
fi
assert_contains "private key cannot be used without prompting" "$tmpdir/auth-import-encrypted.txt"
if [[ -e "$key_path.pub" && ! -s "$key_path.pub" ]]; then
  fail "failed public key derivation must not leave an empty .pub file"
fi

printf 'SELF IMPORT PRIVATE KEY\n' >"$key_path"
chmod 600 "$key_path"
printf 'ssh-ed25519 old-pub wpcloud-test\n' >"$key_path.pub"
if HOME="$home_dir" "$cli" auth site --import-key "$key_path" --force-new-key >"$tmpdir/auth-import-self.txt" 2>&1; then
  fail "auth --import-key should fail cleanly when source is the managed key path"
fi
assert_contains "cannot import the managed deploy key onto itself" "$tmpdir/auth-import-self.txt"
[[ -f "$key_path" ]] || fail "self import must not delete managed private key"
[[ "$(cat "$key_path")" == "SELF IMPORT PRIVATE KEY" ]] || fail "self import must not modify managed private key"

no_ssh_keygen_bin="$tmpdir/no-ssh-keygen-auth-bin"
mkdir -p "$no_ssh_keygen_bin"
for command_name in bash git rsync find flock sort comm cut grep readlink ln rm mv mkdir mktemp touch cat stat date dirname pwd uname chmod cp basename; do
  command_path="$(command -v "$command_name")"
  ln -s "$command_path" "$no_ssh_keygen_bin/$command_name"
done
if HOME="$home_dir" PATH="$no_ssh_keygen_bin" "$cli" auth site --use-key "$external_key" >"$tmpdir/auth-use-no-ssh-keygen.txt" 2>&1; then
  fail "auth --use-key should fail clearly when ssh-keygen is missing"
fi
assert_contains "ssh-keygen is required to validate deploy keys" "$tmpdir/auth-use-no-ssh-keygen.txt"

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

https_home="$tmpdir/https-home"
https_docroot="$tmpdir/https-docroot"
mkdir -p "$https_home" "$https_docroot"
HOME="$https_home" "$cli" init https-site \
  --repo https://git.example.com/team/site.git \
  --docroot "$https_docroot" \
  --deployment-id https-site \
  --default-ref main >/dev/null
HOME="$https_home" "$cli" auth https-site --verify >"$tmpdir/auth-generic-https.txt"
https_config_file="$https_home/.wpcloud-site-git-deploy/deployments/https-site.env"
https_key_path="$https_home/.wpcloud-site-git-deploy/keys/https-site_ed25519"
assert_contains "repo_url=git@git.example.com:team/site.git" "$https_config_file"
assert_contains "ssh_key_path=$https_key_path" "$https_config_file"
assert_contains "Add this public key to the repository host for git.example.com" "$tmpdir/auth-generic-https.txt"
assert_contains "Verified remote access for git@git.example.com:team/site.git" "$tmpdir/auth-generic-https.txt"
assert_contains "git ls-remote git@git.example.com:team/site.git HEAD GIT_SSH_COMMAND=ssh -i $https_key_path" "$tmpdir/git.log"
HOME="$https_home" "$cli" auth https-site --remove >/dev/null
HOME="$https_home" "$cli" doctor https-site >"$tmpdir/doctor-generic-https.txt"
assert_contains "WARN ssh-key: no deploy key configured" "$tmpdir/doctor-generic-https.txt"
assert_contains "OK git-remote: remote access succeeded" "$tmpdir/doctor-generic-https.txt"
no_keygen_public_bin="$tmpdir/no-keygen-public-bin"
mkdir -p "$no_keygen_public_bin"
for command_name in bash rsync find flock sort comm cut grep readlink ln rm mv mkdir mktemp touch cat stat date dirname pwd uname chmod cp; do
  command_path="$(command -v "$command_name")"
  ln -s "$command_path" "$no_keygen_public_bin/$command_name"
done
ln -s "$fake_bin/git" "$no_keygen_public_bin/git"
HOME="$https_home" PATH="$no_keygen_public_bin" "$cli" doctor https-site >"$tmpdir/doctor-generic-https-no-keygen.txt"
assert_contains "WARN command: ssh-keygen not found" "$tmpdir/doctor-generic-https-no-keygen.txt"
assert_contains "WARN ssh-key: no deploy key configured" "$tmpdir/doctor-generic-https-no-keygen.txt"
assert_contains "OK git-remote: remote access succeeded" "$tmpdir/doctor-generic-https-no-keygen.txt"

missing_home="$tmpdir/missing-home"
missing_docroot="$tmpdir/missing-docroot"
mkdir -p "$missing_home" "$missing_docroot"
HOME="$missing_home" "$cli" init missing \
  --repo git@github.com:example/missing.git \
  --docroot "$missing_docroot" \
  --deployment-id missing \
  --default-ref main >/dev/null
HOME="$missing_home" PATH="/usr/bin:/bin" "$cli" doctor missing --offline >"$tmpdir/doctor-missing.txt"
assert_contains "WARN ssh-key: no deploy key configured" "$tmpdir/doctor-missing.txt"

no_keygen_bin="$tmpdir/no-keygen-bin"
mkdir -p "$no_keygen_bin"
for command_name in bash git rsync find flock sort comm cut grep readlink ln rm mv mkdir mktemp touch cat stat date dirname pwd uname chmod cp; do
  command_path="$(command -v "$command_name")"
  ln -s "$command_path" "$no_keygen_bin/$command_name"
done
if HOME="$home_dir" PATH="$no_keygen_bin" "$cli" doctor site --offline >"$tmpdir/doctor-no-ssh-keygen.txt" 2>&1; then
  fail "doctor should fail when ssh-keygen is missing"
fi
assert_contains "WARN command: ssh-keygen not found" "$tmpdir/doctor-no-ssh-keygen.txt"
assert_contains "FAIL ssh-key: ssh-keygen is required to validate deploy keys" "$tmpdir/doctor-no-ssh-keygen.txt"
