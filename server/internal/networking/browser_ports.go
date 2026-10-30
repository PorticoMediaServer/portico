package networking

import (
	"fmt"
	"net/url"
	"strconv"
)

// BrowserRestrictedPort is the Chromium/Firefox restricted-port union.
// Sources checked 2026-10-28:
// https://raw.githubusercontent.com/chromium/chromium/main/net/base/port_util.cc
// https://raw.githubusercontent.com/mozilla-firefox/firefox/main/netwerk/base/nsIOService.cpp
func BrowserRestrictedPort(port int) bool {
	switch port {
	case 0, 1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95, 101, 102, 103, 104, 109, 110, 111, 113, 115, 117, 119, 123, 135, 137, 139, 143, 161, 179, 389, 427, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601, 636, 989, 990, 993, 995, 1719, 1720, 1723, 2049, 3659, 4045, 4190, 5060, 5061, 6000, 6566, 6665, 6666, 6667, 6668, 6669, 6679, 6697, 10080:
		return true
	}
	return false
}
func automaticMappingPort(preferred int) int {
	if BrowserRestrictedPort(preferred) {
		return 32500
	}
	return preferred
}

type RemoteWarning struct {
	Code    string `json:"code"`
	Port    int    `json:"port"`
	BaseURL string `json:"baseUrl,omitempty"`
	// Message is an English fallback; clients localize Code and Port.
	Message string `json:"message"`
}

func remoteWarnings(publicPort int, addresses []string) []RemoteWarning {
	out := []RemoteWarning{}
	add := func(port int, origin string) {
		if BrowserRestrictedPort(port) {
			out = append(out, RemoteWarning{Code: "browser_restricted_port", Port: port, BaseURL: origin, Message: fmt.Sprintf("Web browsers can't use port %d. The apps still can.", port)})
		}
	}
	add(publicPort, "")
	for _, origin := range addresses {
		u, err := url.Parse(origin)
		if err != nil {
			continue
		}
		port := 443
		if u.Scheme == "http" {
			port = 80
		}
		if u.Port() != "" {
			port, err = strconv.Atoi(u.Port())
			if err != nil {
				continue
			}
		}
		add(port, origin)
	}
	return out
}
