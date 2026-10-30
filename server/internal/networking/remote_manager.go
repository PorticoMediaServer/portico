package networking

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net"
	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type RemoteManager struct {
	h              *ClaimHandler
	bind           string
	mapper         GatewayMapper
	wan            *WANObserver
	observe        func(context.Context, string, string, string) (Topology, error)
	advertised     func(context.Context) string
	now            func() time.Time
	networkChanged func()
	wake           chan struct{}
	mu             sync.Mutex
	cancel         context.CancelFunc
	// accessURLs is the console-settings seam wired in cmd/server: the
	// published access URLs plus the custom-certificate domain. A nil function
	// means none. lastAccess is the last good list, kept across a settings
	// read error so a failed read never clears the published routes.
	accessURLsProvider atomic.Pointer[accessURLsFunc]
	accessMu           sync.Mutex
	lastAccess         []string
	// quiescent is set by the step itself: remote access is switched off, or
	// this server has no claim, and so there is nothing for this loop to observe
	// or publish until something changes and wakes it. A thirty-second tick in
	// that state cost a topology observation and a state write every thirty
	// seconds, forever, on every server that does not use remote access — which
	// is most of them.
	quiescent atomic.Bool
	// reprove is a one-shot request (Reprove) that the next route step probe
	// even though the route digest is unchanged and the reproof deadline has
	// not arrived. It is cleared once a step actually reaches the probe.
	reprove atomic.Bool
	// public is this server's own public addresses as the last route step saw
	// them, for request locality (a LAN device that reaches the server through
	// the router's public address). Read without the step's lock.
	public atomic.Pointer[[]netip.Addr]
}

func (h *ClaimHandler) ConfigureRemote(ctx context.Context, bind string, changed func()) error {
	if h == nil || h.certificates == nil {
		return ErrInvalid
	}
	m := &RemoteManager{h: h, bind: bind, mapper: gatewayMapper{}, wan: newWANObserver(gatewayMapper{}), observe: observeTopology, networkChanged: changed, wake: make(chan struct{}, 1)}
	if e := h.runner.Do(ctx, func(ctx context.Context) error { return seedRemoteSettings(ctx, h.store.db) }); e != nil {
		return e
	}
	h.remote = m
	return nil
}
func (h *ClaimHandler) Remote() *RemoteManager {
	if h == nil {
		return nil
	}
	return h.remote
}

// accessURLsFunc reads the published access URLs (the console settings list
// plus the custom-certificate domain, composed by the cmd/server seam). A nil
// return reports a settings read error and keeps the last good list.
type accessURLsFunc func(context.Context) []string

// SetAccessURLs installs the console-settings seam. A nil function means no
// access URLs. It is stored atomically so it can be wired after
// ConfigureRemote and before Run.
func (m *RemoteManager) SetAccessURLs(fn func(context.Context) []string) {
	if m == nil {
		return
	}
	if fn == nil {
		m.accessURLsProvider.Store(nil)
		return
	}
	p := accessURLsFunc(fn)
	m.accessURLsProvider.Store(&p)
}

// PublicPort reports the configured remote public port for published routes,
// defaulting to 32500 when unreadable.
func (m *RemoteManager) PublicPort(ctx context.Context) int {
	if m == nil {
		return 32500
	}
	var port int
	if e := m.h.store.db.QueryRowContext(ctx, `SELECT public_port FROM networking_remote_settings WHERE singleton=1`).Scan(&port); e != nil || port < 1 || port > 65535 {
		return 32500
	}
	return port
}

// accessURLs resolves the published access URLs once per route step:
// the provider's list, deduplicated in order and capped, or the last good
// list when the provider is unset or reports a read error (nil).
func (m *RemoteManager) accessURLs(ctx context.Context) []string {
	if p := m.accessURLsProvider.Load(); p != nil && *p != nil {
		if got := (*p)(ctx); got != nil {
			out := make([]string, 0, len(got))
			seen := map[string]bool{}
			for _, u := range got {
				if u == "" || seen[u] {
					continue
				}
				seen[u] = true
				out = append(out, u)
				if len(out) >= operations.MaxAccessURLs {
					break
				}
			}
			m.accessMu.Lock()
			m.lastAccess = out
			m.accessMu.Unlock()
			return append([]string(nil), out...)
		}
	}
	m.accessMu.Lock()
	defer m.accessMu.Unlock()
	return append([]string(nil), m.lastAccess...)
}

