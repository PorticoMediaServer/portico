package networking

// PCP RFC 6887 and NAT-PMP RFC 6886. No THIRD_PARTY or all-ports deletion.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/netip"
	"time"
)

func pcpRequest(m Mapping, lifetime uint32) ([]byte, error) {
	client, e := netip.ParseAddr(m.Client)
	if e != nil {
		return nil, ErrInvalid
	}
	nonce, e := base64.RawURLEncoding.Strict().DecodeString(m.Nonce)
	if e != nil || len(nonce) != 12 {
		return nil, ErrInvalid
	}
	b := make([]byte, 60)
	b[0] = 2
	b[1] = 1
	binary.BigEndian.PutUint32(b[4:8], lifetime)
	ip := client.As16()
	copy(b[8:24], ip[:])
	copy(b[24:36], nonce)
	b[36] = 6
	binary.BigEndian.PutUint16(b[40:42], uint16(m.InternalPort))
	binary.BigEndian.PutUint16(b[42:44], uint16(m.ExternalPort))
	if m.ExternalAddress != "" {
		if address, e := netip.ParseAddr(m.ExternalAddress); e == nil {
			ip = address.As16()
			copy(b[44:60], ip[:])
		}
	}
	return b, nil
}
func parsePCPResponse(m Mapping, b []byte, lifetime uint32) (Mapping, error) {
	// RFC 6887 version negotiation with older NAT-PMP gateways.
	if len(b) == 8 && b[0] == 0 && b[1] == 129 && binary.BigEndian.Uint16(b[2:4]) == 1 {
		return m, errMappingUnsupported
	}
	if len(b) < 24 || b[0] != 2 || b[1] != 129 {
		return m, errMappingResponse
	}
	if b[3] != 0 {
		switch b[3] {
		case 1, 4, 5:
			return m, errMappingUnsupported
		case 2, 8:
			return m, errMappingDenied
		default:
			return m, errMappingResponse
		}
	}
	nonce, e := base64.RawURLEncoding.Strict().DecodeString(m.Nonce)
	if e != nil || len(nonce) != 12 || len(b) < 60 || !validPCPOptions(b[60:]) || !bytes.Equal(b[24:36], nonce) || b[36] != 6 || int(binary.BigEndian.Uint16(b[40:42])) != m.InternalPort {
		return m, errMappingResponse
	}
	granted := binary.BigEndian.Uint32(b[4:8])
	if (lifetime == 0) != (granted == 0) {
		return m, errMappingResponse
	}
	var ip [16]byte
	copy(ip[:], b[44:60])
	m.ExternalAddress = netip.AddrFrom16(ip).Unmap().String()
	m.ExternalPort = int(binary.BigEndian.Uint16(b[42:44]))
	if granted > 0 && m.ExternalPort == 0 {
		return m, errMappingResponse
	}
	return grantedMapping(m, granted, binary.BigEndian.Uint32(b[8:12]))
}
func pcpMap(ctx context.Context, m Mapping, lifetime uint32) (Mapping, error) {
	b, e := pcpRequest(m, lifetime)
	if e != nil {
		return m, e
	}
	retried := false
	reply, e := gatewayDatagram(ctx, m, b, 8, func(reply []byte) bool {
		if len(reply) == 8 {
			return reply[0] == 0 && reply[1] == 129 && binary.BigEndian.Uint16(reply[2:4]) == 1
		}
		if len(reply) < 24 || reply[0] != 2 || reply[1] != 129 {
			return false
		}
		return len(reply) == 24 && reply[3] != 0 || len(reply) >= 60 && bytes.Equal(reply[24:36], b[24:36]) && reply[36] == b[36] && bytes.Equal(reply[40:42], b[40:42])
	}, &retried)
	if e != nil {
		return m, e
	}
	result, err := parsePCPResponse(m, reply, lifetime)
	if retried && err != nil {
		return result, errMappingResponse // ambiguous earlier MAP; retain cleanup intent
	}
	return result, err
}
func parseNATPMPAddress(b []byte) (string, uint32, error) {
	if len(b) != 12 || b[0] != 0 || b[1] != 128 {
		return "", 0, errMappingResponse
	}
	if e := natpmpResult(binary.BigEndian.Uint16(b[2:4])); e != nil {
		return "", 0, e
	}
	var ip [4]byte
	copy(ip[:], b[8:12])
	return netip.AddrFrom4(ip).String(), binary.BigEndian.Uint32(b[4:8]), nil
}
func natpmpResult(code uint16) error {
	switch code {
	case 0:
		return nil
	case 1, 5:
		return errMappingUnsupported
	case 2:
		return errMappingDenied
	default:
		return errMappingResponse
	}
}
func natpmpRequest(m Mapping, lifetime uint32) []byte {
	b := make([]byte, 12)
	b[1] = 2
	binary.BigEndian.PutUint16(b[4:6], uint16(m.InternalPort))
	if lifetime != 0 {
		binary.BigEndian.PutUint16(b[6:8], uint16(m.ExternalPort))
	}
	binary.BigEndian.PutUint32(b[8:12], lifetime)
	return b
}
func parseNATPMPMapping(m Mapping, b []byte, lifetime uint32) (Mapping, error) {
	if len(b) != 16 || b[0] != 0 || b[1] != 130 || int(binary.BigEndian.Uint16(b[8:10])) != m.InternalPort {
		return m, errMappingResponse
	}
	if e := natpmpResult(binary.BigEndian.Uint16(b[2:4])); e != nil {
		return m, e
	}
	granted := binary.BigEndian.Uint32(b[12:16])
	port := int(binary.BigEndian.Uint16(b[10:12]))
	if (lifetime == 0) != (granted == 0) || granted > 0 && port == 0 {
		return m, errMappingResponse
	}
	m.ExternalPort = port
	return grantedMapping(m, granted, binary.BigEndian.Uint32(b[4:8]))
}
func natpmpMap(ctx context.Context, m Mapping, lifetime uint32) (Mapping, error) {
	retried := false
	reply, e := gatewayDatagram(ctx, m, natpmpRequest(m, lifetime), 16, func(reply []byte) bool {
		return len(reply) == 16 && reply[0] == 0 && reply[1] == 130 && int(binary.BigEndian.Uint16(reply[8:10])) == m.InternalPort
	}, &retried)
	if e != nil {
		return m, e
	}
	result, err := parseNATPMPMapping(m, reply, lifetime)
	if retried && err != nil {
		return result, errMappingResponse // a later denial cannot erase an earlier lost grant
	}
	return result, err
}

