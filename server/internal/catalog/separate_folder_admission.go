package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// admissionProof is request-local, never persisted or reused after admission.
type separateFolderProofKey struct{}
type separateFolderProof struct {
	reading     bool
	publication uint64
	ready       bool
	binding     string
}

func (s *Service) separateFolderProof(ctx context.Context, library, object, revision string, authorize func(*sql.Tx) error) (*separateFolderProof, error) {
	if dbwork.InWriteTx(ctx) || dbwork.Snapshot(ctx) != nil {
		return nil, dbwork.ErrNested
	}
	p := &separateFolderProof{reading: true, publication: dbwork.Publications()}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err := dbwork.WithReadSnapshot(readCtx, s.db, func(inner context.Context) error {
		tx := dbwork.Snapshot(inner)
		if err := authorize(tx); err != nil {
			return err
		}
		_, _, _, err := separateFolderAdmitTx(context.WithValue(inner, separateFolderProofKey{}, p), tx, library, object, revision)
		return err
	})
	p.reading = false
	return p, err
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
	proof, err := s.separateFolderProof(ctx, library, object, revision, authorize)
	if err != nil {
		return "", err
	}
	ctx = context.WithValue(ctx, separateFolderProofKey{}, proof)
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
	proof, _ := ctx.Value(separateFolderProofKey{}).(*separateFolderProof)
	if proof == nil {
		return zero, "", false, dbwork.ErrWriterCapacity
	}
	if !proof.reading && proof.publication != dbwork.Publications() {
		return zero, "", false, dbwork.ErrWriterCapacity
	}
	var handle int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_libraries WHERE library_id=? AND retired=0`, library).Scan(&handle)
	if err != nil {
		return zero, "", false, err
	}
	if handle == 0 {
		return zero, "", false, ErrAdminQuery
	}
	var source, directory, asset, objectRoot, objectState, objectMissing string
	if err = tx.QueryRowContext(ctx, `SELECT source_id,directory_path,asset_id,root_incarnation,state,missing_since FROM inventory_objects WHERE id=? AND retired=0`, object).Scan(&source, &directory, &asset, &objectRoot, &objectState, &objectMissing); err != nil {
		return zero, "", false, err
	}
	if source == "" || directory == "" {
		return zero, "", false, ErrAdminQuery
	}
	owner, err := separateFolderAcceptedOwnerTx(ctx, tx, separateFolderOwner(library, source, directory))
	if err != nil {
		return zero, "", false, err
	}
	if actor, typed, e := separateFolderDecodeActor(owner); e != nil {
		return zero, "", false, e
	} else if typed {
		if _, e = actor.authorizeTx(ctx, tx, library); e != nil {
			return zero, "", false, e
		}
	}
	// Read one raw latest owner key. Terminal/status/object checks happen
	// after LIMIT; a long terminal prefix cannot turn a retry into a scan.
	var latest compactcatalog.MutationRef
	var lastOwnerGeneration int64
	err = tx.QueryRowContext(ctx, separateFolderLatestOwnerSQL, owner).Scan(&latest.ID, &latest.Generation, &lastOwnerGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return zero, "", false, err
	}
	var prior *compactcatalog.EntityMutation
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
			prior = &held
		}
		if held.Outcome == "separated" && held.SplitObjectID == object && held.SplitObjectRevision == revision {
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
 WHERE a.token=? AND e.library_id=? AND e.kind IN(?,?) AND e.retired=0 ORDER BY e.id LIMIT 1`, asset, handle, int(compactcatalog.Track), int(compactcatalog.Part)).Scan(&item, &kind); err != nil {
		return zero, "", false, err
	}
	table, parentColumn, containerKind := "catalog_songs", "album_id", compactcatalog.Album
	if kind == compactcatalog.Part {
		table, parentColumn, containerKind = "catalog_book_files", "book_id", compactcatalog.Book
	}
	var container, grandparent int64
	var title string
	var year int
	if err = tx.QueryRowContext(ctx, `SELECT c.id,COALESCE(c.parent_id,0),c.title,c.year FROM `+table+` x JOIN catalog_entities c ON c.id=x.`+parentColumn+` WHERE x.entity_id=? AND c.retired=0`, item).Scan(&container, &grandparent, &title, &year); err != nil {
		return zero, "", false, err
	}
	// A partial abandoned command may already have moved this selected asset.
	// Only its exact retained subject/target receipt can recover the original.
	if prior != nil {
		if container != prior.Subject && container != prior.TargetSubject {
			return zero, "", false, compactcatalog.ErrMutationStale
		}
		if err := separateFolderReceiptLiveTx(ctx, tx, *prior); err != nil {
			return zero, "", false, err
		}
		container = prior.Subject
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(parent_id,0),title,year FROM catalog_entities WHERE id=? AND library_id=? AND kind=? AND retired=0`, container, handle, containerKind).Scan(&grandparent, &title, &year); err != nil {
			return zero, "", false, err
		}
	}
	var containerPublic, containerIncarnation, containerPublication string
	if err := tx.QueryRowContext(ctx, `SELECT pid(e.public_id),COALESCE(ah.incarnation,''),COALESCE(ph.incarnation,'') FROM catalog_entities e LEFT JOIN mb_album_heads ah ON ah.album_id=e.id LEFT JOIN metadata_publication_heads ph ON ph.item_id=e.id WHERE e.id=? AND e.retired=0`, container).Scan(&containerPublic, &containerIncarnation, &containerPublication); err != nil {
		return zero, "", false, err
	}
	witness, err := separateFolderSourceTx(ctx, tx, source)
	if err != nil {
		return zero, "", false, err
	}
	var sourceLive, sourceEnabled bool
	var sourceHealth string
	if err := tx.QueryRowContext(ctx, `SELECT library_id=?,enabled,health FROM library_sources WHERE id=?`, library, source).Scan(&sourceLive, &sourceEnabled, &sourceHealth); err != nil {
		return zero, "", false, err
	}
	// Offline, disabled and root-changed sources retain accepted intent. The
	// coordinator pauses them before moving any file. Removing is terminal
	// intent and must not admit a new command. Bind all three scalar values so
	// the writer also rechecks the exact state proved on the read snapshot.
	if !sourceLive || sourceHealth == "removing" {
		return zero, "", false, compactcatalog.ErrMutationStale
	}
	if objectRoot != witness.Incarnation || objectState != "available" || objectMissing != "" {
		return zero, "", false, compactcatalog.ErrMutationStale
	}
	root, err := compactcatalog.LibraryRootTx(ctx, tx, handle)
	if err != nil {
		return zero, "", false, err
	}
	bindingBytes, err := json.Marshal([]any{library, object, revision, handle, source, directory, asset, owner, latest, lastOwnerGeneration, item, kind, container, grandparent, title, year, witness, root, objectRoot, objectState, objectMissing, containerPublic, containerIncarnation, containerPublication, sourceLive, sourceEnabled, sourceHealth})
	if err != nil {
		return zero, "", false, err
	}
	binding := string(bindingBytes)
	if proof.reading {
		if err := separateFolderAdmissionWindowTx(ctx, tx, container, containerKind, source, witness.Incarnation, directory); err != nil {
			return zero, "", false, err
		}
		proof.binding, proof.ready = binding, true
		return zero, "", false, nil
	}
	if !proof.ready || proof.binding != binding {
		return zero, "", false, dbwork.ErrWriterCapacity
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
