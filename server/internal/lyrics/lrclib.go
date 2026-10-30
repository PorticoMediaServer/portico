package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// LRCLIB implements only read/search. The optional origin is operator-supplied,
// never supplied by a media item or a search result. No provider URLs or tokens
// are used as client delivery URLs.
type LRCLIB struct {
	origin string
	client *http.Client

	// One pacing clock for every caller. LRCLIB is a free community service:
	// a bulk run over a music library must not send it requests as fast as the
	// network allows, and a refusal must quiet every worker, not just one.
	mu   sync.Mutex
	next time.Time
}

const (
	lrclibInterval     = 500 * time.Millisecond
	lrclibDefaultPause = time.Minute
	lrclibMaxWait      = 5 * time.Second
)

// errProviderPaused is ErrUnavailable with its reason: the provider answered a
// request with a rate limit or an overload, and the pause it asked for has not
// passed. The request was not judged, so it is worth sending again afterwards.
var errProviderPaused = fmt.Errorf("%w", ErrUnavailable)

// pace waits for this caller's turn. When the provider has asked for a longer
// pause than a caller should sit through, it reports unavailable at once so the
// work is rescheduled rather than parked on a goroutine.
func (p *LRCLIB) pace(ctx context.Context) error {
	p.mu.Lock()
	now := time.Now()
	wait := p.next.Sub(now)
	if wait > lrclibMaxWait {
		p.mu.Unlock()
		return errProviderPaused
	}
	if wait < 0 {
		wait = 0
	}
	p.next = now.Add(wait + lrclibInterval)
	p.mu.Unlock()
	if wait == 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// resumesIn is how long until the provider's pause, if it asked for one, ends.
func (p *LRCLIB) resumesIn() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Until(p.next)
}

// refused records a rate-limit or overload answer so nothing is sent until the
// provider's own deadline has passed.
func (p *LRCLIB) refused(response *http.Response) {
	pause := lrclibDefaultPause
	if seconds, e := strconv.Atoi(response.Header.Get("Retry-After")); e == nil && seconds > 0 {
		pause = time.Duration(min(seconds, 86400)) * time.Second
	} else if at, e := http.ParseTime(response.Header.Get("Retry-After")); e == nil {
		if until := time.Until(at); until > 0 {
			pause = min(until, 24*time.Hour)
		}
	}
	p.mu.Lock()
	if until := time.Now().Add(pause); until.After(p.next) {
		p.next = until
	}
	p.mu.Unlock()
}

func NewLRCLIB(origin string) (*LRCLIB, error) {
	u, e := validOrigin(origin)
	if e != nil {
		return nil, e
	}
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 2, MaxConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 6 * time.Second, TLSHandshakeTimeout: 5 * time.Second, MaxResponseHeaderBytes: 32 << 10, DialContext: publicDial}
	return &LRCLIB{origin: strings.TrimSuffix(u.String(), "/"), client: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrUnavailable }}}, nil
}
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, ErrUnavailable
	}
	ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if e != nil || len(ips) == 0 || len(ips) > 32 {
		return nil, ErrUnavailable
	}
	// Validate every answer before dialing a numeric address (no second DNS
	// resolution, redirect or environment proxy can bypass origin policy).
	for _, raw := range ips {
		ip := raw.Unmap()
		if !publicIP(ip) {
			return nil, ErrUnavailable
		}
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	for _, ip := range ips {
		conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
	}
	return nil, ErrUnavailable
}
func publicIP(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, s := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}
func (p *LRCLIB) Search(ctx context.Context, query string) ([]acquired, error) {
	check, e := metadataprovider.BeginNetworkAdmission(ctx)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, p.origin+"/api/search?"+url.Values{"q": {query}}.Encode(), nil)
	if e != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Portico/1.0 (lyrics)")
	if e = p.pace(ctx); e != nil {
		if errors.Is(e, errProviderPaused) {
			return nil, errProviderPaused
		}
		return nil, ErrUnavailable
	}
	if e = check(); e != nil {
		return nil, e
	}
	response, e := p.client.Do(req)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 429 || response.StatusCode == 503 {
		p.refused(response)
		return nil, errProviderPaused
	}
	if response.StatusCode != 200 {
		return nil, ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if e != nil || len(raw) > 4<<20 {
		return nil, ErrUnavailable
	}
	if e = check(); e != nil {
		return nil, e
	}
	var records []struct {
		ID           int64   `json:"id"`
		TrackName    string  `json:"trackName"`
		ArtistName   string  `json:"artistName"`
		AlbumName    string  `json:"albumName"`
		Duration     float64 `json:"duration"`
		Plain        *string `json:"plainLyrics"`
		Synced       *string `json:"syncedLyrics"`
		Instrumental bool    `json:"instrumental"`
	}
	if json.Unmarshal(raw, &records) != nil || len(records) > 100 {
		return nil, ErrUnavailable
	}
	out := []acquired{}
	for _, record := range records {
		if record.ID <= 0 || record.Instrumental {
			continue
		}
		label := record.TrackName + " — " + record.ArtistName
		if record.AlbumName != "" {
			label += " · " + record.AlbumName
		}
		if !boundedText(label, 768) {
			continue
		}
		for _, v := range []struct {
			text   *string
			format string
		}{{record.Synced, "lrc"}, {record.Plain, "text"}} {
			if v.text == nil || strings.TrimSpace(*v.text) == "" {
				continue
			}
			doc, e := Parse([]byte(*v.text), v.format)
			if e != nil {
				continue
			}
			out = append(out, acquired{doc: doc, language: "und", title: record.TrackName, artist: record.ArtistName, duration: record.Duration, provenance: Provenance{Origin: "lrclib", ProviderID: strconv.FormatInt(record.ID, 10), Label: label, Rights: "LRCLIB record; rights and language not supplied by the provider."}})
			if len(out) >= 24 {
				return out, nil
			}
		}
	}
	return out, nil
}
