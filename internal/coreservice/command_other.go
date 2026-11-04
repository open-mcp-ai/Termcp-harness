//go:build !windows

package coreservice

import "os/exec"

func configureHidden(*exec.Cmd) {}
