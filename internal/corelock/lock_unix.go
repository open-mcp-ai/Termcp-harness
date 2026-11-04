//go:build !windows

package corelock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLock(file *os.File) (func() error, error) {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() error {
		return unix.Flock(int(file.Fd()), unix.LOCK_UN)
	}, nil
}
