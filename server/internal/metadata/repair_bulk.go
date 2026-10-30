package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

const bulkTargetLimit = 200

// A bulk edit is a set of independent single-target edits, not one large
// transaction. Each target carries its own revision fence and is applied in its
// own transaction, so one stale target cannot discard the other 199, and the
// caller is told exactly which ones failed and why.
type BulkTargetAuthorize func(*sql.Tx, RepairTarget) error

type BulkTarget struct {
	Kind             string `json:"kind"`
	ID               string `json:"id"`
	ExpectedRevision string `json:"expectedRevision"`
}
type BulkListEdit struct {
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
}
type BulkEdit struct {
	OperationID string                     `json:"operationId"`
	Targets     []BulkTarget               `json:"targets"`
	Fields      map[string]RepairFieldEdit `json:"fields,omitempty"`
	Lists       map[string]BulkListEdit    `json:"lists,omitempty"`
	Genres      *BulkListEdit              `json:"genres,omitempty"`
	LockEdited  *bool                      `json:"lockEdited,omitempty"`
}
type BulkResult struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
	Revision string `json:"revision,omitempty"`
}
type BulkReceipt struct {
	OperationID string       `json:"operationId"`
	Results     []BulkResult `json:"results"`
	Updated     int          `json:"updated"`
	Failed      int          `json:"failed"`
}

func validBulkEdit(m BulkEdit) error {
	if len(m.OperationID) == 0 || len(m.OperationID) > 160 || len(m.Targets) == 0 || len(m.Targets) > bulkTargetLimit {
		return ErrRepairInput
	}
	if len(m.Fields) == 0 && len(m.Lists) == 0 && m.Genres == nil {
		return ErrRepairInput
	}
	if len(m.Fields) > 32 || len(m.Lists) > 2 {
		return ErrRepairInput
	}
	kind := m.Targets[0].Kind
	seen := map[string]bool{}
	for _, t := range m.Targets {
		if t.Kind != kind || t.ID == "" || len(t.ID) > 256 || len(t.ExpectedRevision) != 64 || seen[t.ID] {
			return ErrRepairInput
		}
		seen[t.ID] = true
	}
	for name, list := range m.Lists {
		if name != "tags" && name != "labels" {
			return ErrRepairInput
		}
		if _, ok := normalizeList(list.Add); !ok {
			return ErrRepairInput
		}
		if _, ok := normalizeList(list.Remove); !ok {
			return ErrRepairInput
		}
	}
	if m.Genres != nil {
		if _, ok := normalizeList(m.Genres.Add); !ok {
			return ErrRepairInput
		}
		if _, ok := normalizeList(m.Genres.Remove); !ok {
			return ErrRepairInput
		}
	}
	for _, v := range m.Fields {
		if v.Value == nil && v.Values == nil && v.Locked == nil && !v.Automatic {
			return ErrRepairInput
		}
	}
	return nil
}

// BulkEdit applies one edit to many targets. Replaying the same operationId
// returns the stored receipt rather than editing anything a second time.
func (s *Service) BulkEdit(ctx context.Context, m BulkEdit, actor MBActor, authorize func(*sql.Tx) error) (BulkReceipt, error) {
	return s.bulkEditAuthorized(ctx, m, actor, authorize)
}

func (s *Service) BulkEditWithTargetAuthorization(ctx context.Context, m BulkEdit, actor MBActor, authorize func(*sql.Tx) error, targetAuthorize BulkTargetAuthorize) (BulkReceipt, error) {
	return s.bulkEditAuthorized(ctx, m, actor, authorize, targetAuthorize)
}

