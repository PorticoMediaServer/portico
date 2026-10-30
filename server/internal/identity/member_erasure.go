package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/preferenceownership"
)

// Deleting a member removes the person from this server: their account, their
// profiles and everything those profiles saved, their devices and sessions.
// Turning a member off (UpdateDirectMember with Disabled) keeps all of that
// and only stops them signing in; deletion is the one that frees the username
// and clears their data.
//
// One thing is kept on purpose. The owner's play history is the server's own
// record of what was played here, and it keeps its plays. What it loses is who:
// the person's name, their profiles' names and the names they gave their
// devices are blanked in those rows, so the counts and the timeline stay true
// and no longer point at anyone.

// MemberDeletionPreview says what deleting a member will remove, for the
// confirmation an owner sees first. Nothing here is a title or a file: media
// and recordings are never deleted with a person.
type MemberDeletionPreview struct {
	AccountID      string `json:"accountId"`
	Username       string `json:"username"`
	Role           string `json:"role"`
	Revision       int64  `json:"revision"`
	PorticoAccount bool   `json:"porticoAccount"`
	Profiles       int    `json:"profiles"`
	Devices        int    `json:"devices"`
	Playlists      int    `json:"playlists"`
	SavedResources int    `json:"savedResources"`
	// PlaysKept is how many rows of the owner's play history will stay, with
	// this person's names blanked.
	PlaysKept int `json:"playsKept"`
}

// MemberDeletion is the confirmed request: the revision the preview showed.
type MemberDeletion struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}

