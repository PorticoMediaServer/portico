package hosted

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"
)

// Signed profile revisions are separate from credential identity. A changed
// security/PIN/trust revision fences prior families; a name/order edit does not.
func (s *Service) reconcileProfileSecurityTx(ctx context.Context, tx *sql.Tx, p Policy) error {
	for _, m := range p.Members {
		if m.PINRevision == 0 && m.ProfileRevision == 0 && m.AccountEpoch == 0 {
			continue
		}
		var pin, revision, trust, epoch int64
		e := tx.QueryRowContext(ctx, `SELECT pin_revision,profile_revision,trust_revision,account_epoch FROM hosted_profile_revisions WHERE server_id=? AND account_id=? AND profile_id=?`, p.ServerID, m.AccountID, m.ProfileID).Scan(&pin, &revision, &trust, &epoch)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e != nil || pin != m.PINRevision || revision != m.ProfileRevision || trust != m.TrustRevision || epoch != m.AccountEpoch {
			if e = s.revokeRestrictedFamilies(tx, m.AccountID, m.ProfileID); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO hosted_profile_revisions(server_id,account_id,profile_id,pin_revision,profile_revision,trust_revision,account_epoch) VALUES(?,?,?,?,?,?,?) ON CONFLICT(server_id,account_id,profile_id) DO UPDATE SET pin_revision=excluded.pin_revision,profile_revision=excluded.profile_revision,trust_revision=excluded.trust_revision,account_epoch=excluded.account_epoch`, p.ServerID, m.AccountID, m.ProfileID, m.PINRevision, m.ProfileRevision, m.TrustRevision, m.AccountEpoch); e != nil {
			return e
		}
	}
	// The erasure is idempotent and commits with policy application. An ack must
	// never claim a tombstone is applied while its saved data remains live.
	for _, job := range p.ProfileErasures {
		if job.ID == "" || job.AccountID == "" || job.ProfileID == "" || job.Revision != 1 {
			return identity.ErrUnauthorized
		}
		for _, m := range p.Members {
			if m.AccountID == job.AccountID && m.ProfileID == job.ProfileID {
				return identity.ErrUnauthorized
			}
		}
		var account, profile string
		var revision int64
		e := tx.QueryRowContext(ctx, `SELECT account_id,profile_id,revision FROM hosted_profile_erasure_receipts WHERE job_id=? AND server_id=?`, job.ID, p.ServerID).Scan(&account, &profile, &revision)
		if e == nil {
			if account != job.AccountID || profile != job.ProfileID || revision != job.Revision {
				return identity.ErrUnauthorized
			}
			continue
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e = s.revokeRestrictedFamilies(tx, job.AccountID, job.ProfileID); e != nil {
			return e
		}
		if e = identity.EraseProfileSavedDataTx(ctx, tx, identity.Viewer{Authority: "hosted", AccountID: job.AccountID, ProfileID: job.ProfileID, ServerID: p.ServerID}); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO hosted_profile_erasure_receipts(job_id,server_id,account_id,profile_id,revision,applied_at) VALUES(?,?,?,?,?,?)`, job.ID, p.ServerID, job.AccountID, job.ProfileID, job.Revision, time.Now().UTC().Format(time.RFC3339)); e != nil {
			return e
		}
	}
	return nil
}
