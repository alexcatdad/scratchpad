//go:build !darwin && !linux

package main

import (
	"errors"
	"os/exec"
)

func detachCollector(command *exec.Cmd) error {
	return errors.New("background collector startup is supported only on macOS and Linux")
}