// memberTargetTx resolves the member a caller wants to delete and applies the
// tier rule: an administrator deletes strictly below itself, nobody deletes
// the owner (ownership moves only by transfer) and nobody deletes themselves.
// The caller's own session is valid in every refusal here, so each is a 403
// or a hidden 404, never a 401.
func (s *Service) memberTargetTx(ctx context.Context, tx *sql.Tx, bearer, id string) (MemberDeletionPreview, error) {
	out := MemberDeletionPreview{AccountID: id}
	if !validFamilyID(id) {
		return out, ErrDirectInput
	}
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return out, e
	}
	if !Administrative(c.account.Role) {
		return out, ErrForbidden
	}
	var linked sql.NullString
	e = tx.QueryRowContext(ctx, `SELECT a.username,m.role,m.revision,l.hosted_account_id FROM accounts a JOIN direct_memberships m ON m.account_id=a.id LEFT JOIN account_portico_links l ON l.account_id=a.id WHERE a.id=?`, id).Scan(&out.Username, &out.Role, &out.Revision, &linked)
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrNotVisible
	}
	if e != nil {
		return out, e
	}
	if id == c.account.ID || !ManagesTier(c.account.Role, out.Role) {
		return out, ErrForbidden
	}
	out.PorticoAccount = linked.Valid && linked.String != ""
	e = tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM direct_profiles WHERE account_id=?1 AND deleted=0),
 (SELECT count(*) FROM identity_devices WHERE authority='local' AND account_id=?1),
 (SELECT count(*) FROM catalog_playlists WHERE owner_authority='local' AND owner_account=?1 AND deleted=0),
 (SELECT count(*) FROM saved_resources WHERE owner_authority='local' AND owner_account=?1 AND deleted=0),
 (SELECT count(*) FROM play_history WHERE authority='local' AND account_id=?1)`, id).Scan(&out.Profiles, &out.Devices, &out.Playlists, &out.SavedResources, &out.PlaysKept)
	return out, e
}

// PreviewDirectMemberDeletion reads what a deletion would remove. It changes nothing.
func (s *Service) PreviewDirectMemberDeletion(ctx context.Context, bearer, id string) (MemberDeletionPreview, error) {
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return MemberDeletionPreview{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	return s.memberTargetTx(ctx, tx, bearer, id)
}

// DeleteDirectMember deletes a member the caller may manage. The revision is
// the one the preview showed, so a member who was changed in between (given a
// tier, for one) is not deleted on a stale confirmation.
func (s *Service) DeleteDirectMember(ctx context.Context, bearer, id string, q MemberDeletion) error {
	if q.ExpectedRevision < 1 {
		return ErrDirectInput
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	target, e := s.memberTargetTx(ctx, tx, bearer, id)
	if e != nil {
		return e
	}
	if target.Revision != q.ExpectedRevision {
		return ErrProfileChanged
	}
	if e = s.recordOwnerMembershipEndingTx(ctx, tx, id); e != nil {
		return e
	}
	if e = s.EraseMemberTx(ctx, tx, id); e != nil {
		return e
	}
	return gated.Commit()
}

// EraseMemberTx removes one account and everything that belongs to it, inside
// the caller's transaction. Explicit People erasure and confirmed Start over
// use this writer. A departure reported by Services retains the local data.
// The owner's account is refused; it leaves by transfer.
func (s *Service) EraseMemberTx(ctx context.Context, tx *sql.Tx, account string) error {
	var role string
	if e := tx.QueryRowContext(ctx, `SELECT role FROM direct_memberships WHERE account_id=?`, account).Scan(&role); e != nil {
		return e
	}
	if role == TierOwner {
		return ErrForbidden
	}
	if e := s.recordMembershipEndingTx(ctx, tx, account, "", false); e != nil {
		return e
	}
	// The person's API keys and personal access tokens end first. Each is
	// marked ended before the sessions are, because a key that is still live
	// keeps its session through any sweep of the account's sessions
	// (api_credentials.go); marked, it ends with the rest below.
	if _, e := tx.ExecContext(ctx, `UPDATE api_credentials SET revoked_ms=?,ended_reason='member_deleted' WHERE account_id=? AND revoked_ms=0`, time.Now().UnixMilli(), account); e != nil {
		return e
	}
	// Sessions end through the family hook, so playback, queues and readers
	// the person had open are retired with them.
	if e := s.revokeCredentialsWithReasonTx(ctx, tx, account, RevokedMembershipRemoved); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `UPDATE playback_sessions SET state='stopped' WHERE account_id=? AND state<>'stopped'`, account); e != nil {
		return e
	}
	rows, e := tx.QueryContext(ctx, `SELECT id FROM direct_profiles WHERE account_id=?`, account)
	if e != nil {
		return e
	}
	var profiles []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		profiles = append(profiles, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, profile := range profiles {
		if e = EraseProfileSavedDataTx(ctx, tx, Viewer{Authority: "local", AccountID: account, ProfileID: profile, ServerID: s.ID()}); e != nil {
			return e
		}
	}
	if e = ForgetPlayHistoryNamesTx(ctx, tx, "local", account, ""); e != nil {
		return e
	}
	// A code this person approved and nobody has collected can no longer sign
	// a device in as them.
	if _, e = tx.ExecContext(ctx, `UPDATE quick_connect_v1 SET status='cancelled',grant_cipher=NULL,user_hash=NULL WHERE approver_id=? AND status='approved'`, account); e != nil {
		return e
	}
	// Device records are not tied to the account row, so they go explicitly.
	// The sessions they held were revoked above and keep only opaque ids.
	for _, q := range []string{
		`DELETE FROM identity_device_families WHERE device_id IN(SELECT id FROM identity_devices WHERE authority='local' AND account_id=?)`,
		`DELETE FROM identity_refresh_credentials WHERE device_id IN(SELECT id FROM identity_devices WHERE authority='local' AND account_id=?)`,
		`DELETE FROM identity_devices WHERE authority='local' AND account_id=?`,
	} {
		if _, e = tx.ExecContext(ctx, q, account); e != nil {
			return e
		}
	}
	if e = preferenceownership.EraseAccount(ctx, tx, "local", account); e != nil {
		return e
	}
	// Everything else that names the account (membership, profiles, limits,
	// second factor, remembered sign-ins, the Portico Account link) goes with
	// its row. Removing a link tells the Portico Account service, through the
	// membership journal, that this person is no longer a member here.
	_, e = tx.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, account)
	return e
}

// ForgetPlayHistoryNamesTx blanks who a play was by, in the owner's play
// history, for one account or (with a profile named) one of its profiles. The
// plays stay; the names do not.
func ForgetPlayHistoryNamesTx(ctx context.Context, tx *sql.Tx, authority, account, profile string) error {
	if account == "" {
		return nil
	}
	if profile != "" {
		_, e := tx.ExecContext(ctx, `UPDATE play_history SET profile_name='' WHERE authority=? AND account_id=? AND profile_id=? AND profile_name<>''`, authority, account, profile)
		return e
	}
	_, e := tx.ExecContext(ctx, `UPDATE play_history SET user_name='',profile_name='',device_name='' WHERE authority=? AND account_id=? AND (user_name<>'' OR profile_name<>'' OR device_name<>'')`, authority, account)
	return e
}
