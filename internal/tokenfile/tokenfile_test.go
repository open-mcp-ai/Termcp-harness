package tokenfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	const token = "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Fatalf("token = %q, want %q", got, token)
	}
}

func TestReadRejectsEmptyOversizedAndMissing(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(dir, "oversized")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("x", maxBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{empty, oversized, filepath.Join(dir, "missing")} {
		if _, err := Read(path); err == nil {
			t.Fatalf("Read(%q) error = nil", path)
		}
	}
}