func (m *RemoteManager) Wake() {
	if m == nil {
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Reprove asks for the next route step to re-probe the published candidates
// even when nothing about the routes changed, then wakes the loop. A hold
// (NotBefore) is still honoured. It does not change the generation.
func (m *RemoteManager) Reprove() {
	if m == nil {
		return
	}
	m.reprove.Store(true)
	m.Wake()
}

// SetAdvertisedInterface installs the source for the owner's preferred LAN
// interface (the console's advertisedInterface setting). It is read on every
// observation; an empty value means automatic.
func (m *RemoteManager) SetAdvertisedInterface(source func(context.Context) string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.advertised = source
}

func (m *RemoteManager) advertisedInterface(ctx context.Context) string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	source := m.advertised
	m.mu.Unlock()
	if source == nil {
		return ""
	}
	return source(ctx)
}
func (m *RemoteManager) Invalidate() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.Wake()
}

const routeReproofInterval = 7 * 24 * time.Hour

// remoteActiveInterval is the cadence while remote access is actually in use:
// leases have to be renewed and a changed WAN address has to be noticed.
// remoteQuietInterval is the backstop while there is nothing to observe.
const (
	remoteActiveInterval = 30 * time.Second
	remoteQuietInterval  = 6 * time.Hour
)

func (m *RemoteManager) Run(ctx context.Context) {
	if m == nil {
		return
	}
	m.Wake()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		// Anything that changes what this loop would do calls Wake: enabling
		// remote access, a claim arriving, an interface change, a certificate
		// becoming ready. The interval is the backstop for none of those
		// arriving, which is why the quiet one is long.
		wait := remoteActiveInterval
		if m.quiescent.Load() {
			wait = remoteQuietInterval
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			m.shutdown()
			return
		case <-m.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
		step, cancel := context.WithTimeout(ctx, 45*time.Second)
		m.mu.Lock()
		m.cancel = cancel
		m.mu.Unlock()
		_ = m.h.runner.Do(step, m.step)
		cancel()
		m.mu.Lock()
		m.cancel = nil
		m.mu.Unlock()
	}
}
func (m *RemoteManager) shutdown() {
	// Only remove this worker's persisted mappings. Pending deletes survive an
	// unavailable gateway/restart; every supported grant also has a finite lease.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.h.runner.Do(ctx, func(ctx context.Context) error {
		rows, e := m.mappings(ctx)
		if e != nil {
			return e
		}
		c, _, _, _, key, err := m.snapshot(ctx)
		clear(key)
		if err != nil {
			return err
		}
		t, err := m.observe(ctx, m.bind, c.Gateway, m.advertisedInterface(ctx))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if e = m.cleanupMapping(ctx, row, t); e == nil {
				_ = m.removeMapping(ctx, row.ID)
			}
		}
		return nil
	})
}
func topologyDigest(key []byte, v any) string {
	raw, _ := json.Marshal(v)
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}
func (m *RemoteManager) saveState(ctx context.Context, authority string, c RemoteConfig, state remoteState, status RemoteStatus) error {
	raw, e := json.Marshal(status)
	if e != nil {
		return e
	}
	gated, e := m.h.store.tx(ctx)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = m.currentConfigTx(ctx, tx, authority, c.Revision); e != nil {
		return e
	}
	var actual string
	var revision int64
	if e = tx.QueryRowContext(ctx, `SELECT authority,revision FROM networking_remote_settings WHERE singleton=1`).Scan(&actual, &revision); e != nil {
		return e
	}
	if actual != authority || revision != c.Revision {
		return ErrStale
	}
	var current int64
	if e = tx.QueryRowContext(ctx, `SELECT generation FROM networking_remote_state WHERE singleton=1`).Scan(&current); e != nil {
		return e
	}
	if current > state.Generation {
		return ErrStale
	}
	_, e = tx.ExecContext(ctx, `UPDATE networking_remote_state SET generation=?,topology_digest=?,route_digest=?,next_attempt=?,attempts=?,retry_after=?,status=? WHERE singleton=1`, state.Generation, state.TopologyDigest, state.RouteDigest, state.NextAttempt.UnixMilli(), state.Attempts, state.NotBefore.UnixMilli(), string(raw))
	if e != nil {
		return e
	}
	return commitClaimDatabaseTx(ctx, gated)
}
func (m *RemoteManager) step(ctx context.Context) error {
	var server string
	if e := m.h.store.db.QueryRowContext(ctx, `SELECT server_id FROM networking_claim_identity WHERE singleton=1`).Scan(&server); e != nil {
		return e
	}
	return withRouteMutation(ctx, m.h.store.db, server, func(ctx context.Context) error {
		var current string
		if e := m.h.store.db.QueryRowContext(ctx, `SELECT server_id FROM networking_claim_identity WHERE singleton=1`).Scan(&current); e != nil {
			return e
		}
		if current != server {
			return ErrStale
		}
		return m.stepRouteLocked(ctx)
	})
}

