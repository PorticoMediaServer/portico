package identity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"time"
)

var ErrMoveWaiting = errors.New("This account is waiting to move. Use its move link or code.")
var ErrMoveCollision = errors.New("That Portico Account is already on this server as another account.")
var ErrMoveConfirmation = errors.New("Type the server's name exactly to confirm.")
var ErrMoveCode = errors.New("This move code is no longer usable. Ask the server owner for another.")

type AccountMove struct {
	// Captured informational input for the separately signed owner-switch proof.
	// It never appears as an unsigned field in the public move DTO.
	otherPeopleCount   *int       `json:"-"`
	ID                 string     `json:"id"`
	From               SignInKind `json:"from"`
	To                 SignInKind `json:"to"`
	StartOver          bool       `json:"startOver"`
	Stage              string     `json:"stage"`
	Revision           int64      `json:"revision"`
	People             int        `json:"people"`
	Devices            *int       `json:"devices,omitempty"`
	Invitations        int        `json:"invitations"`
	Profiles           int        `json:"profiles"`
	RetainedProfiles   *int64     `json:"retainedProfiles,omitempty"`
	RemovedProfiles    *int64     `json:"removedProfiles,omitempty"`
	Waiting            int        `json:"waiting"`
	ServerName         string     `json:"serverName"`
	OwnAddressRequired bool       `json:"ownAddressRequired"`
}
type MovePreviewRequest struct {
	To        SignInKind `json:"to"`
	StartOver bool       `json:"startOver"`
}
type MoveConfirmRequest struct {
	MoveID           string `json:"moveId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Confirmation     string `json:"confirmation"`
	Username         string `json:"username"`
	Password         string `json:"password"`
}

func (MoveConfirmRequest) String() string { return "[account move credentials redacted]" }

type MoveCode struct {
	ServerID  string `json:"serverId,omitempty"`
	AccountID string `json:"accountId"`
	Username  string `json:"username"`
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
}
type MoveCodes struct {
	Items []MoveCode `json:"items"`
}
type MoveCodeRequest struct {
	AccountIDs       []string `json:"accountIds"`
	MoveID           string   `json:"moveId,omitempty"`
	ExpectedRevision int64    `json:"expectedRevision,omitempty"`
}
type LocalMoveRedemption struct {
	Code     string `json:"code"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (LocalMoveRedemption) String() string { return "[local move redemption redacted]" }

type PorticoMoveProof struct {
	Code   string
	Ticket string
	Who    PorticoIdentity
}

// DepartureWire has a fixed canonical order shared with the Hosted producer.
// It is a deny-only notice for an already installed tuple, never a grant.
type DepartureWire struct {
	Kind                 string `json:"kind"`
	Version              string `json:"version"`
	Audience             string `json:"audience"`
	OperationID          string `json:"operationId"`
	ServerID             string `json:"serverId"`
	PublicKey            string `json:"publicKey"`
	ClaimGeneration      string `json:"claimGeneration"`
	CredentialGeneration string `json:"credentialGeneration"`
	NoticeID             string `json:"noticeId"`
	Reason               string `json:"reason"`
}
type DepartureNotice struct {
	Wire      DepartureWire
	Payload   []byte
	Signature []byte
}

func (s *Service) moveOwnerTx(ctx context.Context, tx *sql.Tx, bearer string) (directCaller, error) {
	c, e := s.profileManagerTx(ctx, tx, bearer)
	if e != nil {
		return c, e
	}
	if c.account.Role != TierOwner {
		return c, ErrForbidden
	}
	return c, nil
}
func readMoveTx(ctx context.Context, tx *sql.Tx, id string) (out AccountMove, err error) {
	var devices int
	var count, retained, removed sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT id,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,invitations,other_people_count,retained_profiles,removed_profiles FROM identity_moves WHERE id=?`, id).Scan(&out.ID, &out.From, &out.To, &out.StartOver, &out.Stage, &out.Revision, &out.People, &devices, &out.Profiles, &out.ServerName, &out.Invitations, &count, &retained, &removed)
	if err == nil && count.Valid && count.Int64 >= 0 && count.Int64 <= 10000 {
		value := int(count.Int64)
		out.otherPeopleCount = &value
	}
	if err == nil && (retained.Valid || removed.Valid) {
		if !out.StartOver || !retained.Valid || !removed.Valid || retained.Int64 < 0 || removed.Int64 < 0 || retained.Int64 > 9007199254740991 || removed.Int64 > 9007199254740991 || retained.Int64 > 9007199254740991-removed.Int64 || retained.Int64+removed.Int64 != int64(out.Profiles) {
			return out, ErrDirectInput
		}
		out.RetainedProfiles, out.RemovedProfiles = &retained.Int64, &removed.Int64
	}
	if !out.StartOver {
		out.Devices = &devices
	}
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM identity_move_waiting WHERE move_id=? AND state='waiting'`, id).Scan(&out.Waiting)
	}
	out.OwnAddressRequired = out.To == SignInLocal
	return
}
func (s *Service) PreviewAccountMove(ctx context.Context, bearer string, q MovePreviewRequest) (out AccountMove, err error) {
	if q.To != SignInLocal && q.To != SignInPortico {
		return out, ErrDirectInput
	}
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		c, e := s.moveWebOwnerTx(ctx, tx, bearer)
		if e != nil {
			return e
		}
		kind, e := SignInKindTx(ctx, tx)
		if e != nil {
			return e
		}
		if kind == q.To {
			return ErrConflict
		}
		var active string
		e = tx.QueryRowContext(ctx, `SELECT id FROM identity_moves WHERE stage NOT IN('complete','cancelled')`).Scan(&active)
		if e == nil {
			v, e := readMoveTx(ctx, tx, active)
			if e != nil {
				return e
			}
			if v.To != q.To || v.StartOver != q.StartOver {
				return ErrConflict
			}
			out = v
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		out = AccountMove{ID: Token(), From: kind, To: q.To, StartOver: q.StartOver, Stage: "preview", Revision: 1, ServerName: s.nameTx(ctx, tx), OwnAddressRequired: q.To == SignInLocal}
		var devices int
		if e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM accounts WHERE id<>?1),(SELECT count(*) FROM identity_devices WHERE platform<>'api'),(SELECT count(*) FROM direct_profiles WHERE deleted=0)`, c.account.ID).Scan(&out.People, &devices, &out.Profiles); e != nil {
			return e
		}
		if q.StartOver {
			// Capture once with the same undeleted base and exact move owner.
			// Later reads and active-preview reuse only replay these stored facts.
			var retained, removed int64
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE account_id=?1),count(*) FILTER (WHERE account_id<>?1) FROM direct_profiles WHERE deleted=0`, c.account.ID).Scan(&retained, &removed); e != nil {
				return e
			}
			if retained < 0 || removed < 0 || retained > 9007199254740991-removed || retained+removed != int64(out.Profiles) {
				return ErrDirectInput
			}
			out.RetainedProfiles, out.RemovedProfiles = &retained, &removed
		}
		if kind == SignInLocal && q.To == SignInPortico {
			out.otherPeopleCount, e = captureOwnerSwitchCountTx(ctx, tx, c.account.ID)
			if e != nil {
				return e
			}
		}
		if !q.StartOver {
			out.Devices = &devices
		}
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM access_invitations WHERE state='pending'`).Scan(&out.Invitations); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms,invitations,other_people_count,retained_profiles,removed_profiles) VALUES(?,?,?,?,?,?,'preview',1,?,?,?,?,?,?,?,?,?)`, out.ID, c.account.ID, c.account.Revision, kind, q.To, q.StartOver, out.People, devices, out.Profiles, out.ServerName, time.Now().UnixMilli(), out.Invitations, out.otherPeopleCount, out.RetainedProfiles, out.RemovedProfiles)
		return e
	})
	return
}
func (s *Service) nameTx(ctx context.Context, tx *sql.Tx) string {
	var name string
	_ = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='name'`).Scan(&name)
	if name == "" {
		name = "Portico"
	}
	return name
}
func (s *Service) AccountMove(ctx context.Context, bearer string) (out *AccountMove, err error) {
	g, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return nil, e
	}
	defer g.Rollback()
	if _, e = s.moveOwnerTx(ctx, g.Tx(), bearer); e != nil {
		return nil, e
	}
	var id string
	e = g.Tx().QueryRowContext(ctx, `SELECT id FROM identity_moves WHERE stage<>'cancelled' ORDER BY created_ms DESC LIMIT 1`).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	v, e := readMoveTx(ctx, g.Tx(), id)
	return &v, e
}
func (s *Service) CancelAccountMove(ctx context.Context, bearer, id string, revision int64) error {
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if _, e := s.moveWebOwnerTx(ctx, tx, bearer); e != nil {
			return e
		}
		result, e := tx.ExecContext(ctx, `UPDATE identity_moves SET stage='cancelled',revision=revision+1 WHERE id=? AND revision=? AND stage='preview' AND owner_confirmed=0`, id, revision)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrProfileChanged
		}
		return nil
	})
}
func (s *Service) confirmMoveTx(ctx context.Context, tx *sql.Tx, c directCaller, q MoveConfirmRequest, to SignInKind) (AccountMove, error) {
	v, e := readMoveTx(ctx, tx, q.MoveID)
	if e != nil {
		return v, e
	}
	var owner string
	var ownerRevision int64
	if e = tx.QueryRowContext(ctx, `SELECT owner_id,owner_revision FROM identity_moves WHERE id=?`, v.ID).Scan(&owner, &ownerRevision); e != nil {
		return v, e
	}
	if v.Revision != q.ExpectedRevision || v.Stage != "preview" || v.To != to || owner != c.account.ID || ownerRevision != c.account.Revision {
		return v, ErrProfileChanged
	}
	kind, e := SignInKindTx(ctx, tx)
	if e != nil {
		return v, e
	}
	if kind != v.From {
		return v, ErrProfileChanged
	}
	if v.StartOver && q.Confirmation != s.nameTx(ctx, tx) {
		return v, ErrMoveConfirmation
	}
	var people, devices, invitations int
	if e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM accounts WHERE id<>?1),(SELECT count(*) FROM identity_devices WHERE platform<>'api')`, c.account.ID).Scan(&people, &devices); e != nil {
		return v, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM access_invitations WHERE state='pending'`).Scan(&invitations); e != nil {
		return v, e
	}
	if people != v.People || invitations != v.Invitations || (v.Devices != nil && devices != *v.Devices) {
		return v, ErrProfileChanged
	}
	return v, nil
}
func (s *Service) prepareMoveMembersTx(ctx context.Context, tx *sql.Tx, c directCaller, v AccountMove) error {
	return s.prepareMoveMembersKeepingFamilyTx(ctx, tx, c, v, "")
}

// The sole nonempty keepFamily caller is the guarded owner-claim installation
// hook. Keeping that family does not keep owner rights: the caller must narrow
// it in this same transaction before publishing the installed claim.
func (s *Service) prepareMoveMembersKeepingFamilyTx(ctx context.Context, tx *sql.Tx, c directCaller, v AccountMove, keepFamily string) error {
	if keepFamily != "" && (keepFamily != c.familyID || c.account.Role != TierOwner || v.To != SignInPortico) {
		return ErrForbidden
	}
	rows, e := tx.QueryContext(ctx, `SELECT id FROM accounts WHERE id<>? ORDER BY id`, c.account.ID)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, id := range ids {
		if v.StartOver {
			if _, e = tx.ExecContext(ctx, `DELETE FROM play_history WHERE authority='local' AND account_id=?`, id); e != nil {
				return e
			}
			if e = s.EraseMemberTx(ctx, tx, id); e != nil {
				return e
			}
		} else {
			// Disabled accounts retain their disabled state. Waiting is an additional
			// sign-in fence, not a disable operation that would destroy their API keys.
			if _, e = tx.ExecContext(ctx, `INSERT INTO identity_move_waiting(account_id,move_id,state,old_password_hash,old_password_until_ms,created_ms) SELECT id,?,'waiting',CASE WHEN ?='portico' THEN password_hash ELSE NULL END,CASE WHEN ?='portico' THEN ? ELSE 0 END,? FROM accounts WHERE id=? ON CONFLICT(account_id) DO UPDATE SET move_id=excluded.move_id,state=excluded.state,old_password_hash=excluded.old_password_hash,old_password_until_ms=excluded.old_password_until_ms,created_ms=excluded.created_ms`, v.ID, v.To, v.To, time.Now().Add(30*24*time.Hour).UnixMilli(), time.Now().UnixMilli(), id); e != nil {
				return e
			}
			if e = s.revokeCredentialsWithReasonTx(ctx, tx, id, RevokedExplicitSignout); e != nil {
				return e
			}
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE accounts SET password_hash=X'',epoch=epoch+CASE WHEN id=? AND ?<>'' THEN 0 ELSE 1 END`, c.account.ID, keepFamily); e != nil {
		return e
	}
	for _, query := range []string{`DELETE FROM identity_account_factors`, `DELETE FROM identity_recovery_codes`, `DELETE FROM identity_factor_challenges`, `DELETE FROM member_password_resets`, `DELETE FROM identity_move_codes`, `DELETE FROM identity_move_link_tickets`, `UPDATE access_invitations SET state='revoked',revision=revision+1 WHERE state='pending'`, `UPDATE quick_connect_v1 SET status='cancelled',grant_cipher=NULL,user_hash=NULL WHERE status IN('pending','approved')`} {
		if _, e = tx.ExecContext(ctx, query); e != nil {
			return e
		}
	}
	if keepFamily == "" {
		return s.revokeCredentialsWithReasonTx(ctx, tx, c.account.ID, RevokedExplicitSignout)
	}
	_, e = s.revokeWhereTx(ctx, tx, RevokedExplicitSignout, `authority='local' AND account_id=? AND id<>?`, c.account.ID, keepFamily)
	return e
}

