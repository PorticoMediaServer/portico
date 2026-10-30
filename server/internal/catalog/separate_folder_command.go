package catalog

// P2-59 / O-022 SeparateFolder actual command.
//
// The owner asks for one folder of an album or book to become a title of its
// own. The old implementation moved every file in a single writer transaction;
// a real three-file folder is 409 changed rows against an interactive plan of
// 160. This file narrows that work into bounded quanta of the one existing
// catalog_entity_mutations command:
//
//	admitted -> create the separated container (one writer)
//	children -> either retire one original child's claims or move one file
//	finalize -> touch both containers and publish "separated"
//
// A quantum is exactly one gated writer transaction. It either retires claims
// for the original child (through the approved phasedpurge units, at most one
// changed unit or 24 effects) or publishes one file (SeparateFolderFileTx).
// Claims are never re-pointed: the retirement ends them truthfully and the
// file move carries no download/prepared-media rows. The new container and
// every new child are KeepApart, so automatic provider adoption cannot join
// them back.
//
// Durable file progress is the existing pair of integer frontier columns: the
// parent's current_key is the last raw child PK considered and cursor is the
// last raw link PK considered inside that child. No result list is retained.
// The shared retirement cursor is the approved catalog_purge_jobs row for the
// exact child mutation: its plan_digest binds the compiled units and its TEXT
// cursor carries the engine's full Unit/Verify cursor across restart. A purge
// job for a separate child is never a retained-title capture; the retention
// selector must exclude mode='separate' (reported seam).
//
// No schema is added. The catalogue may not import downloads or preparedmedia
// (downloads imports playback which imports catalog). The retirement hooks
// therefore arrive as explicit function fields, normally attached to the
// request context by the composition root. A command that actually finds a
// claim and has no hook refuses visibly; it never skips cancellation,
// revocation or the worker interrupt.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

const (
	// separateFolderScanLimit is the existing admission guard. The old command
	// refused a folder with more than 4000 container files because it moved all
	// of them in one step; the refusal is retained even though each file now has
	// its own quantum.
	separateFolderScanLimit = 4000

	// separateFolderForegroundQuanta bounds one API call. It is not a scheduler:
	// a command that needs more turns stays active and a later call resumes the
	// same mutation. It never reports the separated id before finalize.
	separateFolderForegroundQuanta = 32

	separateFolderRetirementReadBound = 8

	// separateFolderRawWindow bounds raw (child PK, link PK) rows examined in
	// one file quantum. It is a caller-visible work bound, not a proof that the
	// eligibility lookup is a point.
	separateFolderRawWindow = 128
)

var (
	// ErrSeparateFolderPending is honest backpressure: the command is still
	// admitted and progressing, but this call did not reach finalize. It wraps
	// the existing request_in_progress error; callers must not treat it as a
	// completed split and must not report a public id.
	ErrSeparateFolderPending = fmt.Errorf("%w: separate folder is still progressing", compactcatalog.ErrEntityMutating)

	// ErrSeparateFolderHooks means an actual claim exists and the composition
	// root did not supply the prepared-media/receipt owner operations. Retiring
	// nothing and moving the file would fabricate a successful split.
	ErrSeparateFolderHooks = errors.New("separate folder: prepared-media cancel/interrupt and receipt revocation hooks are not composed")

	// ErrSeparateFolderCursor is a malformed or forged command cursor. The
	// command is left visibly blocked rather than reinterpreted.
	ErrSeparateFolderCursor = errors.New("separate folder: command cursor is not one this plan produced")

	// ErrSeparateFolderIncomplete means finalize still sees a folder file but no
	// file unit can move it (its asset/object is no longer available under the
	// accepted source). The command does not claim a partial split succeeded.
	ErrSeparateFolderIncomplete = errors.New("separate folder: a folder file is no longer available under the accepted source")
)

// SeparateFolderHooks are the existing owner operations the retirement units
// invoke. They are the production wrappers preparedmedia.CancelForRetirementTx,
// preparedmedia.Service.Interrupt and downloads.RevokeForRetirement. The
// catalogue declares the shape only; it imports neither package.
type SeparateFolderHooks struct {
	CancelJob     func(context.Context, *sql.Tx, string) error
	Interrupt     func(string)
	RevokeReceipt func(context.Context, *sql.Tx, string) error
}

func (h SeparateFolderHooks) complete() bool {
	return h.CancelJob != nil && h.Interrupt != nil && h.RevokeReceipt != nil
}

type separateFolderHooksKey struct{}

// WithSeparateFolderHooks attaches the composed owner operations to a request.
// The HTTP composition root owns this call; the Service also carries the
// same hooks for accepted background continuation after the request ends.
func WithSeparateFolderHooks(ctx context.Context, hooks SeparateFolderHooks) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, separateFolderHooksKey{}, hooks)
}

func separateFolderHooksFrom(ctx context.Context) SeparateFolderHooks {
	if ctx == nil {
		return SeparateFolderHooks{}
	}
	hooks, _ := ctx.Value(separateFolderHooksKey{}).(SeparateFolderHooks)
	return hooks
}

// SeparateFolderCoordinatorHooks are the explicit authority and retirement
// hooks for one command step. Authorize is the same callback the HTTP owner
// admission used; a step without it makes no write.
type SeparateFolderCoordinatorHooks struct {
	Authorize  func(*sql.Tx) error
	Retirement SeparateFolderHooks
}

