package portmap

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The three ways a home router can be asked to open a port. Each is short and bounded: a
// router that does not answer costs at most a few seconds, and nothing it says can make the
// server reach anything but the router itself.

// mapping is what a router agreed to.
type mapping struct {
	router       Router
	pinhole      string
	protocol     string
	externalPort int
	externalIP   netip.Addr
	lifetime     time.Duration
	// internal and asked are the ports this server asked for, to tell when the owner changed them.
	internal, asked int
	// nonce is the PCP mapping's own: its renewals and its removal must carry it (RFC 6887
	// section 11.2), or the router refuses them.
	nonce []byte
}

// localAddrTowards is this machine's address on the way to the router.
func localAddrTowards(gateway netip.Addr) (netip.Addr, error) {
	conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(gateway, 9)))
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap(), nil
}

// errUnsupported: the router does not speak this protocol. The next one is tried.
var errUnsupported = errors.New("the router does not speak this protocol")

// udpAsk sends req to the router and waits for a reply of at least min bytes that ok accepts,
// sending again after 250 ms, 500 ms and 1 s, as NAT-PMP and PCP ask.
func udpAsk(ctx context.Context, router netip.AddrPort, req []byte, min int, ok func([]byte) bool, source ...netip.Addr) ([]byte, netip.Addr, error) {
	conn, err := net.DialUDP("udp", udpSource(source), net.UDPAddrFromAddrPort(router))
	if err != nil {
		return nil, netip.Addr{}, err
	}
	defer conn.Close()
	local := conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
	buf := make([]byte, 1100)
	for wait := 250 * time.Millisecond; wait <= time.Second; wait *= 2 {
		if _, err := conn.Write(req); err != nil {
			return nil, local, errUnsupported
		}
		deadline := time.Now().Add(wait)
		if d, has := ctx.Deadline(); has && d.Before(deadline) {
			deadline = d
		}
		conn.SetReadDeadline(deadline)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break // a timeout, or the port is closed: send again
			}
			if n >= min && ok(buf[:n]) {
				return buf[:n], local, nil
			}
		}
		if ctx.Err() != nil {
			return nil, local, ctx.Err()
		}
	}
	return nil, local, errUnsupported
}

func udpSource(source []netip.Addr) *net.UDPAddr {
	if len(source) == 1 && source[0].IsValid() {
		return net.UDPAddrFromAddrPort(netip.AddrPortFrom(source[0], 0))
	}
	return nil
}

// natPMP asks with NAT-PMP (RFC 6886). lifetime 0 removes the mapping.
func natPMP(ctx context.Context, router netip.AddrPort, internal, external int, lifetime time.Duration) (mapping, error) {
	addr, _, err := udpAsk(ctx, router, []byte{0, 0}, 12, func(b []byte) bool { return b[0] == 0 && b[1] == 128 })
	if err != nil {
		return mapping{}, err
	}
	if code := binary.BigEndian.Uint16(addr[2:]); code != 0 {
		return mapping{}, fmt.Errorf("the router refused NAT-PMP (result %d)", code)
	}
	req := make([]byte, 12)
	req[1] = 2 // map TCP
	binary.BigEndian.PutUint16(req[4:], uint16(internal))
	binary.BigEndian.PutUint16(req[6:], uint16(external))
	binary.BigEndian.PutUint32(req[8:], uint32(lifetime/time.Second))
	resp, _, err := udpAsk(ctx, router, req, 16, func(b []byte) bool {
		return b[0] == 0 && b[1] == 130 && int(binary.BigEndian.Uint16(b[8:])) == internal
	})
	if err != nil {
		return mapping{}, err
	}
	if code := binary.BigEndian.Uint16(resp[2:]); code != 0 {
		return mapping{}, fmt.Errorf("the router refused the port (NAT-PMP result %d)", code)
	}
	return mapping{protocol: "NAT-PMP", externalPort: int(binary.BigEndian.Uint16(resp[10:])),
		externalIP: netip.AddrFrom4([4]byte(addr[8:12])), lifetime: time.Duration(binary.BigEndian.Uint32(resp[12:])) * time.Second}, nil
}