// accountMovePlan bounds the move's security fence: the account's sign-in,
// profile and session rows move together, touching more indexed pages than a
// single revocation does.
var accountMovePlan = dbwork.WritePlan{Name: "account-move", Rows: 160, Pages: 128, VMOps: 500_000, Why: "an account moves atomically with its sign-in, profiles and sessions; it is a single security fence"}

func (s *Service) ConfirmLocalMove(ctx context.Context, bearer string, q MoveConfirmRequest) (out DirectSignIn, err error) {
	ctx = dbwork.WithPlan(ctx, accountMovePlan)
	if !directPassword(q.Password) {
		return out, ErrDirectInput
	}
	q.Username = strings.ToLower(strings.TrimSpace(q.Username))
	if len(q.Username) < 3 || len(q.Username) > 64 || strings.ContainsAny(q.Username, " \t\r\n@") || !directName(q.Username) {
		return out, ErrDirectInput
	}
	if err = s.takeHashSlot(ctx); err != nil {
		return out, err
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(q.Password), PasswordCost)
	<-s.hashSlots
	if e != nil {
		return out, e
	}
	// Signing is local, before opening the writer. Recheck every tuple and the
	// owner in the actual writer before publishing either the notice or the flip.
	var notice *DepartureNotice
	if s.PrepareIdentityDeparture != nil {
		reason := "moving_to_local"
		g, e := dbwork.BeginSnapshot(ctx, s.db)
		if e != nil {
			return out, e
		}
		v, e := readMoveTx(ctx, g.Tx(), q.MoveID)
		g.Rollback()
		if e != nil {
			return out, e
		}
		if v.StartOver {
			reason = "start_over_local"
		}
		notice, e = s.PrepareIdentityDeparture(ctx, reason)
		if e != nil {
			return out, e
		}
	}
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		c, e := s.moveWebOwnerTx(ctx, tx, bearer)
		if e != nil {
			return e
		}
		v, e := s.confirmMoveTx(ctx, tx, c, q, SignInLocal)
		if e != nil {
			return e
		}
		var taken bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE username=? COLLATE NOCASE AND id<>?)`, q.Username, c.account.ID).Scan(&taken); e != nil {
			return e
		}
		if taken {
			return ErrRegistrationTaken
		}
		if e = ValidateNewLocalPasswordTx(ctx, tx, q.Password); e != nil {
			return e
		}
		if e = s.persistDepartureTx(ctx, tx, notice, v); e != nil {
			return e
		}
		if e = s.prepareMoveMembersTx(ctx, tx, c, v); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM account_portico_links`); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE identity_state SET sign_in_kind='local',revision=revision+1 WHERE singleton=1`); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE accounts SET username=?,password_hash=?,email='',display_name='' WHERE id=?`, q.Username, hash, c.account.ID); e != nil {
			return e
		}
		// Invalidate all positive managed routing material in the same commit.
		for _, query := range []string{`DELETE FROM networking_certificates`, `DELETE FROM networking_certificate_state`, `UPDATE networking_certificate_settings SET enabled=0,revision=revision+1`, `DELETE FROM networking_claim_credentials`, `UPDATE networking_claim_identity SET active_operation_id=NULL,installed_operation_id=NULL,pending_operation_id=NULL`, `UPDATE networking_claim_authority SET state='unclaimed' WHERE state='active'`, `UPDATE identity_managed_route SET enabled=0,revision=revision+1 WHERE singleton=1`} {
			if _, e = tx.ExecContext(ctx, query); e != nil {
				return e
			}
		}
		stage := "moving_to_local"
		if v.StartOver || v.People == 0 {
			stage = "complete"
		}
		if _, e = tx.ExecContext(ctx, `UPDATE identity_moves SET stage=?,revision=revision+1,confirmed_ms=? WHERE id=?`, stage, time.Now().UnixMilli(), v.ID); e != nil {
			return e
		}
		deviceID := c.deviceID
		c, e = s.directAccountTx(ctx, tx, c.account.ID)
		if e != nil {
			return e
		}
		out, e = s.directSignInTx(withExistingDevice(ctx, deviceID), tx, c, true)
		return e
	})
	return
}
func (s *Service) persistDepartureTx(ctx context.Context, tx *sql.Tx, notice *DepartureNotice, v AccountMove) error {
	var op, server, claim, credential string
	var public []byte
	e := tx.QueryRowContext(ctx, `SELECT i.operation_id,i.server_id,i.public_key,i.claim_generation,i.credential_generation FROM networking_claim_identity c JOIN networking_claim_intents i ON i.operation_id=c.installed_operation_id WHERE c.singleton=1 AND i.stage='installed'`).Scan(&op, &server, &public, &claim, &credential)
	if errors.Is(e, sql.ErrNoRows) {
		if notice != nil {
			return ErrUnauthorized
		}
		return nil
	}
	if e != nil {
		return e
	}
	if notice == nil {
		return ErrUnauthorized
	}
	w := notice.Wire
	canonical, _ := json.Marshal(w)
	reason := "moving_to_local"
	if v.StartOver {
		reason = "start_over_local"
	}
	if w.Kind != "portico.server.leave" || w.Version != "1" || w.OperationID != op || w.ServerID != server || w.ClaimGeneration != claim || w.CredentialGeneration != credential || w.PublicKey != base64.RawURLEncoding.EncodeToString(public) || w.NoticeID == "" || w.Reason != reason || !bytes.Equal(canonical, notice.Payload) || !ed25519.Verify(public, canonical, notice.Signature) {
		return ErrUnauthorized
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO identity_service_outbox(notice_id,operation_id,server_id,claim_generation,credential_generation,payload,signature,created_ms) VALUES(?,?,?,?,?,?,?,?)`, w.NoticeID, op, server, claim, credential, notice.Payload, notice.Signature, time.Now().UnixMilli())
	return e
}
func (s *Service) CompleteMoveToPorticoTx(ctx context.Context, tx *sql.Tx, c directCaller, who PorticoIdentity) (DirectSignIn, error) {
	var id string
	var revision int64
	var confirmation string
	if e := tx.QueryRowContext(ctx, `SELECT id,revision,confirmation FROM identity_moves WHERE owner_id=? AND to_kind='portico' AND stage='preview' AND owner_confirmed=1`, c.account.ID).Scan(&id, &revision, &confirmation); e != nil {
		return DirectSignIn{}, e
	}
	q := MoveConfirmRequest{MoveID: id, ExpectedRevision: revision, Confirmation: confirmation}
	v, e := s.confirmMoveTx(ctx, tx, c, q, SignInPortico)
	if e != nil {
		return DirectSignIn{}, e
	}
	if who.Return == nil || who.Return.Purpose != "switch" {
		return DirectSignIn{}, ErrUnauthorized
	}
	if e = CheckPorticoReturnTx(ctx, tx, who); e != nil {
		return DirectSignIn{}, e
	}
	var reserved bool
	var username string
	if e = tx.QueryRowContext(ctx, `SELECT owner_change_reserved,approved_username FROM networking_claim_intents WHERE operation_id=? AND account_id=?`, who.Return.OperationID, who.AccountID).Scan(&reserved, &username); e != nil {
		return DirectSignIn{}, e
	}
	if !reserved || username != who.Username {
		return DirectSignIn{}, ErrUnauthorized
	}
	var collision bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_portico_links WHERE hosted_account_id=? AND account_id<>?) OR EXISTS(SELECT 1 FROM accounts WHERE username=? COLLATE NOCASE AND id<>?)`, who.AccountID, c.account.ID, username, c.account.ID).Scan(&collision); e != nil {
		return DirectSignIn{}, e
	}
	if collision {
		return DirectSignIn{}, ErrMoveCollision
	}
	if e = s.prepareMoveMembersTx(ctx, tx, c, v); e != nil {
		return DirectSignIn{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_claim_intents SET owner_change_reserved=0 WHERE operation_id=?`, who.Return.OperationID); e != nil {
		return DirectSignIn{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE identity_state SET sign_in_kind='portico',revision=revision+1`); e != nil {
		return DirectSignIn{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO account_portico_links(account_id,hosted_account_id,consent) VALUES(?,?,?)`, c.account.ID, who.AccountID, who.Consent); e != nil {
		return DirectSignIn{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE accounts SET username=? WHERE id=?`, username, c.account.ID); e != nil {
		return DirectSignIn{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE identity_managed_route SET enabled=1,revision=revision+1 WHERE singleton=1`); e != nil {
		return DirectSignIn{}, e
	}
	stage := "moving_to_portico"
	if v.StartOver || v.People == 0 {
		stage = "complete"
	}
	if _, e = tx.ExecContext(ctx, `UPDATE identity_moves SET stage=?,revision=revision+1,confirmed_ms=? WHERE id=?`, stage, time.Now().UnixMilli(), v.ID); e != nil {
		return DirectSignIn{}, e
	}
	return s.PorticoSignInTx(ctx, tx, who)
}
func (s *Service) ConfirmPorticoMove(ctx context.Context, bearer string, q MoveConfirmRequest) (AccountMove, error) {
	var out AccountMove
	e := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		c, e := s.moveWebOwnerTx(ctx, tx, bearer)
		if e != nil {
			return e
		}
		v, e := s.confirmMoveTx(ctx, tx, c, q, SignInPortico)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE identity_moves SET confirmation=?,owner_confirmed=1 WHERE id=?`, q.Confirmation, v.ID)
		out = v
		return e
	})
	return out, e
}
func (s *Service) MakeMoveCodes(ctx context.Context, bearer string, q MoveCodeRequest) (out MoveCodes, err error) {
	if len(q.AccountIDs) > 2000 || (q.MoveID == "") != (q.ExpectedRevision == 0) || q.ExpectedRevision < 0 || len(q.MoveID) > 128 {
		return out, ErrDirectInput
	}
	wanted := map[string]bool{}
	for _, id := range q.AccountIDs {
		if !validFamilyID(id) || wanted[id] {
			return out, ErrDirectInput
		}
		wanted[id] = true
	}
	out.Items = []MoveCode{}
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if _, e := s.moveWebOwnerTx(ctx, tx, bearer); e != nil {
			return e
		}
		if q.MoveID != "" {
			move, e := readMoveTx(ctx, tx, q.MoveID)
			if e != nil || move.Revision != q.ExpectedRevision || (move.Stage != "moving_to_local" && move.Stage != "moving_to_portico") {
				return ErrConflict
			}
		}
		serverID, e := LocalLinkServerIDTx(ctx, tx)
		if e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, `SELECT a.id,a.username,w.move_id FROM identity_move_waiting w JOIN accounts a ON a.id=w.account_id JOIN identity_moves m ON m.id=w.move_id WHERE w.state='waiting' AND m.stage IN('moving_to_local','moving_to_portico') AND (?='' OR m.id=?) ORDER BY a.username,a.id`, q.MoveID, q.MoveID)
		if e != nil {
			return e
		}
		type target struct{ id, name, move string }
		targets := []target{}
		for rows.Next() {
			var t target
			if e = rows.Scan(&t.id, &t.name, &t.move); e != nil {
				rows.Close()
				return e
			}
			if len(wanted) == 0 || wanted[t.id] {
				targets = append(targets, t)
				if len(targets) > 2000 {
					rows.Close()
					return ErrDirectInput
				}
			}
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		if len(wanted) > 0 && len(targets) != len(wanted) {
			return ErrNotVisible
		}
		for _, t := range targets {
			code := Token()
			until := time.Now().Add(7 * 24 * time.Hour)
			if _, e = tx.ExecContext(ctx, `INSERT INTO identity_move_codes(account_id,move_id,code_hash,expires_ms) VALUES(?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET move_id=excluded.move_id,code_hash=excluded.code_hash,expires_ms=excluded.expires_ms`, t.id, t.move, Digest(code), until.UnixMilli()); e != nil {
				return e
			}
			out.Items = append(out.Items, MoveCode{ServerID: serverID, AccountID: t.id, Username: t.name, Code: code, ExpiresAt: until.UTC().Format(time.RFC3339)})
		}
		return nil
	})
	return
}
func waitingCodeTx(ctx context.Context, tx *sql.Tx, code string) (string, error) {
	if len(code) != 43 {
		return "", ErrMoveCode
	}
	var id string
	e := tx.QueryRowContext(ctx, `SELECT c.account_id FROM identity_move_codes c JOIN identity_move_waiting w ON w.account_id=c.account_id AND w.move_id=c.move_id JOIN identity_moves m ON m.id=w.move_id WHERE c.code_hash=? AND c.expires_ms>? AND w.state='waiting' AND m.stage IN('moving_to_local','moving_to_portico')`, Digest(code), time.Now().UnixMilli()).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return "", ErrMoveCode
	}
	return id, e
}
func finishWaitingTx(ctx context.Context, tx *sql.Tx, account string) error {
	if _, e := tx.ExecContext(ctx, `UPDATE identity_move_waiting SET state='linked',old_password_hash=NULL,old_password_until_ms=0 WHERE account_id=? AND state='waiting'`, account); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM identity_move_link_tickets WHERE account_id=?`, account); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM identity_move_codes WHERE account_id=?`, account); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `UPDATE identity_moves SET stage='complete',revision=revision+1 WHERE stage IN('moving_to_local','moving_to_portico') AND NOT EXISTS(SELECT 1 FROM identity_move_waiting w WHERE w.move_id=identity_moves.id AND w.state='waiting')`)
	return e
}
func (s *Service) RedeemLocalMove(ctx context.Context, q LocalMoveRedemption) (out DirectSignIn, err error) {
	if err = s.RequireSignInKind(ctx, SignInLocal); err != nil {
		return out, err
	}
	q.Username = strings.ToLower(strings.TrimSpace(q.Username))
	if len(q.Code) != 43 || len(q.Username) < 3 || len(q.Username) > 64 || strings.ContainsAny(q.Username, " \t\r\n@") || !directPassword(q.Password) {
		return out, ErrDirectInput
	}
	if err = s.takeHashSlot(ctx); err != nil {
		return out, err
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(q.Password), PasswordCost)
	<-s.hashSlots
	if e != nil {
		return out, e
	}
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if e := RequireSignInKindTx(ctx, tx, SignInLocal); e != nil {
			return e
		}
		if e := ValidateNewLocalPasswordTx(ctx, tx, q.Password); e != nil {
			return e
		}
		id, e := waitingCodeTx(ctx, tx, q.Code)
		if e != nil {
			return e
		}
		var collision bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE username=? COLLATE NOCASE AND id<>?)`, q.Username, id).Scan(&collision); e != nil {
			return e
		}
		if collision {
			return ErrRegistrationTaken
		}
		if _, e = tx.ExecContext(ctx, `UPDATE accounts SET username=?,password_hash=?,epoch=epoch+1 WHERE id=?`, q.Username, hash, id); e != nil {
			return e
		}
		if e = finishWaitingTx(ctx, tx, id); e != nil {
			return e
		}
		c, e := s.directAccountTx(ctx, tx, id)
		if e != nil {
			return e
		}
		out, e = s.directSignInTx(ctx, tx, c, true)
		return e
	})
	return
}
func (s *Service) RedeemPorticoMove(ctx context.Context, q PorticoMoveProof) (out DirectSignIn, err error) {
	if err = s.RequireSignInKind(ctx, SignInPortico); err != nil {
		return out, err
	}
	if (q.Code == "") == (q.Ticket == "") {
		return out, ErrMoveCode
	}
	var id string
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if e := RequireSignInKindTx(ctx, tx, SignInPortico); e != nil {
			return e
		}
		if q.Code != "" {
			var e error
			id, e = waitingCodeTx(ctx, tx, q.Code)
			if e != nil {
				return e
			}
		} else {
			if len(q.Ticket) != 43 {
				return ErrMoveCode
			}
			e := tx.QueryRowContext(ctx, `SELECT t.account_id FROM identity_move_link_tickets t JOIN identity_move_waiting w ON w.account_id=t.account_id JOIN accounts a ON a.id=t.account_id JOIN identity_moves m ON m.id=w.move_id WHERE t.token_hash=? AND t.installation_id=? AND t.expires_ms>? AND t.epoch=a.epoch AND w.state='waiting' AND w.old_password_until_ms>? AND m.stage='moving_to_portico'`, Digest(q.Ticket), moveInstallation(ctx), time.Now().UnixMilli(), time.Now().UnixMilli()).Scan(&id)
			if errors.Is(e, sql.ErrNoRows) {
				return ErrMoveCode
			}
			if e != nil {
				return e
			}
		}
		if !validHostedAccountID(q.Who.AccountID) || !directName(q.Who.Username) || q.Who.Consent == "" {
			return ErrUnauthorized
		}
		if e := admitPorticoSignerTx(ctx, tx, q.Who.SignerKeyID); e != nil {
			return e
		}
		var collision bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_portico_links l JOIN direct_memberships m ON m.account_id=l.account_id WHERE l.hosted_account_id=? AND l.account_id<>? AND m.disabled=0) OR EXISTS(SELECT 1 FROM accounts WHERE username=? COLLATE NOCASE AND id<>?)`, q.Who.AccountID, id, q.Who.Username, id).Scan(&collision); e != nil {
			return e
		}
		if collision {
			var username string
			if e := tx.QueryRowContext(ctx, `SELECT a.username FROM accounts a LEFT JOIN account_portico_links l ON l.account_id=a.id WHERE a.id<>? AND (l.hosted_account_id=? OR a.username=? COLLATE NOCASE) ORDER BY a.username LIMIT 1`, id, q.Who.AccountID, q.Who.Username).Scan(&username); e != nil {
				return e
			}
			return &MoveCollisionError{Username: username, ServerName: s.nameTx(ctx, tx)}
		}
		if _, e := tx.ExecContext(ctx, `DELETE FROM account_portico_links WHERE hosted_account_id=? AND account_id IN(SELECT account_id FROM direct_memberships WHERE disabled=1)`, q.Who.AccountID); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO account_portico_links(account_id,hosted_account_id,consent) VALUES(?,?,?)`, id, q.Who.AccountID, q.Who.Consent); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `UPDATE accounts SET username=?,password_hash=X'',epoch=epoch+1 WHERE id=?`, q.Who.Username, id); e != nil {
			return e
		}
		if e := finishWaitingTx(ctx, tx, id); e != nil {
			return e
		}
		var e error
		out, e = s.PorticoSignInTx(ctx, tx, q.Who)
		return e
	})
	return
}
func (s *Service) FinishAccountMove(ctx context.Context, bearer, id string, revision int64) error {
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if _, e := s.moveWebOwnerTx(ctx, tx, bearer); e != nil {
			return e
		}
		v, e := readMoveTx(ctx, tx, id)
		if e != nil {
			return e
		}
		if v.Revision != revision || v.Stage != "moving_to_portico" && v.Stage != "moving_to_local" {
			return ErrProfileChanged
		}
		if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET disabled=1,disabled_reason='did_not_move',revision=revision+1 WHERE account_id IN(SELECT account_id FROM identity_move_waiting WHERE move_id=? AND state='waiting')`, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE identity_move_waiting SET state='did_not_move',old_password_hash=NULL,old_password_until_ms=0 WHERE move_id=? AND state='waiting'`, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM identity_move_codes WHERE move_id=?`, id); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE identity_moves SET stage='complete',revision=revision+1 WHERE id=?`, id)
		return e
	})
}
