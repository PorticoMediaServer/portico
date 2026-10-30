package networking

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func gatewayURL(raw, gateway string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 1024 || u.Scheme != "http" || u.Hostname() != gateway || !gatewayAddress(gateway) || u.User != nil || u.Fragment != "" || u.Opaque != "" || !validEndpointPort(u) || strings.ContainsAny(raw, "\\\r\n\t") {
		return nil, ErrInvalid
	}
	return u, nil
}
func gatewayHTTP(ctx context.Context, m Mapping, method, target, action string, body []byte) ([]byte, int, error) {
	u, e := gatewayURL(target, m.Gateway)
	if e != nil {
		return nil, 0, e
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(m.Client)}}).DialContext(ctx, "tcp4", net.JoinHostPort(m.Gateway, port))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if e != nil {
		return nil, 0, e
	}
	if action != "" {
		req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
		req.Header.Set("SOAPAction", `"`+m.ServiceType+"#"+action+`"`)
	}
	response, e := client.Do(req)
	if e != nil {
		return nil, 0, e
	}
	defer response.Body.Close()
	b, e := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	if e != nil || len(b) > 128<<10 {
		return nil, response.StatusCode, errMappingResponse
	}
	return b, response.StatusCode, nil
}

type upnpDevice struct {
	Services []struct {
		Type    string `xml:"serviceType"`
		Control string `xml:"controlURL"`
	} `xml:"serviceList>service"`
	Children []upnpDevice `xml:"deviceList>device"`
}

func natService(t string) bool {
	return t == "urn:schemas-upnp-org:service:WANIPConnection:1" || t == "urn:schemas-upnp-org:service:WANIPConnection:2" || t == "urn:schemas-upnp-org:service:WANPPPConnection:1"
}

// firewallService is IGDv2's IPv6 firewall control, which opens inbound
// pinholes to a global IPv6 address (there is no address translation).
func firewallService(t string) bool {
	return t == "urn:schemas-upnp-org:service:WANIPv6FirewallControl:1"
}

func findUPnPServiceOf(device upnpDevice, wanted func(string) bool) (string, string) {
	for _, s := range device.Services {
		if wanted(s.Type) {
			return s.Type, s.Control
		}
	}
	for _, d := range device.Children {
		t, c := findUPnPServiceOf(d, wanted)
		if t != "" {
			return t, c
		}
	}
	return "", ""
}
func discoverUPnP(ctx context.Context, m Mapping) (Mapping, error) {
	return discoverUPnPService(ctx, m, natService)
}

// discoverUPnPService finds the gateway's device description over SSDP and
// the control URL of the first service wanted accepts.
func discoverUPnPService(ctx context.Context, m Mapping, wanted func(string) bool) (Mapping, error) {
	if m.ControlURL != "" {
		if _, e := gatewayURL(m.ControlURL, m.Gateway); e != nil {
			return m, e
		}
		return m, nil
	}
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(m.Client)})
	if e != nil {
		return m, e
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	request := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n\r\n"
	if _, e = conn.WriteToUDP([]byte(request), &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}); e != nil {
		return m, e
	}
	b := make([]byte, 8192)
	location := ""
	for count := 0; count < 16; count++ {
		n, peer, e := conn.ReadFromUDP(b)
		if e != nil {
			return m, e
		}
		if peer.IP.String() != m.Gateway || n == len(b) {
			continue
		}
		response, e := http.ReadResponse(bufio.NewReader(bytes.NewReader(b[:n])), nil)
		if e != nil {
			continue
		}
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			continue
		}
		location = response.Header.Get("Location")
		if _, e = gatewayURL(location, m.Gateway); e == nil {
			break
		}
		location = ""
	}
	if location == "" {
		return m, errMappingUnsupported
	}
	raw, status, e := gatewayHTTP(ctx, m, "GET", location, "", nil)
	if e != nil {
		return m, e
	}
	if status != 200 {
		return m, errMappingResponse
	}
	var root struct {
		Device upnpDevice `xml:"device"`
	}
	if xml.Unmarshal(raw, &root) != nil {
		return m, errMappingResponse
	}
	service, control := findUPnPServiceOf(root.Device, wanted)
	if service == "" {
		return m, errMappingUnsupported
	}
	origin, _ := url.Parse(location)
	relative, e := url.Parse(control)
	if e != nil {
		return m, errMappingResponse
	}
	endpoint := origin.ResolveReference(relative)
	if _, e = gatewayURL(endpoint.String(), m.Gateway); e != nil {
		return m, e
	}
	m.ServiceType = service
	m.ControlURL = endpoint.String()
	return m, nil
}
func soapEscape(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}
func upnpCall(ctx context.Context, m Mapping, action string, args [][2]string) (map[string]string, error) {
	return upnpCallFields(ctx, m, action, args, "New")
}