// pcp asks with PCP (RFC 6887), MAP for TCP. lifetime 0 removes the mapping. nonce is the held
// mapping's, for a renewal or a removal; nil asks for a new mapping under a new nonce. A router
// that answers in NAT-PMP's version says it does not speak PCP.
func pcp(ctx context.Context, router netip.AddrPort, internal, external int, lifetime time.Duration, held []byte, source ...netip.Addr) (mapping, error) {
	// The request names the address the router sees this server at, which is only known once
	// a socket is open towards it.
	probe, err := net.DialUDP("udp", udpSource(source), net.UDPAddrFromAddrPort(router))
	if err != nil {
		return mapping{}, err
	}
	local := probe.LocalAddr().(*net.UDPAddr).AddrPort().Addr()
	probe.Close()
	req := make([]byte, 60)
	req[0], req[1] = 2, 1
	binary.BigEndian.PutUint32(req[4:], uint32(lifetime/time.Second))
	client := netip.AddrFrom16(local.As16())
	copy(req[8:24], client.AsSlice())
	nonce := req[24:36]
	if len(held) == len(nonce) {
		copy(nonce, held)
	} else {
		rand.Read(nonce)
	}
	req[36] = 6 // TCP
	binary.BigEndian.PutUint16(req[40:], uint16(internal))
	binary.BigEndian.PutUint16(req[42:], uint16(external))
	if local.Is4() || local.Is4In6() {
		copy(req[44:60], netip.AddrFrom16(netip.IPv4Unspecified().As16()).AsSlice())
	}
	var unsupported bool
	resp, _, err := udpAsk(ctx, router, req, 4, func(b []byte) bool {
		if b[0] == 0 { // a NAT-PMP router's "unsupported version"
			unsupported = true
			return true
		}
		return len(b) >= 60 && b[0] == 2 && b[1] == 0x81 && bytes.Equal(b[24:36], nonce)
	}, source...)
	if err != nil {
		return mapping{}, err
	}
	if unsupported || resp[3] == 1 {
		return mapping{}, errUnsupported
	}
	if resp[3] != 0 {
		return mapping{}, fmt.Errorf("the router refused the port (PCP result %d)", resp[3])
	}
	ip, _ := netip.AddrFromSlice(resp[44:60])
	return mapping{protocol: "PCP", externalPort: int(binary.BigEndian.Uint16(resp[42:])), externalIP: ip.Unmap(),
		lifetime: time.Duration(binary.BigEndian.Uint32(resp[4:])) * time.Second, nonce: append([]byte(nil), nonce...)}, nil
}

// maxDescription bounds the router's description document and every SOAP answer.
const maxDescription = 64 << 10

// upnpControl finds the router's port-mapping service with SSDP and returns its control
// address and service type. Only an answer from the router's own address is believed, and
// only addresses on that router are followed, so a device on the network cannot point the
// server anywhere else.
func upnpControl(ctx context.Context, r Router, wanted string) (string, string, error) {
	var source []netip.Addr
	if r.Client.IsValid() {
		source = []netip.Addr{r.Client}
	}
	conn, err := net.ListenUDP("udp", udpSource(source))
	if err != nil {
		return "", "", err
	}
	defer conn.Close()
	target := wanted
	if target == "" {
		target = "urn:schemas-upnp-org:device:InternetGatewayDevice:1"
	}
	ssdpHost := netip.AddrPortFrom(r.SSDP.Addr().WithZone(""), r.SSDP.Port())
	search := "M-SEARCH * HTTP/1.1\r\nHOST: " + ssdpHost.String() + "\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + target + "\r\n\r\n"
	if _, err := conn.WriteToUDPAddrPort([]byte(search), r.SSDP); err != nil {
		return "", "", errUnsupported
	}
	deadline := time.Now().Add(3 * time.Second)
	if d, has := ctx.Deadline(); has && d.Before(deadline) {
		deadline = d
	}
	conn.SetReadDeadline(deadline)
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return "", "", errUnsupported
		}
		if from.Addr().Unmap().WithZone("") != r.Gateway.Unmap().WithZone("") || r.Gateway.Zone() != "" && from.Addr().Zone() != r.Gateway.Zone() {
			continue
		}
		location := ""
		for _, line := range strings.Split(string(buf[:n]), "\r\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "location") {
				location = strings.TrimSpace(v)
			}
		}
		u, err := url.Parse(location)
		if err != nil || !routerURL(u, r.Gateway) {
			continue
		}
		return upnpService(ctx, u, r, wanted)
	}
}

