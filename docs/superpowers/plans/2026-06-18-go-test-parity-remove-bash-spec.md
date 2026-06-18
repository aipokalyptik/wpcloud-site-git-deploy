# Go Test Parity And Bash Spec Removal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring Go local tests and Go live E2E coverage to parity or better than the preserved Bash oracle, then remove the old Bash implementation, Bash tests, and Bash-era docs from the repo.

**Architecture:** Treat `spec/tests/*.sh` as the coverage checklist, not as code to keep. Move each meaningful Bash oracle behavior into focused Go unit/integration tests, Go CLI black-box tests, or the Go live E2E matrix. Delete `spec/` only after a checked-in parity matrix shows every Bash scenario is either covered by Go, intentionally obsolete because of the redesigned CLI/state model, or covered by live E2E.

**Tech Stack:** Go `testing`, stdlib filesystem/git fixtures, existing package layout under `internal/`, shell only for `scripts/live-e2e.sh` and `Makefile` wrappers, external commands `git`, `git-lfs`, `ssh-keygen`, `rsync`, and WP Cloud live E2E.

---

## File Structure

- Create: `docs/testing-matrix.md`
  - Human-readable coverage matrix mapping every Bash oracle behavior to Go local test, Go live E2E test, or intentional obsolete behavior.
- Create: `internal/testutil/files.go`
  - Shared helpers for temp homes, docroots, file writes, symlink reads, assertions, path checks, and command output checks.
- Create: `internal/testutil/git.go`
  - Shared local Git fixture helpers: init repo, commit, branch, tag, submodule setup, LFS fixture setup, and worktree registry checks.
- Create: `internal/testutil/fakebin.go`
  - Shared fake command directory helpers for `git`, `git-lfs`, `ssh`, `ssh-keygen`, `rsync`, and failure injection.
- Modify: `internal/cli/parser_test.go`
  - Add complete parser parity tests for flag-only Go CLI behavior and rejected legacy Bash syntax.
- Modify: `internal/cli/run_test.go`
  - Expand black-box Go CLI tests for init/config/status/list/destroy/auth/doctor/deploy/rollback/inspection behavior.
- Modify: `internal/engine/deploy_test.go`
  - Expand local deploy integration: refs, no-op/force, deploy root, excludes, hardlinks, worktree cleanup, LFS, submodules, cache fetch/gc, configured SSH env.
- Modify: `internal/engine/promote_test.go`
  - Expand promotion invariants: atomic reclaim, exact foreign takeover, foreign ancestor/descendant rejection, scoped symlink assertions, exchanged-path retry, locks, maintenance ownership, rollback failure cleanup.
- Modify: `internal/claims/claims_test.go`
  - Expand claim algebra: protected anchors, sticky boundaries, shared media root-file rejection, newline rejection, removed claims, deployment namespace excludes.
- Modify: `internal/publicfs/publicfs_test.go`
  - Expand public symlink audit tests: relative target, absolute target rejection, HOME-containing target rejection, outside-docroot resolution rejection.
- Modify: `internal/auth/auth_test.go`
  - Expand URL conversion, key source, and SSH command tests.
- Modify: `internal/doctor/doctor_test.go`
  - Expand report aggregation and failure/warning exit semantics.
- Modify: `scripts/live-e2e.sh`
  - Add missing live scenarios for Go-only E2E parity.
- Modify: `tests/test_live_e2e_static.sh`
  - Keep/extend static guardrails for live E2E behavior that is easy to regress.
- Modify: `Makefile`
  - Add `make test-local`, `make test-live`, and remove any dependency on `spec-test` once `spec/` is deleted.
- Modify: `README.md`
  - Replace “Bash oracle” language with “Go test suite and live E2E matrix” after parity lands.
- Delete at the end only: `spec/`
  - Delete Bash implementation, Bash tests, Bash docs, and Bash helper references after all parity tests pass.

## Bash Oracle Coverage Groups To Preserve

The Bash suite currently protects these behavior groups:

