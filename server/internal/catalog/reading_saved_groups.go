package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

type ReadingSavedGroupKind string

const (
	ReadingSavedSeries    ReadingSavedGroupKind = "series"
	ReadingSavedMagazine  ReadingSavedGroupKind = "magazine"
	ReadingSavedNewspaper ReadingSavedGroupKind = "newspaper"
)

func (ReadingSavedGroupKind) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"series", "magazine", "newspaper"}}
}

type ReadingSavedGroupTarget struct {
	LibraryID string                `json:"libraryId"`
	GroupKind ReadingSavedGroupKind `json:"groupKind"`
	SeriesID  string                `json:"seriesId"`
}

// The private key and proof come from the current ready, visible accepted
// group in the same transaction. Neither can be supplied by an HTTP body.
type ReadingSavedGroupProof struct{ Key, ViewerFence string }
type ReadingSavedGroupResolver func(context.Context, *sql.Tx, identity.Principal, ReadingSavedGroupTarget) (ReadingSavedGroupProof, error)

// Reference removal re-admits only the original normal personal actor. It must
// not resolve a target/library/group, including for an unavailable reference.
type ReadingSavedReferenceAuthorizer func(context.Context, *sql.Tx, identity.Principal) error

type ReadingSavedGroupRemoval struct {
	ReferenceID string `json:"referenceId"`
	Revision    int64  `json:"revision"`
	Removed     bool   `json:"removed"`
	OperationID string `json:"operationId"`
}

type ReadingSavedGroupState struct {
	ReadingSavedGroupTarget
	Listed      bool   `json:"listed"`
	Revision    int64  `json:"revision"`
	ReferenceID string `json:"referenceId,omitempty"`
	OperationID string `json:"operationId,omitempty"`
}

func validReadingSavedGroupTarget(t ReadingSavedGroupTarget) bool {
	return personalOperation.MatchString(t.LibraryID) && len(t.SeriesID) == 43 && personalOperation.MatchString(t.SeriesID) &&
		(t.GroupKind == ReadingSavedSeries || t.GroupKind == ReadingSavedMagazine || t.GroupKind == ReadingSavedNewspaper)
}

func readingSavedGroupActor(p identity.Principal, server string) error {
	if !identity.CanUsePersonalState(p) || server == "" || p.ServerID != server {
		return identity.ErrForbidden
	}
	return nil
}

func readingSavedGroupProof(ctx context.Context, tx *sql.Tx, p identity.Principal, target ReadingSavedGroupTarget, resolve ReadingSavedGroupResolver) (ReadingSavedGroupProof, error) {
	if resolve == nil {
		return ReadingSavedGroupProof{}, identity.ErrForbidden
	}
	proof, err := resolve(ctx, tx, p, target)
	if err != nil {
		return proof, err
	}
	if proof.Key == "" || !utf8.ValidString(proof.Key) || len(proof.Key) > 1<<20 || len(proof.ViewerFence) != 64 {
		return ReadingSavedGroupProof{}, ErrReadingProjectionChanged
	}
	if _, err := hex.DecodeString(proof.ViewerFence); err != nil {
		return ReadingSavedGroupProof{}, ErrReadingProjectionChanged
	}
	return proof, nil
}

