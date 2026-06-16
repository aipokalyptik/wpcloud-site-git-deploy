# User Guide

`wpcloud-site-git-deploy` deploys files from a Git repository into a WP Cloud
or Pressable site docroot from inside the site SSH environment. It is intended
for WordPress sites where the production filesystem is reachable over SSH, but
HTTP requests do not have access to the SSH user's `$HOME`.

The tool keeps Git state, deployment configuration, temporary worktrees, and
deploy keys under `$HOME/.wpcloud-site-git-deploy`. It copies each web-visible
release into `/srv/htdocs/.wpcloud-site-git-deploy/deployments/<deployment-id>/` and
then exposes files through relative public symlinks under `/srv/htdocs`.

## Mental Model

A deployment has two names:

- `<name>` is the local CLI name, used in commands such as
  `wpcloud-site-git-deploy update site`.
- `<deployment-id>` is the docroot ownership namespace. It must be unique for
  each independent deployment that writes to the same docroot.

Each deploy:

1. Fetches the configured repository under
   `$HOME/.wpcloud-site-git-deploy/repos/<name>/`.
2. Resolves a branch, tag, or commit.
3. Creates a temporary Git worktree under
   `$HOME/.wpcloud-site-git-deploy/tmp/`.
4. Prepares submodules and Git LFS content when needed.
5. Copies the deployable tree into the docroot deployment namespace.
6. Promotes the release and atomically updates the public symlinks.
7. Prunes older releases according to `keep_releases`.

The public paths under `/srv/htdocs` never point back into `$HOME`. They are
relative symlinks into the docroot-contained deployment namespace.

## Requirements

Run this tool as the site SSH user.

Required on the site:

- Bash
- Git
- `rsync`
- GNU `find`
- `ssh-keygen` for deploy-key setup and validation
- `flock`, `sort`, `comm`, `cut`, `grep`, `cat`, `readlink`, `ln`, `mv`,
  `rm`, `mkdir`, `mktemp`, `touch`, and `stat`
- Git LFS only when your repository uses LFS-tracked paths

The shipped `exchange-rename` helper is Linux amd64 and uses
`renameat2(RENAME_EXCHANGE)`. Production deploys are expected to run on the
site's Linux SSH environment, not on macOS.

## Install Or Upgrade

From an SSH session on the site:

```bash
git clone https://github.com/aipokalyptik/wpcloud-site-git-deploy.git /tmp/wpcloud-site-git-deploy
/tmp/wpcloud-site-git-deploy/scripts/install.sh
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
```

Confirm the install:

```bash
wpcloud-site-git-deploy --version
wpcloud-site-git-deploy --help
```

The installer writes:

- `$HOME/.wpcloud-site-git-deploy/bin/wpcloud-site-git-deploy`
- `$HOME/.wpcloud-site-git-deploy/bin/exchange-rename`

For system or chroot packaging, `exchange-rename` may also be provided anywhere
in the site user's `PATH`. The CLI prefers the managed helper above when it
exists, then falls back to the first executable `exchange-rename` on `PATH`.

It also removes the obsolete
`$HOME/.wpcloud-site-git-deploy/lib/remote-deploy.sh` file if an older install
left one behind.

To upgrade later, repeat the install commands. Existing deployment config,
keys, repo caches, and releases are preserved.

## First Deployment

Initialize a deployment:

```bash
wpcloud-site-git-deploy init site \
  --repo https://github.com/example/site-content.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main \
  --keep-releases 3
```

Set up repository access:

```bash
wpcloud-site-git-deploy auth site
```

For HTTPS URLs, `auth` converts the stored URL to `git@host:path` SSH form.
Add the printed public key to the repository as a read-only deploy key, then
validate:

```bash
wpcloud-site-git-deploy doctor site
```

Deploy:

```bash
wpcloud-site-git-deploy deploy site --branch main
```

After the first deploy, normal ongoing updates usually use:

```bash
wpcloud-site-git-deploy update site
```

`update` fetches the configured default branch and deploys it only when the
active release does not already match that commit and deploy root.

## Authentication

The CLI controls Git authentication with a deployment-specific
`GIT_SSH_COMMAND`. It does not edit `~/.ssh/config`.

### Generate A Deploy Key

```bash
wpcloud-site-git-deploy auth site
```

This creates or reuses:

```text
$HOME/.wpcloud-site-git-deploy/keys/site_ed25519
```

Add the printed public key to your Git host as a read-only deploy key. For
GitHub, add it under repository settings, not under your personal account keys.
For HTTPS URLs, `auth` converts the stored URL to the equivalent SSH form
before writing the key path.

### Use An Existing Key In Place

Use this when the key already lives somewhere safe and you do not want the tool
to copy or delete it:

```bash
wpcloud-site-git-deploy auth site --use-key ~/.ssh/id_ed25519
```

The key must be readable by the SSH user, owner-only permissioned, and usable
without a passphrase prompt.