1. CLI parser/help/version/static script readability checks.
2. Init/config/status/list behavior and state layout.
3. Auth generate/reuse/force/use/import/remove/purge, GitHub and generic provider URL handling, key validation, `ssh-keygen` missing behavior.
4. Doctor command aggregation, offline mode, command checks, key checks, remote access checks, and public symlink audits.
5. Git cache clone/fetch/gc and cached inspection behavior with `--fetch`.
6. Deploy by tag, branch, commit, default ref, no-op, and force.
7. Deploy root behavior and validation.
8. Default excludes: `.env`, `.github`, `.DS_Store`, `.gitignore`, `.gitattributes`, `.gitmodules`, secret dotdirs.
9. Worktree cleanup and repo worktree registry cleanup.
10. `rsync --link-dest` hardlink reuse.
11. Git LFS detection, successful pull, missing tool failure, unresolved pointer rejection scoped to LFS paths, non-LFS pointer-shaped file allowed, `GIT_SSH_COMMAND` propagation.
12. Submodule initialization and failure/recovery behavior.
13. Release metadata safety, no shell execution, releases output, rollback selection, explicit rollback target, missing target failure.
14. Post-deploy config, one-run override, failure behavior, and clearing behavior.
15. Maintenance marker PHP content, disabled maintenance, custom maintenance file, ownership preservation, stale marker cleanup, rollback cleanup.
16. Lock behavior: concurrent deploy/update/rollback fails without removing the active deploy maintenance marker.
17. Public symlink invariants and full-docroot audit.
18. Promotion invariants: relative symlinks, atomic exchange, cached exchange capability, exact foreign takeover, foreign ancestor/descendant rejection.
19. Shared WordPress path policy: cache/upgrade/maintenance rejected, uploads/blogs.dir leaf regular files allowed, shared container root claims rejected, shared container symlinks rejected, parent directories left behind.
20. Live WP Cloud coverage: install, deploy, no-op/force, deploy root, post-deploy/maintenance, shared paths, refs, foreign takeover, LFS hydration, submodules, auth, doctor, rollback, inspection.

## Task 1: Add The Parity Matrix

**Files:**
- Create: `docs/testing-matrix.md`

- [ ] **Step 1: Write the matrix skeleton**

Create `docs/testing-matrix.md` with this exact structure:

