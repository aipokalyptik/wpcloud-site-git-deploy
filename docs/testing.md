# Testing And Release Verification

This project has two verification layers:

- Local compatibility tests in `tests/run.sh`.
- A destructive live E2E matrix against a throwaway WP Cloud or Pressable site
  for release-critical changes.

Do not store site credentials in this repository. Keep local secrets in
`.env.local`, which is ignored by Git.

## Local Tests

Run the full local suite from the repository root:

```bash
tests/run.sh
```

The suite runs:

- `tests/test_cli_git_deploy.sh`, a black-box deploy test using local Git
  repositories, fake Linux-only command shims where needed, cached inspection
  checks, rollback, worktree cleanup, no-op deploys, deploy-root behavior,
  hardlink reuse, and Git LFS behavior.
- `tests/test_auth_doctor.sh`, a setup-focused test for deploy-key generation,
  existing-key use, managed key import, auth removal, GitHub HTTPS-to-SSH
  conversion, generic SSH guidance, key validation failures, doctor
  diagnostics, and configured `GIT_SSH_COMMAND` verification.
- `tests/test_remote_invariants.sh`, a direct test of the embedded
  `__remote-deploy` path for public symlink invariants, scoped assertion
  behavior, and full-docroot audit behavior.
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
behavior and the static Linux amd64 `exchange-rename` helper.

## Live E2E Matrix

Run the live matrix before treating release-critical deploy changes as ready.
Use a disposable site because the matrix intentionally mutates docroot content,
tests rollback, tests protected-path rejection, and verifies cross-deployment
isolation.

Current live coverage should include:

- Baseline deploy.
- Content change deploy.
- Complex path deploy.
- File removal.
- File-to-directory and directory-to-file swaps.
- Symlink deploy.
- Git LFS add and remove.
- Public submodule add and remove.
- Private submodule failure, followed by recovery after removal.
- Protected anchor rejection, followed by recovery.
- Layered second deployment.
- Foreign-layer conflict rejection, followed by recovery.
- Rollback, release listing, branch/tag/commit inspection, and retention.
- Public symlink invariant audit: all owned public symlinks are relative and
  resolve under `/srv/htdocs`.

Release `v1.1.0` was cut after a full live matrix completed at
`2026-06-16T00:04:39Z`. It covered the scenarios above against the throwaway
WP Cloud/Pressable site after the existing/imported deploy-key support and
key-validation edge-case fixes.

Expected failures in that run:

- Private submodule deploy failed before promotion with `could not read Username`
  because the site had no credentials for that private repository.
- Protected anchor deploy failed with `protected path: wp-load.php`.
- Foreign-layer deploy failed with
  `claim owned by another deployment: layer-owned`.

Those failures are guardrails, not regressions.

## Human Review Checklist

Before pushing a release-sensitive change:

1. Confirm public docs use `/srv/htdocs`, not `~/htdocs` or a symlinked home
   path.
2. Confirm install docs mention only the Bash CLI and `exchange-rename` helper.
3. Confirm auth docs describe `auth`, `doctor`, the state-managed deploy key,
   and the fact that the CLI does not edit `~/.ssh/config`.
4. Confirm deploy docs preserve the `$HOME` state layout and docroot release
   namespace.
5. Confirm LFS, submodule, rollback, cached inspection, no-op deploy,
   deploy-root, and symlink safety behavior still match the CLI.
6. Run `tests/run.sh` and `git diff --check`.
7. Repeat the live E2E matrix for changes that touch promotion, rollback,
   claims, LFS/submodules, repository caching, or install/runtime layout.
