package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

type parentCleanupKey struct{}
type parentCleanupCustody struct {
	family  string
	library bool
}

type administrationDeleteTargetKey struct{}
type administrationDeleteTarget struct {
	plan  TitleDeletePlan
	owner string
}

// WithAdministrationDeleteTarget binds only this live approved target's
// continuation. It admits nothing before file outcomes are known and stores
// no callback or selected members. The service reauthorizes each writer.
func WithAdministrationDeleteTarget(ctx context.Context, plan TitleDeletePlan, owner string) context.Context {
	return context.WithValue(ctx, administrationDeleteTargetKey{}, administrationDeleteTarget{plan, owner})
}

// An empty Show is a consequence of an actual Episode/Season removal. It
// owns no member selection or provider authority. The existing library
// removal job already owns a bounded container phase and needs no extra root.
func enqueueEmptyShowTx(ctx context.Context, tx *sql.Tx, show int64) error {
	custody, _ := ctx.Value(parentCleanupKey{}).(parentCleanupCustody)
	if custody.library || show == 0 {
		return nil
	}
	var public string
	e := tx.QueryRowContext(ctx, `SELECT pid(e.public_id) FROM catalog_entities e WHERE e.id=? AND e.kind=2 AND NOT EXISTS(SELECT 1 FROM catalog_episodes INDEXED BY catalog_episodes_show_entity WHERE show_id=e.id)`, show).Scan(&public)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if approved, ok := ctx.Value(administrationDeleteTargetKey{}).(administrationDeleteTarget); ok && approved.plan.ItemID == public {
		current, e := CaptureTitleDeleteTx(ctx, tx, show)
		if e != nil {
			return e
		}
		if current != approved.plan {
			return nil
		}
		// This writer already physically removed its Episode. Only admission is
		// permitted here: an older overlap is handled in the separate approved
		// zero-member turn, without combining supersession/extra physical work.
		e = beginAdministrationContainerTx(ctx, tx, show, approved.plan, approved.owner)
		if errors.Is(e, compactcatalog.ErrEntityMutating) {
			return nil
		}
		return e
	}
	if held, e := entityMutationHeldTx(ctx, tx, show); e != nil || held {
		return e
	}
	var generation int64
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(max(owner_generation),0)+1 FROM catalog_entity_mutations WHERE owner_kind='empty-parent' AND owner_id=?`, public).Scan(&generation); e != nil {
		return e
	}
	family := ""
	if custody.family != "" {
		var ownedSeason bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entity_mutations WHERE subject_ancestor_1=? AND family_id=? AND entity_kind=3 AND status='active')`, show, custody.family).Scan(&ownedSeason); e != nil {
			return e
		}
		if ownedSeason {
			family = custody.family
		}
	}
	ref, e := compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: show, OwnerKind: "empty-parent", OwnerID: public, OwnerGeneration: generation, Family: family, Mode: "members", CauseOperation: "empty-show"})
	if errors.Is(e, compactcatalog.ErrEntityMutating) {
		// Another Season command still owns this Show lifetime. Its own terminal
		// Season removal repeats this exact empty-parent admission after releasing
		// that custody; an Episode's physical work need not roll back to wait.
		return nil
	}
	if e != nil {
		return e
	}
	return enqueueCatalogMutationTx(ctx, tx, ref)
}

