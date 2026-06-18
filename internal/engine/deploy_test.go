package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
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

	second, err := Deploy(t.Context(), DeployOptions{StateRoot: stateRoot, Config: cfg})
	if err != nil {
		t.Fatalf("default-ref deploy failed: %v", err)
	}
	if !second.NoOp {
		t.Fatal("same commit/default ref deploy should be a no-op")
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