```markdown
# Go Testing Matrix

This matrix replaces the old Bash oracle as the source of truth for test
coverage. Every behavior formerly protected by `spec/tests/*.sh` must be listed
here before `spec/` is removed.

Status values:

- `go-local`: covered by Go unit or integration tests.
- `go-live`: covered by `scripts/live-e2e.sh`.
- `obsolete`: intentionally not preserved because the Go rewrite changed the
  CLI, config, state layout, or runtime model.
- `gap`: known missing coverage that must be closed before removing `spec/`.

| Area | Behavior | Status | Evidence |
| --- | --- | --- | --- |
```

- [ ] **Step 2: Add initial rows for every Bash oracle group**

Add one row for each of the 20 coverage groups above. Mark rows as `gap` unless the current Go tests or live E2E already clearly cover the behavior. Use exact test names where known, for example:

```markdown
| Deploy refs | Deploy default ref, branch, tag, and commit | gap | `internal/cli/run_test.go:TestRunDeployUsesDefaultRef` covers default only; live E2E covers tag/commit |
| Shared media | Regular files under uploads/blogs.dir deploy as leaf symlinks | go-local, go-live | `internal/engine/promote_test.go:TestPromoteAllowsSharedMediaLeafAndRejectsSharedRuntimePath`; `scripts/live-e2e.sh:e2e-01-baseline` |
| Bash positional CLI | `deploy site`, `update site`, and `config site --post-deploy` syntax | obsolete | Go CLI intentionally uses flags only and removed `update` |
```

- [ ] **Step 3: Run documentation check**

Run:

```bash
git diff --check docs/testing-matrix.md
```

Expected: no output and exit 0.

- [ ] **Step 4: Commit**

```bash
git add docs/testing-matrix.md
git commit -m "Document Go test parity matrix"
```

## Task 2: Add Shared Go Test Utilities

**Files:**
- Create: `internal/testutil/files.go`
- Create: `internal/testutil/git.go`
- Create: `internal/testutil/fakebin.go`

- [ ] **Step 1: Add filesystem helpers**

Create `internal/testutil/files.go`:

```go
package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func WriteFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

func WriteExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent for executable %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func ReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func ReadlinkSlash(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	return filepath.ToSlash(target)
}

func AssertContains(t *testing.T, got, needle string) {
	t.Helper()
	if !strings.Contains(got, needle) {
		t.Fatalf("expected output to contain %q\noutput:\n%s", needle, got)
	}
}

func AssertNotContains(t *testing.T, got, needle string) {
	t.Helper()
	if strings.Contains(got, needle) {
		t.Fatalf("expected output not to contain %q\noutput:\n%s", needle, got)
	}
}

func AssertNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s not to exist, stat err=%v", path, err)
	}
}
```

- [ ] **Step 2: Add Git helpers**

Create `internal/testutil/git.go`:

```go
package testutil

import (
	"os/exec"
	"strings"
	"testing"
)

func RunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
	return string(output)
}

func InitRepo(t *testing.T, dir string) {
	t.Helper()
	RunGit(t, dir, "init", "-b", "main")
	RunGit(t, dir, "config", "user.email", "test@example.invalid")
	RunGit(t, dir, "config", "user.name", "WP Cloud Deploy Test")
}

func CommitAll(t *testing.T, dir, message string) string {
	t.Helper()
	RunGit(t, dir, "add", "-A")
	RunGit(t, dir, "commit", "-m", message)
	return strings.TrimSpace(RunGit(t, dir, "rev-parse", "HEAD"))
}
```

- [ ] **Step 3: Add fake-bin helpers**

Create `internal/testutil/fakebin.go`:

```go
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func FakeBin(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	return dir
}

func WriteFakeCommand(t *testing.T, binDir, name, script string) string {
	t.Helper()
	path := filepath.Join(binDir, name)
	WriteExecutable(t, path, "#!/usr/bin/env bash\nset -euo pipefail\n"+script)
	return path
}
```

- [ ] **Step 4: Run tests**

Run:

```bash
go test ./internal/testutil
go test ./...
```

Expected: all packages pass.

- [ ] **Step 5: Commit**

```bash
git add internal/testutil
git commit -m "Add shared Go test utilities"
```

## Task 3: Parser And CLI Surface Parity

**Files:**
- Modify: `internal/cli/parser_test.go`
- Modify: `docs/testing-matrix.md`

- [ ] **Step 1: Add parser tests for Go syntax and obsolete Bash syntax**

Add tests named:

```go
func TestParseRejectsStrayPositionalsAndLegacyUpdate(t *testing.T) {
	_, err := Parse([]string{"deploy", "site"})
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("expected positional deploy name to fail, got %v", err)
	}
	_, err = Parse([]string{"update", "--name", "site"})
	if err == nil || !strings.Contains(err.Error(), "unknown command: update") {
		t.Fatalf("expected removed update command to fail, got %v", err)
	}
}

func TestParseMissingValuesAndMutuallyExclusiveRefs(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"init", "--name", "site", "--repo"}, "flag needs an argument"},
		{[]string{"deploy", "--name", "site", "--branch"}, "flag needs an argument"},
		{[]string{"deploy", "--name", "site", "--branch", "main", "--tag", "v1"}, "choose only one ref"},
		{[]string{"branches", "--name", "site", "--limit"}, "flag needs an argument"},
		{[]string{"config", "--name", "site"}, "config requires --set or --unset"},
	}
	for _, tc := range cases {
		_, err := Parse(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("Parse(%v) error = %v, want %q", tc.args, err, tc.want)
		}
	}
}
```

- [ ] **Step 2: Add config key tests**

Add:

```go
func TestParseConfigSupportsKnownKeysAndRejectsUnknown(t *testing.T) {
	cmd, err := Parse([]string{"config", "--name", "site", "--set", "repo_url=git@example.com:team/site.git", "--set", "default_ref=main", "--unset", "post_deploy"})
	if err != nil {
		t.Fatalf("expected config parse to pass: %v", err)
	}
	if len(cmd.Set) != 2 || len(cmd.Unset) != 1 {
		t.Fatalf("unexpected config command: %#v", cmd)
	}
	_, err = Parse([]string{"config", "--name", "site", "--set", "unknown=value"})
	if err == nil || !strings.Contains(err.Error(), "unsupported config key") {
		t.Fatalf("expected unknown config key to fail, got %v", err)
	}
}
```

- [ ] **Step 3: Update matrix rows**

Set parser/help/config syntax rows in `docs/testing-matrix.md` to `go-local` or `obsolete` as appropriate, with evidence pointing at `internal/cli/parser_test.go`.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/cli
make check
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/parser_test.go docs/testing-matrix.md
git commit -m "Cover Go CLI parser parity"
```

## Task 4: Auth And Doctor Parity

**Files:**
- Modify: `internal/auth/auth_test.go`
- Modify: `internal/cli/run_test.go`
- Modify: `internal/doctor/doctor_test.go`
- Modify: `docs/testing-matrix.md`

- [ ] **Step 1: Add auth helper unit tests**

Add table tests covering:

```go
func TestHTTPSURLToSSHProviderGeneric(t *testing.T) {
	cases := map[string]string{
		"https://github.com/team/site.git":      "git@github.com:team/site.git",
		"https://gitlab.com/team/site":          "git@gitlab.com:team/site",
		"https://git.example.com/team/site.git": "git@git.example.com:team/site.git",
	}
	for input, want := range cases {
		got, ok := HTTPSURLToSSH(input)
		if !ok || got != want {
			t.Fatalf("HTTPSURLToSSH(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
}
```

- [ ] **Step 2: Add CLI auth black-box tests**

In `internal/cli/run_test.go`, add tests named:

- `TestRunAuthGenerateReuseForceRemovePurge`
- `TestRunAuthUseExternalKeyDoesNotPurgeExternalFile`
- `TestRunAuthImportKeyCopiesAndChmodsManagedKey`
- `TestRunAuthRejectsInvalidMissingPermissiveAndSelfImportKeys`
- `TestRunDoctorReportsAllKeyAndRemoteFailures`

Use fake `ssh-keygen` and fake `git` scripts through `PATH` to force:

```bash
ssh-keygen -y success
ssh-keygen -y invalid-key failure
git ls-remote exit 7
```

Assert these conditions:

- generated key exists under `$HOME/.wpcloud-site-git-deploy/keys/<name>_ed25519`;
- `--force-new-key` changes key contents;
- `--remove` clears `SSHKeyPath` but leaves managed key;
- `--remove --purge-key` deletes managed key;
- `--use-key` stores external path and purge does not delete it;
- `--import-key` copies into managed key path with mode `0600`;
- missing, unreadable, group/world-readable, invalid, and self-import keys fail;
- doctor reports command warnings/failures and remote failure without stopping after the first failure.

- [ ] **Step 3: Update doctor unit tests**

Add report-level tests:

```go
func TestReportFailsOnlyWhenFailuresExist(t *testing.T) {
	var report doctor.Report
	report.OK("config", "loaded")
	report.Warn("git-lfs", "not installed")
	if report.Failed() {
		t.Fatal("warnings should not make report fail")
	}
	report.Fail("ssh-key", "missing")
	if !report.Failed() {
		t.Fatal("failures should make report fail")
	}
}
```

- [ ] **Step 4: Update matrix rows**

Mark auth/doctor rows as `go-local`; leave live auth rows marked `go-live`.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/auth ./internal/doctor ./internal/cli
make check
```

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/auth_test.go internal/doctor/doctor_test.go internal/cli/run_test.go docs/testing-matrix.md
git commit -m "Cover auth and doctor parity"
```

## Task 5: Deploy Integration Parity

**Files:**
- Modify: `internal/engine/deploy_test.go`
- Modify: `internal/cli/run_test.go`
- Modify: `docs/testing-matrix.md`

- [ ] **Step 1: Add local deploy scenario test**

Add a test named `TestDeployRefsNoOpForceInspectAndCleanup` that:

1. Creates a local Git repo with `main`, `feature`, tag `v1`, and a later commit.
2. Initializes Go config with `DefaultRef: main`.
3. Deploys `--tag v1`, asserts main content.
4. Deploys `--branch feature`, asserts feature content.
5. Deploys `--commit <mainCommit>`, asserts main content.
6. Deploys default ref, asserts latest main content.
7. Deploys same commit again without `--force`, asserts `no_op=true` and release count unchanged.
8. Deploys same commit with `--force`, asserts release count increases.
9. Runs branches/tags/commits before and after a later repo change with `--fetch`, asserting cached inspection is stale until fetch.
10. Asserts worktree temp directory is removed and `git worktree list --porcelain` does not mention it.

- [ ] **Step 2: Add deploy root and excludes test**

Add `TestDeployRootAndDefaultExcludes`:

- repo root contains `README.md`, `.env`, `.github/workflows/test.yml`, `.DS_Store`, `.gitignore`, `.gitattributes`, `.gitmodules`;
- `public/index.html` contains deploy-root content;
- config `DeployRoot: public`;
- deploy publishes `public/index.html` as docroot `index.html`;
- deploy does not publish `public/` path itself;
- deploy does not publish excluded files;
- changing `DeployRoot` to missing path fails with `deploy root does not exist`;
- clearing `DeployRoot` publishes root files.

- [ ] **Step 3: Add hardlink reuse test**

Add `TestDeployReusesUnchangedFilesWithHardlinks`:

- First release has `assets/app.txt` and `index.html`.
- Second release changes only `index.html`.
- Assert release `assets/app.txt` inode matches between releases when the test filesystem supports hardlinks.
- Assert `index.html` inode differs.

- [ ] **Step 4: Add Git auth env propagation and gc test**

Use a fake `git` wrapper in `PATH` that delegates to real git but logs arguments and `GIT_SSH_COMMAND`.

Assert:

- clone uses configured `GIT_SSH_COMMAND`;
- fetch uses configured `GIT_SSH_COMMAND`;
- submodule update uses configured `GIT_SSH_COMMAND`;
- fetch runs `git gc --auto`.

- [ ] **Step 5: Update matrix rows**

Set deploy refs, deploy-root, default excludes, worktree cleanup, hardlink reuse, cached inspection, and Git auth env rows to `go-local`.

- [ ] **Step 6: Run tests**

```bash
go test ./internal/engine ./internal/cli
make check
```

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/deploy_test.go internal/cli/run_test.go docs/testing-matrix.md
git commit -m "Cover deploy integration parity"
```

## Task 6: LFS And Submodule Parity

**Files:**
- Modify: `internal/engine/deploy_test.go`
- Modify: `scripts/live-e2e.sh`
- Modify: `docs/testing-matrix.md`

- [ ] **Step 1: Add local LFS success and scoped pointer tests**

Add tests using a fake `git-lfs` executable:

- `TestDeployHydratesLFSFilesAndAllowsNonLFSPointerShapedFiles`
- `TestDeployRejectsUnresolvedLFSPointersOnlyForLFSPaths`
- `TestDeployFailsClearlyWhenGitLFSIsMissingForLFSRepo`

Fixture:

```text
.gitattributes: *.bin filter=lfs diff=lfs merge=lfs -text
media.bin: Git LFS pointer
notes.txt: Git LFS pointer-shaped text, not LFS-tracked
```

Fake `git-lfs pull` should replace `media.bin` with `hydrated lfs content`. Assert `notes.txt` remains pointer-shaped and deploy succeeds.

- [ ] **Step 2: Add local submodule tests**

Add:

- `TestDeployInitializesPublicSubmodules`
- `TestDeployPrivateSubmoduleFailureDoesNotPromote`

Use a local submodule repo for the success case. For failure, use an invalid URL and assert deploy fails before current changes.

- [ ] **Step 3: Extend live E2E for private submodule failure and recovery**

In `scripts/live-e2e.sh`, after the successful submodule scenario:

1. Add a submodule with an intentionally inaccessible URL.
2. Commit and push.
3. Run deploy and assert nonzero failure.
4. Assert current site still serves the previous good release.
5. Remove the bad submodule.
6. Commit and deploy successfully.

- [ ] **Step 4: Update matrix rows**

Mark LFS and submodule rows as `go-local, go-live`.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/engine
make check
scripts/live-e2e.sh
```

Expected: local tests pass; live E2E passes and evidence includes LFS success plus private submodule failure/recovery.

- [ ] **Step 6: Commit**

```bash
git add internal/engine/deploy_test.go scripts/live-e2e.sh docs/testing-matrix.md
git commit -m "Cover LFS and submodule parity"
```

## Task 7: Promotion, Claims, Symlink, And Shared-Path Parity

**Files:**
- Modify: `internal/claims/claims_test.go`
- Modify: `internal/publicfs/publicfs_test.go`
- Modify: `internal/engine/promote_test.go`
- Modify: `docs/testing-matrix.md`

- [ ] **Step 1: Expand claim tests**

Add tests for:

- newline-containing public path rejection;
- protected anchor rejection;
- sticky boundary compression;
- shared root file rejection for exact `wp-content/uploads` and `wp-content/blogs.dir`;
- shared media parent directories left after leaf removal;
- deployment namespace and Git metadata excluded.

- [ ] **Step 2: Expand public symlink audit tests**

Add tests for:

- valid relative target under docroot passes;
- absolute target fails;
- target containing HOME fails;
- relative target resolving outside docroot fails;
- full-docroot audit finds a bad foreign symlink.

- [ ] **Step 3: Expand promotion tests**

Add tests for:

- atomic reclaim of existing file, directory, and exact foreign symlink;
- foreign ancestor rejection leaves current unchanged;
- foreign descendant rejection leaves current unchanged;
- exchanged-path cleanup is retried if stale records exist;
- deploy lock prevents concurrent promotion;
- scoped assertion failure aborts after corrupted link target injection;
- rollback reuses claim application but does not write new release metadata or prune.

- [ ] **Step 4: Update matrix rows**

Mark claims, promotion, cross-deployment, public symlink, shared path, lock, and rollback engine rows as `go-local`.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/claims ./internal/publicfs ./internal/engine
make check
```

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add internal/claims/claims_test.go internal/publicfs/publicfs_test.go internal/engine/promote_test.go docs/testing-matrix.md
git commit -m "Cover promotion and claim parity"
```

## Task 8: Maintenance, Post-Deploy, Metadata, Rollback, And Pruning Parity

**Files:**
- Modify: `internal/engine/promote_test.go`
- Modify: `internal/releases/releases_test.go`
- Modify: `internal/cli/run_test.go`
- Modify: `scripts/live-e2e.sh`
- Modify: `docs/testing-matrix.md`

- [ ] **Step 1: Add maintenance tests**

Add tests for:

- PHP maintenance marker content includes `<?php`, `$upgrading = <timestamp>;`, tool marker, deployment id, release id, and created timestamp;
- successful deploy removes owned marker;
- failed post-deploy keeps promoted release current and removes owned marker;
- non-owned maintenance file is preserved;
- stale old-format and new-format owned markers are removed during rollback failure;
- `--no-maintenance-file` / disabled config creates no marker;
- custom maintenance file path is honored.

- [ ] **Step 2: Add post-deploy tests**

Add CLI tests for:

- configured `post_deploy` runs on default deploy;
- `--post-deploy` one-run override runs instead of configured hook;
- clearing `post_deploy` stops automatic hook execution;
- failing hook exits nonzero and current release changes to the promoted release.

- [ ] **Step 3: Add metadata and rollback tests**

Add tests for:

- release metadata JSON contains release id, ref mode, ref value, commit, deploy root, deployed timestamp;
- metadata tampering cannot execute code because JSON is parsed, not sourced;
- explicit rollback target works;
- missing rollback target fails and does not print success;
- default rollback selects previous non-current release;
- keep-release pruning preserves active release and retention count.

- [ ] **Step 4: Extend live E2E**

Add live E2E cases:

- `config --set maintenance_file=.custom-maintenance` then deploy and assert marker is cleaned up;
- `config --set maintenance=false` or `--no-maintenance-file` equivalent config path, deploy and assert no marker;
- failing post-deploy exits nonzero and leaves new release current;
- rollback missing target fails and leaves current unchanged.

- [ ] **Step 5: Update matrix rows**

Mark maintenance, post-deploy, metadata, rollback, and pruning rows as `go-local, go-live` where applicable.

- [ ] **Step 6: Run tests**

```bash
go test ./internal/engine ./internal/releases ./internal/cli
make check
scripts/live-e2e.sh
```

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/promote_test.go internal/releases/releases_test.go internal/cli/run_test.go scripts/live-e2e.sh docs/testing-matrix.md
git commit -m "Cover maintenance rollback and metadata parity"
```

## Task 9: Live E2E Parity Expansion

**Files:**
- Modify: `scripts/live-e2e.sh`
- Modify: `tests/test_live_e2e_static.sh`
- Modify: `docs/testing-matrix.md`

- [ ] **Step 1: Add missing live happy paths**

Extend `scripts/live-e2e.sh` with named sections:

- `e2e-17-auth-force-new-key`
- `e2e-18-init-custom-maintenance`
- `e2e-19-config-no-maintenance`
- `e2e-20-post-deploy-failure-current`
- `e2e-21-shared-upgrade-and-maintenance-reject`
- `e2e-22-shared-media-remove-leaves-parents`
- `e2e-23-rollback-missing-target`
- `e2e-24-destroy-leaves-docroot-serving`

- [ ] **Step 2: Add static guard checks**

Update `tests/test_live_e2e_static.sh` to require each new `e2e-17` through `e2e-24` label.

- [ ] **Step 3: Run live E2E**

```bash
scripts/live-e2e.sh
```

Expected: pass, with evidence file containing all new labels.

- [ ] **Step 4: Update matrix rows**

Set all live rows to `go-live` and point evidence to `scripts/live-e2e.sh`.

- [ ] **Step 5: Commit**

```bash
git add scripts/live-e2e.sh tests/test_live_e2e_static.sh docs/testing-matrix.md
git commit -m "Expand Go live E2E parity coverage"
```

## Task 10: Remove Bash Spec And Bash-Era Docs

**Files:**
- Delete: `spec/`
- Modify: `README.md`
- Modify: `Makefile`
- Modify: `.github/workflows/*` if they invoke `spec/tests/run.sh`
- Modify: `docs/testing-matrix.md`

- [ ] **Step 1: Confirm no matrix gaps remain**

Run:

```bash
grep -n '| gap |' docs/testing-matrix.md
```

Expected: no output.

- [ ] **Step 2: Delete `spec/`**

Run:

```bash
rm -rf spec
```

- [ ] **Step 3: Update Makefile**

Remove the `spec-test` target and any help text that mentions the preserved Bash oracle. Ensure `make check` is the main local verifier and `make live-e2e` is the live verifier.

- [ ] **Step 4: Update README**

Remove language like:

- “previous Bash implementation is preserved under `spec/`”
- “Bash reference implementation and tests”
- “Bash oracle”
- “Bash-era user documentation remains”

Replace with:

```markdown
The Go implementation is the only supported implementation. Correctness is
guarded by Go unit/integration tests, the Go testing matrix, and the disposable
WP Cloud live E2E matrix.
```

- [ ] **Step 5: Update CI**

Search:

```bash
rg -n "spec/|spec-test|Bash oracle|bin/wpcloud-site-git-deploy|__remote-deploy" .
```

For every hit:

- if it references deleted Bash implementation/docs/tests, remove or rewrite it;
- if it references a Go diagnostic behavior, update wording to `doctor`.

- [ ] **Step 6: Run full verification**

Run:

```bash
make clean
make check
make build
scripts/live-e2e.sh
git status --short
```

Expected:

- local checks pass;
- static Linux amd64 binary builds;
- live E2E passes;
- only intended source/doc deletions and updates are dirty before commit.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "Remove Bash reference after Go test parity"
```

## Task 11: Final Release Confidence Pass

**Files:**
- Modify only if verification exposes a real issue.

- [ ] **Step 1: Run final local verification**

```bash
make clean
make check
make build
```

Expected: pass.

- [ ] **Step 2: Run final live verification**

```bash
scripts/live-e2e.sh
```

Expected: pass.

- [ ] **Step 3: Push**

```bash
git push origin main
```

- [ ] **Step 4: Decide release action**

If this lands on `main` and all checks pass, cut the next release tag according to the repo’s release convention. If the repo is still in pre-release Go rewrite mode, push `main` only and wait for explicit release approval.

## Acceptance Criteria

- `docs/testing-matrix.md` exists and has no `gap` rows.
- Every behavior formerly protected by `spec/tests/*.sh` is covered by a Go local test, Go live E2E test, or documented as obsolete due to the accepted Go rewrite.
- `spec/` is removed.
- `README.md`, `Makefile`, and CI no longer reference the Bash oracle.
- `make check` passes.
- `make build` passes and creates `dist/wpcloud-site-git-deploy-linux-amd64`.
- `scripts/live-e2e.sh` passes on the disposable WP Cloud/Pressable site.
- Working tree is clean after commit and push.

## Self-Review

- Spec coverage: The plan maps all observed Bash oracle groups to concrete Go local or live E2E tasks, then gates deletion of `spec/` on a no-gap testing matrix.
- Placeholder scan: No task uses “TBD” or leaves an unnamed test category. Each gap has named files, named test functions, or named E2E labels.
- Type consistency: New helpers live under `internal/testutil`; existing packages keep current names (`internal/cli`, `internal/engine`, `internal/claims`, `internal/publicfs`, `internal/releases`, `internal/auth`, `internal/doctor`).
