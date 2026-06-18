package publicfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExchangePaths(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Exchange(a, b); err != nil {
		if errors.Is(err, ErrExchangeUnsupported) {
			t.Skip("renameat2 exchange is not supported on this platform")
		}
		t.Fatalf("exchange failed: %v", err)
	}
	aContent, _ := os.ReadFile(a)
	bContent, _ := os.ReadFile(b)
	if string(aContent) != "b" || string(bContent) != "a" {
		t.Fatalf("paths were not exchanged: a=%q b=%q", aContent, bContent)
	}
}
