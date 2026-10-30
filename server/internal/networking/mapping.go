package networking

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"time"
)

const mappingLifetime = 3600

var errMappingUnsupported = errors.New("mapping protocol unsupported")
var errMappingDenied = errors.New("mapping denied by gateway")
var errMappingConflict = errors.New("mapping belongs to another service")
var errMappingBrowserPort = errors.New("browser restricted mapping port")
var errMappingResponse = errors.New("invalid gateway response")

type Mapping struct {
	ID              string    `json:"id"`
	Authority       string    `json:"authority"`
	Network         string    `json:"network,omitempty"`
	Topology        string    `json:"topology"`
	Revision        int64     `json:"revision"`
	Protocol        string    `json:"protocol"`
	Gateway         string    `json:"gateway"`
	Client          string    `json:"client"`
	InternalPort    int       `json:"internalPort"`
	ExternalPort    int       `json:"externalPort"`
	ExternalAddress string    `json:"externalAddress"`
	Nonce           string    `json:"nonce"`
	ControlURL      string    `json:"controlUrl,omitempty"`
	ServiceType     string    `json:"serviceType,omitempty"`
	Description     string    `json:"description"`
	State           string    `json:"state"`
	ExpiresAt       time.Time `json:"expiresAt"`
	PotentialUntil  time.Time `json:"potentialUntil"`
	EpochGeneration uint64    `json:"epochGeneration"`
	RenewAt         time.Time `json:"renewAt"`
	NextAttempt     time.Time `json:"nextAttempt"`
	// MutationPending is committed BEFORE a gateway effect. A lost/malformed
	// reply cannot be treated as proof that the requested lease expired.
	MutationPending bool      `json:"mutationPending,omitempty"`
	EpochObservedAt time.Time `json:"epochObservedAt,omitempty"`
	Epoch           uint32    `json:"epoch"`
	ErrorCode       string    `json:"errorCode,omitempty"`
	// IPv6 pinholes (protocol upnp6): the global IPv6 address admitted, the
	// router's pinhole id, and whether the router reported no IPv6 firewall.
	PinholeClient string `json:"pinholeClient,omitempty"`
	PinholeID     string `json:"pinholeId,omitempty"`
	FirewallOpen  bool   `json:"firewallOpen,omitempty"`
	// Failures counts consecutive failed attempts, for backoff (A84).
	Failures int `json:"failures,omitempty"`
}
type GatewayMapper interface {
	Prepare(context.Context, Mapping) (Mapping, error)
	Map(context.Context, Mapping, uint32) (Mapping, error)
}
type gatewayMapper struct{}

func mappingNonce() string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (gatewayMapper) Prepare(ctx context.Context, m Mapping) (Mapping, error) {
	if !gatewayAddress(m.Gateway) || net.ParseIP(m.Client) == nil || m.InternalPort < 1 || m.InternalPort > 65535 || m.ExternalPort < 1 || m.ExternalPort > 65535 {
		return m, ErrInvalid
	}
	switch m.Protocol {
	case "pcp":
		return pcpPrepare(ctx, m)
	case "natpmp":
		b, e := gatewayDatagram(ctx, m, []byte{0, 0}, 12, func(b []byte) bool { return len(b) == 12 && b[0] == 0 && b[1] == 128 })
		if e != nil {
			return m, e
		}
		ip, epoch, e := parseNATPMPAddress(b)
		if e != nil {
			return m, e
		}
		if mappingEpochReset(m, epoch, time.Now()) || m.ExternalAddress != "" && m.ExternalAddress != ip {
			m.EpochGeneration++
			m.State = "pending"
			m.ExpiresAt = time.Time{}
		}
		m.ExternalAddress = ip
		m.Epoch, m.EpochObservedAt = epoch, time.Now()
		return m, nil
	case "upnp":
		return discoverUPnP(ctx, m)
	case pinholeProtocol:
		return pinholePrepare(ctx, m)
	}
	return m, errMappingUnsupported
}
func (gatewayMapper) Map(ctx context.Context, m Mapping, lifetime uint32) (Mapping, error) {
	switch m.Protocol {
	case "pcp":
		return pcpMap(ctx, m, lifetime)
	case "natpmp":
		return natpmpMap(ctx, m, lifetime)
	case "upnp":
		return upnpMap(ctx, m, lifetime)
	case pinholeProtocol:
		return pinholeMap(ctx, m, lifetime)
	}
	return m, errMappingUnsupported
}

