//go:build !windows

package coreprocess

import "os/exec"

func configureCommand(command *exec.Cmd) {}
