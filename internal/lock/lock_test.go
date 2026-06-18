package lock

import (
	"path/filepath"
	"testing"
)

func TestAcquireRejectsSecondLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first lock failed: %v", err)
	}
	defer first.Close()

	second, err := Acquire(path)
	if err == nil {
		second.Close()
		t.Fatal("second lock should fail")
	}
}
