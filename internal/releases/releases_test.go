package releases

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReleaseIDIncludesTimestampCommitAndRandomSuffix(t *testing.T) {
	id, err := NewID(time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC), "abcdef1234567890")
	if err != nil {
		t.Fatalf("new id failed: %v", err)
	}
	if len(id) < len("20260618120000-abcdef123456-0000") {
		t.Fatalf("release id too short: %s", id)
	}
	if id[:27] != "20260618120000-abcdef123456" {
		t.Fatalf("unexpected release id prefix: %s", id)
	}
}

func TestCurrentReleaseMatchesCommitAndDeployRoot(t *testing.T) {
	meta := Metadata{Commit: "abc", DeployRoot: "public"}
	if !CurrentMatches(meta, "abc", "public") {
		t.Fatal("matching commit and deploy root should be a no-op")
	}
	if CurrentMatches(meta, "abc", "") {
		t.Fatal("different deploy root should not be a no-op")
	}
	if CurrentMatches(meta, "def", "public") {
		t.Fatal("different commit should not be a no-op")
	}
}

func TestSaveLoadMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r1.json")
	meta := Metadata{
		ReleaseID:  "r1",
		RefMode:    "branch",
		RefValue:   "main",
		Commit:     "abc",
		DeployRoot: "public",
		DeployedAt: time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC),
	}
	if err := SaveMetadata(path, meta); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	loaded, err := LoadMetadata(path)
	if err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	if loaded.ReleaseID != meta.ReleaseID || loaded.Commit != meta.Commit || loaded.DeployRoot != meta.DeployRoot {
		t.Fatalf("metadata mismatch: %#v", loaded)
	}
}

func TestPruneKeepsActiveAndNewest(t *testing.T) {
	dir := t.TempDir()
	for i, name := range []string{"old", "active", "new"} {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		ts := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(path, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Prune(dir, 2, "active")
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}
	if len(removed) != 1 || removed[0] != "old" {
		t.Fatalf("unexpected removed releases: %#v", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "active")); err != nil {
		t.Fatalf("active release was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); err != nil {
		t.Fatalf("newest release was removed: %v", err)
	}
}
