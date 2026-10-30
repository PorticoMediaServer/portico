package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func relationshipLocks(ctx context.Context, tx *sql.Tx, t RepairTarget) (map[string]bool, error) {
	out := map[string]bool{}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT relationship,locked FROM metadata_relationship_decisions WHERE kind=? AND entity_id=?`, t.Kind, entity)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var locked bool
		if err = rows.Scan(&key, &locked); err != nil {
			return out, err
		}
		out[key] = locked
	}
	return out, rows.Err()
}
func setRelationshipLock(ctx context.Context, tx *sql.Tx, t RepairTarget, class string, locked bool, actor, now string) error {
	if class != "credit" && class != "genre" && class != "identity" {
		return ErrRepairInput
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	values, err := readRepairRelationships(ctx, tx, t)
	if err != nil {
		return err
	}
	kept := []RepairRelationship{}
	for _, v := range values {
		if v.Kind == class {
			kept = append(kept, v)
		}
	}
	if class == "credit" && t.Kind == "item" {
		available, e := creditOverridesAvailable(ctx, tx, entity)
		if e != nil {
			return e
		}
		normal, e := normalizedCreditFamily(ctx, tx, entity)
		if e != nil {
			return e
		}
		if available || normal {
			return applyEffectiveCreditSet(ctx, tx, t, entity, kept, locked, actor, now, false)
		}
	}
	raw, _ := json.Marshal(kept)
	_, err = tx.ExecContext(ctx, `INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(kind,entity_id,relationship) DO UPDATE SET locked=excluded.locked,actor=excluded.actor,observed_at=excluded.observed_at`, t.Kind, entity, class, string(raw), "owner_lock", locked, actor, now)
	if err != nil {
		return err
	}
	if class == "identity" {
		if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET generation=generation+1,revision=revision+1,lease='',lease_until='' WHERE target_kind=? AND target_id=?`, t.Kind, entity); err != nil {
			return err
		}
		if t.Kind == "item" {
			if _, err = tx.ExecContext(ctx, `UPDATE metadata_movie_selections SET locked=? WHERE item_id=? AND locked<>?`, locked, entity, locked); err != nil {
				return err
			}
		}

		if t.Kind == "item" {
			if _, err = tx.ExecContext(ctx, `UPDATE metadata_publication_heads SET identity_revision=identity_revision+1 WHERE item_id=?`, entity); err != nil {
				return err
			}
		}
		if t.Kind == "album" || t.Kind == "item" {
			kind := t.Kind
			if kind == "item" {
				kind = "song"
			}
			_, err = tx.ExecContext(ctx, `UPDATE mb_jobs SET manual=?,revision=revision+1,generation=generation+1,lease='',lease_until='' WHERE kind=? AND entity_id=?`, locked, kind, entity)
		}
		if err != nil {
			return err
		}
		if t.Kind == "show" {
			// JobRevision participates in TVDB's publication fence. Invalidate
			// in-flight search without discarding accepted episode evidence.
			_, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET revision=revision+1,lease='',lease_until='' WHERE show_id=?`, entity)
		}
	}
	return err
}

func repairIdentityLocked(ctx context.Context, tx *sql.Tx, t RepairTarget) (bool, error) {
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return false, err
	}
	var locked bool
	err = tx.QueryRowContext(ctx, `SELECT locked FROM metadata_relationship_decisions WHERE kind=? AND entity_id=? AND relationship='identity'`, t.Kind, entity).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return locked, err
}

// Owner edits preserve provider/person identity on existing credits. Newly named
// credits are local and scoped; a name never grants a provider identity.
// Provider families stay as published: only the owner's manual family is
// replaced, through the catalogue write API.
func applyRelationshipSet(ctx context.Context, tx *sql.Tx, t RepairTarget, class string, values []RepairRelationship, locked bool, actor, now string, restore bool) error {
	return applyRelationshipSetMode(ctx, tx, t, class, values, locked, actor, now, restore, false)
}
func applyRelationshipSetMode(ctx context.Context, tx *sql.Tx, t RepairTarget, class string, values []RepairRelationship, locked bool, actor, now string, restore, effective bool) error {
	if t.Kind != "item" || (class != "credit" && class != "genre") || len(values) > 512 || class == "genre" && len(values) > 64 {
		return ErrRepairInput
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	current, err := readRepairRelationships(ctx, tx, t)
	if err != nil {
		return err
	}
	if class == "credit" {
		// Old clients retain the manual-family contract. A dropped/unreadable row
		// is never silently promoted into a provider exclusion by the new server.
		if !restore && !effective && !providerCreditsUnchanged(current, values) {
			return ErrRepairInput
		}
		available, e := creditOverridesAvailable(ctx, tx, entity)
		if e != nil {
			return e
		}
		if available || restore {
			return applyEffectiveCreditSet(ctx, tx, t, entity, values, locked, actor, now, restore)
		}
		if effective {
			return ErrRepairInput
		}
	}
	// This writer replaces only the manual family. A provider edit must not
	// be acknowledged when the published provider row would remain unchanged.
	if !restore && class == "credit" && !providerCreditsUnchanged(current, values) {
		return ErrRepairInput
	}
	known := map[string]RepairRelationship{}
	for _, v := range current {
		known[v.Provider+":"+v.RecordID] = v
	}
	seen := map[string]bool{}
	for n := range values {
		v := &values[n]
		if v.Kind != class || strings.TrimSpace(v.Label) == "" || !publicationTitle(v.Label, 300) || !publicationText(v.Role, 500) || !publicationTitle(v.Department, 200) {
			return ErrRepairInput
		}
		if !restore {
			if v.RecordID == "" {
				v.RecordID = "local:" + identity.Token()
				v.Provider = "manual"
				v.TargetKind = map[string]string{"credit": "scoped_credit", "genre": "genre"}[class]
				v.TargetID = v.RecordID
			} else {
				old, ok := known[v.Provider+":"+v.RecordID]
				if !ok || old.Kind != class || old.TargetID != v.TargetID || old.TargetKind != v.TargetKind {
					return ErrRepairInput
				}
			}
		}
		if v.Provider == "" || v.RecordID == "" || len(v.RecordID) > 256 || len(v.TargetID) > 256 {
			return ErrRepairInput
		}
		key := v.Provider + ":" + v.RecordID
		if class == "credit" {
			key += ":" + v.Role
		} else {
			v.Role = ""
			v.Department = ""
		}
		if seen[key] {
			return ErrRepairInput
		}
		seen[key] = true
		v.Ordinal = n
		v.Locked = locked
		v.Source = "manual"
		v.Observed = now
	}
	if class == "credit" {
		if err = writeOwnerCredits(ctx, tx, entity, values); err != nil {
			return err
		}
	} else {
		terms := []compactcatalog.Term{}
		for _, v := range values {
			if v.Provider != "manual" {
				continue
			}
			terms = append(terms, compactcatalog.Term{SourceID: v.RecordID, Name: v.Label})
		}
		if err = compactcatalog.SetTermsTx(ctx, tx, entity, compactcatalog.VocabGenre, "manual", terms); err != nil {
			return err
		}
	}
	// The item with its replaced credit or genre family. The write API queues
	// the derived work with the write.
	raw, _ := json.Marshal(values)
	_, err = tx.ExecContext(ctx, `INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(kind,entity_id,relationship) DO UPDATE SET value_json=excluded.value_json,source=excluded.source,locked=excluded.locked,actor=excluded.actor,observed_at=excluded.observed_at`, t.Kind, entity, class, string(raw), "manual", locked, actor, now)
	return err
}

// Provider identity and published content are immutable on the manual-family
// command. Source/lock/observed are decision metadata, not provider identity.
// A new row without a record ID is deliberately excluded: it becomes a scoped
// manual credit below, regardless of any supplied provider hint.
func providerCreditsUnchanged(current, values []RepairRelationship) bool {
	type credit struct {
		provider, record, targetKind, target, label, role, department string
		ordinal                                                       int
	}
	collect := func(rows []RepairRelationship) []credit {
		out := []credit{}
		for _, row := range rows {
			if row.Kind == "credit" && row.Provider != "manual" && row.RecordID != "" {
				out = append(out, credit{row.Provider, row.RecordID, row.TargetKind, row.TargetID, row.Label, row.Role, row.Department, row.Ordinal})
			}
		}
		return out
	}
	before, after := collect(current), collect(values)
	for _, row := range current {
		// Legacy provider rows without a stable record tuple cannot be
		// round-tripped as a new manual row without duplicating their credit.
		if row.Kind == "credit" && row.Provider != "manual" && row.RecordID == "" {
			return false
		}
	}
	if len(before) != len(after) {
		return false
	}
	for n := range before {
		if before[n] != after[n] {
			return false
		}
	}
	return true
}

// writeOwnerCredits replaces one item's manual credit family. Provider
// families are left alone; a person-linked manual credit reuses the person
// its token names, a name-only credit stays scoped and unlinked.
func writeOwnerCredits(ctx context.Context, tx *sql.Tx, entity int64, values []RepairRelationship) error {
	credits := []compactcatalog.Credit{}
	for _, v := range values {
		if v.Provider != "manual" {
			continue
		}
		c := compactcatalog.Credit{
			CreditID:     v.RecordID,
			CreditedName: v.Label,
			PersonName:   v.Label,
			Role:         v.Role,
			Department:   v.Department,
			Ordinal:      len(credits),
		}
		if v.TargetKind == "person" {
			var key, name string
			if err := tx.QueryRowContext(ctx, `SELECT identity_key,name FROM catalog_people WHERE token=?`, v.TargetID).Scan(&key, &name); err != nil {
				return ErrRepairInput
			}
			c.PersonKey = key
			c.PersonName = name
			c.ProviderPersonID = v.TargetID
		}
		credits = append(credits, c)
	}
	return compactcatalog.SetCreditsTx(ctx, tx, entity, "manual", credits)
}
func relationshipClassLocked(ctx context.Context, tx *sql.Tx, item string, class string) (bool, error) {
	entity, err := resolveEntity(ctx, tx, item)
	if err != nil {
		return false, err
	}
	var locked bool
	err = tx.QueryRowContext(ctx, `SELECT locked FROM metadata_relationship_decisions WHERE kind='item' AND entity_id=? AND relationship=?`, entity, class).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return locked, err
}