// Connected UDP admits replies only from the selected gateway and port. Source
// IP is bound to the same interface which will receive forwarded traffic.
func gatewayDatagram(ctx context.Context, m Mapping, request []byte, min int, matches func([]byte) bool, retransmitted ...*bool) ([]byte, error) {
	c, e := net.DialUDP("udp4", &net.UDPAddr{IP: net.ParseIP(m.Client)}, &net.UDPAddr{IP: net.ParseIP(m.Gateway), Port: 5351})
	if e != nil {
		return nil, e
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	buffer := make([]byte, 1101)
	wait := 250 * time.Millisecond
	if m.Protocol == "pcp" {
		wait = 1 * time.Second
	}
	for attempt := 0; attempt < 3; attempt++ {
		// Even an explicit rejection of a retransmission cannot prove that an
		// earlier lost MAP request did not acquire a lease.
		if attempt > 0 && len(retransmitted) > 0 && retransmitted[0] != nil {
			*retransmitted[0] = true
		}
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		deadline := time.Now().Add(wait)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = c.SetDeadline(deadline)
		if _, e = c.Write(request); e != nil {
			return nil, e
		}
		// Delayed/unrelated datagrams from the gateway do not own this request.
		// Bound both the deadline and rejected packet count.
		for discarded := 0; discarded < 32; discarded++ {
			n, readErr := c.Read(buffer)
			if readErr == nil {
				if n < min || n > 1100 || !matches(buffer[:n]) {
					continue
				}
				return append([]byte(nil), buffer[:n]...), nil
			}
			var ne net.Error
			if !errors.As(readErr, &ne) || !ne.Timeout() {
				return nil, readErr
			}
			e = readErr
			break
		}
		wait *= 2
	}
	if e == nil {
		e = errMappingResponse
	}
	return nil, e
}
func mappingError(err error) string {
	switch {
	case errors.Is(err, errMappingBrowserPort):
		return "browser_restricted_port"
	case errors.Is(err, errMappingUnsupported):
		return "mapping_unsupported"
	case errors.Is(err, errMappingDenied):
		return "mapping_denied"
	case errors.Is(err, errMappingConflict):
		return "port_in_use"
	case errors.Is(err, errMappingResponse):
		return "mapping_response_invalid"
	default:
		return "gateway_unavailable"
	}
}

// Compare against elapsed local time, not just the last epoch value: a
// reboot can have a larger uptime by the next half-lease renewal. Arithmetic
// is widened so uint32 wrap cannot turn a new epoch into a plausible one.
func mappingEpochReset(m Mapping, epoch uint32, now time.Time) bool {
	if m.Protocol != "pcp" && m.Protocol != "natpmp" {
		return false
	}
	elapsed := now.Sub(m.EpochObservedAt).Seconds()
	if m.EpochObservedAt.IsZero() || elapsed < 0 {
		elapsed = 0
	}
	delta := int64(epoch) - int64(m.Epoch)
	if m.Protocol == "pcp" {
		return delta < -1 || !m.EpochObservedAt.IsZero() &&
			(float64(delta)+2 < elapsed*0.9375 || float64(delta)-2 > elapsed*1.0625)
	}
	return float64(delta)+2 < elapsed*0.875
}
func grantedMapping(m Mapping, lifetime, epoch uint32) (Mapping, error) {
	now := time.Now()
	m.MutationPending = false
	if lifetime == 0 {
		m.State = "deleted"
		m.ExpiresAt, m.PotentialUntil = now, now
		return m, nil
	}
	if mappingEpochReset(m, epoch, now) {
		m.EpochGeneration++
	}
	m.Epoch, m.EpochObservedAt = epoch, now
	m.ExpiresAt = now.Add(time.Duration(lifetime) * time.Second)
	m.PotentialUntil = m.ExpiresAt
	// Keep the ACTUAL lease/identity for owned cleanup even when unsupported.
	if lifetime > 86400 {
		m.State = "cleanup_pending"
		m.NextAttempt = now
		return m, errMappingResponse
	}
	m.State = "mapped"
	m.RenewAt = now.Add(time.Duration(lifetime) * time.Second / 2)
	m.NextAttempt = m.RenewAt
	m.ErrorCode = ""
	return m, nil
}