// SeparateFolderCoordinator drives one quantum of an admitted separate
// command. Before, when non-nil, runs after the built-in actual-binding check
// and may add caller-specific custody reads; it must not write. The zero
// Before is the built-in check alone.
type SeparateFolderCoordinator struct {
	Hooks  SeparateFolderCoordinatorHooks
	Before func(context.Context, *sql.Tx, compactcatalog.EntityMutation) error
}

// Step performs at most one command quantum in the supplied writer. It never
// commits; the caller rolls back on error and re-reads the same mutation next
// call. A completed step has Outcome "separated"; ErrSeparateFolderPending
// means the command is still active.
func (c SeparateFolderCoordinator) Step(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) (compactcatalog.MutationStep, error) {
	m, err := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if m.Status == "terminal" {
		return mutationTerminal(m), nil
	}
	if m.Mode != "separate" || m.OwnerKind != "separate" {
		return compactcatalog.MutationStep{}, ErrSeparateFolderCursor
	}
	if m.Parent != "" {
		// A child mutation is driven only by its actual parent command; it is
		// never a second top-level command and is never scheduled on its own.
		return compactcatalog.MutationStep{}, ErrSeparateFolderCursor
	}
	typed, err := separateFolderActorAuthorizeTx(ctx, tx, m)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if c.Hooks.Authorize == nil && !typed {
		return compactcatalog.MutationStep{}, fmt.Errorf("%w: no durable owner authorization is composed", ErrSeparateFolderPending)
	}
	if c.Hooks.Authorize != nil {
		if err = c.Hooks.Authorize(tx); err != nil {
			return compactcatalog.MutationStep{}, err
		}
	}
	var enabled bool
	var health string
	if err := tx.QueryRowContext(ctx, `SELECT enabled,health FROM library_sources WHERE id=? AND library_id=(SELECT library_id FROM catalog_libraries WHERE id=?)`, m.SplitSourceID, m.Library).Scan(&enabled, &health); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if !enabled || health == "offline" || health == "root_changed" || health == "removing" {
		return compactcatalog.MutationStep{More: true, SourcePaused: true, Outcome: "pending"}, nil
	}
	if err = separateFolderActualBindingsTx(ctx, tx, m); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if err = separateFolderSourceCurrentTx(ctx, tx, m); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if c.Before != nil {
		if err = c.Before(ctx, tx, m); err != nil {
			return compactcatalog.MutationStep{}, err
		}
	}
	ctx = compactcatalog.WithMutationFamily(ctx, m.Family)
	if m.Child != "" {
		return c.childStep(ctx, tx, m)
	}
	return c.parentStep(ctx, tx, m)
}

// SeparateFolderCommandStepTx is the narrow dispatch used by the existing
// catalog mutation owner. Typed actors revalidate their accepted local
// membership and profile lifetime; legacy manual callers require the explicit
// foreground authorization callback. The operations adapter supplies the
// service retirement hooks to the accepted background path.
func SeparateFolderCommandStepTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) (compactcatalog.MutationStep, error) {
	// The scheduler's mutation writer uses the title-removal plan. A split
	// quantum is not a removal: force the unchanged interactive plan on the
	// live transaction, then keep the ordinary authority refusal below.
	if err := dbwork.UsePlan(tx, dbwork.DefaultPlan(dbwork.ClassInteractive)); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	return SeparateFolderCoordinator{Hooks: SeparateFolderCoordinatorHooks{Retirement: separateFolderHooksFrom(ctx)}}.Step(ctx, tx, ref)
}

