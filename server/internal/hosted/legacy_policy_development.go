//go:build !release

package hosted

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Historical policy fixture retained only for isolated authorization tests.
// Tests in other packages (httpapi) use it, so it cannot live in a _test file;
// the !release tag keeps it out of every shipped binary (A59).
func (s *Service) Apply(signed Signed) error {
	if s.current != nil {
		return errors.New("current policy requires exact installed claim transaction")
	}
	raw, e := s.verifyEnvelope(context.Background(), signed)
	if e != nil {
		return identity.ErrUnauthorized
	}

	var p Policy
	if e = json.Unmarshal(raw, &p); e != nil {
		return e
	}
	expires, e := time.Parse(time.RFC3339, p.ExpiresAt)
	if e != nil || !expires.After(time.Now()) {
		return errors.New("policy expired")
	}
	issued, e := time.Parse(time.RFC3339, p.IssuedAt)
	if e != nil || issued.After(time.Now().Add(time.Minute)) || expires.Sub(issued) > 90*24*time.Hour+time.Minute || p.ServerID != s.identity.ID() || p.Revision < 1 {
		return errors.New("invalid hosted policy bounds")
	}
	gated, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassFrom(context.Background(), dbwork.ClassInteractive))
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var rev int64
	var payload string
	e = tx.QueryRow(`SELECT revision,payload FROM policy WHERE server_id=?`, p.ServerID).Scan(&rev, &payload)
	if e == nil && (p.Revision < rev || (p.Revision == rev && !validRenewal(payload, raw))) {
		return errors.New("hosted policy rollback or collision")
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO policy VALUES(?,?,?,?) ON CONFLICT(server_id) DO UPDATE SET revision=excluded.revision,payload=excluded.payload,expires_at=excluded.expires_at`, p.ServerID, p.Revision, string(raw), p.ExpiresAt); e != nil {
		return e
	}
	if e = s.reconcileProfileSecurityTx(context.Background(), tx, p); e != nil {
		return e
	}
	if e = s.revokeFamiliesOutsidePolicy(tx, p); e != nil {
		return e
	}
	rows, e := tx.Query(`SELECT ps.id,ps.account_id,ps.profile_id,cl.library_id FROM playback_sessions ps JOIN authorization_access s ON s.hash=ps.session_hash JOIN catalog_entities e ON e.id=ps.item_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE s.authority='hosted' AND ps.state NOT IN ('stopped','ended','failed')`)
	if e != nil {
		return e
	}
	var stop []string
	for rows.Next() {
		var id, account, profile, library string
		if e = rows.Scan(&id, &account, &profile, &library); e != nil {
			rows.Close()
			return e
		}
		allowed := false
		for _, m := range p.Members {
			if m.AccountID == account && m.ProfileID == profile && (((m.Role == "owner" || m.AllLibraries) && !m.LibraryRestricted) || contains(m.AllowedLibraries, library)) {
				allowed = true
			}
		}
		if !allowed {
			stop = append(stop, id)
		}
	}
	rows.Close()
	for _, id := range stop {
		if _, e = tx.Exec(`UPDATE playback_sessions SET state='stopped' WHERE id=?`, id); e != nil {
			return e
		}
	}
	return gated.Commit()
}
