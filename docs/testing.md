# Testing And Release Verification

This project has two verification layers:

- Linux local compatibility tests in `tests/run.sh`.
- A destructive live E2E matrix in `scripts/live-e2e.sh` against a throwaway
  WP Cloud or Pressable site for release-critical changes.

Do not store site credentials in this repository. Keep local secrets in
`.env.local`, which is ignored by Git.

## Local Tests

Run the full local suite from the repository root on a Linux host:

```bash
tests/run.sh
```

This can be a production-like throwaway site over SSH, Linux CI, or a Linux
container/VM. Native macOS runs exit early with guidance instead of attempting
partial shimmed coverage.

The suite runs:

- `tests/test_cli_git_deploy.sh`, a black-box deploy test using local Git
  repositories, cached inspection checks, rollback, worktree cleanup, no-op
  deploys, `--force`, deploy-root behavior, public post-deploy hooks,
  maintenance-file behavior, hardlink reuse, and Git LFS behavior.
- `tests/test_auth_doctor.sh`, a setup-focused test for deploy-key generation,
  existing-key use, managed key import, auth removal, HTTPS-to-SSH
  conversion, generic SSH guidance, key validation failures, doctor
  diagnostics, and configured `GIT_SSH_COMMAND` verification.
- `tests/test_remote_invariants.sh`, a direct test of the embedded
  `__remote-deploy` path for public symlink invariants, scoped assertion
  behavior, shared-path rejection, maintenance marker ownership, and
  full-docroot audit behavior.
- `shellcheck` over the CLI, installer, and tests when `shellcheck` is
  installed.

Additional fast checks:

```bash
bash -n bin/wpcloud-site-git-deploy scripts/install.sh tests/*.sh
git diff --check
```

## CI

`.github/workflows/ci.yml` runs on pushes to `main` and on pull requests. It
uses Ubuntu, installs `shellcheck`, and runs `tests/run.sh`.

The Linux CI path is important because production deploys depend on GNU command
behavior and either native `mv --exchange` or the static Linux amd64
`exchange-rename` fallback helper.

## Live E2E Matrix

Run the live matrix before treating release-critical deploy changes as ready.
Use a disposable site because the matrix intentionally mutates docroot content,
tests rollback, tests protected-path rejection, and verifies cross-deployment
isolation.

The maintained live matrix is:

```bash
scripts/live-e2e.sh
```

It reads site credentials from `.env.local` or the environment:

```bash
WPCLOUD_CLI_SSH_HOST=...
WPCLOUD_CLI_SSH_PORT=...
WPCLOUD_CLI_SSH_USERNAME=...
WPCLOUD_CLI_SSH_PASSWORD=...
```

The script installs a bundle of the current checkout on the throwaway site, so
it can validate untagged local changes before a release. It writes evidence to
`tmp/live-e2e-evidence.md` by default.

Current live coverage includes:

- Baseline deploy.
- Default exclude coverage for tracked `.env`, `.github/`, `.DS_Store`,
  `.gitignore`, `.gitattributes`, `.gitmodules`, `.aws/`, `.ssh/`, `.npmrc`,
  `.pypirc`, and `.netrc` paths.
- No-op update when the resolved commit and deploy inputs already match the
  current release.
- `update --force` and `deploy --force` same-commit redeploys.
- Hardlink reuse across forced same-commit releases.
- Concurrent deployment rejection while another promotion is running.
- Deploy-root positive, invalid, missing, clear, and restore behavior.
- Init-time `--deploy-root` and init-time custom `--maintenance-file`.
- Content change deploy.
- Configured post-deploy hook execution.
- One-run `--post-deploy` override behavior.
- Failing post-deploy behavior: the promoted release remains current, the
  command exits nonzero, and the tool-owned maintenance marker is removed.
- Default `.maintenance` creation during promotion and post-deploy.
- `maintenance_file=none` behavior.
- Preservation of a non-owned pre-existing maintenance file.
- Stale tool-owned maintenance marker cleanup during rollback.
- Custom configured maintenance file behavior.
- Complex path deploy.
- File removal.
- Release metadata storage as `metadata/<release-id>/cfg-*` value files,
  including no-op and release-list reads without evaluating unexpected files.
- File-to-directory and directory-to-file swaps.
- Symlink deploy.
- Git LFS add and remove.
- Shared media container leaf-file deploys under `wp-content/uploads` and
  `wp-content/blogs.dir`, including cleanup that removes only the owned leaf
  symlink and leaves parent directories intact.
- Shared runtime/control path rejection for `wp-content/cache`,
  `wp-content/upgrade`, and `.maintenance`.
- Public submodule add and remove.
- Private submodule failure, followed by recovery after removal.
- Protected anchor rejection, followed by recovery.
- Explicit `deploy --tag` and `deploy --commit`.
- Layered second deployment.
- Foreign-layer conflict rejection, followed by recovery.
- Generated deploy-key setup, `auth --verify`, offline and online `doctor`,
  external `auth --use-key`, managed `auth --import-key`, generated-key
  rotation, auth removal, and managed-key purge behavior.
- Explicit `rollback --to`, missing rollback target rejection, release listing,
  branch/tag/commit inspection with and without `--fetch`, list limits, and
  retention.
- Public symlink invariant audit: all owned public symlinks are relative and
  resolve under `/srv/htdocs`.
- Hidden full-docroot symlink audit command coverage against a controlled live
  docroot fixture.

Expected failures in that run:

- Private submodule deploy failed before promotion with `could not read Username`
  because the site had no credentials for that private repository.
- Protected anchor deploy failed with `protected path: wp-load.php`.
- Shared media container regular files deployed as leaf symlinks, and shared
  runtime/control path deploys failed with `shared path cannot be deployed: ...`.
- Foreign-layer deploy failed with
  `claim owned by another deployment: layer-owned`.

Those failures are guardrails, not regressions.

## Human Review Checklist

Before pushing a release-sensitive change:

1. Confirm public docs use `/srv/htdocs`, not `~/htdocs` or a symlinked home
   path.
2. Confirm install docs mention the Bash CLI, native `mv --exchange`, and the
   `exchange-rename` fallback helper.
3. Confirm auth docs describe `auth`, `doctor`, the state-managed deploy key,
   and the fact that the CLI does not edit `~/.ssh/config`.
4. Confirm deploy docs preserve the `$HOME` state layout and docroot release
   namespace.
5. Confirm LFS, submodule, rollback, cached inspection, no-op deploy, `--force`,
   deploy-root, post-deploy, maintenance-file, shared-path, and symlink safety
   behavior still match the CLI.
6. Run `tests/run.sh` and `git diff --check`.
7. Repeat the live E2E matrix for changes that touch promotion, rollback,
   claims, post-deploy hooks, maintenance handling, shared-path policy,
   LFS/submodules, repository caching, or install/runtime layout.
