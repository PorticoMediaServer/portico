package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func (s *Service) separateFolderBackground(ctx context.Context, id string) (operations.JobObservation, error) {
	out := operations.JobObservation{State: "running", Phase: "separating folder"}
	ctx = dbwork.WithPlan(ctx, dbwork.DefaultPlan(dbwork.ClassInteractive))
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		ref, err := compactcatalog.MutationRefTx(ctx, tx, id)
		if err != nil {
			return err
		}
		m, err := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if err != nil {
			return err
		}
		if m.Mode != "separate" || m.Parent != "" {
			return ErrSeparateFolderCursor
		}
		if m.Status == "terminal" {
			if m.Outcome == "separated" {
				out.State, out.Phase = "succeeded", "folder separated"
			} else {
				out.State, out.Phase, out.ErrorCode = "failed", "split did not finish", "authority_revoked"
			}
			return acknowledgeMutationTx(ctx, tx, ref)
		}
		typed, err := separateFolderActorAuthorizeTx(ctx, tx, m)
		if !typed && err == nil {
			out.State, out.Phase = "paused", "authorized owner resume required"
			return nil
		}
		if err != nil {
			if !errors.Is(err, identity.ErrUnauthorized) {
				return err
			}
			// A revoked command may only release its own private custody. No
			// more claims or catalogue files are changed, and no successful ID
			// is published. One current child is abandoned before the root.
			done, err := abandonSeparateFolderTurnTx(ctx, tx, m)
			if err != nil {
				return err
			}
			out.Phase, out.ErrorCode = "authority revoked; releasing custody", "authority_revoked"
			if done {
				out.State = "failed"
			}
			return nil
		}
		var enabled bool
		var health string
		if err := tx.QueryRowContext(ctx, `SELECT enabled,health FROM library_sources WHERE id=? AND library_id=(SELECT library_id FROM catalog_libraries WHERE id=?)`, m.SplitSourceID, m.Library).Scan(&enabled, &health); err != nil {
			return err
		}
		if health == "removing" {
			// Removal retires this source lifetime. Unlike an offline source,
			// it cannot resume this accepted command. Release only its private
			// custody, one bounded child/root turn at a time, without moving files.
			done, err := abandonSeparateFolderTurnTx(ctx, tx, m)
			if err != nil {
				return err
			}
			out.Phase, out.ErrorCode = "source removed; releasing custody", "authority_revoked"
			if done {
				out.State = "failed"
			}
			return nil
		}
		if !enabled || health == "offline" || health == "root_changed" {
			out.Phase = "waiting for source"
			return nil
		}
		step, err := (SeparateFolderCoordinator{Hooks: SeparateFolderCoordinatorHooks{Retirement: s.SeparateFolderRetirement}}).Step(ctx, tx, ref)
		if errors.Is(err, ErrSeparateFolderRetirementRetry) {
			out.Phase = "waiting for prepared cancellation"
			return nil
		}
		if err != nil {
			return err
		}
		if step.Complete {
			if step.Outcome != "separated" {
				return compactcatalog.ErrMutationStale
			}
			out.State, out.Phase = "succeeded", "folder separated"
			return acknowledgeMutationTx(ctx, tx, ref)
		}
		return nil
	})
	return out, err
}

// This cancellation changes only typed command state and its cursor-only job.
// It never restores, deletes or transfers any user's catalogue/personal rows.
func abandonSeparateFolderTurnTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (bool, error) {
	if m.Child != "" {
		ref, err := compactcatalog.MutationRefTx(ctx, tx, m.Child)
		if err != nil {
			return false, err
		}
		child, err := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if err != nil {
			return false, err
		}
		if child.Mode != "separate" || child.Parent != m.Ref.ID || child.Family != m.Family || child.Child != "" {
			return false, ErrSeparateFolderCursor
		}
		var otherPrivate bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_purge_identities WHERE mutation_id=?1) OR EXISTS(SELECT 1 FROM retained_title_state INDEXED BY retained_title_state_capture WHERE capture_mutation_id=?1)`, child.Ref.ID).Scan(&otherPrivate); err != nil {
			return false, err
		}
		if otherPrivate {
			return false, ErrSeparateFolderCursor
		}
		if err := separateFolderDropRetirementJobTx(ctx, tx, ref); err != nil {
			return false, err
		}
		if child.Status != "terminal" {
			if _, err := compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "blocked"); err != nil {
				return false, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET current_child=NULL WHERE mutation_id=? AND generation=? AND current_child=?`, m.Ref.ID, m.Ref.Generation, child.Ref.ID); err != nil {
			return false, err
		}
		return false, acknowledgeMutationTx(ctx, tx, ref)
	}
	if _, err := compactcatalog.FinishEntityMutationTx(ctx, tx, m.Ref, "blocked"); err != nil {
		return false, err
	}
	return true, acknowledgeMutationTx(ctx, tx, m.Ref)
}