### Import An Existing Key

Use this when you want the tool to own a copy under its state directory:

```bash
wpcloud-site-git-deploy auth site --import-key ~/.ssh/id_ed25519
```

The private key is copied to the managed key path, chmodded to `600`, and a
`.pub` file is derived with `ssh-keygen -y`. If the managed key already exists,
replace it explicitly:

```bash
wpcloud-site-git-deploy auth site --import-key ~/.ssh/id_ed25519 --force-new-key
```

### Rotate A Generated Deploy Key

Use `--force-new-key` by itself to replace the tool-managed generated key:

```bash
wpcloud-site-git-deploy auth site --force-new-key
```

Add the new printed public key to the repository host, remove the old deploy
key from the host, then verify:

```bash
wpcloud-site-git-deploy doctor site
```

### Verify Access

After adding the public key to the Git host:

```bash
wpcloud-site-git-deploy auth site --verify
wpcloud-site-git-deploy doctor site
```

`doctor` checks local commands, config, docroot access, helper availability,
key permissions, public-key derivation, and remote repository access.

If the repository is public HTTPS, local, or already accessible through the
site user's default Git credentials, `auth` is optional. `doctor` will warn
that no tool-managed key is configured, then verify remote access with normal
Git behavior.

### Remove Key Configuration

Stop using the configured key:

```bash
wpcloud-site-git-deploy auth site --remove
```

Remove a managed key file too:

```bash
wpcloud-site-git-deploy auth site --remove --purge-key
```

Keys referenced with `--use-key` are external and are not deleted by
`--purge-key`.

## Deploy Roots

Use `--deploy-root` when the deployable files live in a subdirectory of the
repository. The CLI still checks out and prepares the whole repository, but
deploys the deploy root contents as the docroot root.

Example:

```bash
wpcloud-site-git-deploy init site \
  --repo git@github.com:example/site.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main \
  --deploy-root public
```

With this config, repository path `public/index.php` deploys to
`/srv/htdocs/index.php`, not `/srv/htdocs/public/index.php`.

Change or clear the deploy root later:

```bash
wpcloud-site-git-deploy config site --deploy-root build/output
wpcloud-site-git-deploy config site --clear-deploy-root
```

## Deploy Commands

Deploy a branch:

```bash
wpcloud-site-git-deploy deploy site --branch main
```

Deploy a tag:

```bash
wpcloud-site-git-deploy deploy site --tag v1.2.3
```

Deploy an exact commit:

```bash
wpcloud-site-git-deploy deploy site --commit 0123456789abcdef0123456789abcdef01234567
```

Deploy the configured default branch:

```bash
wpcloud-site-git-deploy update site
```

If the resolved commit and deploy root already match the active release,
`deploy` and `update` print a `no-op ...` line and exit successfully without
creating a new release. This makes `update` safe for WP Cloud API cron.

## Inspecting State

Show deployment config and active release:

```bash
wpcloud-site-git-deploy status site
```

List kept releases:

```bash
wpcloud-site-git-deploy releases site
```

List cached branches, tags, and commits:

```bash
wpcloud-site-git-deploy branches site
wpcloud-site-git-deploy tags site
wpcloud-site-git-deploy commits site --limit 10
```

Inspection commands read the local cache by default. Refresh first with
`--fetch`:

```bash
wpcloud-site-git-deploy branches site --fetch
wpcloud-site-git-deploy tags site --fetch
wpcloud-site-git-deploy commits site --fetch --limit 20
```

## Rollback

Rollback to the previous metadata-backed release:

```bash
wpcloud-site-git-deploy rollback site
```

Rollback to a specific release:

```bash
wpcloud-site-git-deploy releases site
wpcloud-site-git-deploy rollback site --to 20260613120000-abcdef123456-abcd
```

Rollback updates public symlinks and `current` back to an existing release. It
does not create a new release, fetch Git, or prune releases.

## Retention

`--keep-releases N` is configured during `init` and defaults to `3`. It controls
how many successful promoted release directories remain available after each
deploy.

Example:

```bash
wpcloud-site-git-deploy init site \
  --repo git@github.com:example/site.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main \
  --keep-releases 5
```

To change it later, edit:

```text
$HOME/.wpcloud-site-git-deploy/deployments/<name>.env
```

Set:

```bash
keep_releases=5
```

Then run the next deploy. Pruning happens after successful deploy promotion.

## Changing Initialized Settings

The `config` command only manages `deploy_root`. Other initialized settings are
stored in:

```text
$HOME/.wpcloud-site-git-deploy/deployments/<name>.env
```

Edit that file when you need to change:

- `repo_url`
- `docroot`
- `deployment_id`
- `default_ref`
- `keep_releases`

After changing `repo_url`, run `auth` and `doctor` again because repository
access may need a different deploy key. After changing `deployment_id`, the CLI
starts using a different docroot namespace; the old namespace and public
symlinks are not automatically removed. Reinitializing with a new `<name>` is
often clearer when changing both repository identity and deployment ownership.