// All observations are refreshed after entering the same mutation lane used by
// certificate route construction and publication. No database writer is
// retained across the Hosted effect.
func (m *RemoteManager) stepRouteLocked(ctx context.Context) error {
	c, q, authority, state, key, e := m.snapshot(ctx)
	if e != nil {
		return e
	}
	defer clear(key)
	// Recorded before anything else so the loop knows how long to sleep even if
	// this pass fails: a server with remote access off has nothing here to poll
	// for, and every path that turns it back on calls Wake.
	m.quiescent.Store(!c.Enabled || q.Scope.ID == "")
	cert, e := m.h.certificates.Status(ctx)
	if e != nil {
		return e
	}
	if cert.AuthorityID != authority {
		return ErrStale
	}
	topo, e := m.observe(ctx, m.bind, c.Gateway, m.advertisedInterface(ctx))
	if e != nil {
		return e
	}
	status := RemoteStatus{AuthorityID: authority, Config: c, Generation: state.Generation, State: "lan_only", Topology: topo, Candidates: []RemoteCandidate{}, Mappings: []MappingSummary{}}
	_, p, _ := net.SplitHostPort(m.bind)
	port, _ := strconv.Atoi(p)
	now := time.Now().UTC()
	if m.now != nil {
		now = m.now().UTC()
	}
	status.CheckedAt = &now
	digest := topologyDigest(key, struct {
		Topology  Topology
		Authority string
		Revision  int64
		TLS       bool
	}{topo, authority, c.Revision, cert.TLSReady && cert.PubliclyTrusted && cert.ListenerBound})
	topologyChanged := digest != state.TopologyDigest
	if topologyChanged {
		// A changed topology is a reason to look, not a reason to call Hosted. It used to
		// clear the retry deadline, which forced a full PublishRoutes and a Hosted dial-back
		// proof even when nothing Hosted holds had moved — a renumbered gateway, a TLS
		// readiness flag flipping, the bind address changing. What Hosted holds is the route
		// digest below, and a change to that clears the deadline on its own. The mapping
		// identity still moves with the topology, and losing a mapping does change the route
		// digest, so a topology change that matters still publishes at once.
		state.TopologyDigest = digest
		if m.networkChanged != nil {
			m.networkChanged()
		}
	}
	// Behind NAT the WAN address is detected, locally where the router will say and otherwise
	// from public address services. Only a change reaches Hosted. The observer is IPv4-only,
	// so it is consulted whenever no interface already carries a routable IPv4 — a host with a
	// global IPv6 and a NAT'd IPv4 used to be skipped entirely and never learned its IPv4.
	wanSource := ""
	freshWAN := ""
	if c.Enabled && q.Scope.ID != "" {
		var ip netip.Addr
		if hasPublicIPv4(topo) {
			// A directly bound public interface is an address observation, not a
			// reachability proof. Feed the ordinary certificate route namer too;
			// otherwise its route tuple stays empty on a non-NAT host forever.
			ip = directCertificateIPv4(topo, cert.Config.PublicAddress)
		} else {
			ip, wanSource = m.wan.Observe(ctx, topo, port, topologyChanged)
			if ip.IsValid() && WANSourceFresh(wanSource) {
				freshWAN = ip.String()
			} else {
				ip = netip.Addr{}
			}
		}
		if ip.IsValid() && ip.String() != cert.Config.PublicAddress {
			if changed, err := m.h.certificates.observedPublicAddress(ctx, ip.String()); err == nil && changed {
				cert.Config.PublicAddress = ip.String()
				state.NextAttempt = time.Time{}
			}
		}
	}
	lanTLSReady := cert.TLSReady && cert.PubliclyTrusted && cert.ListenerBound
	access := m.accessURLs(ctx)
	// A valid certificate can precede its listener binding during boot. This
	// local startup gap is not a changed route or an owner withdrawal. Preserve
	// the durable candidates and mapping leases until the listener is ready.
	if cert.TLSReady && cert.PubliclyTrusted && !cert.ListenerBound && cert.Config.Enabled && (c.Enabled || c.LANSharing) {
		var raw string
		if err := m.h.store.db.QueryRowContext(ctx, `SELECT status FROM networking_remote_state WHERE singleton=1`).Scan(&raw); err != nil {
			return err
		}
		var old RemoteStatus
		if json.Unmarshal([]byte(raw), &old) == nil && old.AuthorityID == authority && old.Generation == state.Generation && old.Config == c {
			manual := []string{}
			automatic := false
			for _, candidate := range old.Candidates {
				if candidate.Class == "manual" {
					manual = append(manual, candidate.BaseURL)
				} else {
					automatic = true
				}
			}
			if automatic && strings.Join(manual, "\x00") == strings.Join(access, "\x00") {
				old.CheckedAt = &now
				old.Warnings = remoteWarnings(c.PublicPort, access)
				return m.saveState(ctx, authority, c, state, old)
			}
		}
	}

	lanCandidates := []RemoteCandidate{}
	for _, raw := range topo.LAN {
		if !lanTLSReady || !c.Enabled && !c.LANSharing {
			break
		}
		ip, err := netip.ParseAddr(raw)
		if err == nil {
			origin, err := m.routeName(q, ip.String(), port)
			if err == nil {
				lanCandidates = append(lanCandidates, RemoteCandidate{BaseURL: origin, Class: "lan", State: "probe_required", Address: ip.String(), Port: port})
			}
		}
	}
	ready := lanTLSReady && q.Scope.ID != ""
	mappings, mapError, e := m.reconcileMappings(ctx, c, authority, digest, topo, port, c.Enabled && c.Mapping && ready)
	if e != nil {
		return e
	}
	pinholes, e := m.reconcilePinholes(ctx, c, authority, digest, topo, port, c.Enabled && c.Mapping && c.UPnP && ready, now)
	if e != nil {
		return e
	}
	mappings = append(mappings, pinholes...)
	addresses := map[string]int{}
	observed := map[string]int{}
	// Locality observes our real public interfaces even when automatic remote
	// publication is disabled or TLS is still loading. Saved WAN hints are not
	// fresh observations.
	for _, raw := range topo.Public {
		if ip, err := netip.ParseAddr(raw); err == nil && publicAddress(ip) {
			observed[ip.String()] = port
		}
	}
	if freshWAN != "" {
		observed[freshWAN] = cert.Config.PublicPort
	}

	if c.Enabled && ready {
		for _, raw := range topo.Public {
			// Every allowed family is offered for an exact inbound proof.
			// Hosted signs it only after that proof succeeds.
			if ip, err := netip.ParseAddr(raw); err == nil && ip.Is6() && !ip.Is4In6() && !c.IPv6Open {
				status.IPv6 = "disabled"
				continue
			}
			addresses[raw] = port
			observed[raw] = port
		}
		if freshWAN != "" {
			observed[freshWAN] = cert.Config.PublicPort
		}
		if cert.Config.PublicAddress != "" {
			ip, err := netip.ParseAddr(cert.Config.PublicAddress)
			if err == nil && publicAddress(ip) && (ip.Is4() || c.IPv6Open) {
				addresses[ip.String()] = cert.Config.PublicPort
			}
		}
	}
	if c.IPv6Open && hasPublicIPv6(topo) {
		status.IPv6 = "unverified"
	}
	for _, mapping := range mappings {
		status.Mappings = append(status.Mappings, MappingSummary{mapping.Protocol, mapping.State, mapping.ErrorCode, mapping.ExternalPort, mapping.ExpiresAt})
		if mapping.Protocol == pinholeProtocol && c.Enabled && ready && mapping.State == "mapped" && now.Before(mapping.ExpiresAt) && c.IPv6Open {
			if mapping.FirewallOpen {
				status.IPv6 = "firewall_open"
			} else {
				status.IPv6 = "pinhole"
			}
		}
		if c.Enabled && ready && mapping.Authority == authority && mapping.Topology == digest && mapping.State == "mapped" && now.Before(mapping.ExpiresAt) {
			ip, err := netip.ParseAddr(mapping.ExternalAddress)
			if err == nil && publicAddress(ip) && (ip.Is4() || c.IPv6Open) {
				addresses[ip.String()] = mapping.ExternalPort
				observed[ip.String()] = mapping.ExternalPort
			} else {
				mapError = "cgnat_or_double_nat"
			}
		}
	}
	m.recordPublic(observed)
	status.ErrorCode = mapError
	// The published access URLs are resolved once per route step (the loop is
	// already rate-limited); a settings read error keeps the last good list.
	status.Warnings = remoteWarnings(c.PublicPort, access)
	if !c.Enabled {
		status.State = "disabled"
		status.ErrorCode = ""
	} else if q.Scope.ID == "" {
		status.State = "claim_required"
	} else if !ready && len(access) == 0 {
		status.State = "certificate_pending"
		status.ErrorCode = cert.ErrorCode
	} else {
		status.State = "remote_unavailable"
	}
	if topo.ErrorCode == "listener_loopback_only" {
		status.ErrorCode = topo.ErrorCode
	}
	if q.Scope.ID != "" {
		for _, u := range access {
			status.Candidates = append(status.Candidates, RemoteCandidate{BaseURL: u, Class: "manual", State: "probe_required", Insecure: strings.HasPrefix(u, "http://")})
		}
	}
	// Names encode each literal address locally. A WAN transition
	// reuses the existing wildcard and never initiates another certificate order.
	addressKeys := make([]string, 0, len(addresses))
	for raw := range addresses {
		addressKeys = append(addressKeys, raw)
	}
	sort.Strings(addressKeys)
	var nameErr error
	for _, raw := range addressKeys {
		if len(status.Candidates) >= maxPublishedCandidates {
			break
		}
		origin, err := m.routeName(q, raw, addresses[raw])
		if err != nil {
			status.ErrorCode = "route_name_retry"
			nameErr = err
			continue
		}
		duplicate := false
		for _, candidate := range status.Candidates {
			if candidate.BaseURL == origin {
				duplicate = true
			}
		}
		if !duplicate {
			status.Candidates = append(status.Candidates, RemoteCandidate{BaseURL: origin, Class: "public", State: "probe_required", Address: raw, Port: addresses[raw]})
		}
	}
	// Manual addresses retain owner order, followed by public and LAN routes.
	// One budget applies to all classes, so Hosted never rejects the entire set.
	status.Candidates = boundedRouteCandidates(append(status.Candidates, lanCandidates...))
	observation := RouteObservation{RemoteEnabled: c.Enabled, CGNAT: status.ErrorCode == "cgnat_or_double_nat", Candidates: []PublishedCandidate{}}
	for _, candidate := range status.Candidates {
		observation.Candidates = append(observation.Candidates, PublishedCandidate{BaseURL: candidate.BaseURL, Class: candidate.Class, Insecure: candidate.Insecure, Address: candidate.Address, Port: candidate.Port})
	}
	epochs := map[string]uint64{}
	for _, mapping := range mappings {
		if mapping.State == "mapped" {
			epochs[mapping.ID] = mapping.EpochGeneration
		}
	}
	routeDigest := topologyDigest(key, struct {
		Observation RouteObservation
		Epochs      map[string]uint64
	}{observation, epochs})
	changed := state.RouteDigest != routeDigest
	if changed {
		state.Generation++
		state.RouteDigest = routeDigest
		state.NextAttempt = time.Time{}
		state.Attempts = 0
	}
	status.Generation = state.Generation
	observation.Generation = state.Generation
	if q.Scope.ID == "" {
		return m.saveState(ctx, authority, c, state, status)
	}
	// Healthy idle ticks perform only local observations/lease checks. Keep the
	// last proof visible until its bounded renewal, not a fake success per tick.
	if now.Before(state.NotBefore) || (!changed && now.Before(state.NextAttempt) && !m.reprove.Load()) {
		var raw string
		_ = m.h.store.db.QueryRowContext(ctx, `SELECT status FROM networking_remote_state WHERE singleton=1`).Scan(&raw)
		var old RemoteStatus
		if json.Unmarshal([]byte(raw), &old) == nil && old.AuthorityID == authority && old.Generation == state.Generation {
			status.Candidates = old.Candidates
			status.State = old.State
			status.ErrorCode = old.ErrorCode
		}
		status.NextAttempt = &state.NextAttempt
		return m.saveState(ctx, authority, c, state, status)
	}
	m.reprove.Store(false)
	// Persist the exact generation before the remote effect. A lost response is
	// retried with this same generation+digest; it cannot create a second topology.
	if e = m.saveState(ctx, authority, c, state, status); e != nil {
		return e
	}
	var receipt struct {
		Generation int64 `json:"generation,string"`
	}
	caller, ok := m.h.transport.(endpointCaller)
	if !ok {
		return ErrUnavailable
	}
	e = caller.CallServer(ctx, q.Scope.Intent, PublishRoutes, observation, &receipt)
	if e == nil && receipt.Generation != state.Generation {
		e = ErrInvalid
	}
	anyPublic, verified := false, false
	hasManual := false
	if e == nil {
		for index, candidate := range status.Candidates {
			// Members' apps test manual routes themselves; Hosted cannot reach
			// a VPN or private proxy, so manual routes are published but never
			// dial-back proven — exactly like LAN routes.
			if candidate.Class == "manual" {
				hasManual = true
				continue
			}
			if candidate.Class == "lan" {
				continue
			}
			anyPublic = true
			proof, err := m.h.proveRoute(ctx, q.Scope.Intent, state.Generation, candidate.BaseURL)
			if err == nil {
				status.Candidates[index].State = "reachable"
				status.Candidates[index].VerifiedAt = &proof.VerifiedAt
				if strings.Contains(candidate.Address, ":") {
					status.IPv6 = "verified"
				}
				verified = true
			} else {
				status.Candidates[index].State = "probe_required"
				e = err
			}
		}
	}
	if e == nil && nameErr != nil {
		e = nameErr
	}
	if verified {
		status.State = "reachable"
		status.ErrorCode = ""
	} else if e != nil {
		status.ErrorCode = "external_proof_or_hosted_unavailable"
		if !anyPublic {
			status.State = "hosted_unavailable"
		}
	} else if hasManual && !anyPublic {
		// Only manual routes: nothing was proven and there is nothing to
		// retry — the routes stay published with no error code and the normal
		// weekly reproof deadline, not a retry backoff.
		status.State = "manual_only"
		status.ErrorCode = ""
	} else if c.Enabled && len(addresses) == 0 && len(access) == 0 && ready && status.ErrorCode == "" {
		status.ErrorCode = "no_public_route"
	}
	if e != nil {
		state.Attempts++
		if state.Attempts > 6 {
			state.Attempts = 6
		}
		retry := ControlRetryAt(e)
		state.NextAttempt = RetryAt(now, 15*time.Second, state.Attempts, 6, retry)
		if retry.After(now) {
			state.NotBefore = state.NextAttempt
		}
	} else {
		state.Attempts = 0
		// Unchanged routes are re-proved weekly: enough to keep the owner's reachability status
		// honest without Hosted dialling every server every half hour. Any change publishes at once.
		state.NextAttempt = now.Add(routeReproofInterval + time.Duration(rand.IntN(6*3600))*time.Second)
	}
	status.NextAttempt = &state.NextAttempt
	if saved := m.saveState(ctx, authority, c, state, status); saved != nil {
		return saved
	}
	return e
}
func (m *RemoteManager) reconcileMappings(ctx context.Context, c RemoteConfig, authority, topology string, t Topology, port int, enabled bool) ([]Mapping, string, error) {
	all, e := m.mappings(ctx)
	if e != nil {
		return nil, "", e
	}
	// IPv6 pinholes are reconciled separately (reconcilePinholes).
	rows := []Mapping{}
	for _, row := range all {
		if row.Protocol != pinholeProtocol {
			rows = append(rows, row)
		}
	}
	now := time.Now()
	kept := []Mapping{}
	chosen := map[string]bool{"pcp": c.PCP, "natpmp": c.NATPMP, "upnp": c.UPnP}
	for _, row := range rows {
		valid := row.State != "cleanup_pending" && !BrowserRestrictedPort(row.ExternalPort) && enabled && chosen[row.Protocol] && row.Authority == authority && row.Topology == topology && row.Revision == c.Revision && row.Gateway == t.Gateway && row.Client == t.LocalAddress
		if !valid {
			expiry := row.ExpiresAt
			if row.PotentialUntil.After(expiry) {
				expiry = row.PotentialUntil
			}
			if !row.MutationPending && !now.Before(expiry) {
				if e = m.removeMapping(ctx, row.ID); e != nil {
					return nil, "", e
				}
				continue
			}
			if row.State != "cleanup_pending" || !now.Before(row.NextAttempt) {
				err := m.cleanupMapping(ctx, row, t)
				if err == nil || errors.Is(err, errMappingConflict) {
					if e = m.removeMapping(ctx, row.ID); e != nil {
						return nil, "", e
					}
					continue
				}
				row.State = "cleanup_pending"
				row.ErrorCode = "mapping_cleanup_pending"
				row.NextAttempt = now.Add(time.Minute)
				if e = m.saveMapping(ctx, row); e != nil {
					return nil, "", e
				}
			}
			kept = append(kept, row)
			continue
		}
		kept = append(kept, row)
	}
	if !enabled {
		return kept, "", nil
	}
	if t.Gateway == "" || t.LocalAddress == "" {
		return kept, t.ErrorCode, nil
	}
	// A pending cleanup owns its gateway tuple until removed/expired. A new
	// nonce/protocol must not race it, particularly NAT-PMP's nonce-less delete.
	for _, row := range kept {
		if row.Gateway == t.Gateway && row.Client == t.LocalAddress && row.InternalPort == port && row.State == "cleanup_pending" {
			return kept, "mapping_cleanup_pending", nil
		}
	}
	preferred := ""
	for _, row := range kept {
		if row.Authority == authority && row.Topology == topology && row.Revision == c.Revision &&
			(row.MutationPending || row.State == "mapped" && now.Before(row.ExpiresAt)) {
			preferred = row.Protocol
			break
		}
	}
	code := "mapping_unsupported"
	for _, protocol := range []string{"pcp", "natpmp", "upnp"} {
		if !chosen[protocol] || preferred != "" && protocol != preferred {
			continue
		}
		var row Mapping
		found := false
		for _, v := range kept {
			if v.Authority == authority && v.Topology == topology && v.Revision == c.Revision && v.Protocol == protocol {
				row = v
				found = true
				break
			}
		}
		if found && now.Before(row.NextAttempt) {
			if row.State == "mapped" && now.Before(row.ExpiresAt) {
				return kept, "", nil
			}
			code = row.ErrorCode
			continue
		}
		if !found {
			if len(kept) >= 12 {
				return kept, "mapping_cleanup_pending", nil
			}
			row = Mapping{ID: hex.EncodeToString([]byte(mappingNonce())), Authority: authority, Topology: topology, Network: mappingNetwork(t), Revision: c.Revision, Protocol: protocol, Gateway: t.Gateway, Client: t.LocalAddress, InternalPort: port, ExternalPort: automaticMappingPort(c.PublicPort), Nonce: mappingNonce(), State: "preparing"}
			row.Description = "Portico-" + row.ID[:16]
		}
		beforeAttempt := row
		prepared, err := m.mapper.Prepare(ctx, row)
		row = prepared
		if err == nil {
			row.MutationPending = true
			row.PotentialUntil = time.Now().Add(mappingLifetime * time.Second)
			if row.State != "mapped" || !now.Before(row.ExpiresAt) {
				row.State = "pending"
				row.ExpiresAt = row.PotentialUntil
			}
			row.NextAttempt = now.Add(2 * time.Minute)
			// Receipt before UDP/SOAP mutation, including UPnP control URL and PCP nonce.
			if e = m.saveMappingIntent(ctx, row); e != nil {
				return nil, "", e
			}
			prepared, err = m.mapper.Map(ctx, row, mappingLifetime)
			row = prepared // Preserve actual grant/epoch even on an unsupported lease.
			if err == nil && BrowserRestrictedPort(row.ExternalPort) {
				// Retain the actual grant for ownership-safe cleanup; never publish it.
				row.State = "cleanup_pending"
				row.NextAttempt = now
				err = errMappingBrowserPort
			}
			if errors.Is(err, errMappingUnsupported) || errors.Is(err, errMappingDenied) || errors.Is(err, errMappingConflict) {
				row.MutationPending = beforeAttempt.MutationPending
				row.PotentialUntil = beforeAttempt.PotentialUntil
				row.ExpiresAt = beforeAttempt.ExpiresAt
				// A rejected renewal does not erase the previous successful
				// or ambiguous effect. Retire it before trying another protocol.
				if row.MutationPending || now.Before(row.PotentialUntil) {
					row.State = "cleanup_pending"
				} else {
					row.State = "failed"
				}
			}
		}
		if err != nil {
			row.ErrorCode = mappingError(err)
			code = row.ErrorCode
			// A88: a router without this protocol answers the same way every
			// time. Each consecutive failure doubles the wait (2 minutes up to a
			// day); a new topology or settings revision is a new row, probed at
			// once.
			row.Failures++
			row.NextAttempt = now.Add(failureBackoff(2*time.Minute, row.Failures))
			if row.ExpiresAt.IsZero() {
				row.ExpiresAt = now.Add(mappingLifetime * time.Second)
			}
		} else {
			row.Failures = 0
		}
		if e = m.saveMapping(ctx, row); e != nil {
			return nil, "", e
		}
		replaced := false
		for i, v := range kept {
			if v.ID == row.ID {
				kept[i] = row
				replaced = true
			}
		}
		if !replaced {
			kept = append(kept, row)
		}
		if err == nil {
			return kept, "", nil
		}
		if row.MutationPending || row.State == "cleanup_pending" {
			return kept, code, nil // Recover the same request, not a competing mapping.
		}
	}
	return kept, code, nil
}

