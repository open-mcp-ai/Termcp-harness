//go:build !windows

package atomicfile

func isTransientRenameError(error) bool {
	return false
}
