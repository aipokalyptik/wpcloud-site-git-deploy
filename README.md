# WP Cloud Site Git Deploy

`wpcloud-site-git-deploy` is a Bash CLI for deploying a Git repository from an SSH session on a WP Cloud or Pressable site.

It keeps Git checkouts, config, and credentials under `$HOME`, but copies every web-visible release into the docroot deployment namespace before promotion. Public symlinks are always relative links into `/srv/htdocs/.github-ssh-deploy/deployments/<deployment-id>/current/...`; they never point back into `$HOME`.

The installed runtime is intentionally small: one Bash CLI plus a static Linux
`exchange-rename` helper for atomic path swaps. The remote promotion engine is
embedded in the CLI and is not installed as a separate `lib/` file.

## Documentation Map

- [docs/code-flow.md](docs/code-flow.md) explains the main CLI and embedded
  remote deployment flow.
- [docs/testing.md](docs/testing.md) explains local verification, CI, and the
  live WP Cloud/Pressable E2E matrix.

## Requirements

On the site SSH user:

- Bash
- Git
- `rsync`
- GNU `find`
- `flock`, `sort`, `comm`, `cut`, `grep`, `readlink`, `ln`, `mv`, `rm`,
  `mkdir`, `mktemp`, and `touch`
- Git LFS only when the deployed repository has LFS-tracked paths

The committed helper binary is Linux amd64 and uses
`renameat2(RENAME_EXCHANGE)`. macOS is supported for local tests through test
shims, not as a production deploy target.

## Install

From an SSH session on the site:

```bash
git clone https://github.com/aipokalyptik/wpcloud-site-git-deploy.git /tmp/wpcloud-site-git-deploy
/tmp/wpcloud-site-git-deploy/scripts/install.sh
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
```

Installed runtime files:

- `$HOME/.wpcloud-site-git-deploy/bin/wpcloud-site-git-deploy`
- `$HOME/.wpcloud-site-git-deploy/bin/exchange-rename`

Runtime state created as you initialize and deploy sites:

- `$HOME/.wpcloud-site-git-deploy/deployments/<name>.env`
- `$HOME/.wpcloud-site-git-deploy/keys/<name>_ed25519`
- `$HOME/.wpcloud-site-git-deploy/repos/<name>/`
- `$HOME/.wpcloud-site-git-deploy/tmp/`

The installer also removes the obsolete
`$HOME/.wpcloud-site-git-deploy/lib/remote-deploy.sh` file if an older install
left one behind.

## Quick Start

```bash
wpcloud-site-git-deploy init site \
  --repo https://github.com/example/site-content.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main

wpcloud-site-git-deploy auth site
# Add the printed public key to the repository as a read-only deploy key.
wpcloud-site-git-deploy doctor site

wpcloud-site-git-deploy deploy site --branch main
wpcloud-site-git-deploy deploy site --tag v1.2.3
wpcloud-site-git-deploy deploy site --commit 0123456789abcdef
wpcloud-site-git-deploy update site
wpcloud-site-git-deploy rollback site
```

For repositories where the deployable files live in a subdirectory, configure a
deploy root:

```bash
wpcloud-site-git-deploy init site \
  --repo https://github.com/example/site-content.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main \
  --deploy-root public

wpcloud-site-git-deploy config site --deploy-root build/output
wpcloud-site-git-deploy config site --clear-deploy-root
```

When `deploy_root` is set, the CLI still checks out and prepares the full
repository, but deploys the contents of that subdirectory as the docroot root.
For example, `public/index.php` deploys to `/srv/htdocs/index.php`, not
`/srv/htdocs/public/index.php`.

Inspection and status commands:

```bash
wpcloud-site-git-deploy status site
wpcloud-site-git-deploy releases site
wpcloud-site-git-deploy branches site
wpcloud-site-git-deploy tags site
wpcloud-site-git-deploy commits site --limit 10
wpcloud-site-git-deploy branches site --fetch
wpcloud-site-git-deploy doctor site --offline
```

Branch, tag, and commit inspection commands read from the local repository
cache by default. Add `--fetch` when you want those commands to refresh the
cache from the remote before listing refs.

Command output is script-friendly:

- `deploy` prints `<release-id> <ref-mode> <commit>`.
- `update` prints `<release-id> branch <commit>`.
- `deploy` and `update` print `no-op <release-id> <ref-mode> <commit>` when
  the fetched commit and configured deploy root already match the active
  release.
- `rollback` prints `rolled back to <release-id>`.
- `status` prints `name`, `repo`, `docroot`, `deployment_id`,
  `default_ref`, `deploy_root`, and `current` as `key=value` lines.
- `doctor` prints `OK`, `WARN`, and `FAIL` lines and exits nonzero when any
  required check fails.

## Git Auth

This tool runs as the site SSH user, so Git credentials live in that user’s `$HOME`, outside the HTTP request context.

For SSH remotes, run:

```bash
wpcloud-site-git-deploy auth site
```

