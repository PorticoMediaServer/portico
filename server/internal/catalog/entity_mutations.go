package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// One actual pin mutation retains its accepted boundary while prospective
// recording counts are prepared and folded through separate bounded writers.
// It never shares a turn with another entity or physical deletion.
// Private preparation changes <=11 count facts plus its state/lease points;
// accepted folding changes <=11 base rows +11 private rows +1 state row. The
// pin flip has only point trigger effects; it never updates the release tracks.
// MB WAL enforcement is intentionally off; actual flip/fold WAL writes are
// observed telemetry, with checkpointing and reserved WAL recovery as protection.
var entityPinDrainPlan = dbwork.WritePlan{Name: "catalog-one-mb-pin", Rows: 24, Pages: 2048, VMOps: 4_000_000, Why: "one staged raw11 accounting quantum, one exact pin flip, or one <=11-fact fold; custody/source-read cost remains separately fenced and measured"}

func MutationWriteContext(ctx context.Context) context.Context {
	return dbwork.WithPlan(ctx, titleRemovalPlan)
}

func entityMutationHeldTx(ctx context.Context, tx *sql.Tx, entity int64) (held bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entity_mutations WHERE status<>'terminal' AND (entity_id=?1 OR target_id=?1))`, entity).Scan(&held)
	return
}
func entityMutationHeld(ctx context.Context, tx *sql.Tx, entity int64) bool {
	h, e := entityMutationHeldTx(ctx, tx, entity)
	return e != nil || h
}
func requireCompleteMergeTx(ctx context.Context, tx *sql.Tx, from, into int64) error {
	if from == into {
		return nil
	}
	if err := compactcatalog.RequireEntityAcquirableTx(ctx, tx, from); err != nil {
		return err
	}
	if err := compactcatalog.RequireEntityAcquirableTx(ctx, tx, into); err != nil {
		return err
	}
	// Compatibility is complete-only: reject a pin anywhere in the actual
	// outgoing music tree before moving children or additive personal state.
	var pinned bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mb_song_links WHERE item_id=?1) OR EXISTS(SELECT 1 FROM mb_album_links WHERE album_id=?1)
 OR EXISTS(SELECT 1 FROM catalog_songs s JOIN mb_song_links l ON l.item_id=s.entity_id WHERE s.album_id=?1)
 OR EXISTS(SELECT 1 FROM catalog_songs s JOIN mb_album_links l ON l.album_id=s.entity_id WHERE s.album_id=?1)
 OR EXISTS(SELECT 1 FROM catalog_albums a JOIN mb_song_links l ON l.item_id=a.entity_id WHERE a.artist_id=?1)
 OR EXISTS(SELECT 1 FROM catalog_albums a JOIN catalog_songs s ON s.album_id=a.entity_id JOIN mb_album_links l ON l.album_id=s.entity_id WHERE a.artist_id=?1)
 OR EXISTS(SELECT 1 FROM catalog_episodes ep WHERE (ep.show_id=?1 OR ep.season_id=?1) AND (EXISTS(SELECT 1 FROM mb_song_links WHERE item_id=ep.entity_id) OR EXISTS(SELECT 1 FROM mb_album_links WHERE album_id=ep.entity_id)))
 OR EXISTS(SELECT 1 FROM catalog_albums a JOIN mb_album_links l ON l.album_id=a.entity_id WHERE a.artist_id=?1)
 OR EXISTS(SELECT 1 FROM catalog_albums a JOIN catalog_songs s ON s.album_id=a.entity_id JOIN mb_song_links l ON l.item_id=s.entity_id WHERE a.artist_id=?1)`, from).Scan(&pinned)
	if err != nil {
		return err
	}
	if pinned {
		return compactcatalog.ErrEntityPinned
	}
	return nil
}

