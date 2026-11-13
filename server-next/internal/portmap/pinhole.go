package portmap

import (
	"context"
	"encoding/xml"
	"errors"
	"strconv"
	"time"
)

const ipv6FirewallService = "urn:schemas-upnp-org:service:WANIPv6FirewallControl:1"

// IGDv2 firewall control opens the server's actual IPv6 listener, without NAT
// or a second public port. Discovery and SOAP originate from InternalClient.
// https://upnp.org/specs/gw/UPnP-gw-WANIPv6FirewallControl-v1-Service.pdf
func ipv6Pinhole(ctx context.Context, r Router, internal int, life time.Duration, held *mapping) (mapping, error) {
	out := mapping{protocol: "UPnP IPv6", internal: internal, externalPort: internal, externalIP: r.Client, lifetime: life, router: r}
	if held != nil && held.protocol == out.protocol && held.router == r && held.internal == internal {
		out.pinhole = held.pinhole
	}
	if life == 0 && out.pinhole == "" {
		return out, nil // The router's IPv6 firewall was already off: nothing to close.
	}
	control, service, err := upnpControl(ctx, r, ipv6FirewallService)
	if err != nil {
		return out, err
	}
	call := func(action string, args [][2]string) ([]byte, int, error) {
		raw, code, err := upnpCall(ctx, control, service, action, args, r.Client)
		if err != nil {
			return raw, code, err
		}
		var envelope struct {
			Body struct {
				Responses []struct{ XMLName xml.Name } `xml:",any"`
			} `xml:"Body"`
		}
		if xml.Unmarshal(raw, &envelope) != nil || len(envelope.Body.Responses) != 1 ||
			envelope.Body.Responses[0].XMLName != (xml.Name{Space: service, Local: action + "Response"}) {
			return nil, 0, errors.New("the router sent an invalid IPv6 firewall answer")
		}
		return raw, 0, nil
	}
	if life == 0 {
		_, code, err := call("DeletePinhole", [][2]string{{"UniqueID", out.pinhole}})
		if code == 704 {
			err = nil // A restarted router already forgot this opening.
		}
		return out, err
	}
	raw, _, err := call("GetFirewallStatus", nil)
	if err != nil {
		return out, err
	}
	var status struct {
		Enabled *bool `xml:"Body>GetFirewallStatusResponse>FirewallEnabled"`
		Allowed *bool `xml:"Body>GetFirewallStatusResponse>InboundPinholeAllowed"`
	}
	if xml.Unmarshal(raw, &status) != nil || status.Enabled == nil || status.Allowed == nil {
		return out, errors.New("the router did not report its IPv6 firewall state")
	}
	if !*status.Enabled {
		out.pinhole = ""
		return out, nil
	}
	if !*status.Allowed {
		return out, errors.New("the router does not allow IPv6 openings")
	}
	lease := strconv.Itoa(int(min(life/time.Second, 86400)))
	if out.pinhole != "" {
		_, code, err := call("UpdatePinhole", [][2]string{{"UniqueID", out.pinhole}, {"NewLeaseTime", lease}})
		if err == nil || code != 704 {
			return out, err
		}
		out.pinhole = ""
	}
	raw, code, err := call("AddPinhole", [][2]string{{"RemoteHost", ""}, {"RemotePort", "0"}, {"InternalClient", r.Client.String()},
		{"InternalPort", strconv.Itoa(internal)}, {"Protocol", "6"}, {"LeaseTime", lease}})
	if code == 702 {
		return out, nil // The firewall was disabled between checking and opening it.
	}
	if err != nil {
		return out, err
	}
	var added struct {
		ID string `xml:"Body>AddPinholeResponse>UniqueID"`
	}
	if xml.Unmarshal(raw, &added) != nil {
		return out, errors.New("the router did not identify the IPv6 opening")
	}
	if _, err := strconv.ParseUint(added.ID, 10, 16); err != nil {
		return out, errors.New("the router did not identify the IPv6 opening")
	}
	out.pinhole = added.ID
	return out, nil
}