func (s *Service) bulkEditAuthorized(ctx context.Context, m BulkEdit, actor MBActor, authorize func(*sql.Tx) error, targetAuthorize ...BulkTargetAuthorize) (BulkReceipt, error) {
	if actor.AccountID == "" || authorize == nil || len(targetAuthorize) > 1 {
		return BulkReceipt{}, ErrRepairInput
	}
	if err := validBulkEdit(m); err != nil {
		return BulkReceipt{}, err
	}
	scope := "metadata-bulk:" + actor.Authority + ":" + actor.AccountID + ":" + actor.ProfileID
	now := s.publicationTime().UnixMilli()
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return BulkReceipt{}, err
	}
	tx := gated.Tx()
	if err = authorize(tx); err != nil {
		gated.Rollback()
		return BulkReceipt{}, err
	}
	stored, digest, err := operations.Receipt(tx, scope, m.OperationID, m, now)
	if err == nil && stored != "" && len(targetAuthorize) == 1 {
		var prior BulkReceipt
		if json.Unmarshal([]byte(stored), &prior) != nil {
			err = ErrRepairInput
		} else {
			for _, t := range prior.Results {
				if t.OK {
					if err = targetAuthorize[0](tx, RepairTarget{Kind: t.Kind, ID: t.ID}); err != nil {
						break
					}
				}
			}
		}
	}
	gated.Rollback()
	if errors.Is(err, operations.ErrInvalid) || errors.Is(err, operations.ErrConflict) {
		return BulkReceipt{}, ErrRepairInput
	}
	if err != nil && !errors.Is(err, operations.ErrExpired) {
		return BulkReceipt{}, err
	}
	if stored != "" {
		var replay BulkReceipt
		if json.Unmarshal([]byte(stored), &replay) != nil {
			return BulkReceipt{}, ErrRepairInput
		}
		return replay, nil
	}
	out := BulkReceipt{OperationID: m.OperationID, Results: []BulkResult{}}
	for _, target := range m.Targets {
		t := RepairTarget{Kind: target.Kind, ID: target.ID}
		result := BulkResult{Kind: t.Kind, ID: t.ID}
		revision, problem := s.bulkTarget(ctx, t, target.ExpectedRevision, m, actor, authorize, targetAuthorize...)
		switch {
		case problem == nil:
			result.OK = true
			result.Revision = revision
			out.Updated++
		default:
			result.Code, result.Message = bulkFailure(problem)
			out.Failed++
		}
		out.Results = append(out.Results, result)
	}
	var gated2 *dbwork.Write
	gated2, err = dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return out, err
	}
	tx = gated2.Tx()
	defer gated2.Rollback()
	if err = authorize(tx); err != nil {
		return out, err
	}
	if err = operations.SaveReceipt(tx, scope, m.OperationID, digest, out, now); err != nil && !errors.Is(err, operations.ErrCapacity) {
		return out, err
	}
	return out, gated2.Commit()
}

func bulkFailure(err error) (string, string) {
	switch {
	case errors.Is(err, ErrRepairConflict):
		return "metadata_conflict", "Metadata changed. Reload and review your decision."
	case errors.Is(err, ErrRepairInput):
		return "invalid_metadata_repair", "This change does not apply to this item."
	case errors.Is(err, sql.ErrNoRows):
		return "not_found", "This item no longer exists."
	}
	return "edit_failed", "The change could not be applied."
}

// bulkTarget applies the whole edit to one target in one transaction. The target
// either takes every part of the edit or none of it.
func (s *Service) bulkTarget(ctx context.Context, t RepairTarget, expected string, m BulkEdit, actor MBActor, authorize func(*sql.Tx) error, targetAuthorize ...BulkTargetAuthorize) (string, error) {
	gated3, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return "", err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if err = authorize(tx); err != nil {
		return "", err
	}
	revision, err := s.applyBulkTargetAuthorizedTx(ctx, tx, t, expected, m, actor, authorize, targetAuthorize...)
	if err != nil {
		return "", err
	}
	return revision, gated3.Commit()
}

// ApplyBulkTargetTx lets the durable selector job commit an edit and its cursor
// atomically. It preserves repair validation/history without an item receipt.
func (s *Service) ApplyBulkTargetTx(ctx context.Context, tx *sql.Tx, t RepairTarget, expected string, m BulkEdit, actor MBActor, authorize func(*sql.Tx) error) (string, error) {
	return s.applyBulkTargetAuthorizedTx(ctx, tx, t, expected, m, actor, authorize)
}

