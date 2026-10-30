package httpapi

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
)

// ParseSetupHosts reads exact authorities, not URLs, patterns or DNS answers.
// Invalid operator configuration is rejected at startup instead of silently
// denying the hostname the owner intended to use.
func ParseSetupHosts(raw string) ([]string, error) {
	var out []string
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		host, ok := canonicalSetupHost(strings.TrimSpace(entry))
		if !ok || len(out) >= 64 {
			return nil, errors.New("PORTICO_SETUP_HOSTS requires at most 64 comma-separated hostnames with optional ports, without schemes or paths")
		}
		out = append(out, host)
	}
	return out, nil
}

func canonicalSetupHost(raw string) (string, bool) {
	canonical, ok := networkpolicy.CanonicalHost(raw)
	if !ok || strings.HasSuffix(canonical, ":") {
		return "", false
	}
	u, err := url.Parse("http://" + canonical)
	if err != nil {
		return "", false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || strings.Contains(host, "*") {
		return "", false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
		return net.JoinHostPort(host, port), true
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]", true
	}
	return host, true
}

func setupHostsMatch(a, b string) bool {
	a, okA := canonicalSetupHost(a)
	b, okB := canonicalSetupHost(b)
	return okA && okB && networkpolicy.HostsMatch(a, b)
}

func setupMachineHostMatches(host, machine string) bool {
	machine = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(machine), "."), ".local")
	return machine != "" && (host == machine || host == machine+".local")
}