func driveApprovedEmptyParentTx(ctx context.Context, tx *sql.Tx, show int64) (compactcatalog.MutationStep, bool, error) {
	var ref compactcatalog.MutationRef
	e := tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations INDEXED BY catalog_entity_mutations_entity_fk WHERE entity_id=? AND owner_kind='empty-parent' AND status='active'`, show).Scan(&ref.ID, &ref.Generation)
	if errors.Is(e, sql.ErrNoRows) {
		return compactcatalog.MutationStep{}, false, nil
	}
	if e != nil {
		return compactcatalog.MutationStep{}, true, e
	}
	custody, e := readMutationConsoleTx(ctx, tx, ref.ID)
	if errors.Is(e, sql.ErrNoRows) {
		return compactcatalog.MutationStep{}, true, compactcatalog.ErrEntityMutating
	}
	if e != nil {
		return compactcatalog.MutationStep{}, true, e
	}
	step, e := emptyParentStepTx(ctx, tx, ref)
	if e != nil || !step.Complete {
		return step, true, e
	}
	if e = completeMutationConsoleTx(ctx, tx, custody, ref.ID, step.Outcome); e != nil {
		return compactcatalog.MutationStep{}, true, e
	}
	return step, true, acknowledgeMutationTx(ctx, tx, ref)
}

func removeEmptySeasonAndSettleShowTx(ctx context.Context, tx *sql.Tx, season int64) error {
	var show int64
	e := tx.QueryRowContext(ctx, `SELECT show_id FROM catalog_seasons WHERE entity_id=?`, season).Scan(&show)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = removeContainerTx(ctx, tx, season); e != nil {
		return e
	}
	return enqueueEmptyShowTx(ctx, tx, show)
}

// Exact FK-prefix probes, never an active-fact scan. Own source hold is the
// only exception; even another same-family Season lifetime must be consumed
// by its owner before this automatic empty Show can physically disappear.
func hasOtherShowLifetimeTx(ctx context.Context, tx *sql.Tx, show int64, own string) (bool, error) {
	for _, column := range []string{"entity_id", "target_id", "subject_ancestor_1", "subject_ancestor_2", "subject_ancestor_3", "subject_ancestor_4", "target_ancestor_1", "target_ancestor_2", "target_ancestor_3", "target_ancestor_4"} {
		var held bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entity_mutations WHERE `+column+`=? AND mutation_id<>?)`, show, own).Scan(&held); e != nil {
			return false, e
		}
		if held {
			return true, nil
		}
	}
	return false, nil
}

func emptyParentStepTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) (compactcatalog.MutationStep, error) {
	m, step, e := checkedMutationTx(ctx, tx, ref)
	if e != nil || m.Status != "active" || step.Complete {
		return step, e
	}
	if m.OwnerKind != "empty-parent" || m.Kind != compactcatalog.Show || m.Mode != "members" || m.CauseOperation != "empty-show" || m.Parent != "" || m.Child != "" || m.Target != 0 {
		return compactcatalog.MutationStep{}, compactcatalog.ErrMutationStale
	}
	ctx = compactcatalog.WithMutationFamily(ctx, m.Family)
	var episodes bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_episodes INDEXED BY catalog_episodes_show_entity WHERE show_id=?)`, m.Entity).Scan(&episodes); e != nil {
		return compactcatalog.MutationStep{}, e
	}
	if episodes {
		return compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "retained")
	}
	if m.Phase == "admitted" {
		return compactcatalog.PendingMutationStep(), compactcatalog.SetMutationPhaseTx(ctx, tx, ref, "finalize")
	}
	var season int64
	e = tx.QueryRowContext(ctx, `SELECT entity_id FROM catalog_seasons INDEXED BY catalog_seasons_show_entity WHERE show_id=? ORDER BY entity_id LIMIT 1`, m.Entity).Scan(&season)
	if e == nil {
		if held, e := entityMutationHeldTx(ctx, tx, season); e != nil || held {
			return compactcatalog.MutationStep{More: true, Outcome: "pending"}, e
		}
		var members bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_episodes INDEXED BY catalog_episodes_season_entity WHERE season_id=?)`, season).Scan(&members); e != nil {
			return compactcatalog.MutationStep{}, e
		}
		if members {
			return compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "retained")
		}
		return compactcatalog.PendingMutationStep(), removeContainerTx(ctx, tx, season)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return compactcatalog.MutationStep{}, e
	}
	if held, e := hasOtherShowLifetimeTx(ctx, tx, m.Entity, ref.ID); e != nil || held {
		return compactcatalog.MutationStep{More: true, Outcome: "pending"}, e
	}
	step, e = compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "removed")
	if e != nil {
		return step, e
	}
	return step, removeContainerTx(ctx, tx, m.Entity)
}
