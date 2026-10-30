//go:build linux

package storage

import "syscall"

// dupTo places fd at target. Linux arm64 has no dup2 system call, so every
// Linux build goes through dup3 (identical semantics with flags=0).
func dupTo(fd, target int) error { return syscall.Dup3(fd, target, 0) }
