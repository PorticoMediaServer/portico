package networking

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/directname"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strconv"
	"time"
)

type RemoteConfig struct {
	LANSharing bool   `json:"lanSharing"`
	Revision   int64  `json:"revision,string"`
	Enabled    bool   `json:"enabled"`
	Mapping    bool   `json:"mapping"`
	PCP        bool   `json:"pcp"`
	NATPMP     bool   `json:"natpmp"`
	UPnP       bool   `json:"upnp"`
	PublicPort int    `json:"publicPort"`
	Gateway    string `json:"gateway"`
	// IPv6Open allows Hosted-proven inbound IPv6 by default; false disables it.
	IPv6Open bool `json:"ipv6Open"`
}
type RemoteStatus struct {
	Warnings    []RemoteWarning   `json:"warnings"`
	AuthorityID string            `json:"authorityId"`
	Config      RemoteConfig      `json:"config"`
	Generation  int64             `json:"generation,string"`
	State       string            `json:"state"`
	ErrorCode   string            `json:"errorCode,omitempty"`
	Topology    Topology          `json:"topology"`
	Candidates  []RemoteCandidate `json:"candidates"`
	Mappings    []MappingSummary  `json:"mappings"`
	// IPv6 reports disabled, unverified, or verified after inbound proof.
	IPv6        string     `json:"ipv6,omitempty"`
	CheckedAt   *time.Time `json:"checkedAt,omitempty"`
	NextAttempt *time.Time `json:"nextAttempt,omitempty"`
}
type RemoteCandidate struct {
	BaseURL    string     `json:"baseUrl"`
	Insecure   bool       `json:"insecure"`
	Address    string     `json:"address,omitempty"`
	Port       int        `json:"port,omitempty"`
	Class      string     `json:"class"`
	State      string     `json:"state"`
	VerifiedAt *time.Time `json:"verifiedAt,omitempty"`
}
type MappingSummary struct {
	Protocol     string    `json:"protocol"`
	State        string    `json:"state"`
	ErrorCode    string    `json:"errorCode,omitempty"`
	ExternalPort int       `json:"externalPort"`
	ExpiresAt    time.Time `json:"expiresAt"`
}
type remoteState struct {
	Generation                  int64
	TopologyDigest, RouteDigest string
	NextAttempt                 time.Time
	NotBefore                   time.Time
	Attempts                    int
}

