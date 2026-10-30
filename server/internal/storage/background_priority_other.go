//go:build !darwin && !linux && !windows

package storage

import "os/exec"

func lowerBackgroundPriority(cmd *exec.Cmd) {}
func applyBackgroundPriority(cmd *exec.Cmd) {}
