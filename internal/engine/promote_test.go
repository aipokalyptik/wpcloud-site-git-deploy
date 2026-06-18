package engine

import (
	"os"
	"path/filepath"
	"testing"
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