// runSeparateFolder is the service seam. It performs bounded quanta of the one
// actual command and returns the separated container's public id only after
// finalize. A retry resumes the same mutation and returns the same id.
//
// Every quantum runs under the default interactive plan (160 rows). The old
// command used the title-removal plan because it moved the whole folder at
// once; per-file units must prove the unchanged interactive bound.
func (s *Service) runSeparateFolder(ctx context.Context, library, object, revision string, authorize func(*sql.Tx) error) (string, error) {
	if s == nil || s.db == nil || authorize == nil || library == "" || object == "" || revision == "" {
		return "", ErrAdminQuery
	}
	ctx = dbwork.WithPlan(ctx, dbwork.DefaultPlan(dbwork.ClassInteractive))
	retirement := separateFolderHooksFrom(ctx)
	if !retirement.complete() {
		retirement = s.SeparateFolderRetirement
	}
	coordinator := SeparateFolderCoordinator{Hooks: SeparateFolderCoordinatorHooks{Authorize: authorize, Retirement: retirement}}
	var ref compactcatalog.MutationRef
	for i := 0; i < separateFolderForegroundQuanta; i++ {
		var (
			step   compactcatalog.MutationStep
			public string
			done   bool
		)
		err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
			if err := authorize(tx); err != nil {
				return err
			}
			if ref.ID == "" {
				next, separated, complete, e := separateFolderAdmitTx(ctx, tx, library, object, revision)
				if e != nil {
					return e
				}
				ref = next
				if complete {
					public, done = separated, true
					return nil
				}
				step = compactcatalog.PendingMutationStep()
				return nil
			}
			next, e := coordinator.Step(ctx, tx, ref)
			if e != nil {
				return e
			}
			step = next
			if step.Complete {
				if step.Outcome != "separated" {
					return compactcatalog.ErrMutationStale
				}
				public, e = separateFolderPublicTx(ctx, tx, ref)
				if e != nil {
					return e
				}
				done = true
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		if done {
			return public, nil
		}
		if !step.Progressed {
			return "", ErrSeparateFolderPending
		}
		dbwork.Yield(ctx)
	}
	// The command is still active. Its cursor is committed; the next call
	// resumes it. This is not a completed split.
	return "", ErrSeparateFolderPending
}

// separateFolderAdmitTx creates the container and the actual command, or
// resumes the one already admitted for this folder. It returns the separated
// public id only when the command already finished and no original folder file
// remains.
func separateFolderAdmitTx(ctx context.Context, tx *sql.Tx, library, object, revision string) (compactcatalog.MutationRef, string, bool, error) {
	var zero compactcatalog.MutationRef
	handle, err := compactcatalog.LibraryTx(ctx, tx, library)
	if err != nil {
		return zero, "", false, err
	}
	var source, directory, asset string
	if err = tx.QueryRowContext(ctx, `SELECT source_id,directory_path,asset_id FROM inventory_objects WHERE id=? AND retired=0`, object).Scan(&source, &directory, &asset); err != nil {
		return zero, "", false, err
	}
	if source == "" || directory == "" {
		return zero, "", false, ErrAdminQuery
	}
	owner, err := separateFolderAcceptedOwnerTx(ctx, tx, separateFolderOwner(library, source, directory))
	if err != nil {
		return zero, "", false, err
	}
	// Read one raw latest owner key. Terminal/status/object checks happen
	// after LIMIT; a long terminal prefix cannot turn a retry into a scan.
	var latest compactcatalog.MutationRef
	var lastOwnerGeneration int64
	err = tx.QueryRowContext(ctx, separateFolderLatestOwnerSQL, owner).Scan(&latest.ID, &latest.Generation, &lastOwnerGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return zero, "", false, err
	}
	if err == nil {
		held, readErr := compactcatalog.ReadEntityMutationTx(ctx, tx, latest)
		if readErr != nil {
			return zero, "", false, readErr
		}
		if held.Parent != "" || held.Mode != "separate" || held.SplitSourceID != source || held.SplitDirectory != directory {
			return zero, "", false, compactcatalog.ErrMutationStale
		}
		if held.Status != "terminal" {
			if held.SplitObjectID != object || held.SplitObjectRevision != revision {
				return zero, "", false, compactcatalog.ErrMutationStale
			}
			return latest, "", false, nil
		}
		if held.Outcome != "separated" {
			return zero, "", false, identity.ErrForbidden
		}
		if held.SplitObjectID == object && held.SplitObjectRevision == revision {
			public, readErr := separateFolderPublicTx(ctx, tx, latest)
			if readErr != nil {
				return zero, "", false, readErr
			}
			return zero, public, true, nil
		}
	}
	var objectRevision string
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM inventory_objects WHERE id=? AND retired=0`, object).Scan(&objectRevision); err != nil {
		return zero, "", false, err
	}
	if objectRevision != revision {
		return zero, "", false, compactcatalog.ErrMutationStale
	}
	var item int64
	var kind compactcatalog.Kind
	if err = tx.QueryRowContext(ctx, `SELECT e.id,e.kind FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id JOIN catalog_entities e ON e.id=l.entity_id
 WHERE a.token=? AND e.library_id=? AND e.kind IN(?,?) ORDER BY e.id LIMIT 1`, asset, handle, int(compactcatalog.Track), int(compactcatalog.Part)).Scan(&item, &kind); err != nil {
		return zero, "", false, err
	}
	table, parentColumn, containerKind := "catalog_songs", "album_id", compactcatalog.Album
	if kind == compactcatalog.Part {
		table, parentColumn, containerKind = "catalog_book_files", "book_id", compactcatalog.Book
	}
	var container, grandparent int64
	var title string
	var year int
	if err = tx.QueryRowContext(ctx, `SELECT c.id,COALESCE(c.parent_id,0),c.title,c.year FROM `+table+` x JOIN catalog_entities c ON c.id=x.`+parentColumn+` WHERE x.entity_id=?`, item).Scan(&container, &grandparent, &title, &year); err != nil {
		return zero, "", false, err
	}
	witness, err := separateFolderSourceTx(ctx, tx, source)
	if err != nil {
		return zero, "", false, err
	}
	if err := separateFolderAdmissionWindowTx(ctx, tx, container, containerKind, source, witness.Incarnation, directory); err != nil {
		return zero, "", false, err
	}
	root, err := compactcatalog.LibraryRootTx(ctx, tx, handle)
	if err != nil {
		return zero, "", false, err
	}
	key := compactcatalog.ItemKey(root, source+"/"+directory, 0) + "#separate"
	separate, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: containerKind, Parent: grandparent, Key: key, Title: title, Year: year})
	if err != nil {
		return zero, "", false, err
	}
	facts := map[string]any{}
	if containerKind == compactcatalog.Album {
		var artist int64
		var localKey, edition string
		if err = tx.QueryRowContext(ctx, `SELECT artist_id,local_key,edition_key FROM catalog_albums WHERE entity_id=?`, container).Scan(&artist, &localKey, &edition); err != nil {
			return zero, "", false, err
		}
		facts = map[string]any{"artist_id": artist, "local_key": localKey + "#separate", "edition_key": edition}
	} else {
		var bookLibrary int64
		var localKey, author, narrator string
		if err = tx.QueryRowContext(ctx, `SELECT library_id,local_key,author,narrator FROM catalog_books WHERE entity_id=?`, container).Scan(&bookLibrary, &localKey, &author, &narrator); err != nil {
			return zero, "", false, err
		}
		facts = map[string]any{"library_id": bookLibrary, "local_key": localKey + "#separate", "author": author, "narrator": narrator}
	}
	if err = compactcatalog.SetFactsTx(ctx, tx, separate, facts); err != nil {
		return zero, "", false, err
	}
	if err = KeepApartTx(ctx, tx, separate); err != nil {
		return zero, "", false, err
	}
	if err = compactcatalog.TouchEntityTx(ctx, tx, separate); err != nil {
		return zero, "", false, err
	}
	if lastOwnerGeneration == int64(^uint64(0)>>1) {
		return zero, "", false, ErrSeparateFolderCursor
	}
	generation := lastOwnerGeneration + 1
	accepted, err := json.Marshal(witness)
	if err != nil {
		return zero, "", false, err
	}
	ref, err := compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{
		Entity: container, Target: separate,
		OwnerKind: "separate", OwnerID: owner, OwnerGeneration: generation, Mode: "separate", AcceptedRevision: string(accepted),
		SplitSourceID: source, SplitDirectory: directory, SplitObjectID: object, SplitObjectRevision: revision,
	})
	if err != nil {
		return zero, "", false, err
	}
	if err = compactcatalog.SetMutationPhaseTx(ctx, tx, ref, "children"); err != nil {
		return zero, "", false, err
	}
	if strings.HasPrefix(owner, separateActorPrefix) {
		if err := enqueueCatalogMutationTx(ctx, tx, ref); err != nil {
			return zero, "", false, err
		}
		binding, _, err := separateFolderDecodeActor(owner)
		if err != nil {
			return zero, "", false, err
		}
		actor := operations.Hash(identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: binding.Account, ProfileID: binding.Profile}))
		if _, err := tx.ExecContext(ctx, `UPDATE console_operations SET actor=? WHERE id=? AND kind=? AND resource=?`, actor, "cm-"+ref.ID, catalogMutationKind, ref.ID); err != nil {
			return zero, "", false, err
		}
	}
	return ref, "", false, nil
}

// separateFolderOwner is the stable command identity of one accepted folder in
// one library. The object/revision that admitted it are immutable evidence, not
// part of the identity: another file in the same folder resumes the command.
func separateFolderOwner(library, source, directory string) string {
	h := sha256.New()
	for _, part := range []string{library, source, directory} {
		fmt.Fprintf(h, "%d:%s|", len(part), part)
	}
	return "folder:" + hex.EncodeToString(h.Sum(nil))
}

func separateFolderPublicTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) (string, error) {
	m, err := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
	if err != nil {
		return "", err
	}
	target := m.Target
	if target == 0 {
		target = m.TargetSubject
	}
	if target == 0 {
		return "", ErrSeparateFolderCursor
	}
	var public string
	if err = tx.QueryRowContext(ctx, `SELECT pid(e.public_id) FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0 WHERE e.id=? AND e.retired=0`, target).Scan(&public); err != nil {
		return "", err
	}
	return public, nil
}

// separateFolderActualBindingsTx rechecks what the command owns every turn:
// the actual original container and the separated container, in one live
// library, in one family, with the immutable split fields this plan uses.
func separateFolderActualBindingsTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) error {
	if m.Entity == 0 || m.Target == 0 || m.TargetPublic == "" || m.SplitSourceID == "" || m.SplitObjectID == "" || m.SplitObjectRevision == "" {
		return ErrSeparateFolderCursor
	}
	if m.Family == "" || m.Parent == "" && m.Family != m.Ref.ID {
		return ErrSeparateFolderCursor
	}
	switch m.Phase {
	case "admitted", "children", "drain", "finalize":
	default:
		return ErrSeparateFolderCursor
	}
	if m.Cursor < 0 || m.CurrentKey < 0 {
		return ErrSeparateFolderCursor
	}
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities a
 JOIN catalog_entities b ON b.id=?2 AND b.library_id=a.library_id AND b.kind=a.kind AND b.retired=0 AND b.public_id=pid_blob(?3)
 JOIN catalog_libraries cl ON cl.id=a.library_id AND cl.retired=0
 WHERE a.id=?1 AND a.library_id=?4 AND a.kind=?5 AND a.retired=0)`, m.Entity, m.Target, m.TargetPublic, m.Library, int(m.Kind)).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return compactcatalog.ErrMutationStale
	}
	var family, ownerKind, ownerID, mode string
	var generation, ownerGeneration int64
	err = tx.QueryRowContext(ctx, `SELECT family_id,owner_kind,owner_id,mode,generation,owner_generation FROM catalog_entity_mutations WHERE mutation_id=?`, m.Ref.ID).Scan(&family, &ownerKind, &ownerID, &mode, &generation, &ownerGeneration)
	if err != nil {
		return err
	}
	if family != m.Family || ownerKind != "separate" || ownerID != m.OwnerID || mode != "separate" || generation != m.Ref.Generation || ownerGeneration != m.OwnerGeneration {
		return ErrSeparateFolderCursor
	}
	return nil
}

