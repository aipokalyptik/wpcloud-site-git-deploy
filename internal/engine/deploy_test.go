package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/lock"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/report"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
)

func TestDeployBranchAndNoOp(t *testing.T) {
	if _, err := os.Stat("/usr/bin/git"); err != nil {
		t.Skip("git fixture requires git")
	}
	repo := initGitRepo(t)
	docroot := t.TempDir()
	stateRoot := filepath.Join(t.TempDir(), "state")
	cfg := config.Deployment{
		SchemaVersion: config.SchemaVersion,
		Name:          "site",
		RepoURL:       repo,
		Docroot:       docroot,
		DeploymentID:  "site",
		DefaultRef:    "main",
		KeepReleases:  3,
		Maintenance:   config.Maintenance{Enabled: false},
	}
	result, err := Deploy(t.Context(), DeployOptions{StateRoot: stateRoot, Config: cfg, RefMode: "branch", RefValue: "main"})
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}
	if result.NoOp {
		t.Fatal("first deploy should not be a no-op")
	}
	if got := string(mustRead(t, filepath.Join(docroot, "index.html"))); got != "hello\n" {
		t.Fatalf("unexpected deployed content: %q", got)
	}
	docrootLayout := state.NewDocroot(docroot, "site")
	stateLayout := state.New(stateRoot)
	successReport := readReportFile(t, result.Report)
	successReportBytes := mustRead(t, result.Report)
	if strings.Contains(string(successReportBytes), "repo_url") || strings.Contains(string(successReportBytes), repo) {
		t.Fatal("deploy report must not include repo_url or repository path")
	}
	if result.Report != docrootLayout.ReleaseStats(result.ReleaseID) {
		t.Fatalf("success report should point at sidecar, got %s", result.Report)
	}
	if successReport.Status != "success" || successReport.ReleaseID != result.ReleaseID || successReport.Commit != result.Commit {
		t.Fatalf("unexpected success report: %#v", successReport)
	}
	assertReportHasPhases(t, successReport, []string{"require_commands", "fetch", "git_features.submodules", "git_features.lfs_pull", "promote.discover_docroot_facts", "promote.compute_claims", "promote.validate_protected", "promote.compute_removed", "promote.overlap_cleanup", "promote.reconcile", "promote.switch_current", "promote.assert_symlinks", "promote.post_deploy_hook", "promote.prune", "metadata_write", "worktree_cleanup"})
	if successReport.Stats.Claims.New == 0 || successReport.Stats.Claims.Reconciled == 0 {
		t.Fatalf("success report should include claim stats: %#v", successReport.Stats.Claims)
	}
	if _, err := os.Stat(stateLayout.LatestRun("site")); err != nil {
		t.Fatalf("latest success report missing: %v", err)
	}

	staleIncoming := filepath.Join(docrootLayout.Base(), "incoming", "stale-before-noop")
	staleWorktree := filepath.Join(stateRoot, "tmp", "site", "stale-before-noop")
	if err := os.MkdirAll(staleIncoming, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staleWorktree, 0o755); err != nil {
		t.Fatal(err)
	}

	second, err := Deploy(t.Context(), DeployOptions{StateRoot: stateRoot, Config: cfg})
	if err != nil {
		t.Fatalf("default-ref deploy failed: %v", err)
	}
	if !second.NoOp {
		t.Fatal("same commit/default ref deploy should be a no-op")
	}
	if second.Report != stateLayout.Runs("site") {
		t.Fatalf("no-op report should point at runs history, got %s", second.Report)
	}
	latestRun := lastRunReport(t, stateLayout.Runs("site"))
	if latestRun.Status != "no_op" || latestRun.ReleaseID != second.ReleaseID {
		t.Fatalf("unexpected no-op report: %#v", latestRun)
	}
	if _, err := os.Stat(docrootLayout.ReleaseStats(second.ReleaseID)); err != nil {
		t.Fatalf("no-op should not remove existing success sidecar, err=%v", err)
	}
	if _, err := os.Stat(staleIncoming); !os.IsNotExist(err) {
		t.Fatalf("no-op deploy should sweep stale incoming, err=%v", err)
	}
	if _, err := os.Stat(staleWorktree); !os.IsNotExist(err) {
		t.Fatalf("no-op deploy should sweep stale worktree, err=%v", err)
	}
}

