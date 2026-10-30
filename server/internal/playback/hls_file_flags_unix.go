//go:build !windows

package playback

import "syscall"

const hlsReadFlags = syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