`auth` creates or reuses
`$HOME/.wpcloud-site-git-deploy/keys/site_ed25519`, stores that path in the
deployment config, and prints the public key to add to the repository host as a
read-only deploy key. For GitHub HTTPS URLs, it converts the stored repository
URL to the equivalent SSH URL before writing the key path.

After adding the public key to the repository host, run:

```bash
wpcloud-site-git-deploy doctor site
```

The CLI does not edit `~/.ssh/config`. When `ssh_key_path` is configured, Git
network operations run with a tool-managed `GIT_SSH_COMMAND` that pins the
deployment to that key.

To stop using the configured deploy key:

```bash
wpcloud-site-git-deploy auth site --remove
```

That clears `ssh_key_path` from the deployment config and leaves key files in
place. Add `--purge-key` to delete key files managed under
`$HOME/.wpcloud-site-git-deploy/keys/`.

For HTTPS remotes, use Git’s standard credential storage or an HTTPS URL/token mechanism appropriate for the site user. Do not place credentials in the repository being deployed.

## Git LFS And Submodules

Submodules are initialized recursively during deploy.

Git LFS is supported when `git-lfs` is available in the site user’s `PATH`.
The CLI does not install Git LFS automatically; install it under `$HOME` or
another user-writable location if the host does not provide it. Deploys fail
instead of publishing pointer files when LFS content remains unresolved.

LFS detection is based on effective Git attributes for tracked paths. After
`git lfs pull`, pointer rejection is scoped to paths that are actually
LFS-tracked so ordinary text files that resemble pointer headers are not
rejected.

## Deploy Model

Each deploy:

1. Fetches or clones the repository under `$HOME/.wpcloud-site-git-deploy/repos/<name>/`.
2. Resolves the requested branch, tag, commit, or configured default ref.
3. Creates a clean worktree under `$HOME/.wpcloud-site-git-deploy/tmp/`.
4. Prepares Git LFS files and submodules when present.
5. Copies deployable files, or the configured deploy-root subdirectory, into `/srv/htdocs/.github-ssh-deploy/deployments/<deployment-id>/incoming/<release-id>/`, using `rsync --link-dest` against the active release when possible so unchanged files are hardlinked across kept releases.
6. Promotes that incoming tree to `releases/<release-id>/`.
7. Reconciles public symlinks and atomically flips `current`.

The previous release tree and public symlinks are used as deploy truth; no manifest is required.

Repository fetches run `git gc --auto` after a successful
`fetch --tags --prune origin`. Deploy and update always fetch. Branch, tag, and
commit inspection only fetch when `--fetch` is provided.

When `ssh_key_path` is configured by `auth`, clone, fetch, Git LFS pull, and
recursive submodule updates all use the configured deploy key through
`GIT_SSH_COMMAND`. The generated SSH command uses
`StrictHostKeyChecking=accept-new`, so the first connection to a Git host uses
trust-on-first-use host-key acceptance instead of requiring users to edit
`~/.ssh/config`.

After fetching and resolving the requested ref, deploy and update compare the
resolved commit plus configured deploy root to the active release metadata. If
both match, the command exits successfully without creating a worktree,
incoming release, promoted release, metadata file, or pruning pass.

## Safety Rules

- Public symlink targets must be relative.
- Public symlink targets must resolve under the configured docroot.
- Public symlink targets must not contain `$HOME`.
- Root/group-owned non-writable anchors are protected from deploy claims.
- Sticky root/group-owned writable directories act as dynamic boundaries, which keeps WordPress plugin/theme deployments from claiming too broad a path.
- Existing paths may be reclaimed only through the atomic `exchange-rename` helper on Linux.
- Deploy-time symlink assertions are scoped to the final claims owned by that
  deployment. The hidden full-docroot audit remains available through
  `wpcloud-site-git-deploy __remote-deploy --assert-public-symlinks` for tests
  and diagnostics.

Default excludes include common Git, credential, and local metadata files such as `.git`, `.git/`, `.gitignore`, `.gitattributes`, `.gitmodules`, `.github/`, `.env`, `.aws/`, `.ssh/`, `.npmrc`, `.pypirc`, `.netrc`, and `.DS_Store`.

## Rollback

```bash
wpcloud-site-git-deploy rollback site
wpcloud-site-git-deploy rollback site --to 20260613120000-abcdef123456-abcd
```

Rollback uses the same conservative symlink reconciliation path as deploy. It flips `current` back to an existing release and cleans only symlinks owned by that deployment.

When `--to` is omitted, rollback prefers metadata-backed successful releases
and skips failed promotions that left a release directory without success
metadata.

## Development

```bash
tests/run.sh
```

Linux CI exercises the real GNU tooling and static `renameat2(RENAME_EXCHANGE)` helper. macOS local tests use small shims for Linux-only command behavior.

Before committing documentation-only changes, run:

```bash
git diff --check
```

Before release-critical changes, run the local suite and then repeat the live
throwaway WP Cloud/Pressable E2E matrix described in
[docs/testing.md](docs/testing.md).
