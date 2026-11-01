// Package atomicfile provides bounded retries for atomic file replacement.
// Retries only handle known transient rename failures; callers still receive
// an error when every attempt fails.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"time"
)

var defaultRetryDelays = []time.Duration{
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
}

// Replace atomically renames tempPath to path. On Windows, bounded retries
// absorb transient sharing/lock failures commonly caused by file scanners.
func Replace(tempPath, path string) error {
	_, err := ReplaceWith(tempPath, path, os.Rename, isTransientRenameError, defaultRetryDelays)
	return err
}

// ReplaceWith exposes the retry policy for deterministic tests. retryDelays
// describes waits after failed attempts and therefore defines at most
// len(retryDelays)+1 rename attempts.
func ReplaceWith(
	tempPath string,
	path string,
	rename func(string, string) error,
	retryable func(error) bool,
	retryDelays []time.Duration,
) (int, error) {
	if rename == nil {
		return 0, errors.New("atomic rename function is required")
	}
	if retryable == nil {
		retryable = func(error) bool { return false }
	}
	attempts := 0
	for index := 0; ; index++ {
		attempts++
		err := rename(tempPath, path)
		if err == nil {
			return attempts, nil
		}
		if !retryable(err) || index >= len(retryDelays) {
			if attempts > 1 {
				return attempts, fmt.Errorf("%w (after %d attempts)", err, attempts)
			}
			return attempts, err
		}
		delay := retryDelays[index]
		if delay > 0 {
			time.Sleep(delay)
		}
	}
}
