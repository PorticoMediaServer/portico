package httpapi

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/remotegate"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
)

// The settings document is read once per router and replaced synchronously on
// every settings save. Warm request decisions do no settings SQL.
type connectionPolicyCache struct {
	mu      sync.Mutex
	applyMu sync.Mutex
	setting atomic.Pointer[connectionSettingValue]
	// uploadAlert mirrors the upload-budget owner alert (upload_budget.go).
	uploadAlert atomic.Bool
	// gate receives a fresh O-169 policy with every installed document.
	gate       *remotegate.Gate
	envProxies []netip.Prefix
	own        func() []netip.Addr
}

type connectionSettingValue struct {
	secureConnections string
	lanNetworks       []string
	revision          int64
	locality          localityPolicy
	accessURLs        []string
	certificateDomain string
	// apiPerMinute is the owner's rate for one API key or access token.
	apiPerMinute int
	// remoteSignIn is the owner's choice for sign-in from outside the home
	// network: one of the identity.RemoteSignIn* values.
	remoteSignIn string
	// dlna is the owner's DLNA switch (dlna_routes.go).
	dlna bool
}

// set installs a settings document as the live policy. Receipts and
// concurrent save completions can arrive out of order, so a document older
// than the live one is ignored: runtime state never follows a historical
// response.
func (c *connectionPolicyCache) set(document operations.SettingsDocument) {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	c.setLocked(document)
}

// setLocked is set for a caller that already holds applyMu (the settings save
// path holds it across reading the committed document and installing it).
func (c *connectionPolicyCache) setLocked(document operations.SettingsDocument) {
	if current := c.setting.Load(); current != nil && document.Revision < current.revision {
		return
	}
	medialimits.ConfigureSubtitles(document.Effective.SubtitleTrackLimit, document.Effective.SubtitleFileLimitMiB)
	urls := []string{}
	if document.Effective.AdvertiseURL != "" {
		urls = append(urls, document.Effective.AdvertiseURL)
	}
	urls = append(urls, document.Effective.AccessURLs...)
	certificateDomain := ""
	if document.Effective.OwnAddressEnabled {
		certificateDomain = document.Effective.CustomCertificateDomain
	}
	secure := document.Effective.SecureConnections
	if secure == "" {
		secure = "preferred"
	}
	c.setting.Store(&connectionSettingValue{secureConnections: secure, lanNetworks: append([]string{}, document.Effective.LANNetworks...), revision: document.Revision, locality: newLocalityPolicy(document.Effective), accessURLs: urls, certificateDomain: certificateDomain, apiPerMinute: document.Effective.APIRequestsPerMinute, remoteSignIn: remoteSignInPolicy(document.Effective.RemoteSignInPolicy), dlna: document.Effective.DLNAEnabled})
	if c.gate != nil {
		c.gate.Set(remoteGatePolicy(document.Effective, c.envProxies, c.own))
	}
}

// remoteSignInPolicy reads the stored choice. An empty value is the default,
// which allows; a value this build does not know closes remote sign-in rather
// than opening it.
func remoteSignInPolicy(stored string) string {
	switch stored {
	case "", identity.RemoteSignInAllow:
		return identity.RemoteSignInAllow
	case identity.RemoteSignInOwnerOnly:
		return identity.RemoteSignInOwnerOnly
	default:
		return identity.RemoteSignInOff
	}
}

// connectionSetting is the cached projection of the connection rows (secure
// connections and request locality), read once and replaced on every save, so
// per-request decisions do no settings SQL. Nil without a console.
// reloadConnectionPolicy replaces the cached policy (and the remote gate's)
// from the stored settings, for writes that bypass the settings save path
// (setup). A failed read leaves the cache as it was.
func (d Dependencies) reloadConnectionPolicy(ctx context.Context) {
	if d.connectionPolicy == nil || d.Console == nil {
		return
	}
	var document operations.SettingsDocument
	if err := dbwork.WithReadSnapshot(ctx, d.Console.DB, func(ctx context.Context) error {
		var err error
		document, err = d.Console.SettingsTx(ctx, dbwork.Snapshot(ctx), operations.AllowServerScope)
		return err
	}); err == nil {
		d.connectionPolicy.set(document)
	}
}

func (d Dependencies) connectionSetting(ctx context.Context) (*connectionSettingValue, error) {
	if d.connectionPolicy == nil || d.Console == nil {
		return nil, nil
	}
	if setting := d.connectionPolicy.setting.Load(); setting != nil {
		return setting, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.connectionPolicy.mu.Lock()
	defer d.connectionPolicy.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if setting := d.connectionPolicy.setting.Load(); setting != nil {
		return setting, nil
	}
	// Request-entry policy uses stored rows only. The owner settings projection
	// also validates media tools, which must not serialize cold API requests.
	var document operations.SettingsDocument
	err := dbwork.WithReadSnapshot(ctx, d.Console.DB, func(ctx context.Context) error {
		var err error
		document, err = d.Console.SettingsTx(ctx, dbwork.Snapshot(ctx), operations.AllowServerScope)
		return err
	})
	if err != nil {
		return nil, err
	}
	d.connectionPolicy.set(document)
	return d.connectionPolicy.setting.Load(), nil
}

// secureTransport shares the trusted scheme parser with origin admission.
func (d Dependencies) secureTransport(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(networkpolicy.Origin(r, d.trustedProxyPrefixes(r.Context())), "https://")
}