func (c SeparateFolderCoordinator) parentStep(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (compactcatalog.MutationStep, error) {
	switch m.Phase {
	case "admitted":
		return compactcatalog.PendingMutationStep(), compactcatalog.SetMutationPhaseTx(ctx, tx, m.Ref, "children")
	case "drain":
		return compactcatalog.PendingMutationStep(), compactcatalog.SetMutationPhaseTx(ctx, tx, m.Ref, "children")
	case "children":
		if m.Child != "" {
			return c.childStep(ctx, tx, m)
		}
		candidate, found, more, childCursor, assetCursor, err := separateFolderNextRawFileTx(ctx, tx, m)
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		if more {
			return compactcatalog.PendingMutationStep(), separateFolderSetFileCursorTx(ctx, tx, m.Ref, childCursor, assetCursor)
		}
		if !found {
			if err = separateFolderSetFileCursorTx(ctx, tx, m.Ref, childCursor, assetCursor); err != nil {
				return compactcatalog.MutationStep{}, err
			}
			return compactcatalog.PendingMutationStep(), compactcatalog.SetMutationPhaseTx(ctx, tx, m.Ref, "finalize")
		}
		owner := m.OwnerID + ":" + m.Ref.ID + ":" + strconv.FormatInt(candidate.child, 10) + ":" + strconv.FormatInt(candidate.asset, 10)
		child, err := compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{
			Entity: candidate.child, OwnerKind: "separate", OwnerID: owner, OwnerGeneration: m.OwnerGeneration,
			Mode: "separate", Parent: m.Ref.ID, Family: m.Family, AcceptedRevision: m.AcceptedRevision,
			SplitSourceID: m.SplitSourceID, SplitDirectory: m.SplitDirectory, SplitObjectID: m.SplitObjectID, SplitObjectRevision: m.SplitObjectRevision,
		})
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET current_key=? WHERE mutation_id=? AND generation=? AND status='active' AND current_key=0`, candidate.asset, child.ID, child.Generation); err != nil {
			return compactcatalog.MutationStep{}, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET current_child=?,current_key=?,cursor=? WHERE mutation_id=? AND generation=? AND current_child IS NULL AND status='active'`, child.ID, candidate.child, candidate.asset, m.Ref.ID, m.Ref.Generation)
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return compactcatalog.MutationStep{}, compactcatalog.ErrMutationStale
		}
		return compactcatalog.PendingMutationStep(), nil
	case "finalize":
		return c.finalizeStep(ctx, tx, m)
	}
	return compactcatalog.MutationStep{}, ErrSeparateFolderCursor
}

