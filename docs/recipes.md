# Recipes

These recipes assume the CLI is installed on the WP Cloud or Pressable site and
available in the SSH user's `PATH`.

```bash
export PATH="$HOME/.wpcloud-site-git-deploy/bin:$PATH"
```

Use `/srv/htdocs` for the docroot in examples. Do not use `~/htdocs` in
configuration, even if your host exposes it as a symlink.

## One-Time Setup For A GitHub Repository

Use this when the deployable repository is on GitHub and you want the CLI to
create the deploy key.

```bash
wpcloud-site-git-deploy init site \
  --repo https://github.com/example/site-content.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main \
  --keep-releases 3

wpcloud-site-git-deploy auth site
```

Add the printed public key to GitHub:

1. Open the repository on GitHub.
2. Go to Settings, then Deploy keys.
3. Add the printed key.
4. Leave write access disabled.

Verify:

```bash
wpcloud-site-git-deploy doctor site
wpcloud-site-git-deploy update site
```

## Use An Existing Deploy Key

Use this when an unencrypted deploy key already exists on the site.

```bash
chmod 600 ~/.ssh/site_deploy_key

wpcloud-site-git-deploy init site \
  --repo git@github.com:example/site-content.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main

wpcloud-site-git-deploy auth site --use-key ~/.ssh/site_deploy_key
wpcloud-site-git-deploy doctor site
```

The key stays where it is. `auth --remove --purge-key` does not delete it.

## Import An Existing Deploy Key

Use this when you want the CLI to manage a copy of an existing unencrypted key.

```bash
chmod 600 ~/.ssh/site_deploy_key

wpcloud-site-git-deploy auth site --import-key ~/.ssh/site_deploy_key
wpcloud-site-git-deploy doctor site
```

If the managed key already exists:

```bash
wpcloud-site-git-deploy auth site --import-key ~/.ssh/site_deploy_key --force-new-key
```

## Rotate A Generated Deploy Key

Use this when the CLI created the key and you want to replace it.

```bash
wpcloud-site-git-deploy auth site --force-new-key
```

Add the new printed public key to the repository host, remove the old deploy
key from the host, and verify:

```bash
wpcloud-site-git-deploy doctor site
```

## Keep A Site Fresh With Cron

`update` is safe for cron because it is a no-op when the fetched commit and
deploy root already match the active release.

Edit the site user's crontab:

```bash
crontab -e
```

Run every five minutes:

```cron
*/5 * * * * PATH="$HOME/.wpcloud-site-git-deploy/bin:$HOME/.local/bin:/usr/local/bin:/usr/bin:/bin" wpcloud-site-git-deploy update site >> "$HOME/.wpcloud-site-git-deploy/cron-site.log" 2>&1
```

Run every hour:

```cron
0 * * * * PATH="$HOME/.wpcloud-site-git-deploy/bin:$HOME/.local/bin:/usr/local/bin:/usr/bin:/bin" wpcloud-site-git-deploy update site >> "$HOME/.wpcloud-site-git-deploy/cron-site.log" 2>&1
```

Check recent cron output:

```bash
tail -100 "$HOME/.wpcloud-site-git-deploy/cron-site.log"
```

Expected no-change output looks like:

```text
no-op 20260613120000-abcdef123456-abcd branch abcdef1234567890...
```

## Trigger Updates From GitHub Actions

This pattern keeps the deployment engine on the site. GitHub Actions only SSHes
into the site and runs `wpcloud-site-git-deploy update`.

Create repository secrets:

- `WPCLOUD_SSH_HOST`, such as `ssh.atomicsites.net` or the host your provider
  gives you.
- `WPCLOUD_SSH_PORT`, usually `22`.
- `WPCLOUD_SSH_USER`, the site SSH username.
- `WPCLOUD_SSH_PASSWORD`, if using password SSH.

Example workflow using password SSH:

