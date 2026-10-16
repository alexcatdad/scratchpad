//go:build darwin || linux

package main

import (
	"os/exec"
	"syscall"
)

func detachCollector(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}
