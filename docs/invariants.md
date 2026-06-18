# `wpcloud-site-git-deploy` Correctness Invariants

This file is the behavioral contract for the Go implementation. The testing
matrix says where each behavior is verified; this document says what the tool
must preserve.

## Public Symlinks And Docroot Containment

- Every HTTP-visible public symlink created by the tool must use a relative
  target.
- Every tool-owned public symlink target must resolve under the real docroot.
- Tool-owned public symlink targets must never point into `$HOME`; on WP Cloud,
  `$HOME` is available over SSH but is not mounted for HTTP requests.
- Deploy-time symlink assertions and `doctor --assert-public-symlinks` validate
  deployment-owned claims. Full-docroot symlink audits remain an internal helper
  because WP Cloud docroots may contain platform-owned symlinks outside the
  deployment namespace.

## Release Promotion

- A new release is prepared outside `current`, then promoted into the docroot
  release namespace before `current` is changed.
- `current` must switch atomically to `releases/<release-id>`. The implementation
  must never remove `current` and then recreate it.
- The `current` target must remain relative inside the deployment namespace.
- A successful deploy writes release metadata only after promotion has completed.
- A failed deploy must clean temporary worktrees and incoming release staging
  when failure occurs before promotion.
- A failed promotion before public path reconciliation must remove the unserved
  release directory so rollback cannot select a rejected release.

## Atomic Reclaim And Exchanged Paths

- Existing public paths are reclaimed atomically by exchanging the new symlink
  with the old path.
- The implementation must use `renameat2(RENAME_EXCHANGE)` in process.
- Exchanged-away paths are recorded durably before they are deleted.
- Cleanup of exchanged paths must be idempotent and retried on a later deploy or
  rollback if a previous run failed after an exchange.
- Reclaiming a normal file, directory, symlink, or exact foreign deployment
  symlink follows the same atomic exchange path.

## Claims

- Claims are computed from the release tree that will be served, after deploy
  root and rsync excludes have been applied.
- Claim reconciliation must merge claims derived from the previous release tree,
  materialized public symlinks still present in the docroot, and claims derived
  from the new release tree.
- Removed claims are deleted only after `current` points at the new release.
- Parent/child claim granularity changes must remove overlapping old symlinks
  before creating new claims, so old symlinks cannot block new directories.
- Newline-containing public paths are unsupported and must fail before
  promotion.

## Cross-Deployment Interactions

- Exact same-path collisions are allowed. If deployment B deploys a claim already
  published by deployment A, deployment B takes over that public path.
- Foreign ancestor symlinks are rejected because they would route the new claim
  through another deployment's `current` release.
- Foreign descendant symlinks inside a real directory being reclaimed are
  rejected because replacing the directory would engulf another deployment.
- Concurrent deploys, rollbacks, and promotions for the same deployment id must
  fail immediately with a non-blocking lock rather than wait.

## Protected Anchors And Sticky Boundaries

- Root/group-owned protected WordPress anchors under the docroot must not be
  claimed by a deployment.
- Sticky, writable, root/group-owned directories act as boundaries. Claims inside
  a sticky boundary are compressed to the next child below the deepest boundary.
- Protected anchor discovery and boundary discovery happen before new claims are
  accepted.

## Shared WordPress Paths

- `wp-content/cache`, `wp-content/upgrade`, and the configured maintenance file
  are runtime/control paths and must be rejected as deploy targets.
- `wp-content/uploads` and `wp-content/blogs.dir` are WordPress-managed shared
  containers.
- Regular files under shared media containers may be deployed as exact leaf
  symlinks.
- The shared media container directories themselves must not be claimed or
  replaced.
- Repo symlinks under shared media containers must be rejected because they can
  behave like directories or point outside the intended regular-file model.
- Removing a shared-container leaf claim removes only that leaf symlink; parent
  directories are left in place.

## No-Op Detection And Force Deploys

- Deploying the configured default ref with no explicit ref selector is valid.
- A deploy is a no-op only when the resolved commit and effective deploy root
  match the current release metadata and `--force` is not set.
- `--force` bypasses no-op detection and creates a new release for the same
  resolved commit.

## Git Preparation

- Repository cache fetches must update refs and tags and run `git gc --auto`.
- Worktrees are created from the cached repository and removed after deploy.
- Submodules are initialized recursively when present.
- Git LFS is required only when effective attributes mark tracked files as LFS.
- After `git lfs pull`, unresolved LFS pointer files under LFS-tracked paths must
  be rejected before promotion.
- Git operations use the configured deploy key through tool-managed
  `GIT_SSH_COMMAND`; the tool must not edit global SSH config.

## Maintenance Mode

- Maintenance mode is optional and enabled by default.
- The maintenance file must be PHP-includable for WordPress and set `$upgrading`
  to a recent timestamp.
- The tool removes only maintenance files it owns, identified by the tool marker
  and matching deployment id.
- A deploy must not remove another deployment's active maintenance marker.
- A successful deploy or rollback removes its owned maintenance marker.
- If a post-deploy hook fails, the promoted release remains active, the command
  exits nonzero, and owned maintenance is cleaned up.

## Post-Deploy Hooks

- Post-deploy hooks run after `current` points at the new release and before old
  release pruning.
- Hook paths are operator-controlled trusted code.
- Hook failure does not roll back automatically.

## Release Metadata, Rollback, And Pruning

- Release metadata records release id, ref mode, ref value, commit, deploy root,
  and deployment timestamp.
- Rollback selects metadata-backed releases and may target an explicit release
  id.
- Rollback reuses claim preparation and application but does not create a new
  release, write new metadata, or prune releases.
- Keep-release pruning preserves the active release and retains the configured
  number of releases.

## Auth And Doctor

- `auth` can generate, reuse, use, import, remove, and purge deploy keys.
- Private keys must be readable by the user, owner-only, valid, and usable
  without an interactive passphrase prompt.
- Imported keys are copied into the tool-managed key directory and chmodded.
- External keys configured with `--use-key` are never modified or purged by the
  tool.
- HTTPS Git repository URLs may be normalized to provider-generic SSH form.
- `doctor` reports all failures it can find before returning nonzero.
- `doctor --offline` skips remote Git access but keeps local validation.
