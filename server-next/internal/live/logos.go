package live

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

// The owner chose the source's own host. Every other logo/redirect destination
// must remain public at the actual dial, through the metadata fetcher's guard.
func logoClient(kind string, cfg LiveSourceConfig) *http.Client {
	origin := cfg.PlaylistURL
	if kind == "xtream" {
		origin = cfg.ServerURL
	}
	if kind == "hdhomerun" {
		origin = cfg.TunerURL
	}
	source, _ := url.Parse(origin)
	own := ""
	if source != nil {
		own = strings.ToLower(strings.TrimSuffix(source.Hostname(), "."))
	}
	transport := fetch.InternetTransport(func(host string) bool { return own == "" || strings.ToLower(strings.TrimSuffix(host, ".")) != own })
	transport.Proxy = nil
	client := newClient(5 * time.Second)
	client.Transport = transport
	return client
}

// refreshLogos runs only after the guide and channel facts are committed. Its
// budget includes reads/writes; unattempted URLs wait for the next refresh.
func (s *Service) refreshLogos(ctx context.Context, src db.LiveSource, cfg LiveSourceConfig, rows []db.SourceChannel, seen map[string]string) {
	if !Document.Get(s.Settings).ImportImages {
		return
	}
	// Refresh serializes each source. Keep recent failures and current pending
	// URLs; URLs removed from a playlist age out after their one-day cooldown.
	s.mu.Lock()
	failed := s.logoFailures[src.PublicID]
	s.mu.Unlock()
	remaining := map[string]time.Time{}
	for address, at := range failed {
		if s.now().Before(at.Add(24 * time.Hour)) {
			remaining[address] = at
		}
	}
	for _, row := range rows {
		if at, ok := failed[row.Logo]; ok && row.BroughtIn && row.LogoHash == "" {
			remaining[row.Logo] = at
		}
	}
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.logoFailures == nil {
			s.logoFailures = map[string]map[string]time.Time{}
		}
		s.logoFailures[src.PublicID] = remaining
	}()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	client := logoClient(src.Kind, cfg)
	defer client.CloseIdleConnections()
	// New URLs go first; yesterday's failures cannot consume their budget.
	for _, retry := range []bool{false, true} {
		for _, row := range rows {
			if ctx.Err() != nil {
				return
			}
			if !Document.Get(s.Settings).ImportImages {
				return
			}
			if !row.BroughtIn || row.Logo == "" || row.LogoHash != "" {
				continue
			}
			at, failedBefore := failed[row.Logo]
			if failedBefore != retry || failedBefore && s.now().Before(at.Add(24*time.Hour)) {
				continue
			}
			hash, tried := seen[row.Logo]
			if !tried {
				var err error
				hash, err = s.channelLogo(ctx, client, row.Logo)
				// An empty entry remembers a failure for this refresh, too.
				seen[row.Logo] = hash
				if err != nil {
					remaining[row.Logo] = s.now()
					s.log.Warn("channel logo unavailable", "source", src.PublicID, "error", err)
				} else {
					delete(remaining, row.Logo)
				}
			}
			if hash != "" {
				if err := s.db.SourceChannelLogo(ctx, src.ID, row.Key, row.Logo, hash, s.now()); err != nil {
					s.log.Warn("storing channel logo", "source", src.PublicID, "error", err)
					return
				}
			}
		}
	}
}

// channelLogo brings a source's picture into the ordinary artwork store. Only
// its content hash leaves the server; an unavailable logo never loses a channel.
func (s *Service) channelLogo(ctx context.Context, client *http.Client, address string) (string, error) {
	if !validLocator(address) {
		return "", fmt.Errorf("invalid logo address")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "Portico")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("the logo address could not be reached")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the picture answered %d", response.StatusCode)
	}
	const limit = 4 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return "", err
	}
	if len(data) > limit {
		return "", fmt.Errorf("the picture is too large")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	if err := s.pictures.Put(hash, data); err != nil {
		return "", err
	}
	return hash, nil
}