## Multiple Deployments In One Docroot

Multiple deployments can share `/srv/htdocs` as long as they own different
paths and use different `deployment-id` values.

Example:

```bash
wpcloud-site-git-deploy init theme \
  --repo git@github.com:example/site-theme.git \
  --docroot /srv/htdocs \
  --deployment-id theme \
  --default-ref main

wpcloud-site-git-deploy init plugin \
  --repo git@github.com:example/site-plugin.git \
  --docroot /srv/htdocs \
  --deployment-id plugin \
  --default-ref main
```

The theme repository should contain only the paths it is meant to own, such as
`wp-content/themes/example-theme/...`. The plugin repository should contain only
its plugin paths, such as `wp-content/plugins/example-plugin/...`.

If two deployments try to claim the same path or overlapping paths, promotion
fails with a message such as:

```text
claim owned by another deployment: wp-content/plugins/example-plugin
```

That failure is intentional. Fix the repository contents or deploy roots so
each deployment owns a distinct path.

## Git LFS And Submodules

Submodules are initialized recursively during deploy.

Git LFS is used only when effective Git attributes mark tracked files as LFS
paths. If `git-lfs` is missing and LFS paths are present, deploy fails rather
than publishing unresolved pointer files.

Private submodules need credentials that are available to the site SSH user. A
private submodule without credentials fails before promotion, leaving the
current release unchanged.

## Safety Behavior

The tool is deliberately conservative:

- Public symlink targets must be relative.
- Public symlink targets must resolve under `/srv/htdocs`.
- Public symlink targets must not contain `$HOME`.
- Root/group-owned protected anchors are rejected.
- Sticky root/group-owned writable directories act as dynamic boundaries.
- Existing public paths are reclaimed only through the Linux
  `exchange-rename` helper.
- Deploy-time assertions validate only the final claims owned by that
  deployment, while the hidden full-docroot audit remains available for tests
  and diagnostics.

Default excludes prevent common Git, credential, and local metadata files from
being published, including `.git`, `.git/`, `.gitignore`, `.gitattributes`,
`.gitmodules`, `.github/`, `.env`, `.aws/`, `.ssh/`, `.npmrc`, `.pypirc`,
`.netrc`, and `.DS_Store`.

## Troubleshooting

Run `doctor` first:

```bash
wpcloud-site-git-deploy doctor site
```

Use offline mode when the Git host is intentionally unavailable:

```bash
wpcloud-site-git-deploy doctor site --offline
```

Common failures:

- `no deploy key configured`: informational warning. For private SSH remotes,
  run `auth`, add the printed public key to the repository host, then run
  `doctor`. For public HTTPS, local, or pre-authenticated remotes, confirm the
  following `git-remote` check succeeds.
- `remote access failed`: the key is not on the Git host, the repo URL is
  wrong, or the key lacks repository access.
- `private key permissions are too open`: run `chmod 600 <key>`.
- `private key cannot be used without prompting`: use an unencrypted deploy
  key; passphrase prompts are not supported for unattended deploys.
- `protected path: wp-load.php`: the repository is trying to own a host-owned
  WordPress anchor. Remove that path from the repository or deploy a narrower
  subdirectory.
- `claim owned by another deployment`: two deployment namespaces overlap.
  Split the paths so each deployment owns a distinct subtree.
- `could not read Username for 'https://...'`: a private HTTPS repo or
  submodule needs credentials. Prefer SSH deploy keys for unattended deploys,
  or configure non-interactive HTTPS credentials for the site user.

## Command Reference

```text
wpcloud-site-git-deploy init <name> --repo URL --docroot /srv/htdocs --deployment-id ID --default-ref main [--keep-releases N] [--deploy-root PATH]
wpcloud-site-git-deploy config <name> [--deploy-root PATH | --clear-deploy-root]
wpcloud-site-git-deploy deploy <name> --branch BRANCH
wpcloud-site-git-deploy deploy <name> --tag TAG
wpcloud-site-git-deploy deploy <name> --commit SHA
wpcloud-site-git-deploy update <name>
wpcloud-site-git-deploy rollback <name> [--to RELEASE_ID]
wpcloud-site-git-deploy releases <name>
wpcloud-site-git-deploy branches <name> [--fetch] [--limit N]
wpcloud-site-git-deploy tags <name> [--fetch] [--limit N]
wpcloud-site-git-deploy commits <name> [--fetch] [--limit N]
wpcloud-site-git-deploy status <name>
wpcloud-site-git-deploy auth <name> [--force-new-key] [--verify] [--use-key PATH | --import-key PATH | --remove [--purge-key]]
wpcloud-site-git-deploy doctor <name> [--offline]
wpcloud-site-git-deploy --help
wpcloud-site-git-deploy --version
```