func TestDeployFailureWritesStateOnlyReport(t *testing.T) {
	repo := initGitRepo(t)
	docroot := t.TempDir()
	stateRoot := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(filepath.Join(repo, "wp-content", "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "wp-content", "cache", "object.bin"), []byte("cache\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "wp-content/cache/object.bin")
	runGit(t, repo, "commit", "-m", "shared-cache")
	cfg := config.Deployment{
		SchemaVersion: config.SchemaVersion,
		Name:          "site",
		RepoURL:       repo,
		Docroot:       docroot,
		DeploymentID:  "site",
		DefaultRef:    "main",
		KeepReleases:  3,
		Maintenance:   config.Maintenance{Enabled: false},
	}

	_, err := Deploy(t.Context(), DeployOptions{StateRoot: stateRoot, Config: cfg})
	if err == nil || !strings.Contains(err.Error(), "shared path cannot be deployed") {
		t.Fatalf("expected shared path deploy failure, got %v", err)
	}
	layout := state.New(stateRoot)
	record := lastRunReport(t, layout.Runs("site"))
	if record.Status != "failed" {
		t.Fatalf("expected failed report, got %#v", record)
	}
	if record.FailedPhase == nil || *record.FailedPhase != "promote.compute_claims" {
		t.Fatalf("unexpected failed phase: %#v", record.FailedPhase)
	}
	if record.Error == nil || !strings.Contains(*record.Error, "shared path cannot be deployed") {
		t.Fatalf("unexpected report error: %#v", record.Error)
	}
	if record.ReleaseID == "" {
		t.Fatal("failure after release creation should record release id")
	}
	if _, err := os.Stat(state.NewDocroot(docroot, "site").ReleaseStats(record.ReleaseID)); !os.IsNotExist(err) {
		t.Fatalf("failed deploy should not write docroot stats sidecar, err=%v", err)
	}
}

func TestRollbackToRelease(t *testing.T) {
	repo := initGitRepo(t)
	docroot := t.TempDir()
	stateRoot := filepath.Join(t.TempDir(), "state")
	cfg := config.Deployment{
		SchemaVersion: config.SchemaVersion,
		Name:          "site",
		RepoURL:       repo,
		Docroot:       docroot,
		DeploymentID:  "site",
		DefaultRef:    "main",
		KeepReleases:  3,
		Maintenance:   config.Maintenance{Enabled: false},
	}
	first, err := Deploy(t.Context(), DeployOptions{StateRoot: stateRoot, Config: cfg})
	if err != nil {
		t.Fatalf("first deploy failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "index.html"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "index.html")
	runGit(t, repo, "commit", "-m", "second")
	if _, err := Deploy(t.Context(), DeployOptions{StateRoot: stateRoot, Config: cfg, Force: true}); err != nil {
		t.Fatalf("second deploy failed: %v", err)
	}
	if got := string(mustRead(t, filepath.Join(docroot, "index.html"))); got != "second\n" {
		t.Fatalf("unexpected second content: %q", got)
	}
	if err := Rollback(RollbackOptions{Config: cfg, ReleaseID: first.ReleaseID}); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if got := string(mustRead(t, filepath.Join(docroot, "index.html"))); got != "hello\n" {
		t.Fatalf("rollback did not restore first release: %q", got)
	}
}

func TestDeployFailsBeforeStagingWhenDeploymentLockIsBusy(t *testing.T) {
	repo := initGitRepo(t)
	docroot := t.TempDir()
	stateRoot := filepath.Join(t.TempDir(), "state")
	cfg := config.Deployment{
		SchemaVersion: config.SchemaVersion,
		Name:          "site",
		RepoURL:       repo,
		Docroot:       docroot,
		DeploymentID:  "site",
		DefaultRef:    "main",
		KeepReleases:  3,
		Maintenance:   config.Maintenance{Enabled: false},
	}
	docrootLayout := state.NewDocroot(docroot, "site")
	if err := os.MkdirAll(docrootLayout.Base(), 0o755); err != nil {
		t.Fatal(err)
	}
	heldLock, err := lock.Acquire(docrootLayout.Lock())
	if err != nil {
		t.Fatalf("test lock acquire failed: %v", err)
	}
	defer heldLock.Close()

	_, err = Deploy(t.Context(), DeployOptions{StateRoot: stateRoot, Config: cfg, Force: true})
	if err == nil || !strings.Contains(err.Error(), "deployment already running") {
		t.Fatalf("expected busy deployment lock, got %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(docrootLayout.Base(), "incoming")); err == nil && len(entries) != 0 {
		t.Fatalf("busy deploy should not leave incoming staging dirs: %#v", entries)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading incoming dir failed: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(stateRoot, "tmp", "site")); err == nil && len(entries) != 0 {
		t.Fatalf("busy deploy should not leave temp worktrees: %#v", entries)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading temp worktree dir failed: %v", err)
	}
}

func TestDeploySweepsStaleIncomingAndWorktreeDirectories(t *testing.T) {
	repo := initGitRepo(t)
	docroot := t.TempDir()
	stateRoot := filepath.Join(t.TempDir(), "state")
	cfg := config.Deployment{
		SchemaVersion: config.SchemaVersion,
		Name:          "site",
		RepoURL:       repo,
		Docroot:       docroot,
		DeploymentID:  "site",
		DefaultRef:    "main",
		KeepReleases:  3,
		Maintenance:   config.Maintenance{Enabled: false},
	}
	docrootLayout := state.NewDocroot(docroot, "site")
	staleIncoming := filepath.Join(docrootLayout.Base(), "incoming", "stale-incoming")
	staleWorktree := filepath.Join(stateRoot, "tmp", "site", "stale-worktree")
	if err := os.MkdirAll(staleIncoming, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staleWorktree, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Deploy(t.Context(), DeployOptions{StateRoot: stateRoot, Config: cfg, Force: true}); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}
	if _, err := os.Stat(staleIncoming); !os.IsNotExist(err) {
		t.Fatalf("stale incoming should be removed, err=%v", err)
	}
	if _, err := os.Stat(staleWorktree); !os.IsNotExist(err) {
		t.Fatalf("stale worktree should be removed, err=%v", err)
	}
}

func TestPrepareGitFeaturesRejectsUnresolvedLFSPointer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git fixture requires git")
	}
	worktree := t.TempDir()
	runGit(t, worktree, "init", "-b", "main")
	runGit(t, worktree, "config", "user.email", "test@example.com")
	runGit(t, worktree, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(worktree, ".gitattributes"), []byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pointer := strings.Join([]string{
		"version https://git-lfs.github.com/spec/v1",
		"oid sha256:1111111111111111111111111111111111111111111111111111111111111111",
		"size 12",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(worktree, "asset.bin"), []byte(pointer), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", ".gitattributes", "asset.bin")

	fakeBin := t.TempDir()
	fakeGitLFS := filepath.Join(fakeBin, "git-lfs")
	if err := os.WriteFile(fakeGitLFS, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := prepareGitFeatures(t.Context(), worktree, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Git LFS pointer files remain after git lfs pull") {
		t.Fatalf("expected unresolved LFS pointer rejection, got %v", err)
	}
}

func readReportFile(t *testing.T, path string) report.Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report %s: %v", path, err)
	}
	var record report.Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("parse report %s: %v", path, err)
	}
	return record
}

func lastRunReport(t *testing.T, path string) report.Record {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open runs report %s: %v", path, err)
	}
	defer file.Close()
	var last string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		last = scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if last == "" {
		t.Fatalf("runs report %s was empty", path)
	}
	var record report.Record
	if err := json.Unmarshal([]byte(last), &record); err != nil {
		t.Fatalf("parse runs report: %v", err)
	}
	return record
}

func assertReportHasPhases(t *testing.T, record report.Record, expected []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, phase := range record.Phases {
		if phase.DurationMS < 0 {
			t.Fatalf("phase duration should be non-negative: %#v", phase)
		}
		seen[phase.Name] = true
	}
	for _, name := range expected {
		if !seen[name] {
			t.Fatalf("report missing phase %s in %#v", name, record.Phases)
		}
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "index.html"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "index.html")
	runGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	result, err := runCommand(t.Context(), "git", args, dir, nil)
	if err != nil {
		t.Fatalf("git %s failed: %v\nstdout=%s\nstderr=%s", strings.Join(args, " "), err, result.Stdout, result.Stderr)
	}
}
