package corelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireAndRelease(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileIdentity)); err != nil {
		t.Fatalf("lock file: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	second, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire() after release error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestAcquireRejectsHeldLock(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer first.Close()
	second, err := Acquire(dir)
	if err == nil {
		_ = second.Close()
		t.Fatal("Acquire() succeeded while lock was held")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Acquire() error = %v, want ErrLocked", err)
	}
}
