package identity

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

// ownerCompletion is a normalized operation binding, not a replacement login
// authority. Its proof bytes are read only when preparing/verifying the signed
// operation; current family, membership, challenge and claim rows remain live.
type ownerCompletion struct {
	operation, move, family, token, owner, profile, phase string
	transaction, installation, challenge                  string
	registration, origin                                  string
	generation, epoch, moveRevision, expires              int64
	routeGeneration                                       int64
	consumed                                              int64
	attempt                                               string
}

// ownerCompletionOperationTx finds the family's operation from any of its
// tokens, so a rotated token still finds (and stays narrowed to) its own move.
// The binding itself is checked by CheckOwnerClaimTx.
func ownerCompletionOperationTx(ctx context.Context, tx *sql.Tx, token string) (string, error) {
	var operation string
	err := tx.QueryRowContext(ctx, `SELECT c.operation_id FROM authorization_family_tokens t JOIN identity_owner_completions c ON c.family_id=t.family_id WHERE t.token_hash=?`, token).Scan(&operation)
	return operation, err
}

// CheckClaimRequestOwnerTx is only the networking claim edge's captured guard.
// Once this family has a bound operation, including a terminal one, it can
// never fall back to general owner authority through this method.
func (s *Service) CheckClaimRequestOwnerTx(ctx context.Context, tx *sql.Tx, expected Principal) error {
	if tx == nil || expected.Hash == "" || expected.Authority != "local" || expected.Role != TierOwner {
		return ErrUnauthorized
	}
	operation, err := ownerCompletionOperationTx(ctx, tx, expected.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return s.RequireMainProfileTx(ctx, tx, expected)
	}
	if err != nil {
		return err
	}
	return s.CheckOwnerClaimTx(ctx, tx, expected, operation)
}

// ClaimRequestOwnerByDigestTx restores only a claim guard after a process
// restart. It does not return an ordinary authenticated principal for routes.
func (s *Service) ClaimRequestOwnerByDigestTx(ctx context.Context, tx *sql.Tx, digest string) (Principal, error) {
	if tx == nil || digest == "" {
		return Principal{}, ErrUnauthorized
	}
	family, err := s.familyTokenTx(ctx, tx, digest)
	if err != nil {
		return Principal{}, err
	}
	if err = s.CheckClaimRequestOwnerTx(ctx, tx, family.principal); err != nil {
		return Principal{}, err
	}
	return family.principal, nil
}

// OwnerClaimAuthority admits an already narrowed token only to the dedicated
// claim-operation HTTP edge. Ordinary AuthenticateContext remains default-deny.
func (s *Service) OwnerClaimAuthority(ctx context.Context, bearer string) (Principal, error) {
	if len(bearer) < 43 || len(bearer) > 2048 || CredentialShaped(bearer) {
		return Principal{}, ErrUnauthorized
	}
	g, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return Principal{}, err
	}
	defer g.Rollback()
	p, err := s.ClaimRequestOwnerByDigestTx(ctx, g.Tx(), Digest(bearer))
	if err != nil {
		return Principal{}, err
	}
	if p.Grant != GrantOwnerClaimCompletion {
		return Principal{}, ErrForbidden
	}
	return p, nil
}

func ownerCompletionTx(ctx context.Context, tx *sql.Tx, operation string) (ownerCompletion, error) {
	var c ownerCompletion
	if tx == nil || !validFamilyID(operation) {
		return c, ErrUnauthorized
	}
	err := tx.QueryRowContext(ctx, `SELECT operation_id,move_id,family_id,token_hash,token_generation,owner_id,owner_profile_id,owner_epoch,phase,transaction_id,installation_id,server_challenge_hash,move_revision,expires_ms,consumed_ms,attempt_id,registration_id,registration_origin,route_generation FROM identity_owner_completions WHERE operation_id=?`, operation).Scan(&c.operation, &c.move, &c.family, &c.token, &c.generation, &c.owner, &c.profile, &c.epoch, &c.phase, &c.transaction, &c.installation, &c.challenge, &c.moveRevision, &c.expires, &c.consumed, &c.attempt, &c.registration, &c.origin, &c.routeGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrUnauthorized
	}
	return c, err
}

