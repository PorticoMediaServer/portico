package operations

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// validateAdministration checks the connectivity, access and diagnostics rows
// workstream G adds to the settings registry. It lives beside the registry it
// validates but in its own file, so the two areas do not share a diff.
//
// Enumerations and networks reject rather than clamp: substituting a security
// policy or a LAN range nobody chose would apply a policy nobody chose. Only the
// numeric bitrate ceiling clamps, like the other numeric controls.
func validateAdministration(v *Settings) []string {
	fields := []string{}
	if v.RemoteSignInPolicy == "" {
		v.RemoteSignInPolicy = "allow"
	}
	if !oneOf(v.RemoteSignInPolicy, RemoteSignInPolicies) {
		fields = append(fields, "values.remoteSignInPolicy")
	}
	if v.ConnectionLimit < 0 || v.ConnectionLimit > 65536 {
		fields = append(fields, "values.connectionLimit")
	}
	if v.AdvertiseURL != "" {
		normalized, ok := AccessOrigin(v.AdvertiseURL)
		if !ok {
			fields = append(fields, "values.advertiseUrl")
		} else {
			v.AdvertiseURL = normalized
		}
	}
	if v.LogDetailMinutes == 0 {
		v.LogDetailMinutes = 30
	}
	if !validLogDetailMinutes(v.LogDetailMinutes) {
		fields = append(fields, "values.logDetailMinutes")
	}
	if !validPeopleInviteRole(v.PeopleInvitationRole) && v.PeopleInvitationRole != "" {
		fields = append(fields, "values.people.invitation_role")
	}
	if !validPeopleInvitationDays(v.PeopleInvitationDays) && v.PeopleInvitationDays != 0 {
		fields = append(fields, "values.people.invitation_days")
	}
	if v.LogLevel == "" {
		v.LogLevel = "info"
	}
	if !oneOf(v.LogLevel, LogLevels) {
		fields = append(fields, "values.logLevel")
	}
	if v.SecureConnections == "" {
		v.SecureConnections = "preferred"
	}
	if v.SecureConnections != "preferred" && v.SecureConnections != "required" {
		fields = append(fields, "values.secureConnections")
	}
	if v.LANNetworks == nil {
		v.LANNetworks = []string{}
	}
	if len(v.LANNetworks) > 64 {
		fields = append(fields, "values.lanNetworks")
	}
	for i, raw := range v.LANNetworks {
		prefix, ok := ParseNetwork(raw)
		if !ok {
			fields = append(fields, "values.lanNetworks")
			break
		}
		v.LANNetworks[i] = prefix.String()
	}
	if v.AccessURLs == nil {
		v.AccessURLs = []string{}
	}
	// Owner-entered HTTP/HTTPS origins are published as entered; HTTP is
	// explicitly marked insecure for clients to apply their own policy.
	if len(v.AccessURLs) > MaxAccessURLs {
		fields = append(fields, "values.accessUrls")
	}
	for i, raw := range v.AccessURLs {
		normalized, ok := AccessOrigin(raw)
		if !ok {
			fields = append(fields, "values.accessUrls")
			break
		}
		v.AccessURLs[i] = normalized
	}
	if v.TrustedProxies == nil {
		v.TrustedProxies = []string{}
	}
	if len(v.TrustedProxies) > 32 {
		fields = append(fields, "values.trustedProxies")
	}
	for i, raw := range v.TrustedProxies {
		prefix, ok := ParseNetwork(raw)
		if !ok {
			fields = append(fields, "values.trustedProxies")
			break
		}
		v.TrustedProxies[i] = prefix.String()
	}
	if v.UploadCapacityKbps < 0 || v.UploadCapacityKbps > 100000000 {
		fields = append(fields, "values.uploadCapacityKbps")
	}
	if v.PausedSessionTimeoutMinutes < 0 || v.PausedSessionTimeoutMinutes > 1440 {
		fields = append(fields, "values.pausedSessionTimeoutMinutes")
	}
	if v.AdvertisedInterface != "" && !interfaceName(v.AdvertisedInterface) {
		fields = append(fields, "values.advertisedInterface")
	}
	// A custom certificate is all three values or none: a domain without a
	// certificate, or a certificate nobody names, would publish a route that
	// cannot complete a handshake.
	custom := []string{v.CustomCertificatePath, v.CustomCertificateKeyPath, v.CustomCertificateDomain}
	set := 0
	for _, value := range custom {
		if value != "" {
			set++
		}
	}
	if v.OwnAddressEnabled && set != len(custom) {
		fields = append(fields, "values.customCertificatePath")
	}
	for _, path := range custom[:2] {
		if path != "" && (!strings.HasPrefix(path, "/") && !windowsAbsolute(path) || len(path) > 1024 || !SafeText(path, 1024) || strings.ContainsAny(path, "\n\t")) {
			fields = append(fields, "values.customCertificatePath")
			break
		}
	}
	if v.CustomCertificateDomain != "" {
		v.CustomCertificateDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(v.CustomCertificateDomain), "."))
		if !hostName(v.CustomCertificateDomain) {
			fields = append(fields, "values.customCertificateDomain")
		}
	}
	if v.LogRetention == nil {
		v.LogRetention = []LogRetention{}
	}
	if len(v.LogRetention) > len(LogCategories) {
		fields = append(fields, "values.logRetention")
	}
	seen := map[string]bool{}
	for _, entry := range v.LogRetention {
		if !oneOf(entry.Category, LogCategories) || seen[entry.Category] || entry.Days < 1 || entry.Days > 90 {
			fields = append(fields, "values.logRetention")
			break
		}
		seen[entry.Category] = true
	}
	return fields
}

