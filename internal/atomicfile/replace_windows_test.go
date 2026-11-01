//go:build windows

package atomicfile

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsTransientRenameErrorsAreAllowlisted(t *testing.T) {
	for _, code := range []windows.Errno{
		windows.ERROR_ACCESS_DENIED,
		windows.ERROR_SHARING_VIOLATION,
		windows.ERROR_LOCK_VIOLATION,
	} {
		if !isTransientRenameError(errors.Join(errors.New("rename"), code)) {
			t.Fatalf("Windows error %d was not classified as transient", code)
		}
	}
	if isTransientRenameError(windows.ERROR_FILE_NOT_FOUND) {
		t.Fatal("file-not-found was classified as transient")
	}
}