type separateFolderCandidate struct {
	child, asset int64
}

// separateFolderNextRawFileTx reads at most one raw (child PK, link PK) row
// from the original container after the durable (current_key,cursor) frontier.
// The raw child PK is read first, then the raw link primary key
// (entity_id,asset_id). LIMIT applies before any folder/source eligibility, so
// a rejected raw row is not a hidden scan: it advances the frontier and the
// next call continues after it. Same-asset links to different children are
// distinct raw link rows, never one assumed total order.
//
// At most separateFolderRawWindow raw rows are examined in one quantum. If the
// window ends without a candidate the frontier is persisted and the caller
// returns pending; this is a measured bound, not a proof that eligibility or
// the inventory-object lookup is a point.
func separateFolderNextRawFileTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (separateFolderCandidate, bool, bool, int64, int64, error) {
	child, asset := m.CurrentKey, m.Cursor
	for examined := 0; examined < separateFolderRawWindow; examined++ {
		if child == 0 {
			next, err := separateFolderNextChildTx(ctx, tx, m, 0)
			if err != nil {
				return separateFolderCandidate{}, false, false, 0, 0, err
			}
			if next == 0 {
				return separateFolderCandidate{}, false, false, 0, 0, nil
			}
			child, asset = next, 0
		}
		var parent int64
		var kind compactcatalog.Kind
		err := tx.QueryRowContext(ctx, `SELECT parent_id,kind FROM catalog_entities WHERE id=?`, child).Scan(&parent, &kind)
		want := compactcatalog.Track
		if m.Kind == compactcatalog.Book {
			want = compactcatalog.Part
		}
		if errors.Is(err, sql.ErrNoRows) || err == nil && (parent != m.Entity || kind != want) {
			next, nextErr := separateFolderNextChildTx(ctx, tx, m, child)
			if nextErr != nil {
				return separateFolderCandidate{}, false, false, 0, 0, nextErr
			}
			if next == 0 {
				return separateFolderCandidate{}, false, false, 0, 0, nil
			}
			child, asset = next, 0
			continue
		}
		if err != nil {
			return separateFolderCandidate{}, false, false, 0, 0, err
		}
		var (
			nextAsset int64
			token     string
		)
		err = tx.QueryRowContext(ctx, `SELECT l.asset_id,a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.asset_id>? ORDER BY l.asset_id LIMIT 1`, child, asset).Scan(&nextAsset, &token)
		if errors.Is(err, sql.ErrNoRows) {
			next, nextErr := separateFolderNextChildTx(ctx, tx, m, child)
			if nextErr != nil {
				return separateFolderCandidate{}, false, false, 0, 0, nextErr
			}
			if next == 0 {
				return separateFolderCandidate{}, false, false, 0, 0, nil
			}
			child, asset = next, 0
			continue
		}
		if err != nil {
			return separateFolderCandidate{}, false, false, 0, 0, err
		}
		asset = nextAsset
		inFolder, _, _, err := separateFolderFolderObjectTx(ctx, tx, token, m.SplitSourceID, separateFolderSourceIncarnation(m), m.SplitDirectory)
		if err != nil {
			return separateFolderCandidate{}, false, false, 0, 0, err
		}
		if !inFolder {
			// Rejected raw row: the advanced asset cursor is durable.
			continue
		}
		return separateFolderCandidate{child: child, asset: asset}, true, false, child, asset, nil
	}
	return separateFolderCandidate{}, false, true, child, asset, nil
}

