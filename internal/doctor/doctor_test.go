package doctor

import "testing"

func TestReportFailsWhenFailureRecorded(t *testing.T) {
	report := NewReport()
	report.OK("config", "loaded")
	report.Warn("git-lfs", "not installed")
	report.Fail("docroot", "not writable")
	if report.Success() {
		t.Fatal("report with failures should not succeed")
	}
	if len(report.Checks) != 3 {
		t.Fatalf("unexpected check count: %d", len(report.Checks))
	}
}

func TestReportSucceedsWithWarnings(t *testing.T) {
	report := NewReport()
	report.OK("config", "loaded")
	report.Warn("git-lfs", "root .gitattributes heuristic only")
	if !report.Success() {
		t.Fatal("warnings alone should not fail doctor")
	}
}