func mappingNetwork(t Topology) string {
	raw, _ := json.Marshal(t)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func (m *RemoteManager) cleanupMapping(ctx context.Context, row Mapping, t Topology) error {
	// Prepare failures never owned a lease. Do not turn their expiry placeholder
	// into authority to delete another program's NAT-PMP mapping.
	if row.PotentialUntil.IsZero() && !row.MutationPending {
		return nil
	}
	if row.Protocol == "natpmp" {
		// NAT-PMP has no nonce/query ownership proof. Do not replay a deletion on
		// another LAN or after router epoch loss; let the known finite lease age.
		if row.Network == "" || row.Network != mappingNetwork(t) || row.Gateway != t.Gateway || row.Client != t.LocalAddress {
			return ErrUnavailable
		}
		current, err := m.mapper.Prepare(ctx, row)
		if err != nil {
			return err
		}
		if current.EpochGeneration != row.EpochGeneration {
			return nil
		}
	}
	_, err := m.mapper.Map(ctx, row, 0)
	return err
}

func hasPublicIPv6(t Topology) bool {
	for _, raw := range t.Public {
		if ip, e := netip.ParseAddr(raw); e == nil && ip.Is6() && !ip.Is4In6() {
			return true
		}
	}
	return false
}

// maxPinholes bounds the IPv6 addresses the server asks the router to admit
// (a host can carry several temporary/stable global addresses).
const maxPinholes = 2

// reconcilePinholes keeps one IPv6 pinhole per published global IPv6 address
// while remote access and UPnP are enabled, renews it at half its lease, and
// removes pinholes for addresses the server no longer has.
func (m *RemoteManager) reconcilePinholes(ctx context.Context, c RemoteConfig, authority, topology string, t Topology, port int, enabled bool, now time.Time) ([]Mapping, error) {
	all, e := m.mappings(ctx)
	if e != nil {
		return nil, e
	}
	wanted := map[string]bool{}
	if enabled && c.IPv6Open && t.Gateway != "" && t.LocalAddress != "" {
		for _, raw := range t.Public {
			if ip, err := netip.ParseAddr(raw); err == nil && ip.Is6() && !ip.Is4In6() && len(wanted) < maxPinholes {
				wanted[ip.String()] = true
			}
		}
	}
	kept := []Mapping{}
	have := map[string]bool{}
	for _, row := range all {
		if row.Protocol != pinholeProtocol {
			continue
		}
		valid := wanted[row.PinholeClient] && row.State != "cleanup_pending" && row.Authority == authority && row.Revision == c.Revision && row.Gateway == t.Gateway && row.Client == t.LocalAddress && row.InternalPort == port
		if !valid {
			if !row.MutationPending && (row.PinholeID == "" && !row.FirewallOpen || !now.Before(row.ExpiresAt)) {
				if e = m.removeMapping(ctx, row.ID); e != nil {
					return nil, e
				}
				continue
			}
			// Close a pinhole the server no longer wants at once; only a
			// failed close waits for its retry time.
			if row.State != "cleanup_pending" || !now.Before(row.NextAttempt) {
				if _, err := m.mapper.Map(ctx, row, 0); err == nil {
					if e = m.removeMapping(ctx, row.ID); e != nil {
						return nil, e
					}
					continue
				}
				row.State = "cleanup_pending"
				row.ErrorCode = "mapping_cleanup_pending"
				row.NextAttempt = now.Add(time.Minute)
				if e = m.saveMapping(ctx, row); e != nil {
					return nil, e
				}
			}
			kept = append(kept, row)
			continue
		}
		have[row.PinholeClient] = true
		if row.Topology != topology {
			// A pinhole names an address and port, not the whole topology.
			row.Topology = topology
			if e = m.saveMapping(ctx, row); e != nil {
				return nil, e
			}
		}
		if now.Before(row.NextAttempt) {
			kept = append(kept, row)
			continue
		}
		row, e = m.mapPinhole(ctx, row, now)
		if e != nil {
			return nil, e
		}
		kept = append(kept, row)
	}
	for address := range wanted {
		if have[address] {
			continue
		}
		row := Mapping{ID: hex.EncodeToString([]byte(mappingNonce())), Authority: authority, Topology: topology, Network: mappingNetwork(t), Revision: c.Revision, Protocol: pinholeProtocol, Gateway: t.Gateway, Client: t.LocalAddress, PinholeClient: address, InternalPort: port, ExternalPort: port, Nonce: mappingNonce(), State: "preparing"}
		row.Description = "Portico-" + row.ID[:16]
		row, e = m.mapPinhole(ctx, row, now)
		if e != nil {
			return nil, e
		}
		kept = append(kept, row)
	}
	return kept, nil
}

// pinholeBackoff is 5 minutes doubled per consecutive failure, capped at 24
// hours, with up to 10% jitter.
func pinholeBackoff(failures int) time.Duration { return failureBackoff(5*time.Minute, failures) }

// failureBackoff is base doubled per consecutive failure after the first,
// capped at 24 hours, with up to 10% jitter.
func failureBackoff(base time.Duration, failures int) time.Duration {
	wait := base
	for i := 1; i < failures && wait < 24*time.Hour; i++ {
		wait *= 2
	}
	wait = min(wait, 24*time.Hour)
	return wait + time.Duration(rand.Int64N(int64(wait/10)+1))
}

// mapPinhole prepares (discovers the firewall service) and opens or renews one
// pinhole, recording the intent before the gateway call like the NAT mapping.
func (m *RemoteManager) mapPinhole(ctx context.Context, row Mapping, now time.Time) (Mapping, error) {
	prepared, err := m.mapper.Prepare(ctx, row)
	if err == nil {
		row = prepared
		row.MutationPending = true
		row.PotentialUntil = now.Add(mappingLifetime * time.Second)
		if e := m.saveMappingIntent(ctx, row); e != nil {
			return row, e
		}
		row, err = m.mapper.Map(ctx, row, mappingLifetime)
	}
	if err != nil {
		row.ErrorCode = mappingError(err)
		if row.PinholeID == "" && !row.FirewallOpen {
			row.MutationPending = false
			row.State = "failed"
		}
		// A84: a router without IPv6 firewall control answers the same way every
		// time. Each failure doubles the wait (5 minutes up to a day), so an idle
		// server stops probing. A new address, gateway or setting revision is a
		// new row and is probed at once.
		row.Failures++
		row.NextAttempt = now.Add(pinholeBackoff(row.Failures))
		if row.ExpiresAt.IsZero() {
			row.ExpiresAt = now.Add(mappingLifetime * time.Second)
		}
	} else {
		row.Failures = 0
	}
	return row, m.saveMapping(ctx, row)
}
