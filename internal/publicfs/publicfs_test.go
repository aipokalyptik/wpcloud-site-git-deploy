package publicfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublicSymlinkTargetIsRelative(t *testing.T) {
	target := PublicSymlinkTarget("site", "wp-content/plugins/demo/plugin.php")
	want := "../../../.wpcloud-site-git-deploy/deployments/site/current/wp-content/plugins/demo/plugin.php"
	if target != want {
		t.Fatalf("target mismatch\nwant: %s\n got: %s", want, target)
	}
}

func TestAssertSymlinkUnderDocroot(t *testing.T) {
	docroot := t.TempDir()
	releasePath := filepath.Join(docroot, ".wpcloud-site-git-deploy", "deployments", "site", "current", "index.html")
	if err := os.MkdirAll(filepath.Dir(releasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasePath, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".wpcloud-site-git-deploy/deployments/site/current/index.html", filepath.Join(docroot, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := AssertClaimSymlinksUnderDocroot(docroot, []string{"index.html"}, ""); err != nil {
		t.Fatalf("valid symlink should pass: %v", err)
	}
}

func TestAssertSymlinkRejectsAbsoluteTarget(t *testing.T) {
	docroot := t.TempDir()
	if err := os.Symlink("/etc/passwd", filepath.Join(docroot, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := AssertClaimSymlinksUnderDocroot(docroot, []string{"index.html"}, ""); err == nil {
		t.Fatal("expected absolute target to fail")
	}
}

func TestAssertSymlinkRejectsOutsideDocroot(t *testing.T) {
	docroot := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../"+filepath.Base(outside)+"/secret", filepath.Join(docroot, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := AssertClaimSymlinksUnderDocroot(docroot, []string{"index.html"}, ""); err == nil {
		t.Fatal("expected outside target to fail")
	}
}

func TestAssertSymlinkRejectsHomeTarget(t *testing.T) {
	docroot := t.TempDir()
	home := t.TempDir()
	if err := os.Symlink(home+"/secret", filepath.Join(docroot, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := AssertClaimSymlinksUnderDocroot(docroot, []string{"index.html"}, home); err == nil {
		t.Fatal("expected HOME-containing target to fail")
	}
}