// CheckOwnerClaimTx alone recognizes the initiating family's same-operation
// transition. Generic SessionFamilyTx, ReauthorizeTx and management gates stay
// unchanged. This method is for the private claim-operation edge, never general
// request authorization. No consumed attempt can be restarted through it.
func (s *Service) CheckOwnerClaimTx(ctx context.Context, tx *sql.Tx, expected Principal, operation string) error {
	_, _, err := s.currentOwnerCompletionTx(ctx, tx, expected, operation, "")
	return err
}

func (s *Service) currentOwnerCompletionTx(ctx context.Context, tx *sql.Tx, expected Principal, operation, attempt string) (ownerCompletion, familyRecord, error) {
	return s.ownerCompletionAuthorityTx(ctx, tx, &expected, operation, attempt, false)
}

// binding is the move's authority: the owner inside the family that started it,
// at the generation captured then. Its token rotates; the move survives that.
func (c ownerCompletion) binding() FamilyBinding {
	return FamilyBinding{FamilyID: c.family, AccountID: c.owner, ProfileID: c.profile, Epoch: int(c.epoch), Generation: c.generation}
}

// caller is nil only for the installation hook, which has no caller session:
// it runs after networking has published the new tuple inside the same,
// still-uncommitted transaction, possibly after the owner's tab has closed.
func (s *Service) ownerCompletionAuthorityTx(ctx context.Context, tx *sql.Tx, caller *Principal, operation, attempt string, installing bool) (ownerCompletion, familyRecord, error) {
	var family familyRecord
	c, err := ownerCompletionTx(ctx, tx, operation)
	if err != nil {
		return c, family, err
	}
	now := time.Now()
	if c.expires <= now.UnixMilli() || caller != nil && (c.owner != caller.AccountID || c.profile != caller.ProfileID || c.epoch != int64(caller.Epoch) || caller.Authority != "local" || caller.Role != TierOwner) {
		return c, family, ErrUnauthorized
	}
	if c.phase != "prepared" && c.phase != "installed" && !(c.phase == "consumed" && attempt != "" && attempt == c.attempt && c.consumed > 0) {
		return c, family, ErrForbidden
	}
	if installing && c.phase != "prepared" {
		return c, family, ErrForbidden
	}
	family, err = s.familyBindingTx(ctx, tx, c.binding(), caller)
	if err != nil {
		return c, family, err
	}
	current := family.principal
	expected := current
	if caller != nil {
		expected = *caller
	}
	if current.Authority != "local" || current.Role != TierOwner {
		return c, family, ErrUnauthorized
	}
	if err = refuseResetDescendantTx(ctx, tx, family.ID); err != nil {
		return c, family, err
	}
	var sameBrowser bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_device_families b JOIN identity_devices d ON d.id=b.device_id WHERE b.family_id=? AND d.authority='local' AND d.account_id=? AND d.installation_id=? AND d.platform='web')`, family.ID, c.owner, c.installation).Scan(&sameBrowser); err != nil {
		return c, family, err
	}
	if !sameBrowser {
		return c, family, ErrUnauthorized
	}
	if c.phase == "prepared" {
		if expected.Grant != "" || current.Grant != "" {
			return c, family, ErrForbidden
		}
		if caller != nil {
			err = s.RequireMainProfileTx(ctx, tx, expected)
		} else {
			err = mainProfileTx(ctx, tx, expected)
		}
		if err != nil {
			return c, family, err
		}
	} else {
		if current.Grant != GrantOwnerClaimCompletion || (expected.Grant != "" && expected.Grant != GrantOwnerClaimCompletion) {
			return c, family, ErrForbidden
		}
		if err = s.checkDirectPrincipalTx(tx, current); err != nil {
			return c, family, err
		}
		var main bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts a JOIN direct_profiles p ON p.account_id=a.id JOIN direct_memberships m ON m.account_id=a.id WHERE a.id=? AND a.profile_id=? AND p.id=? AND p.is_primary=1 AND p.deleted=0 AND a.epoch=? AND m.role='owner' AND m.disabled=0)`, c.owner, c.profile, c.profile, c.epoch).Scan(&main); err != nil {
			return c, family, err
		}
		if !main {
			return c, family, ErrForbidden
		}
	}
	var challenge bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_identity_challenges WHERE challenge_hash=? AND installation_id=? AND expires_ms>=? AND expires_ms>?)`, c.challenge, c.installation, c.expires, now.UnixMilli()).Scan(&challenge)
	if err != nil {
		return c, family, err
	}
	if !challenge {
		return c, family, ErrUnauthorized
	}
	if err = s.ownerCompletionClaimTx(ctx, tx, c, installing); err != nil {
		return c, family, err
	}
	return c, family, ctx.Err()
}

func (s *Service) ownerCompletionClaimTx(ctx context.Context, tx *sql.Tx, c ownerCompletion, installing bool) error {
	var stage, purpose, currentOwner, server, active, installed, pending, kind string
	var account, username, linked, actualUsername, moveStage, moveOwner, moveKind string
	var generation, currentGeneration, moveRevision int64
	var matchingKey, reserved, confirmed, credential, claimActive bool
	err := tx.QueryRowContext(ctx, `SELECT i.stage,i.purpose,i.current_owner_account_id,i.server_id,i.local_generation,k.reset_generation,COALESCE(k.active_operation_id,''),COALESCE(k.installed_operation_id,''),COALESCE(k.pending_operation_id,''),k.public_key=i.public_key AND k.server_id=i.server_id,s.sign_in_kind,i.account_id,i.approved_username,i.owner_change_reserved,COALESCE(l.hosted_account_id,''),a.username,m.stage,m.owner_id,m.to_kind,m.revision,m.owner_confirmed,
 EXISTS(SELECT 1 FROM networking_claim_credentials t WHERE t.operation_id=i.operation_id AND t.server_id=i.server_id AND t.local_generation=i.local_generation AND t.claim_generation=i.claim_generation AND t.credential_generation=i.credential_generation),
 NOT EXISTS(SELECT 1 FROM networking_claim_authority t WHERE t.operation_id=i.operation_id AND (t.server_id<>i.server_id OR t.account_id<>i.account_id OR t.local_generation<>i.local_generation OR t.claim_generation<>i.claim_generation OR t.credential_generation<>i.credential_generation OR t.state<>'active'))
 FROM networking_claim_intents i JOIN networking_claim_identity k ON k.singleton=1 JOIN networking_server_identities keys ON keys.server_id=k.server_id AND keys.public_key=k.public_key JOIN identity_state s ON s.singleton=1 JOIN accounts a ON a.id=? LEFT JOIN account_portico_links l ON l.account_id=a.id JOIN identity_moves m ON m.id=? WHERE i.operation_id=?`, c.owner, c.move, c.operation).Scan(&stage, &purpose, &currentOwner, &server, &generation, &currentGeneration, &active, &installed, &pending, &matchingKey, &kind, &account, &username, &reserved, &linked, &actualUsername, &moveStage, &moveOwner, &moveKind, &moveRevision, &confirmed, &credential, &claimActive)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if purpose != "switch" || currentOwner != "" || server != s.ID() || generation != currentGeneration || !matchingKey || moveOwner != c.owner || moveKind != "portico" || !confirmed {
		return ErrUnauthorized
	}
	if c.phase == "prepared" {
		if kind != "local" || linked != "" || moveStage != "preview" || moveRevision != c.moveRevision {
			return ErrUnauthorized
		}
		if installing {
			if stage != "installed" || active != c.operation || installed != c.operation || pending != "" || !reserved || username == "" || account == "" || !credential || !claimActive {
				return ErrUnauthorized
			}
			return nil
		}
		if pending != c.operation || active != "" || installed != "" {
			return ErrUnauthorized
		}
		switch stage {
		case "prepared":
		case "approved", "finalizing", "retrieving":
			if !reserved || username == "" || account == "" {
				return ErrUnauthorized
			}
		default:
			return ErrUnauthorized
		}
		return nil
	}
	if kind != "portico" || stage != "installed" || active != c.operation || installed != c.operation || pending != "" || reserved || linked != account || username == "" || actualUsername != username || !credential || !claimActive || (moveStage != "moving_to_portico" && moveStage != "complete") || moveRevision != c.moveRevision+1 {
		return ErrUnauthorized
	}
	return nil
}
