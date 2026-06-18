package engine

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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
	if _, err := os.Stat(filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "releases", "r2")); !os.IsNotExist(err) {
		t.Fatalf("rejected pre-public release should be cleaned up, err=%v", err)
	}
}

func TestPromoteLeavesParentDirectoriesAfterRemovingClaim(t *testing.T) {
	docroot := t.TempDir()
	incoming := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r1")
	writeFile(t, incoming, "wp-content/plugins/demo/plugin.php", "old\n")
	if err := Promote(PromoteOptions{Docroot: docroot, DeploymentID: "site", ReleaseID: "r1", KeepReleases: 3, Boundaries: []string{"wp-content/plugins"}}); err != nil {
		t.Fatalf("first promote failed: %v", err)
	}

	incoming = filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "incoming", "r2")
	writeFile(t, incoming, "index.html", "new\n")
	if err := Promote(PromoteOptions{Docroot: docroot, DeploymentID: "site", ReleaseID: "r2", KeepReleases: 3, Boundaries: []string{"wp-content/plugins"}}); err != nil {
		t.Fatalf("second promote failed: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(docroot, "wp-content", "plugins", "demo")); !os.IsNotExist(err) {
		t.Fatalf("removed claim symlink should be gone, err=%v", err)
	}
	for _, rel := range []string{"wp-content", "wp-content/plugins"} {
		info, err := os.Stat(filepath.Join(docroot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("parent directory %q should remain: %v", rel, err)
		}
		if !info.IsDir() {
			t.Fatalf("parent path %q should remain a directory", rel)
		}
	}
}

func TestDiscoverBoundaryClaimsRequiresPrivilegedOwnership(t *testing.T) {
	docroot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(docroot, "wp-content", "plugins"), 0o1777); err != nil {
		t.Fatal(err)
	}

	boundaries, err := discoverBoundaryClaims(docroot)
	if err != nil {
		t.Fatalf("discover boundaries failed: %v", err)
	}
	if len(boundaries) != 0 {
		t.Fatalf("non-root-owned sticky directory should not be a boundary: %#v", boundaries)
	}
}

func TestDiscoverProtectedAnchorsRequiresPrivilegedOwnership(t *testing.T) {
	docroot := t.TempDir()
	protectedPath := filepath.Join(docroot, "wp-config.php")
	if err := os.WriteFile(protectedPath, []byte("local config\n"), 0o444); err != nil {
		t.Fatal(err)
	}

	anchors, err := discoverProtectedAnchors(docroot)
	if err != nil {
		t.Fatalf("discover protected anchors failed: %v", err)
	}
	if len(anchors) != 0 {
		t.Fatalf("non-root-owned read-only file should not be protected: %#v", anchors)
	}
}

func TestStickyBoundaryPredicateRequiresPrivilegedOwnership(t *testing.T) {
	tests := []struct {
		name string
		uid  uint32
		gid  uint32
		want bool
	}{
		{name: "root owned", uid: 0, gid: uint32(os.Getgid()), want: true},
		{name: "root group", uid: uint32(os.Getuid()), gid: 0, want: true},
		{name: "site user", uid: uint32(os.Getuid()), gid: uint32(os.Getgid()), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := fakeFileInfo{sys: &syscall.Stat_t{Uid: tt.uid, Gid: tt.gid}}
			if got := rootOwnedOrRootGroup(info); got != tt.want {
				t.Fatalf("rootOwnedOrRootGroup() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProtectedAnchorPredicateRequiresPrivilegedOwnershipAndEffectiveWritability(t *testing.T) {
	tests := []struct {
		name     string
		uid      uint32
		gid      uint32
		writable bool
		want     bool
	}{
		{name: "root owned not writable", uid: 0, gid: uint32(os.Getgid()), writable: false, want: true},
		{name: "root group not writable", uid: uint32(os.Getuid()), gid: 0, writable: false, want: true},
		{name: "site user not writable", uid: uint32(os.Getuid()), gid: uint32(os.Getgid()), writable: false, want: false},
		{name: "root owned writable", uid: 0, gid: uint32(os.Getgid()), writable: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := protectedAnchorCandidate(tt.uid, tt.gid, tt.writable); got != tt.want {
				t.Fatalf("protectedAnchorCandidate() = %v, want %v", got, tt.want)
			}
		})
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

type fakeFileInfo struct {
	sys any
}

func (fakeFileInfo) Name() string       { return "fake" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return true }
func (f fakeFileInfo) Sys() any         { return f.sys }
