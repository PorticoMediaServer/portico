package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// RefreshSession exchanges a device-bound refresh credential for another 15
// minute access token and a new refresh credential. A retry with the same
// request ID receives the exact committed response, stored plainly, until the
// successor credential is first used (capped at the family horizon). Neither an access bearer nor an installation ID alone can refresh.
func (s *Service) RefreshSession(ctx context.Context, secret, installation, requestID string) (Envelope, error) {
	if len(secret) != 43 || !validInstallationID(installation) || !validFamilyID(requestID) {
		return Envelope{}, ErrUnauthorized
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return Envelope{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	hash := Digest(secret)
	var family, device, expiry, deviceInstallation string
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT c.family_id,c.device_id,c.generation,c.expires_at,d.installation_id FROM identity_refresh_credentials c JOIN identity_devices d ON d.id=c.device_id WHERE c.token_hash=?`, hash).
		Scan(&family, &device, &generation, &expiry, &deviceInstallation)
	if errors.Is(err, sql.ErrNoRows) && migratedRefreshTx(ctx, tx, hash) {
		return Envelope{}, ErrSessionMigrated
	}
	if errors.Is(err, sql.ErrNoRows) {
		out, reused, replayErr := s.replayRefreshTx(ctx, tx, hash, installation, requestID)
		if reused != "" {
			// A known predecessor outside its exact replay is evidence that the
			// refresh credential escaped. Commit the revocation before returning
			// an error; the deferred rollback would otherwise undo it.
			if _, err = s.revokeWhereTx(ctx, tx, RevokedRefreshReuse, `id=?`, reused); err != nil {
				return Envelope{}, err
			}
			if err = gated.Commit(); err != nil {
				return Envelope{}, err
			}
			return Envelope{}, ErrUnauthorized
		}
		return out, replayErr
	}
	if err != nil {
		return Envelope{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	refreshUntil, err := familyTime(expiry)
	if err != nil || !refreshUntil.After(now) || deviceInstallation != installation {
		return Envelope{}, ErrUnauthorized
	}
	var p Principal
	p.ServerID = s.serverID
	var horizonRaw, currentHash string
	var revoked int
	err = tx.QueryRowContext(ctx, `SELECT f.account_id,f.profile_id,f.authority,f.role,f.epoch,f.authorization_horizon,f.revoked,t.token_hash,COALESCE((SELECT grant_kind FROM authorization_family_grants WHERE family_id=f.id),'') FROM authorization_session_families f JOIN authorization_family_tokens t ON t.family_id=f.id AND t.generation=f.current_generation WHERE f.id=?`, family).
		Scan(&p.AccountID, &p.ProfileID, &p.Authority, &p.Role, &p.Epoch, &horizonRaw, &revoked, &currentHash, &p.Grant)
	if err != nil || revoked != 0 {
		return Envelope{}, ErrUnauthorized
	}
	if p.Grant != "" {
		return Envelope{}, ErrForbidden
	}
	p.Hash = currentHash
	oldHorizon, err := familyTime(horizonRaw)
	if err != nil || !oldHorizon.After(now) {
		return Envelope{}, ErrUnauthorized
	}
	if err = s.checkFamilyPrincipalTx(ctx, tx, p); err != nil {
		return Envelope{}, err
	}
	// Outside the member's allowed hours the renewal is refused before anything
	// rotates: the refresh credential stays valid and works again later.
	if err = admitAccountTx(ctx, tx, p.AccountID); err != nil {
		return Envelope{}, err
	}
	window, err := sessionWindowTx(ctx, tx)
	if err != nil {
		return Envelope{}, err
	}
	horizon := now.Add(window)
	// A Portico assertion admits a family for at most90 days. Local refresh
	// never moves that boundary; another live identity proof starts a new family.
	kind, err := SignInKindTx(ctx, tx)
	if err != nil {
		return Envelope{}, err
	}
	if kind == SignInPortico && oldHorizon.Before(horizon) {
		horizon = oldHorizon
	}

	var currentGeneration int64
	if err = tx.QueryRowContext(ctx, `SELECT current_generation FROM authorization_session_families WHERE id=? AND revoked=0`, family).Scan(&currentGeneration); err != nil || currentGeneration < generation {
		return Envelope{}, ErrUnauthorized
	}
	access, nextRefresh := Token(), Token()
	accessUntil := accessExpiry(now, horizon).Format(time.RFC3339)
	horizonText := horizon.Format(time.RFC3339)
	if _, err = tx.ExecContext(ctx, `UPDATE authorization_family_tokens SET retired=1 WHERE token_hash=? AND retired=0`, currentHash); err != nil {
		return Envelope{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at,retired) VALUES(?,?,?,?,0)`, Digest(access), family, currentGeneration+1, accessUntil); err != nil {
		return Envelope{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE authorization_session_families SET current_generation=?,authorization_horizon=? WHERE id=? AND revoked=0`, currentGeneration+1, horizonText, family); err != nil {
		return Envelope{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE identity_refresh_credentials SET token_hash=?,generation=?,expires_at=? WHERE family_id=? AND token_hash=?`, Digest(nextRefresh), currentGeneration+1, horizonText, family, hash); err != nil {
		return Envelope{}, err
	}
	out := Envelope{AccessToken: access, RefreshToken: nextRefresh, DeviceID: device, InstallationID: deviceInstallation, ExpiresAt: accessUntil, Viewer: p.Viewer, SessionFamilyID: family, TokenGeneration: currentGeneration + 1, AuthorizationHorizon: horizonText}
	sealed, err := s.sealSetup(out, refreshAAD(family, hash, installation, requestID))
	if err != nil {
		return Envelope{}, err
	}
	// The exact-response receipt lives until the successor is first used (the
	// rotation below deletes the family's older receipts), capped at the family's
	// horizon. A device whose answer was lost in an outage of any length can replay
	// the same request and get the same credentials; once the legitimate device has
	// moved on, the predecessor is reuse and revokes the family as before.
	if _, err = tx.ExecContext(ctx, `DELETE FROM identity_refresh_receipts WHERE family_id=?`, family); err != nil {
		return Envelope{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO identity_refresh_receipts(predecessor_hash,family_id,request_id,ciphertext,expires_at) VALUES(?,?,?,?,?)`, hash, family, requestID, sealed, horizonText); err != nil {
		return Envelope{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO identity_refresh_predecessors(predecessor_hash,family_id) VALUES(?,?)`, hash, family); err != nil {
		return Envelope{}, err
	}
	if err = gated.Commit(); err != nil {
		return Envelope{}, err
	}
	return out, nil
}

func refreshAAD(family, predecessor, installation, request string) string {
	return "first-party-refresh.v1:" + family + ":" + predecessor + ":" + installation + ":" + request
}

// A nonempty reused family identifies a known predecessor used outside the
// exact-response replay window (after its successor was used). The caller must
// commit its revocation.
func (s *Service) replayRefreshTx(ctx context.Context, tx *sql.Tx, predecessor, installation, request string) (Envelope, string, error) {
	// Check locality even for rejected reuse before the caller can revoke a
	// family. A denied remote request must neither disclose a receipt nor mutate
	// a recovery family that is usable only on the home network.
	var p Principal
	var knownFamily string
	err := tx.QueryRowContext(ctx, `SELECT f.id,f.authority,f.account_id,COALESCE((SELECT grant_kind FROM authorization_family_grants WHERE family_id=f.id),'') FROM authorization_session_families f WHERE f.id IN (SELECT family_id FROM identity_refresh_predecessors WHERE predecessor_hash=? UNION SELECT family_id FROM identity_refresh_receipts WHERE predecessor_hash=?)`, predecessor, predecessor).Scan(&knownFamily, &p.Authority, &p.AccountID, &p.Grant)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Envelope{}, "", err
	}
	if err == nil && p.Grant != "" {
		return Envelope{}, "", ErrForbidden
	}

	var family, storedRequest, until string
	var sealed []byte
	if err := tx.QueryRowContext(ctx, `SELECT family_id,request_id,ciphertext,expires_at FROM identity_refresh_receipts WHERE predecessor_hash=?`, predecessor).Scan(&family, &storedRequest, &sealed, &until); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return Envelope{}, "", err
		}
		if err = tx.QueryRowContext(ctx, `SELECT family_id FROM identity_refresh_predecessors WHERE predecessor_hash=?`, predecessor).Scan(&family); err == nil {
			return Envelope{}, family, ErrUnauthorized
		}
		if errors.Is(err, sql.ErrNoRows) {
			return Envelope{}, "", ErrUnauthorized
		}
		return Envelope{}, "", err
	}
	if storedRequest != request {
		return Envelope{}, family, ErrUnauthorized
	}
	expiry, err := familyTime(until)
	if err != nil || !expiry.After(time.Now()) {
		return Envelope{}, family, ErrUnauthorized
	}
	var out Envelope
	if err = s.openSetup(sealed, refreshAAD(family, predecessor, installation, request), &out); err != nil {
		return Envelope{}, family, ErrUnauthorized
	}
	// Receipts sealed before installationId was included still use the binding
	// in their authenticated data. Return it on an exact replay as well.
	out.InstallationID = installation
	var live bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM authorization_session_families f JOIN identity_refresh_credentials c ON c.family_id=f.id JOIN identity_devices d ON d.id=c.device_id JOIN authorization_family_tokens t ON t.family_id=f.id AND t.generation=f.current_generation WHERE f.id=? AND f.revoked=0 AND d.installation_id=? AND c.token_hash=? AND t.token_hash=?)`, family, installation, Digest(out.RefreshToken), Digest(out.AccessToken)).Scan(&live); err != nil || !live {
		return Envelope{}, "", ErrRenewalRetired
	}
	p = Principal{Viewer: out.Viewer, Hash: Digest(out.AccessToken)}
	if err = tx.QueryRowContext(ctx, `SELECT epoch,server_id FROM authorization_session_families WHERE id=?`, family).Scan(&p.Epoch, &p.ServerID); err != nil {
		return Envelope{}, "", ErrUnauthorized
	}
	if err = s.checkFamilyPrincipalTx(ctx, tx, p); err != nil {
		return Envelope{}, "", err
	}
	if err = admitAccountTx(ctx, tx, p.AccountID); err != nil {
		return Envelope{}, "", err
	}

	return out, "", nil
}