func (s *Service) applyBulkTargetAuthorizedTx(ctx context.Context, tx *sql.Tx, t RepairTarget, expected string, m BulkEdit, actor MBActor, authorize func(*sql.Tx) error, targetAuthorize ...BulkTargetAuthorize) (string, error) {
	if authorize == nil {
		return "", ErrRepairInput
	}
	if err := authorize(tx); err != nil {
		return "", err
	}
	if len(targetAuthorize) > 1 {
		return "", ErrRepairInput
	}
	if len(targetAuthorize) == 1 {
		if err := targetAuthorize[0](tx, t); err != nil {
			return "", err
		}
	}
	before, ent, err := readRepairSnapshot(ctx, tx, t)
	if err != nil {
		return "", err
	}
	base, err := repairRevision(ctx, tx, t, before, ent)
	if err != nil {
		return "", err
	}
	if base != expected {
		return "", ErrRepairConflict
	}
	who := actor.Authority + ":" + actor.AccountID + ":" + actor.ProfileID
	now := s.publicationTime().Format(time.RFC3339Nano)
	lock := true
	if m.LockEdited != nil {
		lock = *m.LockEdited
	}
	specs := schemaIndex(ent.schema)
	for field, edit := range m.Fields {
		spec, known := specs[field]
		if !known || !spec.Bulk {
			return "", ErrRepairInput
		}
		current, ok := before.Fields[field]
		if !ok {
			return "", ErrRepairInput
		}
		// A bulk edit that sets a value always records the lock decision, so a
		// later refresh cannot quietly undo the whole batch.
		if edit.Locked == nil && (edit.Value != nil || edit.Values != nil) {
			locked := lock
			edit.Locked = &locked
		}
		if err = applyRepairField(ctx, tx, t, field, spec, current, edit, who, now); err != nil {
			return "", err
		}
	}
	for name, list := range m.Lists {
		spec, known := specs[name]
		if !known || !spec.Bulk || spec.Type != "list" {
			return "", ErrRepairInput
		}
		current := before.Fields[name]
		merged := mergeList(decodeList(current.Value), list)
		locked := lock
		value := encodeList(merged)
		if err = applyRepairField(ctx, tx, t, name, spec, current, RepairFieldEdit{Value: &value, Locked: &locked}, who, now); err != nil {
			return "", err
		}
	}
	if err = syncCatalogAttributes(ctx, tx, t); err != nil {
		return "", err
	}
	if m.Genres != nil {
		if t.Kind != "item" {
			return "", ErrRepairInput
		}
		if err = applyBulkGenres(ctx, tx, t, before, *m.Genres, lock, who, now); err != nil {
			return "", err
		}
	}
	if err = recordRepair(ctx, tx, t, before, base, "bulk_edit", who); err != nil {
		return "", err
	}
	after, ent, err := readRepairSnapshot(ctx, tx, t)
	if err != nil {
		return "", err
	}
	revision, err := repairRevision(ctx, tx, t, after, ent)
	if err != nil {
		return "", err
	}
	if err = authorize(tx); err != nil {
		return "", err
	}
	return revision, nil
}

// mergeList applies an add/remove list edit. Bulk never replaces a list: two
// items with different tags keep the tags they do not share.
func mergeList(current []string, edit BulkListEdit) []string {
	removed := map[string]bool{}
	for _, v := range edit.Remove {
		removed[strings.ToLower(strings.TrimSpace(v))] = true
	}
	out := []string{}
	for _, v := range current {
		if !removed[strings.ToLower(v)] {
			out = append(out, v)
		}
	}
	out = append(out, edit.Add...)
	merged, ok := normalizeList(out)
	if !ok {
		return current
	}
	return merged
}

// applyBulkGenres adds and removes genre relationships by name. Existing genres
// keep their provider identity; an added name is a local, scoped genre.
func applyBulkGenres(ctx context.Context, tx *sql.Tx, t RepairTarget, before RepairSnapshot, edit BulkListEdit, lock bool, actor, now string) error {
	removed := map[string]bool{}
	for _, v := range edit.Remove {
		removed[strings.ToLower(strings.TrimSpace(v))] = true
	}
	values := []RepairRelationship{}
	present := map[string]bool{}
	for _, v := range before.Relationships {
		if v.Kind != "genre" {
			continue
		}
		if removed[strings.ToLower(v.Label)] {
			continue
		}
		present[strings.ToLower(v.Label)] = true
		values = append(values, v)
	}
	added, ok := normalizeList(edit.Add)
	if !ok {
		return ErrRepairInput
	}
	for _, name := range added {
		if present[strings.ToLower(name)] {
			continue
		}
		present[strings.ToLower(name)] = true
		values = append(values, RepairRelationship{Kind: "genre", Label: name})
	}
	return applyRelationshipSet(ctx, tx, t, "genre", values, lock, actor, now, false)
}

func ValidateJobEdit(m BulkEdit) error {
	m.OperationID = "job-validation"
	m.Targets = []BulkTarget{{Kind: "item", ID: "validation", ExpectedRevision: strings.Repeat("0", 64)}}
	return validBulkEdit(m)
}
func (s *Service) JobEditRevision(ctx context.Context, tx *sql.Tx, item string) (string, error) {
	target, err := s.JobEditTarget(ctx, tx, item)
	if err != nil {
		return "", err
	}
	before, entity, err := readRepairSnapshot(ctx, tx, target)
	if err != nil {
		return "", err
	}
	return repairRevision(ctx, tx, target, before, entity)
}

// JobEditTarget retains the displayed entity's editor namespace. Editing a
// whole view of shows must edit those shows, not expand into their episodes.
func (s *Service) JobEditTarget(ctx context.Context, tx *sql.Tx, id string) (RepairTarget, error) {
	var kind compactcatalog.Kind
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?) AND retired=0`, id).Scan(&kind); err != nil {
		return RepairTarget{}, err
	}
	target := RepairTarget{Kind: compactcatalog.OwnerNamespace(kind), ID: id}
	switch target.Kind {
	case "item", "show", "season", "album", "artist", "book":
		return target, nil
	}
	return RepairTarget{}, ErrRepairInput
}
