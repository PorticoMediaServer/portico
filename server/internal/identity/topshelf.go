package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

var ErrTopShelfToken = errors.New("This Top Shelf feed link has expired. Open Portico on this device to refresh it.")

// TopShelfGrant is what a feed token resolves to. It carries the viewer facts a
// read needs and nothing a session carries: no family token, no renewal, no way
// to widen itself.
type TopShelfGrant struct {
	Viewer    Viewer
	Hash      string
	Epoch     int
	Token     string
	ExpiresAt string
}

// IssueTopShelfToken mints a feed token for one device. The caller must hold a
// live viewing session, and the device must be registered and approved on the
// same account: the extension inherits exactly what the app already had.
// MaxTopShelfLifetime bounds a feed token. The television's home screen asks for the feed
// when nobody is using the app, so a token that lasted a day would leave the shelf empty for
// anyone who watches at weekends. The token can read one small feed and its posters, for one
// profile on one approved device, and it dies at once with the device, the profile or the
// account; thirty days costs little and keeps the shelf filled.
const MaxTopShelfLifetime = 30 * 24 * time.Hour

func (s *Service) IssueTopShelfToken(ctx context.Context, p Principal, installation string, lifetime time.Duration) (TopShelfGrant, error) {
	if !validInstallationID(installation) || p.AccountID == "" || p.ProfileID == "" {
		return TopShelfGrant{}, ErrDeviceInput
	}
	if lifetime <= 0 || lifetime > MaxTopShelfLifetime {
		lifetime = MaxTopShelfLifetime
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return TopShelfGrant{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if _, e = s.SessionFamilyTx(ctx, tx, p); e != nil {
		return TopShelfGrant{}, e
	}
	if p.Authority != "local" {
		return TopShelfGrant{}, ErrDeviceInput
	}
	device, e := s.deviceTx(ctx, tx, p.Authority, p.AccountID, installation, "")
	if e != nil {
		return TopShelfGrant{}, e
	}
	_, epoch, pinRevision, e := s.topShelfViewerTx(ctx, tx, p.Authority, p.AccountID, p.ProfileID)
	if e != nil {
		return TopShelfGrant{}, e
	}
	now := time.Now().UTC()
	out := TopShelfGrant{Viewer: p.Viewer, Hash: p.Hash, Epoch: epoch, Token: Token(), ExpiresAt: now.Add(lifetime).Format(time.RFC3339)}
	if _, e = tx.ExecContext(ctx, `DELETE FROM topshelf_tokens WHERE expires_at<=? OR (device_id=? AND profile_id=?)`, now.Format(time.RFC3339), device.ID, p.ProfileID); e != nil {
		return TopShelfGrant{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO topshelf_tokens(token_hash,device_id,authority,account_id,profile_id,expires_at,account_epoch,pin_revision) VALUES(?,?,?,?,?,?,?,?)`, Digest(out.Token), device.ID, p.Authority, p.AccountID, p.ProfileID, out.ExpiresAt, epoch, pinRevision); e != nil {
		return TopShelfGrant{}, e
	}
	return out, gated.Commit()
}

// TopShelfGrant resolves a feed token and checks its live account, profile and
// device against the issuance epoch and PIN revision. Credential changes also
// explicitly retire the corresponding stored grants.
func (s *Service) TopShelfGrant(ctx context.Context, token string) (TopShelfGrant, error) {
	if len(token) != 43 {
		return TopShelfGrant{}, ErrTopShelfToken
	}
	out, err := s.topShelfGrantByHash(ctx, Digest(token))
	if err == nil {
		out.Token = token
	}
	return out, err
}

// Artwork capabilities refer to the stored digest, never the reusable bearer.
// This helper stays private so only a verified image capability can use it.
func (s *Service) topShelfGrantByHash(ctx context.Context, hash string) (TopShelfGrant, error) {
	gated2, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return TopShelfGrant{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	var authority, account, profile, device, expiry string
	var issuedEpoch, issuedPINRevision int
	if e = tx.QueryRowContext(ctx, `SELECT authority,account_id,profile_id,device_id,expires_at,account_epoch,pin_revision FROM topshelf_tokens WHERE token_hash=?`, hash).Scan(&authority, &account, &profile, &device, &expiry, &issuedEpoch, &issuedPINRevision); e != nil {
		return TopShelfGrant{}, ErrTopShelfToken
	}
	at, err := time.Parse(time.RFC3339, expiry)
	if err != nil || !at.After(time.Now().UTC()) {
		return TopShelfGrant{}, ErrTopShelfToken
	}
	var present bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_devices WHERE id=? AND authority=? AND account_id=?)`, device, authority, account).Scan(&present); e != nil || !present {
		return TopShelfGrant{}, ErrTopShelfToken
	}
	role, epoch, pinRevision, e := s.topShelfViewerTx(ctx, tx, authority, account, profile)
	if e != nil || epoch != issuedEpoch || pinRevision != issuedPINRevision {
		return TopShelfGrant{}, ErrTopShelfToken
	}
	return TopShelfGrant{
		Viewer:    Viewer{AccountID: account, ProfileID: profile, ServerID: s.ID(), Authority: authority, Role: role},
		Hash:      hash,
		Epoch:     epoch,
		ExpiresAt: expiry,
	}, nil
}

// topShelfViewerTx re-derives the viewer a feed token stands for, so nothing about it is
// carried in the token itself.
//
// The account and profile are re-read from the local identity tables.
func (s *Service) topShelfViewerTx(ctx context.Context, tx *sql.Tx, authority, account, profile string) (string, int, int, error) {

	c, e := s.directAccountTx(ctx, tx, account)
	if e != nil {
		return "", 0, 0, e
	}
	var primary, deleted, pinRevision int
	if e = tx.QueryRowContext(ctx, `SELECT is_primary,deleted,pin_revision FROM direct_profiles WHERE account_id=? AND id=?`, account, profile).Scan(&primary, &deleted, &pinRevision); e != nil || deleted != 0 {
		return "", 0, 0, ErrTopShelfToken
	}
	role := c.account.Role
	if primary == 0 {
		role = "member"
	}
	return role, c.epoch, pinRevision, nil
}

type topShelfArtworkGrant struct {
	Version   int    `json:"version"`
	TokenHash string `json:"tokenHash"`
	Item      string `json:"item"`
	Expires   int64  `json:"expires"`
}

// Artwork capabilities authorize one item's art for five minutes. They contain
// only the feed token's digest, authenticated with an item- and expiry-bound MAC.
// Resolving the live stored grant also enforces device and credential revocation.
func (s *Service) TopShelfArtworkCapability(token, item string) (string, error) {
	if len(token) != 43 || !validFamilyID(item) || len(s.setupHashKey) != 32 {
		return "", ErrTopShelfToken
	}
	raw, err := json.Marshal(topShelfArtworkGrant{Version: 1, TokenHash: Digest(token), Item: item, Expires: time.Now().Add(5 * time.Minute).Unix()})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(raw, s.topShelfArtworkMAC(raw)...)), nil
}

func (s *Service) topShelfArtworkMAC(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.setupHashKey)
	mac.Write([]byte("topshelf-art:v1\x00"))
	mac.Write(payload)
	return mac.Sum(nil)
}

func (s *Service) ResolveTopShelfArtwork(ctx context.Context, capability, item string) (TopShelfGrant, error) {
	if len(capability) > 2048 || !validFamilyID(item) || len(s.setupHashKey) != 32 {
		return TopShelfGrant{}, ErrTopShelfToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(capability)
	if err != nil || len(raw) <= sha256.Size {
		return TopShelfGrant{}, ErrTopShelfToken
	}
	payloadBytes, signature := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	if !hmac.Equal(signature, s.topShelfArtworkMAC(payloadBytes)) {
		return TopShelfGrant{}, ErrTopShelfToken
	}
	var payload topShelfArtworkGrant
	if err = json.Unmarshal(payloadBytes, &payload); err != nil || payload.Version != 1 || payload.Item != item || len(payload.TokenHash) != 43 || payload.Expires <= time.Now().Unix() {
		return TopShelfGrant{}, ErrTopShelfToken
	}
	return s.topShelfGrantByHash(ctx, payload.TokenHash)
}