func mutationTerminal(m compactcatalog.EntityMutation) compactcatalog.MutationStep {
	return compactcatalog.MutationStep{Complete: m.Status == "terminal", More: m.Status != "terminal", Outcome: m.Outcome}
}
func checkedMutationTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) (compactcatalog.EntityMutation, compactcatalog.MutationStep, error) {
	m, err := compactcatalog.CheckEntityMutationTx(ctx, tx, ref)
	if errors.Is(err, compactcatalog.ErrMutationStale) || errors.Is(err, sql.ErrNoRows) {
		step, e := staleMutationStepTx(ctx, tx, m)
		return m, step, e
	}
	if err != nil {
		return m, compactcatalog.MutationStep{}, err
	}
	return m, mutationTerminal(m), nil
}
func beginRemovalTx(ctx context.Context, tx *sql.Tx, entity int64, owner, id string, generation int64, mode string, parent *compactcatalog.EntityMutation) (compactcatalog.MutationStep, error) {
	in := compactcatalog.MutationIntent{Entity: entity, OwnerKind: owner, OwnerID: id, OwnerGeneration: generation, Mode: mode}
	if parent != nil {
		in.Parent = parent.Ref.ID
		in.Family = parent.Family
	}
	_, err := compactcatalog.BeginEntityMutationTx(ctx, tx, in)
	return compactcatalog.PendingMutationStep(), err
}
func removeOwnedStepTx(ctx context.Context, tx *sql.Tx, entity int64, owner, id string, generation int64, mode string) (compactcatalog.MutationStep, error) {
	ref, err := compactcatalog.FindEntityMutationTx(ctx, tx, owner, id, generation, entity, mode)
	if errors.Is(err, sql.ErrNoRows) {
		return beginRemovalTx(ctx, tx, entity, owner, id, generation, mode, nil)
	}
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if mode == "force" {
		m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if e != nil {
			return compactcatalog.MutationStep{}, e
		}
		if m.Status == "terminal" && m.Outcome == "retained" {
			_, e = compactcatalog.RestartRetainedMutationTx(ctx, tx, ref)
			return compactcatalog.PendingMutationStep(), e
		}

	}
	return RemoveTitleStepTx(ctx, tx, ref)
}
func mutationChildTx(ctx context.Context, tx *sql.Tx, parent compactcatalog.EntityMutation, entity, target int64, mode string) error {
	ref, err := compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: entity, Target: target, OwnerKind: parent.OwnerKind, OwnerID: parent.OwnerID + ":" + parent.Ref.ID, OwnerGeneration: parent.OwnerGeneration, Mode: mode, Parent: parent.Ref.ID, Family: parent.Family})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET current_child=?,current_key=? WHERE mutation_id=? AND generation=? AND current_child IS NULL`, ref.ID, entity, parent.Ref.ID, parent.Ref.Generation)
	return err
}
func advanceMutationChildTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (compactcatalog.MutationStep, error) {
	ref, err := compactcatalog.MutationRefTx(ctx, tx, m.Child)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	step, err := CatalogMutationStepTx(ctx, tx, ref)
	if err != nil || step.PinDrained || !step.Complete {
		return step, err
	}
	if step.Outcome == "stale" {
		return staleMutationStepTx(ctx, tx, m)
	}
	if step.Outcome == "blocked" {
		return compactcatalog.MutationStep{More: true, Outcome: "blocked"}, nil
	}
	// Only a terminal physical outcome advances the parent's completed key.
	cursor := m.CurrentKey
	if m.Mode == "merge" && m.Kind == compactcatalog.Artist {
		cursor = 0
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET cursor=?,current_child=NULL,current_key=0 WHERE mutation_id=? AND generation=? AND current_child=?`, cursor, m.Ref.ID, m.Ref.Generation, m.Child)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET acknowledged=1 WHERE mutation_id=?`, m.Child)
	}
	if err == nil {
		err = EnqueueMutationCleanupTx(ctx, tx)
	}
	return compactcatalog.PendingMutationStep(), err
}
func CatalogMutationStepTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) (compactcatalog.MutationStep, error) {
	m, err := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if m.OwnerKind == "empty-parent" {
		return emptyParentStepTx(ctx, tx, ref)
	}
	switch m.Mode {
	case "merge":
		return MergeTitlesStepTx(ctx, tx, ref)
	case "adopt":
		return AdoptProviderIdentityStepTx(ctx, tx, ref)
	default:
		return RemoveTitleStepTx(ctx, tx, ref)
	}
}
func RemoveTitleStepTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) (compactcatalog.MutationStep, error) {
	m, step, err := checkedMutationTx(ctx, tx, ref)
	if err == nil && m.OwnerKind == "empty-parent" {
		return emptyParentStepTx(ctx, tx, ref)
	}
	if err != nil || m.Status != "active" || step.Complete || step.Outcome == "blocked" {
		return step, err
	}
	ctx = compactcatalog.WithMutationFamily(ctx, m.Family)
	ctx = context.WithValue(ctx, parentCleanupKey{}, parentCleanupCustody{family: m.Family, library: m.OwnerKind == "library"})
	if m.Child != "" {
		return advanceMutationChildTx(ctx, tx, m)
	}
	if m.Phase == "admitted" {
		phase := "drain"
		if m.Mode == "members" {
			phase = "children"
		}
		return compactcatalog.PendingMutationStep(), compactcatalog.SetMutationPhaseTx(ctx, tx, ref, phase)
	}
	if m.Mode == "members" && m.Phase == "children" {
		if m.OwnerKind == "administration" {
			// Administration's limit=0 pass may inspect empty containers but
			// never adopts a newly attached playable member into old approval.
			if m.Kind == compactcatalog.Artist {
				album, _, e := nextMutationMemberTx(ctx, tx, m.Entity, m.Kind, m.Cursor)
				if e == nil {
					var has bool
					if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_songs INDEXED BY catalog_songs_album_entity WHERE album_id=?)`, album).Scan(&has); e != nil {
						return compactcatalog.MutationStep{}, e
					}
					if has {
						return compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "stale")
					}
					_, e = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET cursor=? WHERE mutation_id=? AND generation=?`, album, ref.ID, ref.Generation)
					return compactcatalog.PendingMutationStep(), e
				}
				if !errors.Is(e, sql.ErrNoRows) {
					return compactcatalog.MutationStep{}, e
				}
			} else {
				has, e := hasTitleMembersTx(ctx, tx, m.Entity, m.Kind)
				if e != nil {
					return compactcatalog.MutationStep{}, e
				}
				if has {
					return compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "stale")
				}
			}
			return compactcatalog.PendingMutationStep(), compactcatalog.SetMutationPhaseTx(ctx, tx, ref, "finalize")
		}

		child, mode, e := nextMutationMemberTx(ctx, tx, m.Entity, m.Kind, m.Cursor)
		err = e
		if err == nil {
			return compactcatalog.PendingMutationStep(), mutationChildTx(ctx, tx, m, child, 0, mode)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return compactcatalog.MutationStep{}, err
		}
		return compactcatalog.PendingMutationStep(), compactcatalog.SetMutationPhaseTx(ctx, tx, ref, "finalize")
	}
	if m.Phase == "drain" {
		if err = dbwork.UsePlan(tx, entityPinDrainPlan); err != nil {
			return compactcatalog.MutationStep{}, err
		}
		return compactcatalog.DrainEntityMBPinTx(ctx, tx, ref)
	}
	if m.Phase != "finalize" {
		return compactcatalog.MutationStep{}, fmt.Errorf("unexpected removal phase %s", m.Phase)
	}
	if m.Mode != "members" {
		if err = compactcatalog.RequireNoEntityMBPinsTx(ctx, tx, m.Entity); err != nil {
			return compactcatalog.MutationStep{}, err
		}
	}
	if m.Mode == "members" && m.Kind == compactcatalog.Show {
		var season int64
		e := tx.QueryRowContext(ctx, `SELECT entity_id FROM catalog_seasons INDEXED BY catalog_seasons_show_entity WHERE show_id=? ORDER BY entity_id LIMIT 1`, m.Entity).Scan(&season)
		if e == nil {
			return compactcatalog.PendingMutationStep(), removeContainerTx(ctx, tx, season)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return compactcatalog.MutationStep{}, e
		}
	}
	// Release the restrictive live FK only with the terminal action. Rollback
	// restores custody if any retained-state or physical write fails.
	step, err = compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "removed")
	if err != nil {
		return step, err
	}
	switch m.Mode {
	case "force":
		_, err = removeLibraryContainerTx(ctx, tx, m.Entity)
	case "members":
		err = settleRemovedMembersTx(ctx, tx, m.Entity, m.Kind)
	default:
		err = RemoveTitleTx(ctx, tx, m.Entity)
	}
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE id=? AND public_id=pid_blob(?))`, m.Entity, m.Public).Scan(&exists)
	if err == nil && exists {
		step.Outcome = "retained"
		_, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET outcome='retained' WHERE mutation_id=?`, ref.ID)
	}
	return step, err
}
func settleRemovedMembersTx(ctx context.Context, tx *sql.Tx, entity int64, kind compactcatalog.Kind) error {
	switch kind {
	case compactcatalog.Show:
		return removeContainerTx(ctx, tx, entity)
	case compactcatalog.Season:
		return removeEmptySeasonAndSettleShowTx(ctx, tx, entity)
	case compactcatalog.Collection:
		return compactcatalog.DeleteEntityTx(ctx, tx, entity)
	case compactcatalog.Album:
		return retireEmptiedAlbumTx(ctx, tx, entity, 0)
	case compactcatalog.Book:
		return settleEmptiedBook(ctx, tx, entity, 0)
	case compactcatalog.Artist:
		return settleAbandonedArtistTx(ctx, tx, entity)
	}
	return nil
}

// DeleteApprovedTitleStepTx binds one actually approved member to the owning
// administration request. A pin quantum is pending, never a removed receipt.
func DeleteApprovedTitleStepTx(ctx context.Context, tx *sql.Tx, plan TitleDeletePlan, owner string) (compactcatalog.MutationStep, error) {
	var ref compactcatalog.MutationRef
	err := tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE owner_kind='administration' AND owner_id=? AND public_id=? AND mode='remove' ORDER BY owner_generation DESC LIMIT 1`, owner, plan.ItemID).Scan(&ref.ID, &ref.Generation)
	if err == nil {
		m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if e != nil {
			return compactcatalog.MutationStep{}, e
		}
		if m.Status == "terminal" {
			return mutationTerminal(m), nil
		}
		current, e := CaptureTitleDeleteTx(ctx, tx, m.Entity)
		if e != nil {
			return compactcatalog.MutationStep{}, e
		}
		if current != plan {
			return compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "stale")
		}
		return RemoveTitleStepTx(ctx, tx, ref)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return compactcatalog.MutationStep{}, err
	}
	var entity int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, plan.ItemID).Scan(&entity)
	if errors.Is(err, sql.ErrNoRows) {
		return compactcatalog.MutationStep{Complete: true, Outcome: "stale"}, nil
	}
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	current, err := CaptureTitleDeleteTx(ctx, tx, entity)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if current != plan {
		return compactcatalog.MutationStep{Complete: true, Outcome: "stale"}, nil
	}
	superseded, e := supersedeAdministrationTx(ctx, tx, entity, owner)
	if e != nil {
		return compactcatalog.MutationStep{}, e
	}
	if superseded {
		return compactcatalog.PendingMutationStep(), nil
	}
	operation, _, ok := strings.Cut(owner, ":")
	if !ok {
		operation = owner
	}
	intent := compactcatalog.MutationIntent{Entity: entity, OwnerKind: "administration", OwnerID: owner, OwnerGeneration: 1, Mode: "remove", CauseOperation: operation}
	var anchor, family string
	e = tx.QueryRowContext(ctx, `SELECT mutation_id,family_id FROM catalog_entity_mutations WHERE owner_kind='administration' AND owner_id=? AND owner_generation=1 AND parent_mutation_id IS NULL LIMIT 1`, owner).Scan(&anchor, &family)
	if e == nil {
		intent.Parent = anchor
		intent.Family = family
	} else if !errors.Is(e, sql.ErrNoRows) {
		return compactcatalog.MutationStep{}, e
	}
	_, err = compactcatalog.BeginEntityMutationTx(ctx, tx, intent)
	return compactcatalog.PendingMutationStep(), err
}

func staleMutationStepTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (compactcatalog.MutationStep, error) {
	if m.Child != "" {
		ref, err := compactcatalog.MutationRefTx(ctx, tx, m.Child)
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		child, err := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		if child.Status != "terminal" {
			_, err = staleMutationStepTx(ctx, tx, child)
			return compactcatalog.PendingMutationStep(), err
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET current_child=NULL,current_key=0 WHERE mutation_id=? AND generation=?`, m.Ref.ID, m.Ref.Generation)
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET acknowledged=1 WHERE mutation_id=? AND status='terminal'`, m.Child); err != nil {
			return compactcatalog.MutationStep{}, err
		}
		if err = EnqueueMutationCleanupTx(ctx, tx); err != nil {
			return compactcatalog.MutationStep{}, err
		}
		return compactcatalog.FinishEntityMutationTx(ctx, tx, m.Ref, "stale")
	}
	return compactcatalog.FinishEntityMutationTx(ctx, tx, m.Ref, "stale")
}

// DeleteEmptyApprovedContainerStepTx is administration's limit=0 policy with
// explicit pending outcomes. Artist album probes and empty show seasons each
// retain an actual indexed command cursor; newly attached members are refused.
func DeleteEmptyApprovedContainerStepTx(ctx context.Context, tx *sql.Tx, plan TitleDeletePlan, owner string) (compactcatalog.MutationStep, error) {
	var ref compactcatalog.MutationRef
	e := tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE owner_kind='administration' AND owner_id=? AND public_id=? AND mode='members' ORDER BY owner_generation DESC LIMIT 1`, owner, plan.ItemID).Scan(&ref.ID, &ref.Generation)
	if e == nil {
		return RemoveTitleStepTx(ctx, tx, ref)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return compactcatalog.MutationStep{}, e
	}
	var entity int64
	e = tx.QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, plan.ItemID).Scan(&entity)
	if errors.Is(e, sql.ErrNoRows) {
		return compactcatalog.MutationStep{Complete: true, Outcome: "removed"}, nil
	}
	if e != nil {
		return compactcatalog.MutationStep{}, e
	}
	current, e := CaptureTitleDeleteTx(ctx, tx, entity)
	if e != nil {
		return compactcatalog.MutationStep{}, e
	}
	if current != plan {
		return compactcatalog.MutationStep{Complete: true, Outcome: "stale"}, nil
	}
	superseded, e := supersedeAdministrationTx(ctx, tx, entity, owner)
	if e != nil {
		return compactcatalog.MutationStep{}, e
	}
	if superseded {
		return compactcatalog.PendingMutationStep(), nil
	}
	var kind compactcatalog.Kind
	if e = tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, entity).Scan(&kind); e != nil {
		return compactcatalog.MutationStep{}, e
	}
	if kind == compactcatalog.Show {
		has, e := hasTitleMembersTx(ctx, tx, entity, kind)
		if e != nil {
			return compactcatalog.MutationStep{}, e
		}
		if has {
			return compactcatalog.MutationStep{Complete: true, Outcome: "stale"}, nil
		}
		superseded, e := supersedeEmptyAdministrationSeasonTx(ctx, tx, entity, owner)
		if e != nil {
			return compactcatalog.MutationStep{}, e
		}
		if superseded {
			return compactcatalog.PendingMutationStep(), nil
		}
		if step, found, e := driveApprovedEmptyParentTx(ctx, tx, entity); found || e != nil {
			return step, e
		}
	}
	return compactcatalog.PendingMutationStep(), beginAdministrationContainerTx(ctx, tx, entity, plan, owner)
}

func beginAdministrationContainerTx(ctx context.Context, tx *sql.Tx, entity int64, plan TitleDeletePlan, owner string) error {
	operation, _, ok := strings.Cut(owner, ":")
	if !ok {
		operation = owner
	}
	in := compactcatalog.MutationIntent{Entity: entity, OwnerKind: "administration", OwnerID: owner, OwnerGeneration: 1, Mode: "members", CauseOperation: operation}
	var anchor, family string
	e := tx.QueryRowContext(ctx, `SELECT mutation_id,family_id FROM catalog_entity_mutations WHERE owner_kind='administration' AND owner_id=? AND owner_generation=1 AND parent_mutation_id IS NULL LIMIT 1`, owner).Scan(&anchor, &family)
	if e == nil {
		in.Parent = anchor
		in.Family = family
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	_, e = compactcatalog.BeginEntityMutationTx(ctx, tx, in)
	return e
}
