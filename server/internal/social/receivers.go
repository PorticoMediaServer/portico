package social

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A Portico receiver is a first-party player — an Android TV, Fire TV or tvOS
// app — that registers itself so a phone can hand playback to it.
//
// Registration publishes a stable receiver id and a rotation fingerprint. The
// fingerprint is the fence: rotating the receiver key immediately revokes every
// grant bound to the old fingerprint, because a grant names the fingerprint it
// was issued against and a mismatched grant fails closed.

// fingerprintPattern is the shape of a base64url SHA-256 digest.
var fingerprintPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// commandVocabulary is the complete receiver command set. A grant's allowed
// commands are the intersection of what the controller asks for and what the
// receiver declared, and must include `load`: a receiver that cannot be given
// something to play is not a handoff target.
var commandVocabulary = map[string]bool{"load": true, "play": true, "pause": true, "seek": true, "stop": true}

type ReceiverRequest struct {
	ProtocolVersion   string   `json:"protocolVersion"`
	DeviceID          string   `json:"-"`
	DisplayName       string   `json:"displayName"`
	Platform          string   `json:"platform,omitempty"`
	KeyFingerprint    string   `json:"keyFingerprint"`
	PublicKey         string   `json:"publicKey,omitempty"`
	SupportedCommands []string `json:"supportedCommands,omitempty"`
	GrantPolicy       string   `json:"grantPolicy,omitempty"`
}

type Receiver struct {
	ID                    string   `json:"id"`
	Kind                  string   `json:"kind"`
	DeviceID              string   `json:"deviceId"`
	DisplayName           string   `json:"displayName"`
	Platform              string   `json:"platform"`
	KeyFingerprint        string   `json:"keyFingerprint"`
	SupportedCommands     []string `json:"supportedCommands"`
	GrantPolicy           string   `json:"grantPolicy"`
	AuthorizationRevision string   `json:"authorizationRevision"`
	State                 string   `json:"state"`
	Presence              string   `json:"presence"`
	LastSeenAt            string   `json:"lastSeenAt"`
	CreatedAt             string   `json:"createdAt"`
}

type ReceiverResponse struct {
	ProtocolVersion string   `json:"protocolVersion"`
	ServerTime      string   `json:"serverTime"`
	Receiver        Receiver `json:"receiver"`
}

type ReceiverListResponse struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ServerTime      string     `json:"serverTime"`
	Receivers       []Receiver `json:"receivers"`
}

type GrantRequest struct {
	ProtocolVersion       string   `json:"protocolVersion"`
	ControllerDeviceID    string   `json:"-"`
	ControllerDisplayName string   `json:"controllerDisplayName,omitempty"`
	RequestedCommands     []string `json:"requestedCommands,omitempty"`
	KeyFingerprint        string   `json:"keyFingerprint,omitempty"`
}

type GrantDecision struct {
	ProtocolVersion string `json:"protocolVersion"`
	Decision        string `json:"decision"`
}

type Grant struct {
	ID                    string   `json:"id"`
	ReceiverID            string   `json:"receiverId"`
	ControllerDeviceID    string   `json:"controllerDeviceId"`
	ControllerDisplayName string   `json:"controllerDisplayName"`
	KeyFingerprint        string   `json:"receiverKeyFingerprint"`
	AllowedCommands       []string `json:"allowedCommands"`
	AuthorizationRevision string   `json:"authorizationRevision"`
	State                 string   `json:"state"`
	ExpiresAt             string   `json:"expiresAt"`
	CreatedAt             string   `json:"createdAt"`
	DecidedAt             *string  `json:"decidedAt"`
}

type GrantResponse struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerTime      string `json:"serverTime"`
	Grant           Grant  `json:"grant"`
}

type GrantListResponse struct {
	ProtocolVersion string  `json:"protocolVersion"`
	ServerTime      string  `json:"serverTime"`
	Grants          []Grant `json:"grants"`
}