// We request no options. Ignore well-formed optional response options only;
// mandatory unknown options or truncated padding cannot be a successful MAP.
func validPCPOptions(b []byte) bool {
	for len(b) > 0 {
		if len(b) < 4 || b[0] < 128 {
			return false
		}
		n := 4 + ((int(binary.BigEndian.Uint16(b[2:4])) + 3) &^ 3)
		if n > len(b) {
			return false
		}
		b = b[n:]
	}
	return true
}

// A non-mutating ANNOUNCE establishes protocol support before the first MAP.
// Silent UPnP-only routers can fall through without leaving a speculative MAP
// receipt. Once a MAP is uncertain we retry its same nonce, never another owner.
func pcpPrepare(ctx context.Context, m Mapping) (Mapping, error) {
	if !m.EpochObservedAt.IsZero() || m.MutationPending {
		return m, nil
	}
	client, e := netip.ParseAddr(m.Client)
	if e != nil {
		return m, ErrInvalid
	}
	request := make([]byte, 24)
	request[0] = 2
	ip := client.As16()
	copy(request[8:24], ip[:])
	reply, e := gatewayDatagram(ctx, m, request, 8, func(b []byte) bool {
		return len(b) == 8 && b[0] == 0 && b[1] == 128 && binary.BigEndian.Uint16(b[2:4]) == 1 || len(b) >= 24 && b[0] == 2 && b[1] == 128
	})
	if e != nil {
		return m, e
	}
	if len(reply) == 8 {
		return m, errMappingUnsupported
	}
	if reply[3] != 0 || !validPCPOptions(reply[24:]) {
		return m, errMappingUnsupported
	}
	m.Epoch = binary.BigEndian.Uint32(reply[8:12])
	m.EpochObservedAt = time.Now()
	return m, nil
}
