package identity

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// ProfileRetirement is logical denial followed by bounded physical cleanup.
// Removed never means that library media or the owner's retained watch history
// was deleted. Those are not profile-private purge resources.
type ProfileRetirement struct {
	ProfileID string `json:"profileId"`
	Lifetime  string `json:"lifetime"`
	State     string `json:"state"`
}
type ProfileRetirementRequest struct {
	ExpectedRevision     int64  `json:"expectedRevision"`
	Lifetime             string `json:"lifetime"`
	DeleteSavedResources bool   `json:"deleteSavedResources"`
}

func validProfileLifetime(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == v
}

// RetireDirectProfile never enumerates sessions or private children in the
// accepting writer. The live profile fence denies all old authority immediately;
// each physical child and family is retired in the compiled purge worker.
func (s *Service) RetireDirectProfile(ctx context.Context, bearer, id string, q ProfileRetirementRequest) (ProfileRetirement, error) {
	out := ProfileRetirement{ProfileID: id, Lifetime: q.Lifetime, State: "removing"}
	if !validFamilyID(id) || q.ExpectedRevision < 1 || !q.DeleteSavedResources || !validProfileLifetime(q.Lifetime) {
		return out, ErrDirectInput
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	c, err := s.profileManagerTx(ctx, tx, bearer)
	if err != nil {
		return out, err
	}
	var lifetime string
	var revision int64
	var primary, deleted bool
	err = tx.QueryRowContext(ctx, `SELECT lifetime,revision,is_primary,deleted FROM direct_profiles WHERE account_id=? AND id=?`, c.account.ID, id).Scan(&lifetime, &revision, &primary, &deleted)
	if err != nil {
		return out, err
	}
	if primary {
		return out, ErrPrimaryProfile
	}
	if lifetime != q.Lifetime {
		return out, ErrProfileChanged
	}
	if deleted {
		return s.profileRetirementTx(ctx, tx, c.account.ID, id, q.Lifetime)
	}
	if revision != q.ExpectedRevision {
		return out, ErrProfileChanged
	}
	var accountLifetime string
	if err = tx.QueryRowContext(ctx, `SELECT incarnation FROM direct_memberships WHERE account_id=?`, c.account.ID).Scan(&accountLifetime); err != nil {
		return out, err
	}
	// Keep the final display name only for the owner's removed-profile history.
	// It is never returned by ordinary profile lists or personal-state readers.
	if _, err = tx.ExecContext(ctx, `UPDATE direct_profiles SET deleted=1,pin_hash=NULL,art='slate',allowed_libraries=NULL,position=0,revision=revision+1,pin_revision=pin_revision+1 WHERE account_id=? AND id=? AND lifetime=? AND deleted=0`, c.account.ID, id, q.Lifetime); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO security_purge_jobs(job_id,authority,account_id,profile_id,account_incarnation,profile_incarnation,state) VALUES(?,'local',?,?,?,?, 'pending')`, Token()[:22], c.account.ID, id, accountLifetime, q.Lifetime); err != nil {
		return out, err
	}
	if err = gated.Commit(); err != nil {
		return out, err
	}
	s.profilePurgeWake.Wake()
	return out, nil
}
func (s *Service) profileRetirementTx(ctx context.Context, tx *sql.Tx, account, id, lifetime string) (ProfileRetirement, error) {
	out := ProfileRetirement{ProfileID: id, Lifetime: lifetime, State: "removed"}
	var state string
	err := tx.QueryRowContext(ctx, `SELECT state FROM security_purge_jobs WHERE authority='local' AND account_id=? AND profile_id=? AND profile_incarnation=?`, account, id, lifetime).Scan(&state)
	if err == nil {
		out.State = "removing"
		if state == "blocked" {
			out.State = "blocked"
		}
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	var actual string
	var deleted bool
	err = tx.QueryRowContext(ctx, `SELECT lifetime,deleted FROM direct_profiles WHERE account_id=? AND id=?`, account, id).Scan(&actual, &deleted)
	if err != nil {
		return out, err
	}
	if actual != lifetime || !deleted {
		return out, ErrProfileChanged
	}
	return out, nil
}
func (s *Service) DirectProfileRetirement(ctx context.Context, bearer, id, lifetime string) (ProfileRetirement, error) {
	if !validFamilyID(id) || !validProfileLifetime(lifetime) {
		return ProfileRetirement{}, ErrDirectInput
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return ProfileRetirement{}, err
	}
	defer gated.Rollback()
	c, err := s.profileManagerTx(ctx, gated.Tx(), bearer)
	if err != nil {
		return ProfileRetirement{}, err
	}
	return s.profileRetirementTx(ctx, gated.Tx(), c.account.ID, id, lifetime)
}

// RunProfilePurge resumes durable work at startup and is quiet when idle.
func (s *Service) RunProfilePurge(ctx context.Context) {
	worker.Run(ctx, "identity.profile-purge", s.profilePurgeWake, func(ctx context.Context) time.Duration {
		worked, err := s.PurgeProfileStep(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("profile cleanup waiting: %v", err)
			}
			return time.Second
		}
		if worked {
			return 10 * time.Millisecond
		}
		return 0
	})
}

func (ProfileRetirement) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"profileId", "lifetime", "state"}, "properties": map[string]any{
		"profileId": map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"lifetime":  map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"},
		"state":     map[string]any{"type": "string", "enum": []string{"removing", "removed", "blocked"}},
	}}
}
func (ProfileRetirementRequest) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"expectedRevision", "lifetime", "deleteSavedResources"}, "properties": map[string]any{
		"expectedRevision":     map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991},
		"lifetime":             map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"},
		"deleteSavedResources": map[string]any{"type": "boolean", "enum": []bool{true}},
	}}
}
