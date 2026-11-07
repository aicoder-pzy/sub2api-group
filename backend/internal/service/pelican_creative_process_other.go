//go:build !linux

package service

import "os/exec"

func configureCreativeCommand(_ *exec.Cmd) {}
