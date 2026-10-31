package identity

import (
	"context"
	"database/sql"
	"time"
)

// FamilyBinding is the authority a multi-step operation keeps between its steps:
// the account, profile and epoch, inside one session family. It never names an
// access token. A routine refresh rotates the token and advances the generation
// inside the same family, and the operation survives that; a revocation, an
// epoch change or another family, account or profile refuses it.
type FamilyBinding struct {
	FamilyID  string
	AccountID string
	ProfileID string
	Epoch     int
	// Generation when captured; later steps need a family generation >= this.
	Generation int64
}

// AuthorizeFamilyBindingTx authorizes a later step bound to b. Callers keep
// their own domain checks (same browser and installation, grants, phase, expiry).
//   - caller != nil: the caller's token must be b's family's CURRENT token,
//     unexpired and unretired, as ordinary access requires, with the same
//     viewer and epoch as the family.
//   - caller == nil: a server-side step with no caller session (an install
//     hook). No access token needs to be unexpired: a closed tab stops
//     refreshing, and that must not strand an approved operation.
//
// Either way the family must be unrevoked, unretired, inside its
// authorization horizon, on this server, of b's account, profile and epoch, and
// at a generation >= b.Generation. Refusal is ErrUnauthorized; other errors
// propagate.
func (s *Service) AuthorizeFamilyBindingTx(ctx context.Context, tx *sql.Tx, b FamilyBinding, caller *Principal) (FamilyState, error) {
	r, err := s.familyBindingTx(ctx, tx, b, caller)
	return r.FamilyState, err
}

func (s *Service) familyBindingTx(ctx context.Context, tx *sql.Tx, b FamilyBinding, caller *Principal) (familyRecord, error) {
	if err := ctx.Err(); err != nil {
		return familyRecord{}, err
	}
	if tx == nil || !validFamilyID(b.FamilyID) || b.AccountID == "" || b.ProfileID == "" || b.Generation < 0 {
		return familyRecord{}, ErrUnauthorized
	}
	now := time.Now()
	var r familyRecord
	var err error
	if caller != nil {
		if caller.Hash == "" {
			return familyRecord{}, ErrUnauthorized
		}
		if r, err = s.familyTokenTx(ctx, tx, caller.Hash); err != nil {
			return familyRecord{}, err
		}
		if r.TokenGeneration != r.currentGeneration || !r.tokenExpiry.After(now) || r.principal.Viewer != caller.Viewer || r.principal.Epoch != caller.Epoch {
			return familyRecord{}, ErrUnauthorized
		}
	} else if r, err = s.familyCurrentTokenTx(ctx, tx, b.FamilyID); err != nil {
		return familyRecord{}, err
	}
	p := r.principal
	if r.ID != b.FamilyID || r.revoked || r.retired || r.currentGeneration < b.Generation || p.AccountID != b.AccountID || p.ProfileID != b.ProfileID || p.Epoch != b.Epoch || p.ServerID != s.ID() || !r.AuthorizationHorizon.After(now) {
		return familyRecord{}, ErrUnauthorized
	}
	return r, nil
}
