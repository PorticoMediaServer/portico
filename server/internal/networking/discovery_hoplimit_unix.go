//go:build linux || freebsd || openbsd || netbsd

package networking

import "syscall"

const discoveryReceiveHopLimit = syscall.IPV6_RECVHOPLIMIT
const discoveryHopLimit = syscall.IPV6_HOPLIMIT
