package livechannels

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// RemoteConfiguration is the allow-listed owner projection of private acquisition
// state. Credential pointers are input only: nil preserves, empty clears.
type RemoteConfiguration struct {
	Kind                  string   `json:"kind"`
	GuideURL              string   `json:"guideUrl"`
	GuideURLRedacted      bool     `json:"guideUrlRedacted"`
	RefreshMs             int      `json:"refreshMs"`
	OwnerLimit            int      `json:"ownerLimit"`
	UseDiscoveredCapacity bool     `json:"useDiscoveredCapacity"`
	Username              *string  `json:"username,omitempty"`
	Password              *string  `json:"password,omitempty"`
	ConfirmedLANRoots     []string `json:"confirmedLanRoots,omitempty"`
}

func readRemoteConfiguration(ctx context.Context, tx *sql.Tx, id string) (map[string]json.RawMessage, map[string]json.RawMessage, remoteConfig, error) {
	var raw []byte
	var c remoteConfig
	if err := tx.QueryRowContext(ctx, `SELECT sealed FROM live_remote_configs WHERE source_id=?`, id).Scan(&raw); err != nil {
		return nil, nil, c, err
	}
	var root, draft map[string]json.RawMessage
	if json.Unmarshal(raw, &c) != nil || json.Unmarshal(raw, &root) != nil || json.Unmarshal(root["draft"], &draft) != nil || root == nil || draft == nil {
		return nil, nil, c, ErrUnavailable
	}
	return root, draft, c, nil
}

