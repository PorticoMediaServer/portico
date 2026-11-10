//go:build !windows

package streaming

import "syscall"

const hlsReadFlags = syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
