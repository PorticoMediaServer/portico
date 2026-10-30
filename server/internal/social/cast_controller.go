package social

// Root50: returning control is a relationship, never installation possession.
// These records grant only channel control. They issue no session/media/queue grant.
import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type CastControllerScope struct {
	ServerID       string `json:"serverId"`
	Authority      string `json:"authority"`
	AccountID      string `json:"accountId"`
	ProfileID      string `json:"profileId"`
	SenderDeviceID string `json:"senderDeviceId"`
	SenderFamilyID string `json:"senderFamilyId"`
}
type CastControllerBinding struct {
	ID               string              `json:"id"`
	BootstrapID      string              `json:"bootstrapId"`
	ReceiverID       string              `json:"receiverId"`
	TargetID         string              `json:"targetId"`
	TargetFamilyID   string              `json:"targetFamilyId"`
	TargetGeneration string              `json:"targetGeneration"`
	Scope            CastControllerScope `json:"scope"`
}
type CastControllerRegister struct {
	BootstrapID  string `json:"bootstrapId"`
	ControlToken string `json:"controlToken"`
}
type CastControllerValidation struct {
	Binding   CastControllerBinding `json:"binding"`
	AttemptID string                `json:"attemptId"`
}
type CastControllerCheck struct {
	BindingID    string `json:"bindingId"`
	ControlToken string `json:"controlToken"`
	AttemptID    string `json:"attemptId,omitempty"`
	Ticket       string `json:"ticket,omitempty"`
}
type CastControllerResponse struct {
	Binding   CastControllerBinding `json:"binding"`
	AttemptID string                `json:"attemptId,omitempty"`
	Ticket    string                `json:"ticket,omitempty"`
}

type castControllerRow struct {
	binding     CastControllerBinding
	proof, role string
	epoch       int
}

func readCastController(ctx context.Context, tx *sql.Tx, id string) (castControllerRow, error) {
	var r castControllerRow
	e := tx.QueryRowContext(ctx, `SELECT id,bootstrap_id,receiver_id,target_id,target_family_id,target_generation,server_id,authority,account_id,profile_id,sender_device_id,sender_family_id,proof_digest,sender_role,sender_epoch FROM social_cast_controllers WHERE id=?`, id).Scan(&r.binding.ID, &r.binding.BootstrapID, &r.binding.ReceiverID, &r.binding.TargetID, &r.binding.TargetFamilyID, &r.binding.TargetGeneration, &r.binding.Scope.ServerID, &r.binding.Scope.Authority, &r.binding.Scope.AccountID, &r.binding.Scope.ProfileID, &r.binding.Scope.SenderDeviceID, &r.binding.Scope.SenderFamilyID, &r.proof, &r.role, &r.epoch)
	if errors.Is(e, sql.ErrNoRows) {
		e = identity.ErrForbidden
	}
	return r, e
}
func (s *Store) controllerCurrent(ctx context.Context, tx *sql.Tx, r castControllerRow) error {
	b := r.binding
	v := b.Scope
	expected := identity.Principal{Viewer: identity.Viewer{ServerID: v.ServerID, Authority: v.Authority, AccountID: v.AccountID, ProfileID: v.ProfileID, Role: r.role}, Epoch: r.epoch}
	family, e := s.Identity.FamilyAuthorityTx(ctx, tx, v.SenderFamilyID, expected)
	if e != nil {
		return e
	}
	if family.DeviceID != v.SenderDeviceID {
		return identity.ErrForbidden
	}
	var found int
	e = tx.QueryRowContext(ctx, `SELECT 1 FROM social_cast_devices WHERE id=? AND bootstrap_id=? AND session_family_id=? AND generation=? AND state='active' AND expires_at_ms>? AND authority=? AND account_id=? AND profile_id=? AND account_epoch=?`, b.TargetID, b.BootstrapID, b.TargetFamilyID, b.TargetGeneration, s.ms(), v.Authority, v.AccountID, v.ProfileID, r.epoch).Scan(&found)
	if e != nil {
		return identity.ErrForbidden
	}
	// The target family must also be live, approved and current. Its role is
	// always member, independently of the source's tier.
	expected.Role = "member"
	expected.Grant = identity.GrantCastTarget
	_, e = s.Identity.FamilyAuthorityTx(ctx, tx, b.TargetFamilyID, expected)
	return e
}

