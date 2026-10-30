//go:build linux || darwin || freebsd || openbsd || netbsd

package networking

import (
	"encoding/binary"
	"syscall"
	"testing"
	"unsafe"
)

func reviewControl(level, kind int, data []byte) []byte {
	b := make([]byte, syscall.CmsgSpace(len(data)))
	header := (*syscall.Cmsghdr)(unsafe.Pointer(&b[0]))
	header.Level = int32(level)
	header.Type = int32(kind)
	header.SetLen(syscall.CmsgLen(len(data)))
	copy(b[syscall.CmsgLen(0):], data)
	return b
}
func TestW2I02DiscoveryAncillaryHopLimitRemainsMandatory(t *testing.T) {
	ttl := make([]byte, 4)
	binary.NativeEndian.PutUint32(ttl, 255)
	for _, kind := range []struct{ level, kind int }{{syscall.IPPROTO_IPV6, discoveryHopLimit}, {syscall.IPPROTO_IP, syscall.IP_RECVTTL}, {syscall.IPPROTO_IP, syscall.IP_TTL}} {
		if !discoveryOnLink(reviewControl(kind.level, kind.kind, ttl)) {
			t.Fatal("on-link rejected", kind)
		}
		if discoveryOnLink(reviewControl(kind.level, kind.kind, []byte{254})) {
			t.Fatal("off-link accepted")
		}
		if !discoveryOnLink(reviewControl(kind.level, kind.kind, []byte{255})) {
			t.Fatal("one-byte TTL rejected")
		}
	}
	if discoveryOnLink(nil) || discoveryOnLink([]byte{1, 2, 3}) || discoveryOnLink(reviewControl(1234, 1234, ttl)) {
		t.Fatal("missing/unrelated control message accepted")
	}
	if !discoveryTruncated(syscall.MSG_CTRUNC) || !discoveryTruncated(syscall.MSG_TRUNC) || discoveryTruncated(0) {
		t.Fatal("truncation checks changed")
	}
}

func TestDiscoveryInterfaceNamesDoNotConflict(t *testing.T) {
	if discoveryInterfaceSuffix("eth0") == discoveryInterfaceSuffix("eth1") {
		t.Fatal("interfaces share unique DNS names")
	}
	if discoveryInterfaceSuffix("eth0") != discoveryInterfaceSuffix("eth0") {
		t.Fatal("unstable discovery name")
	}
}