func normalizeCommands(in []string) ([]string, bool) {
	seen := map[string]bool{}
	for _, c := range in {
		if !commandVocabulary[c] {
			return nil, false
		}
		seen[c] = true
	}
	if !seen["load"] {
		return nil, false
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, true
}

func intersectCommands(a, b []string) []string {
	have := map[string]bool{}
	for _, c := range b {
		have[c] = true
	}
	out := []string{}
	for _, c := range a {
		if have[c] {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// RegisterReceiver registers or re-registers a receiver for the calling viewer.
// Re-registering with a different key fingerprint revokes every open grant on
// that receiver and bumps the authorization revision, so a rotated key can never
// be driven with a credential minted against the old one.
func (s *Store) RegisterReceiver(ctx context.Context, p identity.Principal, req ReceiverRequest) (ReceiverResponse, error) {
	var out ReceiverResponse
	if req.ProtocolVersion != Protocol || !ValidID(req.DeviceID) || !text(req.DisplayName, 64) || !fingerprintPattern.MatchString(req.KeyFingerprint) {
		return out, errInvalid
	}
	if req.Platform != "" && !text(req.Platform, 64) {
		return out, errInvalid
	}
	if req.PublicKey != "" && !text(req.PublicKey, 256) {
		return out, errInvalid
	}
	if len(req.SupportedCommands) == 0 {
		req.SupportedCommands = []string{"load", "play", "pause", "seek", "stop"}
	}
	commands, ok := normalizeCommands(req.SupportedCommands)
	if !ok {
		return out, errInvalid
	}
	if req.GrantPolicy == "" {
		req.GrantPolicy = "per-device"
	}
	switch req.GrantPolicy {
	case "per-device", "always-ask", "open":
	default:
		return out, errInvalid
	}
	raw, e := json.Marshal(commands)
	if e != nil {
		return out, e
	}
	gated, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM social_receivers WHERE authority=? AND account_id=? AND profile_id=? AND state='active' AND device_id<>?`, p.Authority, p.AccountID, p.ProfileID, req.DeviceID).Scan(&count); e != nil {
		return out, e
	}
	if count >= 16 {
		return out, fault("capacity_reached", 429, "You already have several receiving devices.")
	}
	now := s.ms()
	var id, existing string
	var revision int64
	e = tx.QueryRowContext(ctx, `SELECT id,key_fingerprint,authorization_revision FROM social_receivers WHERE authority=? AND account_id=? AND profile_id=? AND device_id=?`, p.Authority, p.AccountID, p.ProfileID, req.DeviceID).Scan(&id, &existing, &revision)
	switch {
	case errors.Is(e, sql.ErrNoRows):
		if e = s.limit(ctx, tx, "receiver-create:"+viewerKey(p.Viewer), 30, time.Hour); e != nil {
			return out, e
		}
		id, revision = token("rcv_"), 1
		if _, e = tx.ExecContext(ctx, `INSERT INTO social_receivers(id,authority,account_id,profile_id,kind,device_id,display_name,platform,key_fingerprint,public_key,supported_commands,grant_policy,authorization_revision,state,created_at_ms,last_seen_ms) VALUES(?,?,?,?,'portico',?,?,?,?,?,?,?,1,'active',?,?)`,
			id, p.Authority, p.AccountID, p.ProfileID, req.DeviceID, req.DisplayName, req.Platform, req.KeyFingerprint, req.PublicKey, string(raw), req.GrantPolicy, now, now); e != nil {
			return out, e
		}
	case e != nil:
		return out, e
	default:
		if existing != req.KeyFingerprint {
			revision++
			if _, e = tx.ExecContext(ctx, `UPDATE social_receiver_grants SET state='revoked',decided_at_ms=? WHERE receiver_id=? AND state IN ('pending','accepted')`, now, id); e != nil {
				return out, e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE social_handoffs SET state='failed',outcome='rejected',reason='receiver-key-rotated',settled_at_ms=?,revision=revision+1 WHERE receiver_id=? AND state IN ('prepared','committing')`, now, id); e != nil {
				return out, e
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE social_receivers SET display_name=?,platform=?,key_fingerprint=?,public_key=?,supported_commands=?,grant_policy=?,authorization_revision=?,state='active',last_seen_ms=? WHERE id=?`,
			req.DisplayName, req.Platform, req.KeyFingerprint, req.PublicKey, string(raw), req.GrantPolicy, revision, now, id); e != nil {
			return out, e
		}
	}
	receiver, e := s.readReceiver(ctx, tx, p, id)
	if e != nil {
		return out, e
	}
	return ReceiverResponse{ProtocolVersion: Protocol, ServerTime: stamp(now), Receiver: receiver}, gated.Commit()
}

func (s *Store) readReceiver(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (Receiver, error) {
	var r Receiver
	var commands string
	var revision, created, seen int64
	e := tx.QueryRowContext(ctx, `SELECT id,kind,device_id,display_name,platform,key_fingerprint,supported_commands,grant_policy,authorization_revision,state,created_at_ms,last_seen_ms FROM social_receivers WHERE id=? AND authority=? AND account_id=? AND profile_id=?`, id, p.Authority, p.AccountID, p.ProfileID).Scan(&r.ID, &r.Kind, &r.DeviceID, &r.DisplayName, &r.Platform, &r.KeyFingerprint, &commands, &r.GrantPolicy, &revision, &r.State, &created, &seen)
	if errors.Is(e, sql.ErrNoRows) {
		return r, errNotFound
	}
	if e != nil {
		return r, e
	}
	_ = json.Unmarshal([]byte(commands), &r.SupportedCommands)
	r.AuthorizationRevision, r.CreatedAt, r.LastSeenAt = counter(revision), stamp(created), stamp(seen)
	r.Presence = "online"
	if s.ms()-seen > ReceiverPresenceTTL*1000 {
		r.Presence = "offline"
	}
	return r, nil
}

// ListReceivers is the viewer's receiver directory: the devices this viewer may
// hand playback to. It never lists another viewer's receivers.
func (s *Store) ListReceivers(ctx context.Context, p identity.Principal) (ReceiverListResponse, error) {
	out := ReceiverListResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), Receivers: []Receiver{}}
	// A read, on a snapshot, with no write gate.
	tx, done, e := dbwork.BeginRead(ctx, s.DB)
	if e != nil {
		return out, e
	}
	defer done()

	rows, e := tx.QueryContext(ctx, `SELECT id FROM social_receivers WHERE authority=? AND account_id=? AND profile_id=? AND state='active' ORDER BY display_name,id`, p.Authority, p.AccountID, p.ProfileID)
	if e != nil {
		return out, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	for _, id := range ids {
		r, e := s.readReceiver(ctx, tx, p, id)
		if e != nil {
			return out, e
		}
		out.Receivers = append(out.Receivers, r)
	}
	return out, nil
}

// RetireReceiver removes a receiver from the directory and revokes its grants.
func (s *Store) RetireReceiver(ctx context.Context, p identity.Principal, id string) error {
	gated3, e := s.begin(ctx)
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if _, e = s.readReceiver(ctx, tx, p, id); e != nil {
		return e
	}
	now := s.ms()
	if _, e = tx.ExecContext(ctx, `UPDATE social_receiver_grants SET state='revoked',decided_at_ms=? WHERE receiver_id=? AND state IN ('pending','accepted')`, now, id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE social_receivers SET state='retired' WHERE id=?`, id); e != nil {
		return e
	}
	return gated3.Commit()
}

// ReceiverHeartbeat refreshes presence. A receiver that stops beating drops to
// `offline` in the directory after ReceiverPresenceTTL; it is not retired,
// because a TV that is merely asleep is still the viewer's TV.
func (s *Store) ReceiverHeartbeat(ctx context.Context, p identity.Principal, id, fingerprint string) (ReceiverResponse, error) {
	var out ReceiverResponse
	gated4, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	r, e := s.readReceiver(ctx, tx, p, id)
	if e != nil {
		return out, e
	}
	if p.DeviceID == "" || p.DeviceID != r.DeviceID {
		return out, fault("receiver_required", 403, "Only the receiving device may do that.")
	}
	if !fingerprintPattern.MatchString(fingerprint) || !sameSecret(fingerprint, r.KeyFingerprint) {
		return out, &Fault{Code: "receiver_key_mismatch", Status: 403, Message: "That receiver key does not match the registration."}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE social_receivers SET last_seen_ms=? WHERE id=?`, s.ms(), id); e != nil {
		return out, e
	}
	if r, e = s.readReceiver(ctx, tx, p, id); e != nil {
		return out, e
	}
	return ReceiverResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), Receiver: r}, gated4.Commit()
}

// RequestGrant is the controller half: a phone asks to control a receiver.
//
// Under `open` the grant is issued immediately. Under `per-device` a controller
// that already holds an accepted grant is re-issued the same grant (accept once
// per device); a new device lands in `pending` for the receiver to decide. Under
// `always-ask` every request lands in `pending`, even a returning device.
func (s *Store) RequestGrant(ctx context.Context, p identity.Principal, receiverID string, req GrantRequest) (GrantResponse, error) {
	var out GrantResponse
	if req.ProtocolVersion != Protocol || !ValidID(req.ControllerDeviceID) {
		return out, errInvalid
	}
	if req.ControllerDisplayName != "" && !text(req.ControllerDisplayName, 64) {
		return out, errInvalid
	}
	if len(req.RequestedCommands) == 0 {
		req.RequestedCommands = []string{"load", "play", "pause", "seek", "stop"}
	}
	requested, ok := normalizeCommands(req.RequestedCommands)
	if !ok {
		return out, errInvalid
	}
	gated5, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	receiver, e := s.readReceiver(ctx, tx, p, receiverID)
	if e != nil {
		return out, e
	}
	if receiver.State != "active" {
		return out, &Fault{Code: "target_unavailable", Status: 409, Message: "That receiver is retired."}
	}
	if req.KeyFingerprint != "" && !sameSecret(req.KeyFingerprint, receiver.KeyFingerprint) {
		return out, &Fault{Code: "receiver_key_mismatch", Status: 403, Message: "That receiver key does not match the registration."}
	}
	allowed := intersectCommands(requested, receiver.SupportedCommands)
	if len(allowed) == 0 || !contains(allowed, "load") {
		return out, &Fault{Code: "participant_incompatible", Status: 409, Message: "The receiver supports none of the requested commands."}
	}
	now := s.ms()
	expires := now + GrantTTLSeconds*1000
	var id, state string
	var existingExpires int64
	e = tx.QueryRowContext(ctx, `SELECT id,state,expires_at_ms FROM social_receiver_grants WHERE receiver_id=? AND controller_device_id=?`, receiverID, req.ControllerDeviceID).Scan(&id, &state, &existingExpires)
	raw, _ := json.Marshal(allowed)
	revision, _ := parseCounter(receiver.AuthorizationRevision)
	switch {
	case errors.Is(e, sql.ErrNoRows):
		id = token("grt_")
		state = "pending"
		if receiver.GrantPolicy == "open" {
			state = "accepted"
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO social_receiver_grants(id,receiver_id,controller_device_id,controller_display_name,authority,account_id,profile_id,key_fingerprint,allowed_commands,authorization_revision,state,created_at_ms,expires_at_ms,decided_at_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, receiverID, req.ControllerDeviceID, req.ControllerDisplayName, p.Authority, p.AccountID, p.ProfileID, receiver.KeyFingerprint, string(raw), revision, state, now, expires, decisionStamp(state, now)); e != nil {
			return out, e
		}
	case e != nil:
		return out, e
	default:
		renew := receiver.GrantPolicy == "per-device" && state == "accepted" && now < existingExpires
		if receiver.GrantPolicy == "open" {
			renew, state = true, "accepted"
		}
		if !renew {
			state = "pending"
		}
		if _, e = tx.ExecContext(ctx, `UPDATE social_receiver_grants SET controller_display_name=?,key_fingerprint=?,allowed_commands=?,authorization_revision=?,state=?,expires_at_ms=?,decided_at_ms=? WHERE id=?`,
			req.ControllerDisplayName, receiver.KeyFingerprint, string(raw), revision, state, expires, decisionStamp(state, now), id); e != nil {
			return out, e
		}
	}
	grant, e := s.readGrant(ctx, tx, p, id)
	if e != nil {
		return out, e
	}
	if e = gated5.Commit(); e != nil {
		return out, e
	}
	s.publishReceiverEvent(p, receiverID, "grant")
	return GrantResponse{ProtocolVersion: Protocol, ServerTime: stamp(now), Grant: grant}, nil
}

func decisionStamp(state string, now int64) any {
	if state == "pending" {
		return nil
	}
	return now
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func (s *Store) readGrant(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (Grant, error) {
	var g Grant
	var commands string
	var revision, created, expires int64
	var decided sql.NullInt64
	e := tx.QueryRowContext(ctx, `SELECT id,receiver_id,controller_device_id,controller_display_name,key_fingerprint,allowed_commands,authorization_revision,state,created_at_ms,expires_at_ms,decided_at_ms FROM social_receiver_grants WHERE id=? AND authority=? AND account_id=? AND profile_id=?`, id, p.Authority, p.AccountID, p.ProfileID).Scan(&g.ID, &g.ReceiverID, &g.ControllerDeviceID, &g.ControllerDisplayName, &g.KeyFingerprint, &commands, &revision, &g.State, &created, &expires, &decided)
	if errors.Is(e, sql.ErrNoRows) {
		return g, errNotFound
	}
	if e != nil {
		return g, e
	}
	_ = json.Unmarshal([]byte(commands), &g.AllowedCommands)
	g.AuthorizationRevision, g.CreatedAt, g.ExpiresAt = counter(revision), stamp(created), stamp(expires)
	if decided.Valid {
		g.DecidedAt = optionalStamp(decided.Int64)
	}
	if g.State == "accepted" && s.ms() >= expires {
		g.State = "expired"
	}
	return g, nil
}

// ListGrants is the receiver half: the TV polls for grants awaiting a decision.
func (s *Store) ListGrants(ctx context.Context, p identity.Principal, receiverID string) (GrantListResponse, error) {
	out := GrantListResponse{ProtocolVersion: Protocol, ServerTime: stamp(s.ms()), Grants: []Grant{}}
	// A read, on a snapshot, with no write gate.
	tx, done, e := dbwork.BeginRead(ctx, s.DB)
	if e != nil {
		return out, e
	}
	defer done()

	_, e = s.readReceiver(ctx, tx, p, receiverID)
	if e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT id FROM social_receiver_grants WHERE receiver_id=? AND authority=? AND account_id=? AND profile_id=? ORDER BY created_at_ms DESC,id`, receiverID, p.Authority, p.AccountID, p.ProfileID)
	if e != nil {
		return out, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	for _, id := range ids {
		g, e := s.readGrant(ctx, tx, p, id)
		if e != nil {
			return out, e
		}
		out.Grants = append(out.Grants, g)
	}
	return out, nil
}

// DecideGrant is the receiver accepting or declining a pending request.
func (s *Store) DecideGrant(ctx context.Context, p identity.Principal, receiverID, grantID string, req GrantDecision) (GrantResponse, error) {
	var out GrantResponse
	if req.ProtocolVersion != Protocol || (req.Decision != "accept" && req.Decision != "decline" && req.Decision != "revoke") {
		return out, errInvalid
	}
	gated7, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated7.Tx()
	defer gated7.Rollback()
	receiver, e := s.readReceiver(ctx, tx, p, receiverID)
	if e != nil {
		return out, e
	}
	if p.DeviceID == "" || p.DeviceID != receiver.DeviceID {
		return out, fault("receiver_required", 403, "Only the receiving device may do that.")
	}
	grant, e := s.readGrant(ctx, tx, p, grantID)
	if e != nil {
		return out, e
	}
	if grant.ReceiverID != receiverID {
		return out, errNotFound
	}
	state := map[string]string{"accept": "accepted", "decline": "declined", "revoke": "revoked"}[req.Decision]
	if req.Decision == "accept" && grant.State != "pending" {
		return out, &Fault{Code: "grant_not_pending", Status: 409, Message: "That authorization is not awaiting a decision."}
	}
	now := s.ms()
	expires := now + GrantTTLSeconds*1000
	if _, e = tx.ExecContext(ctx, `UPDATE social_receiver_grants SET state=?,decided_at_ms=?,expires_at_ms=? WHERE id=?`, state, now, expires, grantID); e != nil {
		return out, e
	}
	if state != "accepted" {
		if _, e = tx.ExecContext(ctx, `UPDATE social_handoffs SET state='failed',outcome='rejected',reason='grant-revoked',settled_at_ms=?,revision=revision+1 WHERE grant_id=? AND state IN ('prepared','committing')`, now, grantID); e != nil {
			return out, e
		}
	}
	if grant, e = s.readGrant(ctx, tx, p, grantID); e != nil {
		return out, e
	}
	return GrantResponse{ProtocolVersion: Protocol, ServerTime: stamp(now), Grant: grant}, gated7.Commit()
}

// ReceiverInbox is what a receiver polls while it waits to be used: the grant
// requests it has not decided yet and the handoffs addressed to it that are still
// open. It is a read and takes no write gate, so a television asking every few
// seconds costs the server a snapshot and nothing else. Presence is still proved
// by the heartbeat; an expired handoff simply stops appearing here.
type ReceiverInbox struct {
	ProtocolVersion string    `json:"protocolVersion"`
	ServerTime      string    `json:"serverTime"`
	ReceiverID      string    `json:"receiverId"`
	Grants          []Grant   `json:"grants"`
	Handoffs        []Handoff `json:"handoffs"`
}

func (s *Store) ReadReceiverInbox(ctx context.Context, p identity.Principal, receiverID string) (ReceiverInbox, error) {
	now := s.ms()
	out := ReceiverInbox{ProtocolVersion: Protocol, ServerTime: stamp(now), ReceiverID: receiverID, Grants: []Grant{}, Handoffs: []Handoff{}}
	tx, done, e := dbwork.BeginRead(ctx, s.DB)
	if e != nil {
		return out, e
	}
	defer done()
	if _, e = s.readReceiver(ctx, tx, p, receiverID); e != nil {
		return out, e
	}
	ids, e := inboxIDs(ctx, tx, `SELECT id FROM social_receiver_grants WHERE receiver_id=? AND authority=? AND account_id=? AND profile_id=? AND state='pending' AND expires_at_ms>? ORDER BY created_at_ms,id LIMIT 16`, receiverID, p.Authority, p.AccountID, p.ProfileID, now)
	if e != nil {
		return out, e
	}
	for _, id := range ids {
		g, e := s.readGrant(ctx, tx, p, id)
		if e != nil {
			return out, e
		}
		out.Grants = append(out.Grants, g)
	}
	rows, e := tx.QueryContext(ctx, `SELECT `+handoffColumns+` FROM social_handoffs WHERE receiver_id=? AND authority=? AND account_id=? AND profile_id=? AND state IN ('prepared','committing') AND expires_at_ms>? ORDER BY created_at_ms,id LIMIT 4`, receiverID, p.Authority, p.AccountID, p.ProfileID, now)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		h, e := scanHandoff(rows)
		if e != nil {
			return out, e
		}
		out.Handoffs = append(out.Handoffs, h.wire())
	}
	return out, rows.Err()
}

func inboxIDs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, e := tx.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
