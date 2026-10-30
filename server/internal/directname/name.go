package directname

import (
	"encoding/base32"
	"errors"
	"net/netip"
	"strconv"
	"strings"
)

// EncodeIP uses one canonical DNS label. IPv6 compression at a label edge is
// expanded to eight hextets so no label starts or ends in a dash.
func EncodeIP(ip netip.Addr) string {
	if validateAnswerAddress(ip) != nil {
		return ""
	}
	raw := ip.String()
	if ip.Is4() {
		return strings.ReplaceAll(raw, ".", "-")
	}
	if strings.HasPrefix(raw, "::") || strings.HasSuffix(raw, "::") {
		bytes := ip.As16()
		words := make([]string, 8)
		for n := range words {
			words[n] = strconv.FormatUint(uint64(bytes[n*2])<<8|uint64(bytes[n*2+1]), 16)
		}
		return strings.Join(words, "-")
	}
	return strings.ReplaceAll(raw, ":", "-")
}
func DecodeIP(label string) (netip.Addr, error) {
	var ip netip.Addr
	var err error
	if strings.Count(label, "-") == 3 && !strings.ContainsAny(label, "abcdef") {
		ip, err = netip.ParseAddr(strings.ReplaceAll(label, "-", "."))
	}
	if !ip.IsValid() {
		ip, err = netip.ParseAddr(strings.ReplaceAll(label, "-", ":"))
	}
	if err != nil || !ip.IsValid() || EncodeIP(ip) != label {
		return netip.Addr{}, ErrInvalidEndpoint
	}
	return ip, nil
}
func IPHostname(ip netip.Addr, namespace, zone string) string {
	label := EncodeIP(ip)
	if label == "" || !ValidNamespace(namespace) || !ValidZone(zone) {
		return ""
	}
	return label + "." + namespace + "." + normalizeZone(zone)
}
func ParseIPHostname(host, zone string) (netip.Addr, string, error) {
	zone = normalizeZone(zone)
	host = normalizeZone(host)
	rest, ok := strings.CutSuffix(host, "."+zone)
	if !ok || !ValidZone(zone) {
		return netip.Addr{}, "", ErrInvalidEndpoint
	}
	label, namespace, ok := strings.Cut(rest, ".")
	if !ok || !ValidNamespace(namespace) {
		return netip.Addr{}, "", ErrInvalidEndpoint
	}
	ip, err := DecodeIP(label)
	return ip, namespace, err
}
func ValidZone(zone string) bool {
	if zone != normalizeZone(zone) || len(zone) > 200 || !strings.Contains(zone, ".") {
		return false
	}
	for _, label := range strings.Split(zone, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

var ErrInvalidEndpoint = errors.New("invalid direct name")

func normalizeZone(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}
func ValidNamespace(ns string) bool {
	if len(ns) != 24 || !strings.HasPrefix(ns, "ptc-") {
		return false
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(ns[4:]))
	return err == nil && len(raw) == 12 && "ptc-"+strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)) == ns
}
func validateAnswerAddress(ip netip.Addr) error {
	if !ip.IsValid() || ip.Is4In6() || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip == netip.MustParseAddr("255.255.255.255") {
		return ErrInvalidEndpoint
	}
	return nil
}
