//go:build darwin

package networking

// Darwin's frozen syscall package omits the RFC 3542 names. These are the
// native netinet6/in6.h values (also exported by x/sys/unix), NOT RFC 2292's
// IPV6_2292HOPLIMIT. Use the matching receive option and ancillary message type.
const discoveryReceiveHopLimit = 0x25
const discoveryHopLimit = 0x2f
