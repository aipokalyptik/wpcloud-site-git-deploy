#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_root="${WPCLOUD_SITE_GIT_DEPLOY_HOME:-$HOME/.wpcloud-site-git-deploy}"
bin_dir="$install_root/bin"
lib_dir="$install_root/lib"

mkdir -p "$bin_dir" "$lib_dir" "$install_root/deployments" "$install_root/repos" "$install_root/tmp"
cp "$repo_root/bin/wpcloud-site-git-deploy" "$bin_dir/wpcloud-site-git-deploy"
cp "$repo_root/helpers/bin/linux-amd64/exchange-rename" "$bin_dir/exchange-rename"
cp "$repo_root/lib/remote-deploy.sh" "$lib_dir/remote-deploy.sh"
chmod 755 "$bin_dir/wpcloud-site-git-deploy" "$bin_dir/exchange-rename"
chmod 755 "$lib_dir/remote-deploy.sh"

printf 'installed to %s\n' "$install_root"
printf 'add this to PATH: %s\n' "$bin_dir"