// A link-local URL learned on this interface may omit its zone. Add only the
// known router's zone; never accept a different host or a different interface.
func routerURL(u *url.URL, gateway netip.Addr) bool {
	if u.Scheme != "http" || u.User != nil {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || ip.Unmap().WithZone("") != gateway.Unmap().WithZone("") || ip.Zone() != "" && ip.Zone() != gateway.Zone() {
		return false
	}
	if gateway.Zone() != "" && ip.Zone() == "" {
		port := u.Port()
		u.Host = "[" + ip.WithZone(gateway.Zone()).String() + "]"
		if port != "" {
			u.Host += ":" + port
		}
	}
	return true
}

// upnpClient never follows a redirect: the router's own address is the only one it talks to.
var upnpClient = &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

func upnpService(ctx context.Context, location *url.URL, r Router, wanted string) (string, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", location.String(), nil)
	resp, err := routerHTTP(r.Client).Do(req)
	if err != nil {
		return "", "", errUnsupported
	}
	defer resp.Body.Close()
	var desc struct {
		URLBase  string `xml:"URLBase"`
		Services []struct {
			Type    string `xml:"serviceType"`
			Control string `xml:"controlURL"`
		} `xml:"device>deviceList>device>deviceList>device>serviceList>service"`
	}
	if resp.StatusCode != 200 || xml.NewDecoder(io.LimitReader(resp.Body, maxDescription)).Decode(&desc) != nil {
		return "", "", errUnsupported
	}
	base := location
	if b, err := url.Parse(desc.URLBase); desc.URLBase != "" && err == nil {
		base = b
	}
	for _, s := range desc.Services {
		if wanted != "" && s.Type != wanted || wanted == "" && !strings.Contains(s.Type, ":WANIPConnection:") && !strings.Contains(s.Type, ":WANPPPConnection:") {
			continue
		}
		control, err := base.Parse(s.Control)
		if err == nil && routerURL(control, r.Gateway) {
			return control.String(), s.Type, nil
		}
	}
	return "", "", errUnsupported
}

// upnpCall sends one SOAP action and returns the answer's body, or the router's UPnP error code.
func upnpCall(ctx context.Context, control, service, action string, args [][2]string, source ...netip.Addr) ([]byte, int, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` + action + ` xmlns:u="` + service + `">`)
	for _, a := range args {
		b.WriteString("<" + a[0] + ">")
		xml.EscapeText(&b, []byte(a[1]))
		b.WriteString("</" + a[0] + ">")
	}
	b.WriteString("</u:" + action + "></s:Body></s:Envelope>")
	req, _ := http.NewRequestWithContext(ctx, "POST", control, strings.NewReader(b.String()))
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+service+"#"+action+`"`)
	client := netip.Addr{}
	if len(source) == 1 {
		client = source[0]
	}
	resp, err := routerHTTP(client).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxDescription))
	if resp.StatusCode != 200 {
		var fault struct {
			Code int `xml:"Body>Fault>detail>UPnPError>errorCode"`
		}
		xml.Unmarshal(body, &fault)
		return nil, fault.Code, fmt.Errorf("the router refused the port (UPnP error %d)", fault.Code)
	}
	return body, 0, nil
}

// upnp asks with UPnP IGD. lifetime 0 removes the mapping. A router that keeps only permanent
// mappings (error 725) is asked again for one; it is removed when the server stops.
func upnp(ctx context.Context, ssdp netip.AddrPort, gateway netip.Addr, local netip.Addr, internal, external int, lifetime time.Duration) (mapping, error) {
	control, service, err := upnpControl(ctx, Router{Gateway: gateway, SSDP: ssdp}, "")
	if err != nil {
		return mapping{}, err
	}
	if lifetime == 0 {
		_, _, err := upnpCall(ctx, control, service, "DeletePortMapping",
			[][2]string{{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(external)}, {"NewProtocol", "TCP"}})
		return mapping{}, err
	}
	add := func(lease time.Duration) (int, error) {
		_, code, err := upnpCall(ctx, control, service, "AddPortMapping", [][2]string{{"NewRemoteHost", ""},
			{"NewExternalPort", strconv.Itoa(external)}, {"NewProtocol", "TCP"}, {"NewInternalPort", strconv.Itoa(internal)},
			{"NewInternalClient", local.String()}, {"NewEnabled", "1"}, {"NewPortMappingDescription", "Portico"},
			{"NewLeaseDuration", strconv.Itoa(int(lease / time.Second))}})
		return code, err
	}
	if code, err := add(lifetime); code == 725 {
		lifetime = 0
		if _, err := add(0); err != nil {
			return mapping{}, err
		}
	} else if err != nil {
		return mapping{}, err
	}
	out := mapping{protocol: "UPnP", externalPort: external, lifetime: lifetime}
	if body, _, err := upnpCall(ctx, control, service, "GetExternalIPAddress", nil); err == nil {
		var ip struct {
			Addr string `xml:"Body>GetExternalIPAddressResponse>NewExternalIPAddress"`
		}
		if xml.Unmarshal(body, &ip) == nil {
			out.externalIP, _ = netip.ParseAddr(ip.Addr)
		}
	}
	return out, nil
}