// separateFolderNextChildTx is the raw child primary-key window. It does not
// filter by folder membership; eligibility is checked after the LIMIT.
func separateFolderNextChildTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation, after int64) (int64, error) {
	var next int64
	err := tx.QueryRowContext(ctx, separateFolderNextChildSQL, m.Entity, after).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return next, err
}

// Every rejected raw object is returned before availability is checked.
func separateFolderFolderObjectTx(ctx context.Context, tx *sql.Tx, token, source, incarnation, directory string) (bool, string, string, error) {
	var id, revision, state, missing string
	err := tx.QueryRowContext(ctx, separateFolderObjectHeadSQL, token, source, incarnation, directory).Scan(&id, &revision, &state, &missing)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", "", nil
	}
	if err != nil {
		return false, "", "", err
	}
	if state != "available" || missing != "" {
		return false, "", "", compactcatalog.ErrMutationStale
	}
	return true, id, revision, nil
}

func separateFolderSourceIncarnation(m compactcatalog.EntityMutation) string {
	w, _ := separateFolderSourceDecode(m.AcceptedRevision)
	return w.Incarnation // caller has already rechecked the exact witness
}

func separateFolderSetFileCursorTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef, child, asset int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET current_key=?,cursor=? WHERE mutation_id=? AND generation=? AND status='active' AND current_child IS NULL`, child, asset, ref.ID, ref.Generation)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return compactcatalog.ErrMutationStale
	}
	return nil
}

func (c SeparateFolderCoordinator) childStep(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (compactcatalog.MutationStep, error) {
	ref, err := compactcatalog.MutationRefTx(ctx, tx, m.Child)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	child, err := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if child.Parent != m.Ref.ID || child.Family != m.Family || child.Mode != "separate" || child.OwnerKind != "separate" ||
		child.Status != "active" || child.Entity == 0 || child.CurrentKey == 0 ||
		child.SplitSourceID != m.SplitSourceID || child.SplitDirectory != m.SplitDirectory ||
		child.SplitObjectID != m.SplitObjectID || child.SplitObjectRevision != m.SplitObjectRevision {
		return compactcatalog.MutationStep{}, ErrSeparateFolderCursor
	}
	var held bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0 WHERE e.id=? AND e.public_id=pid_blob(?) AND e.library_id=? AND e.kind=? AND e.retired=0)`, child.Entity, child.Public, child.Library, int(child.Kind)).Scan(&held); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if !held {
		return compactcatalog.MutationStep{}, compactcatalog.ErrMutationStale
	}
	claims, err := separateFolderClaimsExistTx(ctx, tx, child.Entity)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	switch child.Phase {
	case "admitted":
		if claims && !c.Hooks.Retirement.complete() {
			return compactcatalog.MutationStep{}, ErrSeparateFolderHooks
		}
		phase := "children"
		if claims {
			phase = "drain"
		}
		return compactcatalog.PendingMutationStep(), compactcatalog.SetMutationPhaseTx(ctx, tx, ref, phase)
	case "drain":
		// Once admitted, the shared engine owns completion, including Verify.
		// An empty live-claim probe cannot skip its persisted final proof.
		if !c.Hooks.Retirement.complete() {
			return compactcatalog.MutationStep{}, ErrSeparateFolderHooks
		}
		plan, err := SeparateFolderRetirementUnits(child.Entity, SeparateFolderRetirementHooks{
			CancelJob: c.Hooks.Retirement.CancelJob, Interrupt: c.Hooks.Retirement.Interrupt, RevokeReceipt: c.Hooks.Retirement.RevokeReceipt,
		})
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		digest := SeparateFolderRetirementPlanDigest(child.Entity)
		cursor, err := separateFolderRetirementCursorTx(ctx, tx, child, digest, plan)
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		owner := phasedpurge.Owner{ReadBound: separateFolderRetirementReadBound,
			Before: func(ctx context.Context, tx *sql.Tx) error {
				return separateFolderChildBindingsTx(ctx, tx, m, child, "drain")
			},
			Save: func(ctx context.Context, tx *sql.Tx, cursor phasedpurge.Cursor) error {
				return separateFolderSaveRetirementCursorTx(ctx, tx, child, digest, cursor)
			},
			Finalize: func(ctx context.Context, tx *sql.Tx) error {
				if e := separateFolderDropRetirementJobTx(ctx, tx, ref); e != nil {
					return e
				}
				_, e := tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET phase='children',cursor=0 WHERE mutation_id=? AND generation=? AND status='active' AND phase='drain' AND current_child IS NULL`, ref.ID, ref.Generation)
				return e
			},
		}
		_, err = phasedpurge.Turn(ctx, tx, plan, cursor, owner)
		if errors.Is(err, ErrSeparateFolderRetirementRetry) {
			return compactcatalog.MutationStep{}, fmt.Errorf("%w: %w", ErrSeparateFolderPending, err)
		}
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		return compactcatalog.PendingMutationStep(), nil
	case "children":
		if claims {
			// A producer admitted a claim after the retirement drain. Do not
			// move the file behind a live claim: drain again first. The purge
			// job was deleted at the previous finalize and is recreated empty.
			if !c.Hooks.Retirement.complete() {
				return compactcatalog.MutationStep{}, ErrSeparateFolderHooks
			}
			return compactcatalog.PendingMutationStep(), compactcatalog.SetMutationPhaseTx(ctx, tx, ref, "drain")
		}
		var asset int64
		var token string
		err := tx.QueryRowContext(ctx, `SELECT l.asset_id,a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? AND l.asset_id>? ORDER BY l.asset_id LIMIT 1`, child.Entity, child.Cursor).Scan(&asset, &token)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET phase='finalize',cursor=0 WHERE mutation_id=? AND generation=? AND phase='children'`, child.Ref.ID, child.Ref.Generation)
			return compactcatalog.PendingMutationStep(), err
		}
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		here, _, _, err := separateFolderFolderObjectTx(ctx, tx, token, m.SplitSourceID, separateFolderSourceIncarnation(m), m.SplitDirectory)
		if err != nil {
			return compactcatalog.MutationStep{}, err
		}
		phase := "children"
		if !here {
			phase = "finalize"
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET phase=?,cursor=? WHERE mutation_id=? AND generation=? AND phase='children'`, phase, asset, child.Ref.ID, child.Ref.Generation)
		return compactcatalog.PendingMutationStep(), err
	case "finalize":
		if claims {
			return compactcatalog.MutationStep{}, compactcatalog.ErrEntityMutating
		}
		return c.fileStep(ctx, tx, m, child)
	}
	return compactcatalog.MutationStep{}, ErrSeparateFolderCursor
}

func separateFolderChildBindingsTx(ctx context.Context, tx *sql.Tx, m, child compactcatalog.EntityMutation, phase string) error {
	var currentPhase, parent string
	var status string
	var entity int64
	err := tx.QueryRowContext(ctx, `SELECT parent_mutation_id,status,phase,entity_id FROM catalog_entity_mutations WHERE mutation_id=? AND generation=?`, child.Ref.ID, child.Ref.Generation).Scan(&parent, &status, &currentPhase, &entity)
	if err != nil {
		return err
	}
	if parent != m.Ref.ID || status != "active" || currentPhase != phase || entity != child.Entity {
		return compactcatalog.ErrMutationStale
	}
	if err = separateFolderActualBindingsTx(ctx, tx, m); err != nil {
		return err
	}
	return nil
}

// separateFolderRetirementCursorTx reads the shared-engine cursor from the
// designated retirement store (catalog_purge_jobs, keyed by this exact child
// mutation). plan_digest binds every unit literal, phase, read bound and engine
// limit; generation binds the actual command. A mismatch is a visible block,
// never a reset. The cursor is the engine's full JSON cursor including Verify,
// so a restart during verification cannot rewind to unit 0 and finalize while
// a later unit is non-empty.
func separateFolderRetirementCursorTx(ctx context.Context, tx *sql.Tx, child compactcatalog.EntityMutation, digest string, plan []phasedpurge.Unit) (phasedpurge.Cursor, error) {
	var generation int64
	var storedDigest, raw string
	err := tx.QueryRowContext(ctx, `SELECT generation,plan_digest,cursor FROM catalog_purge_jobs WHERE mutation_id=?`, child.Ref.ID).Scan(&generation, &storedDigest, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_purge_jobs(mutation_id,generation,plan_digest,state,cursor) VALUES(?,?,?,'running','')`, child.Ref.ID, child.Ref.Generation, digest); err != nil {
			return phasedpurge.Cursor{}, err
		}
		return phasedpurge.Cursor{}, nil
	}
	if err != nil {
		return phasedpurge.Cursor{}, err
	}
	if generation != child.Ref.Generation || storedDigest != digest {
		return phasedpurge.Cursor{}, ErrSeparateFolderCursor
	}
	cursor, err := phasedpurge.DecodeCursor(raw, plan)
	if err != nil {
		return phasedpurge.Cursor{}, fmt.Errorf("%w: %v", ErrSeparateFolderCursor, err)
	}
	return cursor, nil
}