```yaml
name: Deploy Site

on:
  push:
    branches: [main]
  workflow_dispatch:

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Install SSH password helper
        run: sudo apt-get update && sudo apt-get install -y sshpass

      - name: Update site deployment
        env:
          SSH_HOST: ${{ secrets.WPCLOUD_SSH_HOST }}
          SSH_PORT: ${{ secrets.WPCLOUD_SSH_PORT }}
          SSH_USER: ${{ secrets.WPCLOUD_SSH_USER }}
          SSH_PASS: ${{ secrets.WPCLOUD_SSH_PASSWORD }}
        run: |
          sshpass -p "$SSH_PASS" ssh \
            -p "${SSH_PORT:-22}" \
            -o StrictHostKeyChecking=accept-new \
            "$SSH_USER@$SSH_HOST" \
            'export PATH="$HOME/.wpcloud-site-git-deploy/bin:$HOME/.local/bin:$PATH"; wpcloud-site-git-deploy update site'
```

Example workflow using an SSH private key secret:

```yaml
name: Deploy Site

on:
  push:
    branches: [main]
  workflow_dispatch:

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Write SSH key
        env:
          SSH_KEY: ${{ secrets.WPCLOUD_SSH_PRIVATE_KEY }}
        run: |
          install -m 700 -d ~/.ssh
          printf '%s\n' "$SSH_KEY" > ~/.ssh/wpcloud_site
          chmod 600 ~/.ssh/wpcloud_site

      - name: Update site deployment
        env:
          SSH_HOST: ${{ secrets.WPCLOUD_SSH_HOST }}
          SSH_PORT: ${{ secrets.WPCLOUD_SSH_PORT }}
          SSH_USER: ${{ secrets.WPCLOUD_SSH_USER }}
        run: |
          ssh \
            -i ~/.ssh/wpcloud_site \
            -p "${SSH_PORT:-22}" \
            -o IdentitiesOnly=yes \
            -o StrictHostKeyChecking=accept-new \
            "$SSH_USER@$SSH_HOST" \
            'export PATH="$HOME/.wpcloud-site-git-deploy/bin:$HOME/.local/bin:$PATH"; wpcloud-site-git-deploy update site'
```

Notes:

- Run `init`, `auth`, and `doctor` on the site before relying on the workflow.
- The GitHub Actions SSH credential is for logging into the site.
- The site's deploy key is for the site to read the content repository.
- Those are different credentials and should not be confused.

## Install Or Upgrade From GitHub Actions

Use this when you want a workflow to install or upgrade the CLI before updating
the site.

```yaml
name: Install Or Update Deploy CLI

on:
  workflow_dispatch:

jobs:
  install:
    runs-on: ubuntu-latest
    steps:
      - name: Install SSH password helper
        run: sudo apt-get update && sudo apt-get install -y sshpass

      - name: Install CLI on site
        env:
          SSH_HOST: ${{ secrets.WPCLOUD_SSH_HOST }}
          SSH_PORT: ${{ secrets.WPCLOUD_SSH_PORT }}
          SSH_USER: ${{ secrets.WPCLOUD_SSH_USER }}
          SSH_PASS: ${{ secrets.WPCLOUD_SSH_PASSWORD }}
        run: |
          sshpass -p "$SSH_PASS" ssh \
            -p "${SSH_PORT:-22}" \
            -o StrictHostKeyChecking=accept-new \
            "$SSH_USER@$SSH_HOST" \
            'set -e; rm -rf /tmp/wpcloud-site-git-deploy; git clone https://github.com/aipokalyptik/wpcloud-site-git-deploy.git /tmp/wpcloud-site-git-deploy; /tmp/wpcloud-site-git-deploy/scripts/install.sh; $HOME/.wpcloud-site-git-deploy/bin/wpcloud-site-git-deploy --version'
```

For stable release use, install from the moving major tag:

```bash
git clone --branch v1 --depth 1 https://github.com/aipokalyptik/wpcloud-site-git-deploy.git /tmp/wpcloud-site-git-deploy
```

For an immutable release, replace `v1` with a specific tag such as `v1.1.0`.

## Multiple Repositories Into One WordPress Site

Use separate deployment names and deployment IDs for independent repositories.
Each repository should contain only the paths it owns.

Theme repository:

