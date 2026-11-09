package writer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// Named create-admission refusals. The product's INSERT would otherwise expose
// an opaque SQLite UNIQUE/CAPACITY failure; these names are stable comparison
// outcomes for the dispatcher and never carry the capability or source rows.
var (
	ErrInvitationIDCollision   = errors.New("writer: invitation id collision")
	ErrInvitationCodeCollision = errors.New("writer: invitation code hash collision")
	ErrInvitationCapacity      = errors.New("writer: invitation live capacity reached")
	ErrInvitationLibrary       = errors.New("writer: invitation library does not exist")
)

const (
	// The pinned product keeps at most 200 live (pending, unexpired) offers and
	// 256 libraries per invitation. These are admission bounds, not new caps.
	usInvitationLiveLimit        int64 = 200
	usInvitationLegacyCodeLen          = 43
	usInvitationInvitationCohort       = "invitation"
)

// usInvitationLegacyCode is the EXACT product derivation from
// access/invitations.go invitationCode: a domain-separated HMAC-SHA256 over the
// invitation id, rendered as unpadded base64url. Legacy redeem recomputes this
// value, so a code produced any other way is refused even when its hash matches
// its own bytes.
func usInvitationLegacyCode(key []byte, id string) (string, error) {
	if len(key) != 32 || !usIdentifier(id) {
		return "", ErrInvalid
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte("portico-invitation-v1\x00" + id))
	code := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	if len(code) != usInvitationLegacyCodeLen {
		return "", ErrInvalid
	}
	return code, nil
}

// usInvitationCodeHash is the product's digest(code): lowercase SHA-256 hex.
func usInvitationCodeHash(code string) (string, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(code)
	if err != nil || len(raw) != 32 {
		return "", ErrInvalid
	}
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:]), nil
}

// usInvitationLegacyLink derives the exact code and its stored hash together.
// Tests use it as the independent product oracle; a production caller derives
// the code outside the writer turn and seals exactly that string.
func usInvitationLegacyLink(key []byte, id string) (code, hash string, err error) {
	code, err = usInvitationLegacyCode(key, id)
	if err != nil {
		return "", "", err
	}
	hash, err = usInvitationCodeHash(code)
	if err != nil {
		return "", "", err
	}
	return code, hash, nil
}

// checkUSInvitationCreateAdmission is the G4 create gate: PK and code_hash
// collisions are named, every requested library must exist, and the
// 200-live-offer cap is enforced against trusted same-frame time, in the legacy
// order (libraries before capacity). All checks run BEFORE the INSERT, so a
// refusal writes no source row.
func checkUSInvitationCreateAdmission(ctx context.Context, turn *commandTurn, p *usInvitationPrepared, nowMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || p == nil || p.kind != usInvitationCreate || nowMS <= 0 || !usIdentifier(p.id) || !usHex(p.codeHash, 64) {
		return ErrInvalid
	}
	row, err := usInvitationRow(ctx, turn, usInvitationExistsID, p.id)
	if err != nil {
		return err
	}
	var hit int64
	switch scan := row.Scan(&hit); {
	case scan == nil:
		return ErrInvitationIDCollision
	case errors.Is(scan, sql.ErrNoRows):
	default:
		return scan
	}
	row, err = usInvitationRow(ctx, turn, usInvitationExistsCode, p.codeHash)
	if err != nil {
		return err
	}
	switch scan := row.Scan(&hit); {
	case scan == nil:
		return ErrInvitationCodeCollision
	case errors.Is(scan, sql.ErrNoRows):
	default:
		return scan
	}
	// Legacy order (access/invitations.go Invite): manage the requested tier,
	// prove every requested library exists, then count live capacity. A missing
	// library is therefore a library refusal even when the server is full.
	if len(p.libraries) > usInvitationMaxLibraries {
		return ErrInvitationLibrary
	}
	if err = checkUSInvitationLibraries(ctx, turn, p.libraries); err != nil {
		return err
	}
	rows, err := usInvitationRows(ctx, turn, usInvitationLiveCount, nowMS, usInvitationLiveLimit+1)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var live int64
	for rows.Next() {
		live++
		if live >= usInvitationLiveLimit {
			return ErrInvitationCapacity
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return nil
}

// checkUSInvitationLibraries proves every requested library exists. An empty
// cohort is the product's "all libraries" default and needs no row. It is shared
// by create admission and edit so both refuse a missing library with the same
// named outcome. It carries no finishCommand of its own: its callers are already
// guarded finish points.
func checkUSInvitationLibraries(ctx context.Context, turn *commandTurn, libraries []string) error {
	if turn == nil || len(libraries) > usInvitationMaxLibraries {
		return ErrInvitationLibrary
	}
	for _, id := range libraries {
		row, err := usInvitationRow(ctx, turn, usInvitationLibraryRead, id)
		if err != nil {
			return err
		}
		var present int64
		if err = row.Scan(&present); errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationLibrary
		} else if err != nil {
			return err
		}
		if present != 1 {
			return ErrInvitationLibrary
		}
	}
	return nil
}

// usInvitationUniqueError maps a late SQLite UNIQUE failure (the preflight can
// race another writer) to the same named collisions. Anything else passes
// through unchanged.
func usInvitationUniqueError(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "access_invitations.code_hash"):
		return errors.Join(ErrInvitationCodeCollision, err)
	case strings.Contains(text, "access_invitations.id"):
		return errors.Join(ErrInvitationIDCollision, err)
	default:
		return err
	}
}