func separateFolderSaveRetirementCursorTx(ctx context.Context, tx *sql.Tx, child compactcatalog.EntityMutation, digest string, cursor phasedpurge.Cursor) error {
	raw, err := phasedpurge.EncodeCursor(cursor)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSeparateFolderCursor, err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE catalog_purge_jobs SET cursor=?,state='running' WHERE mutation_id=? AND generation=? AND plan_digest=?`, raw, child.Ref.ID, child.Ref.Generation, digest)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return compactcatalog.ErrMutationStale
	}
	return nil
}

func separateFolderDropRetirementJobTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_purge_jobs WHERE mutation_id=? AND generation=?`, ref.ID, ref.Generation)
	return err
}

// separateFolderClaimsExistTx uses the same literal state filters and approved
// retirement indexes as the compiled units. It decides only whether a child
// has anything this command must retire; it selects no candidate.
func separateFolderClaimsExistTx(ctx context.Context, tx *sql.Tx, item int64) (bool, error) {
	var found bool
	err := tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM prepared_media_jobs INDEXED BY prepared_media_retirement_jobs WHERE item_id=?1 AND state IN('queued','running','cancelling'))
 OR EXISTS(SELECT 1 FROM prepared_media_versions INDEXED BY prepared_media_item_versions WHERE item_id=?1 AND state='published')
 OR EXISTS(SELECT 1 FROM download_grants INDEXED BY download_grants_retirement WHERE item_id=?1 AND revoked=0)
 OR EXISTS(SELECT 1 FROM download_receipts INDEXED BY download_receipts_retirement WHERE item_id=?1 AND revoked_ms=0)
 OR EXISTS(SELECT 1 FROM download_preparations INDEXED BY download_preparations_retirement WHERE item_id=?1 AND state IN('queued','running','ready','paused','failed'))
 OR EXISTS(SELECT 1 FROM download_request_items INDEXED BY download_request_items_retirement WHERE item_id=?1 AND reason='')
 OR EXISTS(SELECT 1 FROM download_requests INDEXED BY download_requests_retirement WHERE target_id=?1 AND state='capturing')`, item).Scan(&found)
	return found, err
}

func (c SeparateFolderCoordinator) fileStep(ctx context.Context, tx *sql.Tx, m, child compactcatalog.EntityMutation) (compactcatalog.MutationStep, error) {
	table, parentColumn := "catalog_songs", "album_id"
	if child.Kind == compactcatalog.Part {
		table, parentColumn = "catalog_book_files", "book_id"
	}
	var (
		asset         int64
		token, path   string
		objectID      string
		revision      string
		generation    int64
		incarnation   string
		rootIdentity  string
		publicLibrary string
		link          compactcatalog.Link
		end           sql.NullFloat64
	)
	err := tx.QueryRowContext(ctx, `SELECT a.id,a.token,a.path,s.generation,s.incarnation,s.root_identity,cl.library_id,l.part_index,l.start_seconds,l.end_seconds,l.edition
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id
 JOIN library_sources s ON s.id=?3 JOIN catalog_libraries cl ON cl.library_id=s.library_id AND cl.retired=0
 WHERE l.entity_id=?1 AND l.asset_id=?2`, child.Entity, child.CurrentKey, m.SplitSourceID).Scan(&asset, &token, &path, &generation, &incarnation, &rootIdentity, &publicLibrary, &link.Part, &link.Start, &end, &link.Edition)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	found, objectID, revision, err := separateFolderFolderObjectTx(ctx, tx, token, m.SplitSourceID, incarnation, m.SplitDirectory)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if !found {
		return compactcatalog.MutationStep{}, compactcatalog.ErrMutationStale
	}
	if end.Valid {
		link.End = &end.Float64
	}
	elsewhere := child.Cursor != 0
	var entityParent int64
	var factParent int64
	var entityKind compactcatalog.Kind
	if err = tx.QueryRowContext(ctx, `SELECT parent_id,kind FROM catalog_entities WHERE id=?`, child.Entity).Scan(&entityParent, &entityKind); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT `+parentColumn+` FROM `+table+` WHERE entity_id=?`, child.Entity).Scan(&factParent); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if entityParent != m.Entity || factParent != m.Entity || entityKind != child.Kind {
		return compactcatalog.MutationStep{}, compactcatalog.ErrMutationStale
	}
	result, err := SeparateFolderFileTx(ctx, tx, SeparateFolderFileRequest{
		Library: publicLibrary, Object: objectID, Revision: revision,
		Source: m.SplitSourceID, Directory: m.SplitDirectory, SourceGeneration: generation,
		SourceIncarnation: incarnation, RootIdentity: rootIdentity, AssetPath: path,
		OldContainer: m.Entity, NewContainer: m.Target, Child: child.Entity, Asset: asset,
		HasElsewhere: elsewhere, Link: link,
		Before: func(ctx context.Context, tx *sql.Tx) error {
			if err := separateFolderChildBindingsTx(ctx, tx, m, child, "finalize"); err != nil {
				return err
			}
			var exact bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id
 JOIN catalog_libraries cl ON cl.library_id=s.library_id AND cl.retired=0
 WHERE o.id=? AND o.revision=? AND o.asset_id=? AND o.source_id=? AND o.directory_path=? AND o.retired=0 AND o.root_incarnation=s.incarnation
 AND s.library_id=? AND s.generation=? AND s.incarnation=? AND s.root_identity=? AND s.enabled=1 AND s.health NOT IN('offline','root_changed','removing'))`,
				objectID, revision, token, m.SplitSourceID, m.SplitDirectory, publicLibrary, generation, incarnation, rootIdentity).Scan(&exact)
			if err != nil {
				return err
			}
			if !exact {
				return compactcatalog.ErrMutationStale
			}
			return nil
		},
	})
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if result.Repeated {
		return compactcatalog.MutationStep{}, compactcatalog.ErrMutationStale
	}
	if _, err = compactcatalog.FinishEntityMutationTx(ctx, tx, child.Ref, "separated"); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	advanced, err := tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET current_child=NULL,cursor=? WHERE mutation_id=? AND generation=? AND status='active' AND current_child=?`, asset, m.Ref.ID, m.Ref.Generation, child.Ref.ID)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if n, _ := advanced.RowsAffected(); n != 1 {
		return compactcatalog.MutationStep{}, compactcatalog.ErrMutationStale
	}
	return compactcatalog.PendingMutationStep(), nil
}

func (c SeparateFolderCoordinator) finalizeStep(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (compactcatalog.MutationStep, error) {
	if m.Child != "" {
		return compactcatalog.MutationStep{}, compactcatalog.ErrMutationStale
	}
	if m.Phase != "finalize" {
		return compactcatalog.MutationStep{}, ErrSeparateFolderCursor
	}
	// The prior raw pass persisted exhaustion while actual custody fenced all
	// source/attachment producers. Recheck that witness, not a fresh tree scan.
	if err := separateFolderSourceCurrentTx(ctx, tx, m); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	var err error
	for _, id := range []int64{m.Target, m.Entity} {
		if err = compactcatalog.TouchEntityTx(ctx, tx, id); err != nil {
			return compactcatalog.MutationStep{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=(SELECT library_id FROM catalog_libraries WHERE id=?)`, m.Library); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	return compactcatalog.FinishEntityMutationTx(ctx, tx, m.Ref, "separated")
}
