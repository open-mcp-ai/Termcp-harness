package atomicfile

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var errTransientRename = errors.New("transient rename failure")

func TestReplaceWithRetriesTransientFailuresBeforeSuccess(t *testing.T) {
	calls := 0
	attempts, err := ReplaceWith(
		"temp",
		"final",
		func(string, string) error {
			calls++
			if calls < 3 {
				return errTransientRename
			}
			return nil
		},
		func(err error) bool { return errors.Is(err, errTransientRename) },
		[]time.Duration{0, 0},
	)
	if err != nil {
		t.Fatalf("ReplaceWith() error = %v", err)
	}
	if attempts != 3 || calls != 3 {
		t.Fatalf("attempts = %d, calls = %d, want 3", attempts, calls)
	}
}

func TestReplaceWithReturnsBoundedFailure(t *testing.T) {
	calls := 0
	attempts, err := ReplaceWith(
		"temp",
		"final",
		func(string, string) error {
			calls++
			return errTransientRename
		},
		func(err error) bool { return errors.Is(err, errTransientRename) },
		[]time.Duration{0, 0, 0, 0},
	)
	if !errors.Is(err, errTransientRename) {
		t.Fatalf("ReplaceWith() error = %v", err)
	}
	if attempts != 5 || calls != 5 {
		t.Fatalf("attempts = %d, calls = %d, want 5", attempts, calls)
	}
	if !strings.Contains(err.Error(), "after 5 attempts") {
		t.Fatalf("error lacks bounded-attempt receipt: %v", err)
	}
}

func TestReplaceWithDoesNotRetryUnknownErrors(t *testing.T) {
	calls := 0
	permanent := errors.New("permanent failure")
	attempts, err := ReplaceWith(
		"temp",
		"final",
		func(string, string) error {
			calls++
			return permanent
		},
		func(error) bool { return false },
		[]time.Duration{0, 0, 0},
	)
	if !errors.Is(err, permanent) {
		t.Fatalf("ReplaceWith() error = %v", err)
	}
	if attempts != 1 || calls != 1 {
		t.Fatalf("attempts = %d, calls = %d, want 1", attempts, calls)
	}
}