// ReadPersistedConfigurationTx never serializes the private draft, approvals,
// credentials, mappings, locator, or unknown provider-specific fields.
func ReadPersistedConfigurationTx(ctx context.Context, tx *sql.Tx, id string, v *LiveSourceSettings) error {
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM live_sources WHERE id=?`, id).Scan(&state); err != nil {
		return err
	}
	enabled := state == "active"
	v.Enabled = &enabled
	access := "owner-only"
	err := tx.QueryRowContext(ctx, `SELECT viewer_access FROM live_source_settings WHERE source_id=?`, id).Scan(&access)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	v.ViewerAccess = &access
	_, _, c, err := readRemoteConfiguration(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	v.Remote = publicRemoteConfiguration(c)
	return nil
}

func publicRemoteConfiguration(c remoteConfig) *RemoteConfiguration {
	guide := c.Draft.GuideURL
	// URL query values can contain provider tokens. They are write-only too.
	if u, e := url.Parse(guide); e == nil {
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		guide = u.String()
	} else {
		guide = ""
	}
	if c.PrivateGuideURL || guidePathHasCredential(c.Draft.GuideURL, c.Draft.Username, c.Draft.Password) {
		guide = ""
	}
	return &RemoteConfiguration{Kind: c.Draft.Kind, GuideURL: guide, GuideURLRedacted: guide != c.Draft.GuideURL, RefreshMs: c.Draft.RefreshMs, OwnerLimit: c.Draft.OwnerLimit, UseDiscoveredCapacity: c.Draft.UseDiscoveredCapacity}
}

func guidePathHasCredential(raw string, secrets ...string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	for _, secret := range secrets {
		if secret != "" && (strings.Contains(u.Path, secret) || strings.Contains(u.EscapedPath(), secret) || strings.Contains(u.Host, secret)) {
			return true
		}
	}
	return false
}

// PersistedConfigurationSnapshot captures the private policy inputs in the
// caller's authorized read snapshot. It must be prepared after that snapshot closes.
type PersistedConfigurationSnapshot struct {
	sealed []byte
}

// ReadPersistedConfigurationSnapshotTx copies the sealed policy inputs without
// keeping the transaction or its connection alive during network admission.
func ReadPersistedConfigurationSnapshotTx(ctx context.Context, tx *sql.Tx, id string) (*PersistedConfigurationSnapshot, error) {
	var snapshot PersistedConfigurationSnapshot
	if err := tx.QueryRowContext(ctx, `SELECT sealed FROM live_remote_configs WHERE source_id=?`, id).Scan(&snapshot.sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalid
		}
		return nil, err
	}
	return &snapshot, nil
}

// PreparedPersistedConfiguration contains policy-checked inputs tied to the
// exact sealed configuration that supplied the private URL and approvals.
type PreparedPersistedConfiguration struct {
	sealed    []byte
	guide     string
	approvals []remotemedia.Approval
}

// Prepare runs the same network admission as remote preview/save, with no SQL
// transaction held. SavePersistedConfigurationTx fences these inputs before use.
func (snapshot *PersistedConfigurationSnapshot) Prepare(ctx context.Context, v *LiveSourceSettings) (*PreparedPersistedConfiguration, error) {
	if v.Remote == nil {
		return nil, nil
	}
	if snapshot == nil {
		return nil, ErrInvalid
	}
	var c remoteConfig
	if json.Unmarshal(snapshot.sealed, &c) != nil {
		return nil, ErrUnavailable
	}
	r := v.Remote
	if r.Kind != c.Draft.Kind || r.RefreshMs < 300000 || r.RefreshMs > 86400000 || r.OwnerLimit < 0 || r.OwnerLimit > 256 || len(r.ConfirmedLANRoots) > 8 {
		return nil, ErrInvalid
	}
	for _, entry := range []struct {
		value *string
		max   int
	}{{r.Username, 512}, {r.Password, 2048}} {
		if entry.value != nil && (*entry.value != "" && !validText(*entry.value, entry.max)) {
			return nil, ErrInvalid
		}
	}
	if c.Draft.Kind != "xtream" && (r.Username != nil || r.Password != nil) {
		return nil, ErrInvalid
	}
	approvals := c.Approvals
	policy := remotemedia.Policy{Approvals: approvals}
	for _, root := range r.ConfirmedLANRoots {
		approval, e := policy.Approve(ctx, root)
		if e != nil {
			return nil, sourceFetchError(e)
		}
		approvals = append(approvals, approval)
	}
	// A redacted address returned by GET is a placeholder for the private URL.
	// Clearing/replacing it is explicit: clients clear this flag on an edit.
	guide := r.GuideURL
	if r.GuideURLRedacted {
		if guide != publicRemoteConfiguration(c).GuideURL {
			return nil, ErrInvalid
		}
		guide = c.Draft.GuideURL
	}
	if guide != c.Draft.GuideURL && guide != "" {
		if !validLocator(guide) {
			return nil, ErrInvalid
		}
		// New is the exact admission and pinned network policy used by ReadSource;
		// it resolves/adopts the endpoint without issuing an extra HTTP request.
		client, e := remotemedia.New(ctx, guide, remotemedia.Policy{Approvals: approvals})
		if e != nil {
			return nil, sourceFetchError(e)
		}
		client.Close()
	}
	return &PreparedPersistedConfiguration{sealed: snapshot.sealed, guide: guide, approvals: approvals}, nil
}

// SavePersistedConfigurationTx shares the caller's authority and revision-fenced
// transaction. The original JSON maps retain unrecognized private fields.
func SavePersistedConfigurationTx(ctx context.Context, tx *sql.Tx, id string, v *LiveSourceSettings, prepared *PreparedPersistedConfiguration) error {
	if v.Remote != nil {
		if prepared == nil {
			return ErrInvalid
		}
		var sealed []byte
		err := tx.QueryRowContext(ctx, `SELECT sealed FROM live_remote_configs WHERE source_id=?`, id).Scan(&sealed)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !bytes.Equal(sealed, prepared.sealed) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
	}
	if v.ViewerAccess != nil {
		if *v.ViewerAccess != "owner-only" && *v.ViewerAccess != "server-members" {
			return ErrInvalid
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO live_source_settings(source_id,viewer_access) VALUES(?,?) ON CONFLICT(source_id) DO UPDATE SET viewer_access=excluded.viewer_access`, id, *v.ViewerAccess); err != nil {
			return err
		}
	}
	if v.Enabled != nil {
		state := "disabled"
		if *v.Enabled {
			state = "active"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE live_sources SET state=?,revision=revision+1 WHERE id=? AND state<>?`, state, id, state); err != nil {
			return err
		}
	}
	if v.Remote == nil {
		if v.ViewerAccess != nil {
			root, draft, _, err := readRemoteConfiguration(ctx, tx, id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				draft["viewerAccess"], _ = json.Marshal(*v.ViewerAccess)
				root["draft"], _ = json.Marshal(draft)
				raw, _ := json.Marshal(root)
				if _, err = tx.ExecContext(ctx, `UPDATE live_remote_configs SET sealed=?,lease_token='',lease_until_ms=0 WHERE source_id=?`, raw, id); err != nil {
					return err
				}
			}
			_, err = tx.ExecContext(ctx, `UPDATE live_sources SET revision=revision+1 WHERE id=?`, id)
			return err
		}
		return nil
	}
	r := v.Remote
	root, draft, c, err := readRemoteConfiguration(ctx, tx, id)
	if err != nil {
		return err
	}
	guide, approvals := prepared.guide, prepared.approvals
	set := func(key string, value any) { draft[key], _ = json.Marshal(value) }
	// Preserve a private-path marker before old credentials are cleared/replaced.
	// Otherwise the next GET could expose the old credential still in that URL.
	username, password := c.Draft.Username, c.Draft.Password
	if r.Username != nil {
		username = *r.Username
	}
	if r.Password != nil {
		password = *r.Password
	}
	privateGuide := guide == c.Draft.GuideURL && (c.PrivateGuideURL || guidePathHasCredential(guide, c.Draft.Username, c.Draft.Password)) || guidePathHasCredential(guide, username, password)
	root["privateGuideUrl"], _ = json.Marshal(privateGuide)
	set("guideUrl", guide)
	set("refreshMs", r.RefreshMs)
	set("ownerLimit", r.OwnerLimit)
	set("useDiscoveredCapacity", r.UseDiscoveredCapacity)
	if v.ViewerAccess != nil {
		set("viewerAccess", *v.ViewerAccess)
	}
	for _, entry := range []struct {
		key   string
		value *string
	}{{"username", r.Username}, {"password", r.Password}} {
		if entry.value != nil {
			if *entry.value == "" {
				delete(draft, entry.key)
			} else {
				set(entry.key, *entry.value)
			}
		}
	}
	root["draft"], _ = json.Marshal(draft)
	root["approvals"], _ = json.Marshal(approvals)
	raw, err := json.Marshal(root)
	if err != nil {
		return ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `UPDATE live_remote_configs SET sealed=?,next_due_ms=?,lease_token='',lease_until_ms=0 WHERE source_id=?`, raw, time.Now().Add(time.Duration(r.RefreshMs)*time.Millisecond).UnixMilli(), id); err != nil {
		return err
	}
	physical := 0
	if r.UseDiscoveredCapacity {
		physical = c.PhysicalCount
	}
	if _, err = tx.ExecContext(ctx, `UPDATE live_source_settings SET physical_count=?,owner_limit=? WHERE source_id=?`, physical, r.OwnerLimit, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE live_sources SET tuner_count=?,revision=revision+1 WHERE id=?`, effectiveCapacity(c.PhysicalCount, r.OwnerLimit, r.UseDiscoveredCapacity), id)
	return err
}

// ConfigurationChangedTx keeps pause cancellation inside the accepting writer
// gate, exactly as SetEnabled does. Revision checks also fence every publisher.
func (s *Store) ConfigurationChangedTx(ctx context.Context, tx *sql.Tx, id string, v LiveSourceSettings) error {
	if s != nil && (v.Remote != nil || v.Enabled != nil && !*v.Enabled) {
		return dbwork.AfterCommit(tx, func() { s.guideFetches.cancel(id) })
	}
	return nil
}
