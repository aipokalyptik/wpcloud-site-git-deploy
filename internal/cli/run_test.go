package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
)

func TestRunInitListStatusConfigDestroy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run(t.Context(), []string{
		"init",
		"--name", "site",
		"--repo", "git@example.com:team/site.git",
		"--docroot", "/srv/htdocs",
		"--deployment-id", "site",
		"--default-ref", "main",
		"--keep-releases", "4",
		"--no-maintenance-file",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	cfgPath := filepath.Join(home, ".wpcloud-site-git-deploy", "deployments", "site", "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.KeepReleases != 4 || cfg.Maintenance.Enabled {
		t.Fatalf("unexpected config after init: %#v", cfg)
	}

	stdout.Reset()
	if err := Run(t.Context(), []string{"list"}, &stdout, &stderr); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "site" {
		t.Fatalf("unexpected list output: %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(t.Context(), []string{"status", "--name", "site"}, &stdout, &stderr); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "name=site") || !strings.Contains(stdout.String(), "default_ref=main") {
		t.Fatalf("unexpected status: %s", stdout.String())
	}

	if err := Run(t.Context(), []string{"config", "--name", "site", "--set", "default_ref=stable", "--set", "keep_releases=2"}, &stdout, &stderr); err != nil {
		t.Fatalf("config set failed: %v", err)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.DefaultRef != "stable" || cfg.KeepReleases != 2 {
		t.Fatalf("config changes not applied: %#v", cfg)
	}

	if err := Run(t.Context(), []string{"destroy", "--name", "site", "--confirm-destroy", "site"}, &stdout, &stderr); err != nil {
		t.Fatalf("destroy failed: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(cfgPath)); !os.IsNotExist(err) {
		t.Fatalf("deployment state should be removed, stat err=%v", err)
	}
}

func TestRunDeployUsesDefaultRef(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := initGitRepoForCLI(t)
	docroot := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run(t.Context(), []string{
		"init",
		"--name", "site",
		"--repo", repo,
		"--docroot", docroot,
		"--deployment-id", "site",
		"--default-ref", "main",
		"--no-maintenance-file",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	stdout.Reset()
	if err := Run(t.Context(), []string{"deploy", "--name", "site"}, &stdout, &stderr); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "release_id=") || !strings.Contains(stdout.String(), "commit=") {
		t.Fatalf("unexpected deploy output: %s", stdout.String())
	}
	if got := string(mustReadFile(t, filepath.Join(docroot, "index.html"))); got != "hello\n" {
		t.Fatalf("unexpected deployed content: %q", got)
	}
	firstRelease := strings.Fields(strings.TrimSpace(stdout.String()))[0]
	firstRelease = strings.TrimPrefix(firstRelease, "release_id=")

	stdout.Reset()
	if err := Run(t.Context(), []string{"releases", "--name", "site"}, &stdout, &stderr); err != nil {
		t.Fatalf("releases failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "current") {
		t.Fatalf("unexpected releases output: %s", stdout.String())
	}

	stdout.Reset()
	if err := Run(t.Context(), []string{"branches", "--name", "site", "--fetch"}, &stdout, &stderr); err != nil {
		t.Fatalf("branches failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "main") {
		t.Fatalf("unexpected branches output: %s", stdout.String())
	}

	stdout.Reset()
	if err := Run(t.Context(), []string{"commits", "--name", "site", "--limit", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("commits failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "initial") {
		t.Fatalf("unexpected commits output: %s", stdout.String())
	}

	if err := os.WriteFile(filepath.Join(repo, "index.html"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForCLI(t, repo, "add", "index.html")
	runGitForCLI(t, repo, "commit", "-m", "second")
	stdout.Reset()
	if err := Run(t.Context(), []string{"deploy", "--name", "site", "--force"}, &stdout, &stderr); err != nil {
		t.Fatalf("second deploy failed: %v", err)
	}
	if got := string(mustReadFile(t, filepath.Join(docroot, "index.html"))); got != "second\n" {
		t.Fatalf("unexpected second content: %q", got)
	}
	if err := Run(t.Context(), []string{"rollback", "--name", "site", "--to", firstRelease}, &stdout, &stderr); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if got := string(mustReadFile(t, filepath.Join(docroot, "index.html"))); got != "hello\n" {
		t.Fatalf("rollback content mismatch: %q", got)
	}
}

func TestRunRollbackDefaultIgnoresReleaseWithoutMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := initGitRepoForCLI(t)
	docroot := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run(t.Context(), []string{
		"init",
		"--name", "site",
		"--repo", repo,
		"--docroot", docroot,
		"--deployment-id", "site",
		"--default-ref", "main",
		"--no-maintenance-file",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if err := Run(t.Context(), []string{"deploy", "--name", "site"}, &stdout, &stderr); err != nil {
		t.Fatalf("first deploy failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "index.html"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForCLI(t, repo, "add", "index.html")
	runGitForCLI(t, repo, "commit", "-m", "second")
	if err := Run(t.Context(), []string{"deploy", "--name", "site", "--force"}, &stdout, &stderr); err != nil {
		t.Fatalf("second deploy failed: %v", err)
	}

	brokenRelease := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "releases", "broken-without-metadata")
	if err := os.MkdirAll(brokenRelease, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenRelease, "index.html"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := Run(t.Context(), []string{"rollback", "--name", "site"}, &stdout, &stderr); err != nil {
		t.Fatalf("default rollback failed: %v", err)
	}
	if strings.Contains(stdout.String(), "broken-without-metadata") {
		t.Fatalf("default rollback selected metadata-less release: %s", stdout.String())
	}
	if got := string(mustReadFile(t, filepath.Join(docroot, "index.html"))); got != "hello\n" {
		t.Fatalf("default rollback should select metadata-backed release, got %q", got)
	}
}

func TestRunAuthUseKeyRemoveAndDoctorOffline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := initGitRepoForCLI(t)
	docroot := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "deploy_ed25519")
	runExternalOrFail(t, "", "ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run(t.Context(), []string{
		"init",
		"--name", "site",
		"--repo", repo,
		"--docroot", docroot,
		"--deployment-id", "site",
		"--default-ref", "main",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if err := Run(t.Context(), []string{"auth", "--name", "site", "--use-key", keyPath}, &stdout, &stderr); err != nil {
		t.Fatalf("auth use-key failed: %v", err)
	}
	cfg, err := config.Load(filepath.Join(home, ".wpcloud-site-git-deploy", "deployments", "site", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SSHKeyPath != keyPath {
		t.Fatalf("ssh key path not stored: %#v", cfg)
	}
	stdout.Reset()
	if err := Run(t.Context(), []string{"doctor", "--name", "site", "--offline"}, &stdout, &stderr); err != nil {
		t.Fatalf("doctor offline failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "OK config") {
		t.Fatalf("unexpected doctor output: %s", stdout.String())
	}
	if err := Run(t.Context(), []string{"auth", "--name", "site", "--remove"}, &stdout, &stderr); err != nil {
		t.Fatalf("auth remove failed: %v", err)
	}
	cfg, err = config.Load(filepath.Join(home, ".wpcloud-site-git-deploy", "deployments", "site", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SSHKeyPath != "" {
		t.Fatalf("ssh key path should be cleared: %#v", cfg)
	}
}

func TestRunAuthGenerateImportAndPurgeManagedKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := initGitRepoForCLI(t)
	docroot := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run(t.Context(), []string{
		"init",
		"--name", "site",
		"--repo", repo,
		"--docroot", docroot,
		"--deployment-id", "site",
		"--default-ref", "main",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	stdout.Reset()
	if err := Run(t.Context(), []string{"auth", "--name", "site"}, &stdout, &stderr); err != nil {
		t.Fatalf("auth generate failed: %v", err)
	}
	managedKey := filepath.Join(home, ".wpcloud-site-git-deploy", "keys", "site_ed25519")
	if _, err := os.Stat(managedKey); err != nil {
		t.Fatalf("managed key was not created: %v", err)
	}
	if _, err := os.Stat(managedKey + ".pub"); err != nil {
		t.Fatalf("managed public key was not created: %v", err)
	}

	externalKey := filepath.Join(t.TempDir(), "external_ed25519")
	runExternalOrFail(t, "", "ssh-keygen", "-t", "ed25519", "-N", "", "-f", externalKey)
	if err := Run(t.Context(), []string{"auth", "--name", "site", "--import-key", externalKey}, &stdout, &stderr); err == nil {
		t.Fatal("import should fail when managed key exists without --force-new-key")
	}
	if err := Run(t.Context(), []string{"auth", "--name", "site", "--import-key", externalKey, "--force-new-key"}, &stdout, &stderr); err != nil {
		t.Fatalf("import with force failed: %v", err)
	}
	if err := Run(t.Context(), []string{"auth", "--name", "site", "--remove", "--purge-key"}, &stdout, &stderr); err != nil {
		t.Fatalf("auth purge failed: %v", err)
	}
	if _, err := os.Stat(managedKey); !os.IsNotExist(err) {
		t.Fatalf("managed key should be purged, err=%v", err)
	}
}

func initGitRepoForCLI(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitForCLI(t, repo, "init", "-b", "main")
	runGitForCLI(t, repo, "config", "user.email", "test@example.com")
	runGitForCLI(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "index.html"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForCLI(t, repo, "add", "index.html")
	runGitForCLI(t, repo, "commit", "-m", "initial")
	return repo
}

func runGitForCLI(t *testing.T, dir string, args ...string) {
	t.Helper()
	result, err := runExternal(t.Context(), "git", args, dir)
	if err != nil {
		t.Fatalf("git %s failed: %v\nstdout=%s\nstderr=%s", strings.Join(args, " "), err, result.stdout, result.stderr)
	}
}

func runExternalOrFail(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	result, err := runExternal(t.Context(), name, args, dir)
	if err != nil {
		t.Fatalf("%s %s failed: %v\nstdout=%s\nstderr=%s", name, strings.Join(args, " "), err, result.stdout, result.stderr)
	}
}

type externalResult struct {
	stdout string
	stderr string
}

func runExternal(ctx context.Context, name string, args []string, dir string) (externalResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return externalResult{stdout: stdout.String(), stderr: stderr.String()}, err
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
