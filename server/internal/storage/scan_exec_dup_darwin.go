//go:build darwin

package storage

import "syscall"

func dupTo(fd, target int) error { return syscall.Dup2(fd, target) }
