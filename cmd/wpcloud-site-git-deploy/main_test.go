package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublicSymlinkTarget(t *testing.T) {
	got := publicSymlinkTarget("site", "wp-content/plugins/example/plugin.php")
	want := "../../../.github-ssh-deploy/deployments/site/current/wp-content/plugins/example/plugin.php"
	if got != want {
		t.Fatalf("target mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestClaimForPathUsesDeepestBoundary(t *testing.T) {
	boundaries := []string{"wp-content", "wp-content/plugins"}
	if got := claimForPath("wp-content/plugins/foo/index.php", boundaries); got != "wp-content/plugins/foo" {
		t.Fatalf("unexpected claim: %s", got)
	}
	if got := claimForPath("assets/app.css", boundaries); got != "assets" {
		t.Fatalf("unexpected top-level claim: %s", got)
	}
}

func TestValidateClaimsNotProtected(t *testing.T) {
	if err := validateClaimsNotProtected([]string{"wp-content/advanced-cache.php"}, []string{"wp-content/advanced-cache.php"}); err == nil {
		t.Fatal("expected exact protected claim to fail")
	}
	if err := validateClaimsNotProtected([]string{"wp-content"}, []string{"wp-content/advanced-cache.php"}); err == nil {
		t.Fatal("expected ancestor claim to fail")
	}
	if err := validateClaimsNotProtected([]string{"wp-content/plugins/foo"}, []string{"wp-content/advanced-cache.php"}); err != nil {
		t.Fatalf("unexpected validation failure: %v", err)
	}
}

func TestPathIsWritableUsesAccessSemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked")
	if err := os.WriteFile(path, []byte("locked"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if pathIsWritable(path) {
		t.Fatal("expected read-only file to be reported non-writable")
	}
}

func TestEnvConfigCompatibility(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.env")
	if err := os.WriteFile(path, []byte("repo_url='/tmp/repo with space'\ndocroot=/srv/htdocs\ndeployment_id=site\ndefault_ref=main\nkeep_releases=4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := readEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if values["repo_url"] != "/tmp/repo with space" {
		t.Fatalf("shell-quoted value did not round trip: %q", values["repo_url"])
	}
	out := filepath.Join(dir, "out.env")
	if err := writeEnvFile(out, values); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := readEnvFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip["deployment_id"] != "site" || roundTrip["keep_releases"] != "4" {
		t.Fatalf("unexpected round trip values: %#v", roundTrip)
	}
}

func TestSelectRollbackReleasePrefersMetadataBackedRelease(t *testing.T) {
	base := t.TempDir()
	releases := filepath.Join(base, "releases")
	metadata := filepath.Join(base, "metadata")
	if err := os.MkdirAll(releases, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(metadata, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(releases, "old")
	newestUnbacked := filepath.Join(releases, "newest-unbacked")
	if err := os.Mkdir(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(newestUnbacked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "old.env"), []byte("release_id=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_ = os.Chtimes(old, now.Add(-time.Hour), now.Add(-time.Hour))
	_ = os.Chtimes(newestUnbacked, now, now)
	selected, err := selectRollbackRelease(base, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected != "old" {
		t.Fatalf("expected metadata-backed release, got %s", selected)
	}
}

func TestAssertClaimSymlinksUnderDocroot(t *testing.T) {
	docroot := t.TempDir()
	releasePath := filepath.Join(docroot, ".github-ssh-deploy", "deployments", "site", "current")
	if err := os.MkdirAll(releasePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releasePath, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".github-ssh-deploy/deployments/site/current/index.html", filepath.Join(docroot, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := assertClaimSymlinksUnderDocroot(docroot, []string{"index.html"}); err != nil {
		t.Fatalf("expected valid claim symlink: %v", err)
	}
	_ = os.Remove(filepath.Join(docroot, "index.html"))
	if err := os.Symlink("/outside", filepath.Join(docroot, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := assertClaimSymlinksUnderDocroot(docroot, []string{"index.html"}); err == nil {
		t.Fatal("expected absolute target to fail")
	}
}
