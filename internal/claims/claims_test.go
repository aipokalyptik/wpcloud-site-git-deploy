package claims

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeClaimsTopLevelAndStickyBoundary(t *testing.T) {
	release := t.TempDir()
	writeFile(t, release, "index.html")
	writeFile(t, release, "wp-content/plugins/demo/plugin.php")
	writeFile(t, release, "wp-content/themes/theme/style.css")

	result, err := Compute(release, []string{"wp-content/plugins", "wp-content/themes"}, true)
	if err != nil {
		t.Fatalf("compute failed: %v", err)
	}
	assertClaims(t, result, []string{"index.html", "wp-content/plugins/demo", "wp-content/themes/theme"})
}

func TestComputeClaimsSharedMediaLeafFiles(t *testing.T) {
	release := t.TempDir()
	writeFile(t, release, "wp-content/uploads/2026/06/logo.png")
	writeFile(t, release, "wp-content/blogs.dir/1/files/photo.jpg")

	result, err := Compute(release, nil, true)
	if err != nil {
		t.Fatalf("compute failed: %v", err)
	}
	assertClaims(t, result, []string{
		"wp-content/blogs.dir/1/files/photo.jpg",
		"wp-content/uploads/2026/06/logo.png",
	})
}

func TestComputeClaimsRejectsSharedMediaRootFiles(t *testing.T) {
	for _, rel := range []string{"wp-content/uploads", "wp-content/blogs.dir"} {
		t.Run(rel, func(t *testing.T) {
			release := t.TempDir()
			path := filepath.Join(release, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("not a directory\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := Compute(release, nil, true); err == nil {
				t.Fatalf("expected shared media root file %q to fail", rel)
			}
		})
	}
}

func TestComputeClaimsRejectsSharedRuntimePaths(t *testing.T) {
	release := t.TempDir()
	writeFile(t, release, "wp-content/cache/object.bin")
	if _, err := Compute(release, nil, true); err == nil {
		t.Fatal("expected shared runtime path to fail")
	}
}

func TestComputeClaimsRejectsSharedMediaSymlink(t *testing.T) {
	release := t.TempDir()
	if err := os.MkdirAll(filepath.Join(release, "wp-content", "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(release, "wp-content", "uploads", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Compute(release, nil, true); err == nil {
		t.Fatal("expected shared media symlink to fail")
	}
}

func TestComputeClaimsSkipsDeploymentNamespaceAndGit(t *testing.T) {
	release := t.TempDir()
	writeFile(t, release, ".git/config")
	writeFile(t, release, ".wpcloud-site-git-deploy/deployments/site/current/index.html")
	writeFile(t, release, "index.html")

	result, err := Compute(release, nil, true)
	if err != nil {
		t.Fatalf("compute failed: %v", err)
	}
	assertClaims(t, result, []string{"index.html"})
}

func TestRemovedClaims(t *testing.T) {
	removed := Removed([]string{"a", "b", "c"}, []string{"b", "d"})
	assertClaims(t, removed, []string{"a", "c"})
}

func writeFile(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertClaims(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("claim length mismatch\nwant: %#v\n got: %#v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claims mismatch\nwant: %#v\n got: %#v", want, got)
		}
	}
}
