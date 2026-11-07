//go:build !linux

package service

import "os/exec"

func configureCreativeCommand(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Kill() }
}