// LogRetentionDays projects the retention rows as a map for servicelog.
func (s Settings) LogRetentionDays() map[string]int {
	out := map[string]int{}
	for _, category := range LogCategories {
		out[category] = s.DiagnosticDays
	}
	for _, entry := range s.LogRetention {
		out[entry.Category] = entry.Days
	}
	return out
}

// MaxAccessURLs bounds the published access URLs so they fit, with the LAN and
// public routes, in the twelve candidates Hosted carries.
const MaxAccessURLs = 8

// ParseNetwork reads a CIDR network or a bare address (a one-address network),
// masked, so 192.168.1.5/24 cannot read back as a host pretending to be a range.
func ParseNetwork(raw string) (netip.Prefix, bool) {
	raw = strings.TrimSpace(raw)
	if prefix, e := netip.ParsePrefix(raw); e == nil && prefix.Addr().Zone() == "" {
		return netip.PrefixFrom(prefix.Addr().Unmap(), unmappedBits(prefix)).Masked(), true
	}
	addr, e := netip.ParseAddr(raw)
	if e != nil || addr.Zone() != "" {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), true
}

func unmappedBits(prefix netip.Prefix) int {
	if prefix.Addr().Is4In6() {
		if bits := prefix.Bits() - 96; bits >= 0 {
			return bits
		}
		return 0
	}
	return prefix.Bits()
}

func interfaceName(name string) bool {
	if len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-@ ", r)) {
			return false
		}
	}
	return true
}

func windowsAbsolute(path string) bool {
	return len(path) > 2 && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func hostName(name string) bool {
	if len(name) == 0 || len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// AccessOrigin validates owner-supplied HTTP or HTTPS origins without paths.
func AccessOrigin(raw string) (string, bool) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || len(trimmed) > 300 || !SafeText(trimmed, 300) || strings.ContainsAny(trimmed, "\\\r\n\t ") {
		return "", false
	}
	if u.Hostname() == "" {
		return "", false
	}
	if u.Port() != "" {
		n, err := strconv.Atoi(u.Port())
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
	}
	return strings.ToLower(trimmed), true
}