```bash
wpcloud-site-git-deploy init theme \
  --repo git@github.com:example/theme-repo.git \
  --docroot /srv/htdocs \
  --deployment-id theme \
  --default-ref main

wpcloud-site-git-deploy auth theme
wpcloud-site-git-deploy doctor theme
wpcloud-site-git-deploy update theme
```

Plugin repository:

```bash
wpcloud-site-git-deploy init plugin \
  --repo git@github.com:example/plugin-repo.git \
  --docroot /srv/htdocs \
  --deployment-id plugin \
  --default-ref main

wpcloud-site-git-deploy auth plugin
wpcloud-site-git-deploy doctor plugin
wpcloud-site-git-deploy update plugin
```

Example repository layouts:

```text
theme-repo/
  wp-content/themes/example-theme/style.css
  wp-content/themes/example-theme/functions.php

plugin-repo/
  wp-content/plugins/example-plugin/example-plugin.php
```

Do not let both repositories contain the same public path. Cross-deployment
overlap fails promotion to protect the active site.

## Multiple Repositories With One Shared Deploy Key

Use one external key when a Git host allows the same key to read multiple
repositories.

```bash
chmod 600 ~/.ssh/shared_readonly_deploy_key

wpcloud-site-git-deploy auth theme --use-key ~/.ssh/shared_readonly_deploy_key
wpcloud-site-git-deploy auth plugin --use-key ~/.ssh/shared_readonly_deploy_key

wpcloud-site-git-deploy doctor theme
wpcloud-site-git-deploy doctor plugin
```

If your Git host does not allow the same deploy key on multiple repositories,
create or import one key per deployment.

## Monorepo With A Build Output Directory

If your repository has source code and deployable output in a subdirectory,
deploy only the output directory:

```bash
wpcloud-site-git-deploy init site \
  --repo git@github.com:example/site-monorepo.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main \
  --deploy-root public
```

Repository layout:

```text
site-monorepo/
  package.json
  src/
  public/
    index.php
    wp-content/
```

Deploy result:

```text
/srv/htdocs/index.php
/srv/htdocs/wp-content/
```

The `public/` directory name is not deployed as an extra path component.

## GitHub Actions Build, Then Site Pulls Build Output

This pattern keeps build tools out of the WP Cloud site. GitHub Actions builds
artifacts and commits or pushes deployable output to a repository branch that
the site pulls.

Example:

```yaml
name: Build Deploy Output

on:
  push:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-node@v5
        with:
          node-version: 22
      - run: npm ci
      - run: npm run build
      - name: Publish build output branch
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          rm -rf /tmp/deploy-output
          mkdir -p /tmp/deploy-output
          cp -R public/. /tmp/deploy-output/
          git switch --orphan deploy-output
          git rm -rf .
          cp -R /tmp/deploy-output/. .
          git add -A
          git commit -m "Build deploy output"
          git push --force origin deploy-output
```

On the site:

```bash
wpcloud-site-git-deploy init site \
  --repo git@github.com:example/site-monorepo.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref deploy-output
```

Before the first `update`, configure repository access and validate it:

```bash
wpcloud-site-git-deploy auth site
wpcloud-site-git-deploy doctor site
```

Then deploy with:

```bash
wpcloud-site-git-deploy update site
```

## Deploy A Tag For Manual Releases

Use tags when production should move only when a release tag is selected.

```bash
wpcloud-site-git-deploy deploy site --tag v2.4.0
```

Inspect available tags:

```bash
wpcloud-site-git-deploy tags site --fetch --limit 20
```

Rollback if needed:

```bash
wpcloud-site-git-deploy rollback site
```

## Deploy An Exact Commit

Use an exact commit when you need a reproducible deployment that is independent
of branch movement.

```bash
wpcloud-site-git-deploy deploy site --commit 0123456789abcdef0123456789abcdef01234567
```

Find recent commits:

```bash
wpcloud-site-git-deploy commits site --fetch --limit 10
```

## Roll Back And Then Re-Deploy The Latest Branch

Rollback immediately:

```bash
wpcloud-site-git-deploy releases site
wpcloud-site-git-deploy rollback site
```

