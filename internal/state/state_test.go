package state

import (
	"path/filepath"
	"testing"
)

func TestLayoutPaths(t *testing.T) {
	layout := New("/home/site/.wpcloud-site-git-deploy")
	if got := layout.DeploymentConfig("site"); got != filepath.Join("/home/site/.wpcloud-site-git-deploy", "deployments", "site", "config.json") {
		t.Fatalf("unexpected config path: %s", got)
	}
	if got := layout.Runs("site"); got != filepath.Join("/home/site/.wpcloud-site-git-deploy", "deployments", "site", "runs.jsonl") {
		t.Fatalf("unexpected runs path: %s", got)
	}
	if got := layout.LatestRun("site"); got != filepath.Join("/home/site/.wpcloud-site-git-deploy", "deployments", "site", "latest-run.json") {
		t.Fatalf("unexpected latest run path: %s", got)
	}
	if got := layout.Repo("site"); got != filepath.Join("/home/site/.wpcloud-site-git-deploy", "repos", "site") {
		t.Fatalf("unexpected repo path: %s", got)
	}
	if got := layout.Worktree("site", "release-1"); got != filepath.Join("/home/site/.wpcloud-site-git-deploy", "tmp", "site", "release-1") {
		t.Fatalf("unexpected worktree path: %s", got)
	}
	if got := layout.WorktreeRoot("site"); got != filepath.Join("/home/site/.wpcloud-site-git-deploy", "tmp", "site") {
		t.Fatalf("unexpected worktree root path: %s", got)
	}
	if got := layout.Key("site"); got != filepath.Join("/home/site/.wpcloud-site-git-deploy", "keys", "site_ed25519") {
		t.Fatalf("unexpected key path: %s", got)
	}
}

func TestDocrootLayoutPaths(t *testing.T) {
	layout := NewDocroot("/srv/htdocs", "site")
	if got := layout.Current(); got != filepath.Join("/srv/htdocs", ".wpcloud-site-git-deploy", "deployments", "site", "current") {
		t.Fatalf("unexpected current path: %s", got)
	}
	if got := layout.Release("r1"); got != filepath.Join("/srv/htdocs", ".wpcloud-site-git-deploy", "deployments", "site", "releases", "r1") {
		t.Fatalf("unexpected release path: %s", got)
	}
	if got := layout.IncomingRoot(); got != filepath.Join("/srv/htdocs", ".wpcloud-site-git-deploy", "deployments", "site", "incoming") {
		t.Fatalf("unexpected incoming root path: %s", got)
	}
	if got := layout.ReleaseMetadata("r1"); got != filepath.Join("/srv/htdocs", ".wpcloud-site-git-deploy", "deployments", "site", "metadata", "r1.json") {
		t.Fatalf("unexpected metadata path: %s", got)
	}
	if got := layout.ReleaseStats("r1"); got != filepath.Join("/srv/htdocs", ".wpcloud-site-git-deploy", "deployments", "site", "metadata", "r1.stats.json") {
		t.Fatalf("unexpected stats path: %s", got)
	}
}
