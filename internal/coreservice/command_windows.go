//go:build windows

package coreservice

import (
	"os/exec"
	"syscall"
)

func configureHidden(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