After fixing the repository, deploy the latest default branch:

```bash
wpcloud-site-git-deploy update site
```

If the branch still points at the failed content, rollback will be undone by the
next update. Fix or revert the repository first.

## Increase Rollback Retention

Choose retention during initialization:

```bash
wpcloud-site-git-deploy init site \
  --repo git@github.com:example/site.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main \
  --keep-releases 10
```

Change it later by editing:

```text
$HOME/.wpcloud-site-git-deploy/deployments/site.env
```

Set:

```bash
keep_releases=10
```

The next successful deploy applies pruning.

## Change Repository URL Or Default Branch

The `config` command only manages deploy roots. To change the repository URL,
docroot, deployment ID, default branch, or retention, edit:

```text
$HOME/.wpcloud-site-git-deploy/deployments/site.env
```

Example default branch change:

```bash
default_ref=release
```

Example repository URL change:

```bash
repo_url=git@github.com:example/new-site-repo.git
```

After changing `repo_url`, run:

```bash
wpcloud-site-git-deploy auth site
wpcloud-site-git-deploy doctor site
```

Changing `deployment_id` points the CLI at a different docroot namespace. It
does not remove the old namespace or unpublish existing public symlinks. For
large identity changes, creating a new deployment name with `init` is usually
easier to reason about.

## Git LFS Repository

Install or provide `git-lfs` in the site user's `PATH`, then deploy normally:

```bash
export PATH="$HOME/.local/bin:$HOME/.wpcloud-site-git-deploy/bin:$PATH"
wpcloud-site-git-deploy doctor site
wpcloud-site-git-deploy update site
```

If LFS files remain unresolved after `git lfs pull`, deploy fails instead of
publishing pointer files.

## Repository With Submodules

Public submodules work when the site can read their URLs:

```bash
wpcloud-site-git-deploy update site
```

Private submodules need credentials available to the site SSH user. If all
private repos can be read by the same SSH key, configure the parent deployment
with that key:

```bash
wpcloud-site-git-deploy auth site --use-key ~/.ssh/shared_readonly_key
wpcloud-site-git-deploy doctor site
wpcloud-site-git-deploy update site
```

Submodules that require different credentials per repository are not modeled by
the CLI. Use a deploy-output branch or make the submodule content public to the
configured key.

## Recover From A Failed Deploy

Check current state:

```bash
wpcloud-site-git-deploy status site
wpcloud-site-git-deploy releases site
```

Most deploy failures happen before `current` is changed. If the current release
is still healthy, fix the repository or credentials and run:

```bash
wpcloud-site-git-deploy update site
```

If a bad release was promoted, rollback:

```bash
wpcloud-site-git-deploy rollback site
```

Then fix or revert the repository before the next automated update.

## Audit Public Symlinks

The normal deploy path validates symlinks owned by the deployment. Maintainers
can run the hidden full-docroot audit for diagnostics:

```bash
wpcloud-site-git-deploy __remote-deploy --docroot /srv/htdocs --assert-public-symlinks
```

This scans public symlinks under the docroot and checks that targets are
relative and resolve under `/srv/htdocs`.

## Remove A Deployment Configuration

There is no destructive `destroy` command. To stop using a deployment:

```bash
wpcloud-site-git-deploy auth site --remove --purge-key
rm -f "$HOME/.wpcloud-site-git-deploy/deployments/site.env"
```

This stops CLI management for that `<name>`, but it does not unpublish the site
or take any public path down. The last deployed release continues serving
through the existing public symlinks, the
`/srv/htdocs/.github-ssh-deploy/deployments/<deployment-id>/` namespace remains
on disk, and the repo cache under
`$HOME/.wpcloud-site-git-deploy/repos/<name>/` is left in place. Temporary
state under `$HOME/.wpcloud-site-git-deploy/tmp/` may also remain if earlier
work was interrupted.

There is intentionally no clean teardown or unpublish command. Do not manually
delete active release paths under `/srv/htdocs` unless you have confirmed which
public paths are owned by that deployment and have a rollback plan.
