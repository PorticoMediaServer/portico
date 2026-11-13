package live

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// LiveTunerDevice is a tuner found on the server's home network.
type LiveTunerDevice struct {
	DeviceID        string `json:"deviceId"`
	Model           string `json:"model"`
	FriendlyName    string `json:"friendlyName"`
	Address         string `json:"address"`
	BaseURL         string `json:"baseUrl"`
	LineupURL       string `json:"lineupUrl"`
	TunerCount      int    `json:"tunerCount"`
	FirmwareVersion string `json:"firmwareVersion"`
	Configured      bool   `json:"configured"`
}

// LiveTunerDiscovery keeps the existing Find tuners window's response.
type LiveTunerDiscovery struct {
	Devices  []LiveTunerDevice `json:"devices"`
	Searched string            `json:"searchedFor"`
	Enabled  bool              `json:"discoveryEnabled"`
	Message  string            `json:"message,omitempty"`
}

// DiscoverTuners searches only when asked, never adding a source by itself.
func (s *Service) DiscoverTuners(ctx context.Context, window time.Duration) (LiveTunerDiscovery, error) {
	out := LiveTunerDiscovery{Devices: []LiveTunerDevice{}, Searched: "hdhomerun", Enabled: Document.Get(s.Settings).DiscoveryEnabled}
	if !out.Enabled {
		out.Message = "Tuner discovery is switched off in the live source settings."
		return out, nil
	}
	if window <= 0 || window > 10*time.Second {
		window = 2 * time.Second
	}
	targets, prefixes := tunerTargets()
	devices, err := discoverTuners(ctx, window, targets, prefixes)
	if err != nil {
		return out, err
	}
	configured := map[string]bool{}
	sources, err := s.db.LiveSources(ctx)
	if err != nil {
		return out, err
	}
	for _, source := range sources {
		var cfg LiveSourceConfig
		if json.Unmarshal([]byte(source.Config), &cfg) == nil {
			configured[strings.TrimRight(cfg.TunerURL, "/")] = true
		}
	}
	lookup, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	for i := range devices {
		device := &devices[i]
		device.Configured = configured[device.BaseURL]
		s.tunerFacts(lookup, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].DeviceID < devices[j].DeviceID })
	out.Devices = devices
	if len(devices) == 0 {
		out.Message = "No tuner answered on this network."
	}
	return out, nil
}

func (s *Service) tunerFacts(ctx context.Context, device *LiveTunerDevice) {
	ctx, stop := context.WithTimeout(ctx, 750*time.Millisecond)
	defer stop()
	req, err := http.NewRequestWithContext(ctx, "GET", device.BaseURL+"/discover.json", nil)
	if err != nil {
		return
	}
	// Discovery only reads the responding IP. A device may not redirect this lookup elsewhere.
	client := *s.client
	if transport, ok := client.Transport.(*http.Transport); ok {
		copy := transport.Clone()
		copy.Proxy = nil
		copy.DisableKeepAlives = true
		client.Transport = copy
	} else if client.Transport == nil {
		copy := http.DefaultTransport.(*http.Transport).Clone()
		copy.Proxy = nil
		copy.DisableKeepAlives = true
		client.Transport = copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req.Header.Set("User-Agent", Document.Get(s.Settings).IdentifyAs())
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return
	}
	var info struct {
		DeviceID, ModelNumber, FriendlyName, FirmwareVersion string
		TunerCount                                           int
	}
	if json.Unmarshal(raw, &info) != nil {
		return
	}
	if info.DeviceID != "" && validText(info.DeviceID, 64) {
		device.DeviceID = info.DeviceID
	}
	if validText(info.ModelNumber, 128) {
		device.Model = info.ModelNumber
	}
	if validText(info.FriendlyName, 256) {
		device.FriendlyName = info.FriendlyName
	}
	if validText(info.FirmwareVersion, 128) {
		device.FirmwareVersion = info.FirmwareVersion
	}
	if info.TunerCount >= 1 && info.TunerCount <= 256 {
		device.TunerCount = info.TunerCount
	}
}

func tunerPacket() []byte {
	packet := []byte{0, 2, 0, 12, 1, 4, 0, 0, 0, 1, 2, 4, 255, 255, 255, 255}
	return binary.LittleEndian.AppendUint32(packet, crc32.ChecksumIEEE(packet))
}

func tunerReply(packet []byte) bool {
	if len(packet) >= 8 && packet[0] == 0 && packet[1] == 3 {
		size := int(binary.BigEndian.Uint16(packet[2:4]))
		return len(packet) == size+8 && binary.LittleEndian.Uint32(packet[len(packet)-4:]) == crc32.ChecksumIEEE(packet[:len(packet)-4])
	}
	lower := strings.ToLower(string(packet))
	return strings.HasPrefix(lower, "http/1.1 200") && (strings.Contains(lower, "hdhomerun") || strings.Contains(lower, "silicondust"))
}

func discoverTuners(ctx context.Context, window time.Duration, targets []netip.AddrPort, prefixes []netip.Prefix) ([]LiveTunerDevice, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(window)
	if at, ok := ctx.Deadline(); ok && at.Before(deadline) {
		deadline = at
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	for _, target := range targets {
		packet := tunerPacket()
		if target.Port() == 1900 {
			packet = []byte("M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: ssdp:all\r\n\r\n")
		}
		if _, err := conn.WriteToUDPAddrPort(packet, target); err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	seen, devices := map[netip.Addr]bool{}, []LiveTunerDevice{}
	buf := make([]byte, 8192)
	for len(devices) < 64 {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				break
			}
			return nil, err
		}
		ip := from.Addr().Unmap()
		if seen[ip] || !ip.Is4() || !tunerReplyFrom(buf[:n], ip, prefixes) {
			continue
		}
		seen[ip] = true
		root := "http://" + ip.String()
		devices = append(devices, LiveTunerDevice{DeviceID: ip.String(), Address: ip.String(), BaseURL: root, LineupURL: root + "/lineup.json"})
	}
	return devices, nil
}

func tunerTargets() (targets []netip.AddrPort, prefixes []netip.Prefix) {
	targets = []netip.AddrPort{netip.MustParseAddrPort("255.255.255.255:65001"), netip.MustParseAddrPort("239.255.255.250:1900")}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil || !prefix.Addr().Is4() {
				continue
			}
			prefixes = append(prefixes, prefix.Masked())
			if iface.Flags&net.FlagBroadcast == 0 || prefix.Bits() > 30 {
				continue
			}
			ip := prefix.Addr().As4()
			mask := net.CIDRMask(prefix.Bits(), 32)
			for i := range ip {
				ip[i] |= ^mask[i]
			}
			targets = append(targets, netip.AddrPortFrom(netip.AddrFrom4(ip), 65001))
		}
	}
	return targets, prefixes
}

// Only directly connected networks may supply discovery facts. Private ranges
// alone also include routed/VPN networks and cloud metadata endpoints.
func tunerReplyFrom(packet []byte, ip netip.Addr, prefixes []netip.Prefix) bool {
	directlyConnected := ip.IsLoopback()
	for _, prefix := range prefixes {
		directlyConnected = directlyConnected || prefix.Contains(ip)
	}
	return directlyConnected && tunerReply(packet)
}
