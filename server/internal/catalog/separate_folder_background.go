package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func (s *Service) separateFolderBackground(ctx context.Context, id string) (operations.JobObservation, error) {
	out := operations.JobObservation{State: "running", Phase: "separating folder"}
	ctx = dbwork.WithPlan(ctx, dbwork.DefaultPlan(dbwork.ClassInteractive))
	var accepted compactcatalog.EntityMutation
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		ref, err := compactcatalog.MutationRefTx(ctx, tx, id)
		if err != nil {
			return err
		}
		m, err := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if err != nil {
			return err
		}
		accepted = m
		if m.Mode != "separate" || m.Parent != "" {
			return ErrSeparateFolderCursor
		}
		if m.Status == "terminal" {
			if m.Outcome == "separated" {
				out.State, out.Phase = "succeeded", "folder separated"
			} else {
				out.State, out.Phase, out.ErrorCode = "failed", "split did not finish", "authority_revoked"
				if m.Provider == "separate-abandon" {
					out.ErrorCode = m.ProviderKind
				}
			}
			return acknowledgeMutationTx(ctx, tx, ref)
		}

		if m.Provider == "separate-abandon" {
			return errSeparateFolderAbandon{code: m.ProviderKind}
		}
		var enabled, retired bool
		var health string
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(s.enabled,0),COALESCE(s.health,'removing'),l.retired FROM catalog_libraries l LEFT JOIN library_sources s ON s.id=? AND s.library_id=l.library_id WHERE l.id=?`, m.SplitSourceID, m.Library).Scan(&enabled, &health, &retired)
		if errors.Is(err, sql.ErrNoRows) || retired || health == "removing" {
			return errSeparateFolderAbandon{code: "source_removed"}
		}
		if err != nil {
			return err
		}
		typed, err := separateFolderActorAuthorizeTx(ctx, tx, m)
		if !typed && err == nil {
			out.State, out.Phase = "paused", "authorized owner resume required"
			return nil
		}
		if err != nil && !errors.Is(err, errSeparateFolderAccountMoving) {
			return err
		}
		if e := separateFolderSourceCurrentTx(ctx, tx, m); e != nil {
			return e
		}
		if e := separateFolderActualBindingsTx(ctx, tx, m); e != nil {
			return e
		}
		if err != nil {
			return err
		}
		if !enabled || health == "offline" || health == "root_changed" {
			out.Phase = "waiting for source"
			return nil
		}
		step, err := (SeparateFolderCoordinator{Hooks: SeparateFolderCoordinatorHooks{Retirement: s.SeparateFolderRetirement}}).Step(ctx, tx, ref)
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

	// WithWriteTx has rolled the failed quantum back before any release.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return out, err
	}
	if errors.Is(err, errSeparateFolderAccountMoving) {
		out.Phase = "waiting for account move"
		return out, nil
	}
	if errors.Is(err, ErrSeparateFolderRetirementRetry) {
		out.Phase = "waiting for prepared cancellation"
		return out, nil
	}
	if dbwork.Overloaded(err) {
		out.Phase = "waiting for writer"
		return out, nil
	}
	code := ""
	var abandon errSeparateFolderAbandon
	switch {
	case errors.As(err, &abandon):
		code = abandon.code
	case errors.Is(err, identity.ErrUnauthorized):
		code = "authority_revoked"
	case errors.Is(err, compactcatalog.ErrMutationStale), errors.Is(err, sql.ErrNoRows):
		code = "source_changed"
	case errors.Is(err, ErrSeparateFolderCursor):
		code = "invalid_cursor"
	}
	if code == "" {
		return out, err
	}
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		ref, e := compactcatalog.MutationRefTx(ctx, tx, id)
		if e != nil {
			return e
		}
		m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if e != nil {
			return e
		}
		if m.Mode != "separate" || m.Parent != "" || m.Ref != accepted.Ref || m.Family != accepted.Family || m.OwnerID != accepted.OwnerID {
			return ErrSeparateFolderCursor
		}
		if m.Status == "terminal" {
			return nil
		}
		if m.Provider == "separate-abandon" {
			code = m.ProviderKind
		}
		done, e := abandonSeparateFolderTurnTx(ctx, tx, m)
		if e != nil {
			return e
		}
		// Separate commands have no provider binding. The tagged descriptor
		// retains the reason across abandonment turns and terminal reads.
		_, e = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET provider='separate-abandon',provider_kind=? WHERE mutation_id=? AND generation=?`, code, ref.ID, ref.Generation)
		if done {
			out.State = "failed"
		}
		return e
	})
	out.ErrorCode = code
	out.Phase = strings.ReplaceAll(code, "_", " ") + "; releasing custody"
	if errors.Is(err, ErrSeparateFolderCursor) || errors.Is(err, compactcatalog.ErrMutationStale) {
		// Foreign/corrupt custody must stay held for inspection.
		out.State, out.Phase, out.ErrorCode = "failed", "invalid custody; release blocked", "invalid_cursor"
		return out, nil
	}
	return out, err
}

type errSeparateFolderAbandon struct{ code string }

func (e errSeparateFolderAbandon) Error() string { return e.code }

// This cancellation changes only typed command state and its cursor-only job.
// It never restores, deletes or transfers any user's catalogue/personal rows.
func abandonSeparateFolderTurnTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (bool, error) {
	if m.OwnerKind != "separate" || m.Mode != "separate" || m.Parent != "" || m.Family != m.Ref.ID {
		return false, ErrSeparateFolderCursor
	}
	if m.Child != "" {
		ref, err := compactcatalog.MutationRefTx(ctx, tx, m.Child)
		if err != nil {
			return false, err
		}
		child, err := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if err != nil {
			return false, err
		}
		if child.Mode != "separate" || child.Parent != m.Ref.ID || child.Family != m.Family || child.Child != "" || child.OwnerKind != "separate" || child.OwnerID != m.OwnerID+":"+m.Ref.ID+":"+strconv.FormatInt(child.Subject, 10)+":"+strconv.FormatInt(child.CurrentKey, 10) || child.OwnerGeneration != m.OwnerGeneration ||
			child.SplitSourceID != m.SplitSourceID || child.SplitDirectory != m.SplitDirectory ||
			child.SplitObjectID != m.SplitObjectID || child.SplitObjectRevision != m.SplitObjectRevision {
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
