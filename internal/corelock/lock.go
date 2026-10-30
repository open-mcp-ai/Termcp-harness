// Package corelock provides a process-scoped advisory lock for the data
// directory used by a Harness-managed Core process.
package corelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const fileIdentity = ".termcp-harness-core.lock"

var ErrLocked = errors.New("Core data directory is already locked")

type Lock struct {
	file    *os.File
	release func() error
}

// Acquire takes an OS-backed exclusive lock without waiting. The lock file is
// intentionally kept on disk after release so a later process cannot race a
// different file inode at the same path.
func Acquire(dataDir string) (*Lock, error) {
	if dataDir == "" {
		return nil, errors.New("Core data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create Core data directory: %w", err)
	}
	path := filepath.Join(dataDir, fileIdentity)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Core data directory lock: %w", err)
	}
	release, err := tryLock(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	_ = file.Truncate(0)
	_, _ = file.Seek(0, 0)
	_, _ = fmt.Fprintf(file, "pid=%d\n", os.Getpid())
	_ = file.Sync()
	return &Lock{file: file, release: release}, nil
}

func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	var closeErr error
	if l.release != nil {
		closeErr = l.release()
		l.release = nil
	}
	if l.file != nil {
		closeErr = errors.Join(closeErr, l.file.Close())
		l.file = nil
	}
	return closeErr
}
