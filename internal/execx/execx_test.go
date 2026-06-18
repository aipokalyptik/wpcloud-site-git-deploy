package execx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRequireCommandsReportsMissing(t *testing.T) {
	err := RequireCommands(context.Background(), []string{"definitely-not-a-real-command-wpcloud"})
	if err == nil {
		t.Fatal("expected missing command to fail")
	}
	if !strings.Contains(err.Error(), "required command not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCapturesOutputAndEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "print-env")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$WPCLOUD_TEST_VALUE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Command{
		Name: script,
		Env:  []string{"WPCLOUD_TEST_VALUE=ok"},
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Stdout != "ok" {
		t.Fatalf("unexpected stdout: %q", result.Stdout)
	}
}