// upnpCallFields is upnpCall for services whose response arguments don't use
// the IGDv1 "New" prefix (IGDv2 WANIPv6FirewallControl: UniqueID,
// FirewallEnabled, ...). prefix "" captures every response argument.
func upnpCallFields(ctx context.Context, m Mapping, action string, args [][2]string, prefix string) (map[string]string, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` + action + ` xmlns:u="` + m.ServiceType + `">`)
	for _, arg := range args {
		b.WriteString("<" + arg[0] + ">" + soapEscape(arg[1]) + "</" + arg[0] + ">")
	}
	b.WriteString("</u:" + action + "></s:Body></s:Envelope>")
	raw, status, e := gatewayHTTP(ctx, m, "POST", m.ControlURL, action, []byte(b.String()))
	if e != nil {
		return nil, e
	}
	return parseUPnPResponseFields(raw, status, m.ServiceType, action, prefix)
}

// Only a matching SOAP body can acknowledge this action. An HTTP 200 HTML
// error page, empty document, foreign action or unscoped New* field is not a
// successful mutation/cleanup receipt.
func parseUPnPResponse(raw []byte, status int, service, action string) (map[string]string, error) {
	return parseUPnPResponseFields(raw, status, service, action, "New")
}
func parseUPnPResponseFields(raw []byte, status int, service, action, prefix string) (map[string]string, error) {
	const soap = "http://schemas.xmlsoap.org/soap/envelope/"
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	out := map[string]string{}
	stack := []xml.Name{}
	envelopes, bodies, responses, faults := 0, 0, 0, 0
	for {
		token, e := decoder.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, errMappingResponse
		}
		switch v := token.(type) {
		case xml.StartElement:
			stack = append(stack, v.Name)
			if len(stack) > 32 {
				return nil, errMappingResponse
			}
			if len(stack) == 1 {
				if v.Name != (xml.Name{Space: soap, Local: "Envelope"}) {
					return nil, errMappingResponse
				}
				envelopes++
			}
			if len(stack) == 2 && v.Name == (xml.Name{Space: soap, Local: "Body"}) {
				bodies++
			}
			inBody := len(stack) >= 3 && stack[1] == (xml.Name{Space: soap, Local: "Body"})
			if inBody && len(stack) == 3 {
				if v.Name == (xml.Name{Space: service, Local: action + "Response"}) {
					responses++
				} else if v.Name == (xml.Name{Space: soap, Local: "Fault"}) {
					faults++
				} else {
					return nil, errMappingResponse
				}
			}
			field := inBody && len(stack) == 4 && stack[2] == (xml.Name{Space: service, Local: action + "Response"}) && strings.HasPrefix(v.Name.Local, prefix)
			code := inBody && len(stack) >= 4 && stack[2] == (xml.Name{Space: soap, Local: "Fault"}) && v.Name.Local == "errorCode"
			if field || code {
				var value string
				if e = decoder.DecodeElement(&value, &v); e != nil || len(value) > 2048 {
					return nil, errMappingResponse
				}
				if _, duplicate := out[v.Name.Local]; duplicate {
					return nil, errMappingResponse
				}
				out[v.Name.Local] = strings.TrimSpace(value)
				stack = stack[:len(stack)-1]
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errMappingResponse
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && len(bytes.TrimSpace(v)) != 0 {
				return nil, errMappingResponse
			}
		}
	}
	if len(stack) != 0 || envelopes != 1 || bodies != 1 {
		return nil, errMappingResponse
	}
	if faults == 1 && responses == 0 {
		switch out["errorCode"] {
		case "714", "704":
			// 704: WANIPv6FirewallControl NoSuchEntry.
			return nil, errUPnPMissing
		case "702":
			return nil, errFirewallDisabled
		case "701", "703":
			// PinholeSpaceExhausted, InboundPinholeNotAllowed.
			return nil, errMappingDenied
		case "718":
			return nil, errMappingConflict
		case "725":
			return nil, errMappingUnsupported
		case "606":
			return nil, errMappingDenied
		}
	}
	if status != 200 || responses != 1 || faults != 0 {
		return nil, errMappingResponse
	}
	return out, nil
}

var errUPnPMissing = errors.New("mapping absent")
var errFirewallDisabled = errors.New("gateway IPv6 firewall disabled")

func upnpMap(ctx context.Context, m Mapping, lifetime uint32) (Mapping, error) {
	query := [][2]string{{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(m.ExternalPort)}, {"NewProtocol", "TCP"}}
	current, e := upnpCall(ctx, m, "GetSpecificPortMappingEntry", query)
	if e != nil && !errors.Is(e, errUPnPMissing) {
		return m, e
	}
	if e == nil && (current["NewInternalClient"] != m.Client || current["NewInternalPort"] != strconv.Itoa(m.InternalPort) || current["NewPortMappingDescription"] != m.Description) {
		return m, errMappingConflict
	}
	if lifetime == 0 {
		if errors.Is(e, errUPnPMissing) {
			return grantedMapping(m, 0, 0)
		}
		if _, e = upnpCall(ctx, m, "DeletePortMapping", query); e != nil {
			return m, e
		}
		current, e = upnpCall(ctx, m, "GetSpecificPortMappingEntry", query)
		if errors.Is(e, errUPnPMissing) || e == nil && (current["NewInternalClient"] != m.Client || current["NewInternalPort"] != strconv.Itoa(m.InternalPort) || current["NewPortMappingDescription"] != m.Description) {
			return grantedMapping(m, 0, 0)
		}
		if e != nil {
			return m, e
		}
		return m, errMappingResponse
	}
	external, e := upnpCall(ctx, m, "GetExternalIPAddress", nil)
	if e != nil {
		return m, e
	}
	if ip := net.ParseIP(external["NewExternalIPAddress"]); ip == nil {
		return m, errMappingResponse
	} else {
		m.ExternalAddress = ip.String()
	}
	args := append(query, [2]string{"NewInternalPort", strconv.Itoa(m.InternalPort)}, [2]string{"NewInternalClient", m.Client}, [2]string{"NewEnabled", "1"}, [2]string{"NewPortMappingDescription", m.Description}, [2]string{"NewLeaseDuration", fmt.Sprint(lifetime)})
	if _, e = upnpCall(ctx, m, "AddPortMapping", args); e != nil {
		return m, e
	}
	// Read back the actual grant. Never call a permanent/foreign lease our own.
	current, e = upnpCall(ctx, m, "GetSpecificPortMappingEntry", query)
	if e != nil {
		return m, e
	}
	granted, e := strconv.ParseUint(current["NewLeaseDuration"], 10, 32)
	if e != nil || granted == 0 || current["NewInternalClient"] != m.Client || current["NewInternalPort"] != strconv.Itoa(m.InternalPort) || current["NewPortMappingDescription"] != m.Description || current["NewEnabled"] != "1" {
		// Some gateways ignore finite leases and install permanent entries.
		// Keep the owned receipt until read-back-checked deletion succeeds.
		m.State = "cleanup_pending"
		m.MutationPending = true
		return m, errMappingResponse
	}
	return grantedMapping(m, uint32(granted), 0)
}
