package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
)

func TestPromoteCreatesPublicSymlinkAndCurrent(t *testing.T) {
	docroot := t.TempDir()
	incoming := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r1")
	writeFile(t, incoming, "index.html", "hello\n")

	if err := Promote(PromoteOptions{Docroot: docroot, DeploymentID: "site", ReleaseID: "r1", KeepReleases: 3}); err != nil {
		t.Fatalf("promote failed: %v", err)
	}
	if got := readlink(t, filepath.Join(docroot, "index.html")); got != ".wpcloud-site-git-deploy/deployments/site/current/index.html" {
		t.Fatalf("unexpected public target: %s", got)
	}
	if got := readlink(t, filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "current")); got != "releases/r1" {
		t.Fatalf("unexpected current target: %s", got)
	}
	if string(mustRead(t, filepath.Join(docroot, "index.html"))) != "hello\n" {
		t.Fatal("public symlink did not serve release content")
	}
}

func TestPromoteReclaimsExistingFile(t *testing.T) {
	docroot := t.TempDir()
	if err := os.WriteFile(filepath.Join(docroot, "index.html"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	incoming := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r1")
	writeFile(t, incoming, "index.html", "new\n")

	if err := Promote(PromoteOptions{Docroot: docroot, DeploymentID: "site", ReleaseID: "r1", KeepReleases: 3}); err != nil {
		t.Fatalf("promote failed: %v", err)
	}
	if string(mustRead(t, filepath.Join(docroot, "index.html"))) != "new\n" {
		t.Fatal("existing file was not reclaimed")
	}
}

func TestPromoteAllowsExactForeignTakeover(t *testing.T) {
	docroot := t.TempDir()
	if err := os.Symlink(".wpcloud-site-git-deploy/deployments/other/current/index.html", filepath.Join(docroot, "index.html")); err != nil {
		t.Fatal(err)
	}
	incoming := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r1")
	writeFile(t, incoming, "index.html", "new\n")

	if err := Promote(PromoteOptions{Docroot: docroot, DeploymentID: "site", ReleaseID: "r1", KeepReleases: 3}); err != nil {
		t.Fatalf("promote failed: %v", err)
	}
	if got := readlink(t, filepath.Join(docroot, "index.html")); got != ".wpcloud-site-git-deploy/deployments/site/current/index.html" {
		t.Fatalf("exact foreign symlink should be taken over, got %s", got)
	}
}

func TestPromoteRejectsForeignAncestor(t *testing.T) {
	docroot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(docroot, "wp-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../.wpcloud-site-git-deploy/deployments/other/current/plugins", filepath.Join(docroot, "wp-content", "plugins")); err != nil {
		t.Fatal(err)
	}
	incoming := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r1")
	writeFile(t, incoming, "wp-content/plugins/demo/plugin.php", "new\n")

	if err := Promote(PromoteOptions{Docroot: docroot, DeploymentID: "site", ReleaseID: "r1", KeepReleases: 3, Boundaries: []string{"wp-content/plugins"}}); err == nil {
		t.Fatal("expected foreign ancestor to fail")
	}
}

func TestPromoteRunsPostDeployWithWordPressMaintenanceMarker(t *testing.T) {
	docroot := t.TempDir()
	incoming := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r1")
	writeFile(t, incoming, "index.html", "hello\n")
	hook := filepath.Join(docroot, "post-deploy.sh")
	marker := filepath.Join(docroot, "marker.txt")
	if err := os.WriteFile(hook, []byte("test -f .maintenance\ngrep -q '\\$upgrading' .maintenance\nprintf hook > marker.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Promote(PromoteOptions{
		Docroot:      docroot,
		DeploymentID: "site",
		ReleaseID:    "r1",
		KeepReleases: 3,
		PostDeploy:   hook,
		Maintenance:  config.Maintenance{Enabled: true, File: ".maintenance"},
	})
	if err != nil {
		t.Fatalf("promote failed: %v", err)
	}
	if got := string(mustRead(t, marker)); got != "hook" {
		t.Fatalf("post deploy did not run: %q", got)
	}
	if _, err := os.Stat(filepath.Join(docroot, ".maintenance")); !os.IsNotExist(err) {
		t.Fatalf("maintenance file should be removed, err=%v", err)
	}
}

func TestPromotePostDeployFailureKeepsReleaseCurrentAndRemovesMaintenance(t *testing.T) {
	docroot := t.TempDir()
	incoming := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r1")
	writeFile(t, incoming, "index.html", "hello\n")
	hook := filepath.Join(docroot, "post-deploy.sh")
	if err := os.WriteFile(hook, []byte("exit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Promote(PromoteOptions{
		Docroot:      docroot,
		DeploymentID: "site",
		ReleaseID:    "r1",
		KeepReleases: 3,
		PostDeploy:   hook,
		Maintenance:  config.Maintenance{Enabled: true, File: ".maintenance"},
	})
	if err == nil || !strings.Contains(err.Error(), "post-deploy failed") {
		t.Fatalf("expected post-deploy failure, got %v", err)
	}
	if got := readlink(t, filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "current")); got != "releases/r1" {
		t.Fatalf("release should remain current after hook failure, got %s", got)
	}
	if _, err := os.Stat(filepath.Join(docroot, ".maintenance")); !os.IsNotExist(err) {
		t.Fatalf("maintenance file should be removed after failure, err=%v", err)
	}
}

func TestPromoteAllowsSharedMediaLeafAndRejectsSharedRuntimePath(t *testing.T) {
	docroot := t.TempDir()
	incoming := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r1")
	writeFile(t, incoming, "wp-content/uploads/static/logo.png", "image\n")
	writeFile(t, incoming, "wp-content/blogs.dir/1/files/logo.png", "image\n")
	if err := Promote(PromoteOptions{Docroot: docroot, DeploymentID: "site", ReleaseID: "r1", KeepReleases: 3}); err != nil {
		t.Fatalf("shared media leaf deploy failed: %v", err)
	}
	if got := readlink(t, filepath.Join(docroot, "wp-content/uploads/static/logo.png")); !strings.Contains(got, "current/wp-content/uploads/static/logo.png") {
		t.Fatalf("unexpected uploads leaf target: %s", got)
	}

	incoming = filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r2")
	writeFile(t, incoming, "wp-content/cache/object-cache.bin", "cache\n")
	if err := Promote(PromoteOptions{Docroot: docroot, DeploymentID: "site", ReleaseID: "r2", KeepReleases: 3}); err == nil || !strings.Contains(err.Error(), "shared path cannot be deployed") {
		t.Fatalf("expected shared cache rejection, got %v", err)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readlink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(target)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
