package report

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendBoundedJSONLTruncatesToMostRecentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	for i := 0; i < 5; i++ {
		record := Record{SchemaVersion: SchemaVersion, Name: "site", ReleaseID: string(rune('a' + i))}
		if err := AppendBoundedJSONL(path, record, 3); err != nil {
			t.Fatalf("append record %d: %v", i, err)
		}
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var ids []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid jsonl record: %v", err)
		}
		ids = append(ids, record.ReleaseID)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if got := ids; len(got) != 3 || got[0] != "c" || got[1] != "d" || got[2] != "e" {
		t.Fatalf("unexpected retained ids: %#v", got)
	}
}

func TestCollectorWritesSuccessSidecarLatestAndRuns(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "state", "deployments", "site", "runs.jsonl")
	sidecar := filepath.Join(dir, "docroot", ".wpcloud-site-git-deploy", "deployments", "site", "metadata", "r1.stats.json")
	latest := filepath.Join(dir, "state", "deployments", "site", "latest-run.json")

	collector := New(Options{
		ToolVersion:  "test",
		Name:         "site",
		DeploymentID: "site",
		RunsPath:     runs,
		SidecarPath:  sidecar,
		LatestPath:   latest,
	})
	collector.SetRelease("r1")
	collector.SetCommit("abc")
	stop := collector.Phase("fetch")
	stop()
	if err := collector.Finish("success", nil); err != nil {
		t.Fatalf("finish success: %v", err)
	}

	for _, path := range []string{runs, sidecar, latest} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected report file %s: %v", path, err)
		}
	}
	var record Record
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("invalid sidecar json: %v", err)
	}
	if record.Status != "success" || record.ReleaseID != "r1" || len(record.Phases) != 1 {
		t.Fatalf("unexpected sidecar record: %#v", record)
	}
}

func TestCollectorKeepsNoOpAndFailureOutOfDocrootSidecar(t *testing.T) {
	for _, status := range []string{"no_op", "failed"} {
		t.Run(status, func(t *testing.T) {
			dir := t.TempDir()
			runs := filepath.Join(dir, "state", "deployments", "site", "runs.jsonl")
			sidecar := filepath.Join(dir, "docroot", ".wpcloud-site-git-deploy", "deployments", "site", "metadata", "r1.stats.json")
			latest := filepath.Join(dir, "state", "deployments", "site", "latest-run.json")

			collector := New(Options{Name: "site", DeploymentID: "site", RunsPath: runs, SidecarPath: sidecar, LatestPath: latest})
			collector.SetRelease("r1")
			if status == "failed" {
				collector.SetFailedPhase("promote.validate_protected")
			}
			if err := collector.Finish(status, nil); err != nil {
				t.Fatalf("finish %s: %v", status, err)
			}
			if _, err := os.Stat(runs); err != nil {
				t.Fatalf("runs report missing: %v", err)
			}
			if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
				t.Fatalf("%s should not write sidecar, err=%v", status, err)
			}
			if _, err := os.Stat(latest); !os.IsNotExist(err) {
				t.Fatalf("%s should not write latest success, err=%v", status, err)
			}
		})
	}
}