// Called only by the currently paired receiver after an accepted takeover. A
// denied pending pair cannot register, replace or stop the working controller.
func (s *Store) CastControllerRegister(ctx context.Context, p identity.Principal, req CastControllerRegister) (CastControllerResponse, error) {
	var out CastControllerResponse
	if s.Identity == nil || p.Grant != identity.GrantCastTarget || !ValidID(req.BootstrapID) || len(req.ControlToken) != 48 {
		return out, identity.ErrForbidden
	}
	gated, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	target, e := s.Identity.SessionFamilyTx(ctx, tx, p)
	if e != nil {
		return out, e
	}
	var r castControllerRow
	b := &r.binding
	v := &b.Scope
	b.BootstrapID = req.BootstrapID
	b.TargetFamilyID = target.ID
	v.ServerID = p.ServerID
	v.Authority = p.Authority
	v.AccountID = p.AccountID
	v.ProfileID = p.ProfileID
	r.epoch = p.Epoch
	r.proof = digest(req.ControlToken)
	e = tx.QueryRowContext(ctx, `SELECT c.id,CAST(c.generation AS TEXT),b.sdk_receiver_id,b.pairing_family_id,b.pairing_device_id,b.role FROM social_cast_devices c JOIN social_cast_bootstraps b ON b.id=c.bootstrap_id WHERE c.bootstrap_id=? AND c.session_family_id=? AND c.state='active' AND b.state='redeemed' AND b.pairing_device_id<>''`, req.BootstrapID, target.ID).Scan(&b.TargetID, &b.TargetGeneration, &b.ReceiverID, &v.SenderFamilyID, &v.SenderDeviceID, &r.role)
	if e != nil {
		return out, identity.ErrForbidden
	}
	if e = s.controllerCurrent(ctx, tx, r); e != nil {
		return out, e
	}
	// Idempotent registration of the same proof, never proof rotation through a
	// retry or a stale previously paired receiver.
	e = tx.QueryRowContext(ctx, `SELECT id FROM social_cast_controllers WHERE bootstrap_id=?`, req.BootstrapID).Scan(&b.ID)
	if errors.Is(e, sql.ErrNoRows) {
		b.ID = token("cct_")
		_, e = tx.ExecContext(ctx, `INSERT INTO social_cast_controllers(id,bootstrap_id,receiver_id,target_id,target_family_id,target_generation,server_id,authority,account_id,profile_id,sender_device_id,sender_family_id,sender_role,sender_epoch,proof_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, b.ID, b.BootstrapID, b.ReceiverID, b.TargetID, b.TargetFamilyID, b.TargetGeneration, v.ServerID, v.Authority, v.AccountID, v.ProfileID, v.SenderDeviceID, v.SenderFamilyID, r.role, r.epoch, r.proof)
	} else if e == nil {
		var prior castControllerRow
		prior, e = readCastController(ctx, tx, b.ID)
		if e == nil && (prior.proof != r.proof || prior.binding != *b) {
			e = identity.ErrForbidden
		}
	}
	if e != nil {
		return out, e
	}
	out.Binding = *b
	return out, gated.Commit()
}

// Fresh authenticated original sender, before the saved channel proof is used.
func (s *Store) CastControllerValidate(ctx context.Context, p identity.Principal, req CastControllerValidation) (CastControllerResponse, error) {
	var out CastControllerResponse
	if s.Identity == nil || p.Grant != "" || !ValidID(req.Binding.ID) || !ValidID(req.AttemptID) {
		return out, identity.ErrForbidden
	}
	gated, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	r, e := readCastController(ctx, tx, req.Binding.ID)
	if e != nil {
		return out, e
	}
	b := r.binding
	v := b.Scope
	family, e := s.Identity.SessionFamilyTx(ctx, tx, p)
	if e != nil {
		return out, e
	}
	if b != req.Binding || p.ServerID != v.ServerID || p.Authority != v.Authority || p.AccountID != v.AccountID || p.ProfileID != v.ProfileID || family.ID != v.SenderFamilyID || family.DeviceID != v.SenderDeviceID {
		return out, identity.ErrForbidden
	}
	if e = s.controllerCurrent(ctx, tx, r); e != nil {
		return out, e
	}
	ticket := identity.Token()
	_, e = tx.ExecContext(ctx, `UPDATE social_cast_controllers SET ticket_digest=?,ticket_attempt=?,ticket_expires_ms=? WHERE id=?`, digest(ticket), req.AttemptID, s.ms()+15000, b.ID)
	if e != nil {
		return out, e
	}
	out = CastControllerResponse{Binding: b, AttemptID: req.AttemptID, Ticket: ticket}
	return out, gated.Commit()
}

// The receiving session checks both live families and its own exact generation
// before re-binding SDK control, disclosing status, or executing later commands.
func (s *Store) CastControllerCheck(ctx context.Context, p identity.Principal, req CastControllerCheck) (CastControllerResponse, error) {
	var out CastControllerResponse
	if s.Identity == nil || p.Grant != identity.GrantCastTarget || !ValidID(req.BindingID) || len(req.ControlToken) != 48 {
		return out, identity.ErrForbidden
	}
	gated, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	r, e := readCastController(ctx, tx, req.BindingID)
	if e != nil {
		return out, e
	}
	b := r.binding
	v := b.Scope
	family, e := s.Identity.SessionFamilyTx(ctx, tx, p)
	if e != nil {
		return out, e
	}
	if p.ServerID != v.ServerID || p.Authority != v.Authority || p.AccountID != v.AccountID || p.ProfileID != v.ProfileID || family.ID != b.TargetFamilyID || r.proof != digest(req.ControlToken) {
		return out, identity.ErrForbidden
	}
	if e = s.controllerCurrent(ctx, tx, r); e != nil {
		return out, e
	}
	if req.AttemptID != "" || req.Ticket != "" {
		var found int
		if !ValidID(req.AttemptID) || len(req.Ticket) > 256 || len(req.Ticket) < 43 {
			return out, identity.ErrForbidden
		}
		e = tx.QueryRowContext(ctx, `SELECT 1 FROM social_cast_controllers WHERE id=? AND ticket_digest=? AND ticket_attempt=? AND ticket_expires_ms>?`, b.ID, digest(req.Ticket), req.AttemptID, s.ms()).Scan(&found)
		if e != nil {
			return out, identity.ErrForbidden
		}
	}
	out = CastControllerResponse{Binding: b, AttemptID: req.AttemptID}
	return out, gated.Commit()
}