// seedRemoteSettings creates the settings singleton with its signing key. The
// tables come from migration 0040; the key is random, so the row is seeded here.
func seedRemoteSettings(ctx context.Context, db *sql.DB) error {
	var present int
	if e := db.QueryRowContext(ctx, `SELECT count(*) FROM networking_remote_settings WHERE singleton=1`).Scan(&present); e != nil || present != 0 {
		return e
	}
	gated, e := dbwork.Begin(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		return e
	}
	defer clear(key)
	if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO networking_remote_settings(singleton,signature_key,ipv6_open) VALUES(1,?,1)`, key); e != nil {
		return e
	}
	return commitClaimDatabaseTx(ctx, gated)
}
func (m *RemoteManager) snapshot(ctx context.Context) (RemoteConfig, certificateState, string, remoteState, []byte, error) {
	var c RemoteConfig
	var state remoteState
	var saved string
	var key []byte
	gated, e := m.h.store.tx(ctx)
	if e != nil {
		return c, certificateState{}, "", state, key, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	_, cert, authority, e := m.h.certificates.snapshotTx(ctx, tx)
	if e != nil {
		return c, cert, authority, state, key, e
	}
	// The manual_url column is retired: the published access URLs live in the
	// console settings now. The column stays (migrations are never edited), so
	// the SELECT keeps its place with a constant and the value is discarded.
	var discard string
	e = tx.QueryRowContext(ctx, `SELECT authority,revision,enabled,lan_sharing,mapping,pcp,natpmp,upnp,public_port,gateway,'',ipv6_open,signature_key FROM networking_remote_settings WHERE singleton=1`).Scan(&saved, &c.Revision, &c.Enabled, &c.LANSharing, &c.Mapping, &c.PCP, &c.NATPMP, &c.UPnP, &c.PublicPort, &c.Gateway, &discard, &c.IPv6Open, &key)
	if e != nil {
		return c, cert, authority, state, key, e
	}
	if saved != authority {
		// Only an actually installed/acknowledged Portico scope gets the managed
		// route default. Approval or an unclaimed Local scope cannot enable it.
		// Once this scope exists, later snapshots preserve its owner's off choice.
		enabled := false
		if cert.Scope.ID != "" {
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_state s JOIN identity_managed_route m ON m.singleton=s.singleton WHERE s.singleton=1 AND s.sign_in_kind='portico' AND m.enabled=1)`).Scan(&enabled); e != nil {
				return c, cert, authority, state, key, e
			}
		}
		c = RemoteConfig{Revision: c.Revision + 1, Enabled: enabled, PCP: true, NATPMP: true, UPnP: true, PublicPort: 32500, IPv6Open: true}
		if _, e = tx.ExecContext(ctx, `UPDATE networking_remote_settings SET authority=?,revision=?,enabled=?,lan_sharing=0,mapping=0,pcp=1,natpmp=1,upnp=1,public_port=32500,gateway='',manual_url='',ipv6_open=1 WHERE singleton=1`, authority, c.Revision, enabled); e != nil {
			return c, cert, authority, state, key, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE networking_remote_state SET generation=generation+1,topology_digest='',route_digest='',next_attempt=0,attempts=0,status='{}' WHERE singleton=1`); e != nil {
			return c, cert, authority, state, key, e
		}
	}
	var next, notBefore int64
	e = tx.QueryRowContext(ctx, `SELECT generation,topology_digest,route_digest,next_attempt,attempts,retry_after FROM networking_remote_state WHERE singleton=1`).Scan(&state.Generation, &state.TopologyDigest, &state.RouteDigest, &next, &state.Attempts, &notBefore)
	if e != nil {
		return c, cert, authority, state, key, e
	}
	if next > 0 {
		state.NextAttempt = time.UnixMilli(next)
	}
	if notBefore > 0 {
		state.NotBefore = time.UnixMilli(notBefore)
	}
	return c, cert, authority, state, key, commitClaimDatabaseTx(ctx, gated)
}
func (m *RemoteManager) configure(ctx context.Context, authority string, c RemoteConfig) error {
	if c.Revision < 1 || c.PublicPort < 1 || c.PublicPort > 65535 || c.Gateway != "" && !gatewayAddress(c.Gateway) {
		return ErrInvalid
	}
	var e error
	if _, _, protocol := usermutation.RequestFrom(ctx); !protocol {
		old, _, id, _, key, e := m.snapshot(ctx)
		clear(key)
		if e != nil {
			return e
		}
		if authority != id || old.Revision != c.Revision {
			return ErrStale
		}

	}
	gated2, e := m.h.store.tx(ctx)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if e = guardClaimRequestTx(ctx, tx); e != nil {
		return e
	}
	r, e := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if e != nil {
		return e
	}
	if r != nil {
		return nil
	}
	if e = m.currentConfigTx(ctx, tx, authority, c.Revision); e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, `UPDATE networking_remote_settings SET revision=revision+1,enabled=?,lan_sharing=?,mapping=?,pcp=?,natpmp=?,upnp=?,public_port=?,gateway=?,manual_url='',ipv6_open=? WHERE singleton=1 AND authority=? AND revision=?`, c.Enabled, c.LANSharing, c.Mapping, c.PCP, c.NATPMP, c.UPnP, c.PublicPort, c.Gateway, c.IPv6Open, authority, c.Revision)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil || n != 1 {
		return ErrStale
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_remote_state SET generation=generation+1,topology_digest='',route_digest='',next_attempt=0,attempts=0 WHERE singleton=1`); e != nil {
		return e
	}
	if e = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: "remote", Revision: c.Revision + 1}, time.Now().UnixMilli()); e != nil {
		return e
	}
	return commitClaimDatabaseTx(ctx, gated2)
}
func (m *RemoteManager) mappings(ctx context.Context) ([]Mapping, error) {
	rows, e := m.h.store.db.QueryContext(ctx, `SELECT payload FROM networking_mappings ORDER BY expires_at,id LIMIT 32`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Mapping{}
	for rows.Next() {
		var raw string
		var v Mapping
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if len(raw) > 8192 || json.Unmarshal([]byte(raw), &v) != nil {
			return nil, ErrInvalid
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (m *RemoteManager) saveMapping(ctx context.Context, v Mapping) error {
	return m.persistMapping(ctx, v, false)
}
func (m *RemoteManager) saveMappingIntent(ctx context.Context, v Mapping) error {
	return m.persistMapping(ctx, v, true)
}
func (m *RemoteManager) persistMapping(ctx context.Context, v Mapping, current bool) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	gated3, e := m.h.store.tx(ctx)
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if current {
		if e = m.currentConfigTx(ctx, tx, v.Authority, v.Revision); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO networking_mappings(id,authority,protocol,expires_at,next_attempt,payload) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET expires_at=excluded.expires_at,next_attempt=excluded.next_attempt,payload=excluded.payload`, v.ID, v.Authority, v.Protocol, v.ExpiresAt.UnixMilli(), v.NextAttempt.UnixMilli(), string(raw))
	if e != nil {
		return e
	}
	return commitClaimDatabaseTx(ctx, gated3)
}
func (m *RemoteManager) removeMapping(ctx context.Context, id string) error {
	gated4, e := m.h.store.tx(ctx)
	if e != nil {
		return e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	if _, e = tx.ExecContext(ctx, `DELETE FROM networking_mappings WHERE id=?`, id); e != nil {
		return e
	}
	return commitClaimDatabaseTx(ctx, gated4)
}
func (m *RemoteManager) routeName(q certificateState, address string, port int) (string, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil || port < 1 || port > 65535 {
		return "", ErrInvalid
	}
	host := directname.IPHostname(ip, q.Namespace, m.h.certificates.directDomain)
	if host == "" {
		return "", ErrInvalid
	}
	origin := "https://" + host
	if port != 443 {
		origin += ":" + strconv.Itoa(port)
	}
	return origin, nil
}

// Settings alone are not installed-claim authority. They may still contain the
// previous scope until the next observation. Recheck the real claim in the same
// transaction as publication/configuration/pre-mutation intent persistence.
func (m *RemoteManager) currentConfigTx(ctx context.Context, tx *sql.Tx, authority string, revision int64) error {
	_, _, actual, e := m.h.certificates.snapshotTx(ctx, tx)
	if e != nil {
		return e
	}
	if actual != authority {
		return ErrStale
	}
	var saved string
	var current int64
	if e = tx.QueryRowContext(ctx, `SELECT authority,revision FROM networking_remote_settings WHERE singleton=1`).Scan(&saved, &current); e != nil {
		return e
	}
	if saved != authority || current != revision {
		return ErrStale
	}
	return ctx.Err()
}