func readReadingSavedGroup(ctx context.Context, tx *sql.Tx, owner, server string, target ReadingSavedGroupTarget, key string) (ReadingSavedGroupState, error) {
	out := ReadingSavedGroupState{ReadingSavedGroupTarget: target}
	err := tx.QueryRowContext(ctx, `SELECT reference_id,listed,revision FROM reading_saved_groups
 WHERE owner_key=? AND server_id=? AND library_id=? AND group_kind=? AND series_key=?`, owner, server, target.LibraryID, target.GroupKind, key).Scan(&out.ReferenceID, &out.Listed, &out.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	return out, err
}

func bumpReadingSavedGroups(ctx context.Context, tx *sql.Tx, owner, server string) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO reading_saved_group_owners(owner_key,server_id,revision) VALUES(?,?,1)
 ON CONFLICT(owner_key,server_id) DO UPDATE SET revision=reading_saved_group_owners.revision+1 WHERE reading_saved_group_owners.revision<9007199254740991`, owner, server)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrPersonalConflict
	}
	return nil
}

func (s *Service) RemoveReadingSavedReference(p identity.Principal, server, reference, operation string, expected int64, authorize ReadingSavedReferenceAuthorizer) (ReadingSavedGroupRemoval, error) {
	if err := requireCatalogMutationContext(s.Context(), operation); err != nil {
		return ReadingSavedGroupRemoval{}, err
	}
	var out ReadingSavedGroupRemoval
	if err := readingSavedGroupActor(p, server); err != nil {
		return out, err
	}
	if authorize == nil {
		return out, identity.ErrForbidden
	}
	if !personalOperation.MatchString(reference) || !personalOperation.MatchString(operation) || expected < 1 || expected > 9007199254740991 {
		return out, requestinput.New("invalid private Saved reference intent")
	}
	g, err := dbwork.Begin(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive))
	if err != nil {
		return out, err
	}
	defer g.Rollback()
	tx := g.Tx()
	if err = authorize(s.Context(), tx, p); err != nil {
		return out, err
	}
	_, _, protocol := usermutation.RequestFrom(s.Context())
	priorReceipt, err := usermutation.CheckContext(s.Context(), tx, time.Now().UnixMilli())
	if err != nil {
		return out, err
	}
	if priorReceipt != nil {
		return ReadingSavedGroupRemoval{ReferenceID: priorReceipt.TargetID, Revision: priorReceipt.Revision, Removed: true, OperationID: operation}, nil
	}
	owner := identity.PersonalKey(p.Viewer)
	// Select no target/key/metadata. Foreign and absent references both miss.
	var revision int64
	var listed bool
	if err = tx.QueryRowContext(s.Context(), `SELECT revision,listed FROM reading_saved_groups WHERE reference_id=? AND owner_key=? AND server_id=?`, reference, owner, server).Scan(&revision, &listed); err != nil {
		return out, err
	}
	hash := operationHash(struct {
		Kind, Reference string
		Expected        int64
	}{"reference-remove", reference, expected})
	if !protocol {
		var previous, receipt string
		err = tx.QueryRowContext(s.Context(), `SELECT request_hash,response FROM reading_saved_group_receipts WHERE owner_key=? AND server_id=? AND operation_id=? AND created_at>=?`, owner, server, operation, time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339)).Scan(&previous, &receipt)
		if err == nil {
			if previous != hash {
				return out, ErrOperationConflict
			}
			if err = json.Unmarshal([]byte(receipt), &out); err != nil {
				return out, err
			}
			if out.ReferenceID != reference || out.OperationID != operation || !out.Removed || out.Revision < 1 || out.Revision > 9007199254740991 {
				return ReadingSavedGroupRemoval{}, ErrReadingProjectionChanged
			}
			return out, g.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if err = checkRetired(tx, owner, "reading_saved_group:"+server, operation, hash); err != nil {
			return out, err
		}
	}
	if revision != expected {
		return out, ErrPersonalConflict
	}
	if listed {
		if revision == 9007199254740991 {
			return out, ErrPersonalConflict
		}
		result, err := tx.ExecContext(s.Context(), `UPDATE reading_saved_groups SET listed=0,revision=revision+1 WHERE reference_id=? AND owner_key=? AND server_id=? AND revision=?`, reference, owner, server, expected)
		if err != nil {
			return out, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return out, err
		}
		if affected != 1 {
			return out, ErrPersonalConflict
		}
		revision++
		if err = bumpReadingSavedGroups(s.Context(), tx, owner, server); err != nil {
			return out, err
		}
	}
	out = ReadingSavedGroupRemoval{ReferenceID: reference, Revision: revision, Removed: true, OperationID: operation}
	if protocol {
		if err = usermutation.SaveContext(s.Context(), tx, usermutation.Result{Status: "removed", TargetID: reference, Revision: out.Revision}, time.Now().UnixMilli()); err != nil {
			return out, err
		}
		return out, g.Commit()
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if err = fenceOperation(tx, owner, "reading_saved_group:"+server, operation, hash); err != nil {
		return out, err
	}
	_, err = tx.ExecContext(s.Context(), `INSERT INTO reading_saved_group_receipts(owner_key,server_id,operation_id,request_hash,response,created_at) VALUES(?,?,?,?,?,?)`, owner, server, operation, hash, string(encoded), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return out, err
	}
	return out, g.Commit()
}

func (s *Service) ReadingSavedGroup(p identity.Principal, server string, target ReadingSavedGroupTarget, resolve ReadingSavedGroupResolver) (ReadingSavedGroupState, ReadingSavedGroupProof, error) {
	var out ReadingSavedGroupState
	var proof ReadingSavedGroupProof
	if err := readingSavedGroupActor(p, server); err != nil {
		return out, proof, err
	}
	if !validReadingSavedGroupTarget(target) {
		return out, proof, requestinput.New("invalid Reading group target")
	}
	err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		var err error
		proof, err = readingSavedGroupProof(ctx, tx, p, target, resolve)
		if err != nil {
			return err
		}
		out, err = readReadingSavedGroup(ctx, tx, identity.PersonalKey(p.Viewer), server, target, proof.Key)
		return err
	})
	return out, proof, err
}

// Membership changes one dynamic private reference. It never expands the group
// into leaf saves, changes files, or writes reading/progress/playback state.
func (s *Service) SetReadingSavedGroup(p identity.Principal, server string, target ReadingSavedGroupTarget, operation string, expected int64, listed bool, resolve ReadingSavedGroupResolver) (ReadingSavedGroupState, ReadingSavedGroupProof, error) {
	if err := requireCatalogMutationContext(s.Context(), operation); err != nil {
		return ReadingSavedGroupState{}, ReadingSavedGroupProof{}, err
	}
	var out ReadingSavedGroupState
	var proof ReadingSavedGroupProof
	if err := readingSavedGroupActor(p, server); err != nil {
		return out, proof, err
	}
	if !validReadingSavedGroupTarget(target) || !personalOperation.MatchString(operation) || expected < 0 || expected > 9007199254740991 {
		return out, proof, requestinput.New("invalid Reading group intent")
	}
	g, err := dbwork.Begin(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive))
	if err != nil {
		return out, proof, err
	}
	defer g.Rollback()
	tx := g.Tx()
	_, _, protocol := usermutation.RequestFrom(s.Context())
	priorReceipt, err := usermutation.CheckContext(s.Context(), tx, time.Now().UnixMilli())
	if err != nil {
		return out, proof, err
	}
	if priorReceipt != nil {
		out = ReadingSavedGroupState{ReadingSavedGroupTarget: target, ReferenceID: priorReceipt.TargetID, Revision: priorReceipt.Revision, OperationID: operation}
		if out.ReferenceID != "" {
			err = tx.QueryRowContext(s.Context(), `SELECT listed,revision FROM reading_saved_groups WHERE reference_id=? AND owner_key=? AND server_id=?`, out.ReferenceID, identity.PersonalKey(p.Viewer), server).Scan(&out.Listed, &out.Revision)
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
			}
		}
		if err != nil {
			return out, proof, err
		}
		proof, err = readingSavedGroupProof(s.Context(), tx, p, target, resolve)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrReadingProjectionChanged) {
			err = nil
		}
		return out, proof, err
	}
	proof, err = readingSavedGroupProof(s.Context(), tx, p, target, resolve)
	if err != nil {
		return out, proof, err
	}
	owner := identity.PersonalKey(p.Viewer)
	hash := operationHash(struct {
		Target   ReadingSavedGroupTarget
		Key      string
		Expected int64
		Listed   bool
	}{target, proof.Key, expected, listed})
	if !protocol {
		var previous, receipt string
		err = tx.QueryRowContext(s.Context(), `SELECT request_hash,response FROM reading_saved_group_receipts WHERE owner_key=? AND server_id=? AND operation_id=? AND created_at>=?`, owner, server, operation, time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339)).Scan(&previous, &receipt)
		if err == nil {
			if previous != hash {
				return out, proof, ErrOperationConflict
			}
			if err = json.Unmarshal([]byte(receipt), &out); err != nil {
				return out, proof, err
			}
			if out.ReadingSavedGroupTarget != target || out.OperationID != operation || out.Revision < 0 || out.Revision > 9007199254740991 {
				return ReadingSavedGroupState{}, proof, ErrReadingProjectionChanged
			}
			return out, proof, g.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, proof, err
		}
		if err = checkRetired(tx, owner, "reading_saved_group:"+server, operation, hash); err != nil {
			return out, proof, err
		}
	}
	out, err = readReadingSavedGroup(s.Context(), tx, owner, server, target, proof.Key)
	if err != nil {
		return out, proof, err
	}
	changed := false
	if out.Revision != expected {
		return out, proof, ErrPersonalConflict
	}
	if out.ReferenceID == "" && listed {
		out.ReferenceID = identity.Token()
		out.Revision = 1
		out.Listed = true
		changed = true
		_, err = tx.ExecContext(s.Context(), `INSERT INTO reading_saved_groups(reference_id,owner_key,server_id,library_id,group_kind,series_key,listed,revision) VALUES(?,?,?,?,?,?,1,1)`, out.ReferenceID, owner, server, target.LibraryID, target.GroupKind, proof.Key)
	} else if out.ReferenceID != "" && out.Listed != listed {
		if out.Revision == 9007199254740991 {
			return out, proof, ErrPersonalConflict
		}
		out.Listed = listed
		out.Revision++
		var result sql.Result
		result, err = tx.ExecContext(s.Context(), `UPDATE reading_saved_groups SET listed=?,revision=? WHERE reference_id=? AND owner_key=? AND server_id=? AND revision=?`, listed, out.Revision, out.ReferenceID, owner, server, expected)
		if err == nil {
			affected, e := result.RowsAffected()
			if e != nil {
				return out, proof, e
			}
			if affected != 1 {
				return out, proof, ErrPersonalConflict
			}
		}
		changed = true
	}
	if err != nil {
		return out, proof, err
	}
	if changed {
		if err = bumpReadingSavedGroups(s.Context(), tx, owner, server); err != nil {
			return out, proof, err
		}
	}
	out.OperationID = operation
	if protocol {
		if err = usermutation.SaveContext(s.Context(), tx, usermutation.Result{Status: "applied", TargetID: out.ReferenceID, Revision: out.Revision}, time.Now().UnixMilli()); err != nil {
			return out, proof, err
		}
		return out, proof, g.Commit()
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return out, proof, err
	}
	if err = fenceOperation(tx, owner, "reading_saved_group:"+server, operation, hash); err != nil {
		return out, proof, err
	}
	_, err = tx.ExecContext(s.Context(), `INSERT INTO reading_saved_group_receipts(owner_key,server_id,operation_id,request_hash,response,created_at) VALUES(?,?,?,?,?,?)`, owner, server, operation, hash, string(encoded), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return out, proof, err
	}
	return out, proof, g.Commit()
}
