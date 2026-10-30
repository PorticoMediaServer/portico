package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// OwnerCreditEntry is a normalized decision, never a replacement provider identity.
// SourceRowID names the sealed accepted tuple; EntryID names an existing scoped add.
type OwnerCreditEntry struct {
	EntryID, SourceRowID, Operation, CreditedName, Role, Department string
}
type EffectiveCredit struct {
	SourceRowID, OwnerEntryID, Provider, SourceStatus string
	Original                                          AcceptedCreditTuple
	CreditedName, Role, Department                    string
}

// ReadEffectiveCredits reads the ONE public materialization, including its private
// repair provenance. Public/person/derived readers continue to use catalog_credits.
func ReadEffectiveCredits(ctx context.Context, q CreditSourceReader, entity int64) ([]EffectiveCredit, error) {
	rows, err := q.QueryContext(ctx, `SELECT COALESCE(c.source_row_id,''),COALESCE(c.owner_entry_id,''),c.provider,c.source_status,c.credited_name,rl.label,dl.label,
 COALESCE(r.original_record_reference,''),COALESCE(r.original_credit_id,c.credit_id),COALESCE(r.original_provider_person_id,c.provider_person_id),COALESCE(r.original_person_identity_key,p.identity_key,''),COALESCE(r.original_person_token,p.token,''),
 COALESCE(r.original_target_kind,CASE WHEN c.person_id IS NULL THEN 'scoped_credit' ELSE 'person' END),COALESCE(r.original_target_id,p.token,c.credit_id),COALESCE(r.original_credited_name,c.credited_name),COALESCE(r.original_role,rl.label),COALESCE(r.original_department,dl.label),COALESCE(r.original_source_ordinal,c.source_ordinal)
 FROM catalog_credits c JOIN catalog_credit_labels rl ON rl.id=c.role_id JOIN catalog_credit_labels dl ON dl.id=c.department_id LEFT JOIN catalog_people p ON p.id=c.person_id LEFT JOIN credit_source_rows r ON r.source_row_id=c.source_row_id AND r.entity_id=c.entity_id WHERE c.entity_id=? ORDER BY c.ord`, entity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EffectiveCredit{}
	for rows.Next() {
		var c EffectiveCredit
		args := []any{&c.SourceRowID, &c.OwnerEntryID, &c.Provider, &c.SourceStatus, &c.CreditedName, &c.Role, &c.Department}
		args = append(args, creditSourceTupleDest(&c.Original)...)
		if err = rows.Scan(args...); err != nil {
			return nil, err
		}
		if len(out) >= 4096 {
			return nil, errors.New("effective credit budget exceeded")
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type capturedCredit struct {
	provider, source, status string
	position                 int
	original                 AcceptedCreditTuple
}

func creditHeads(ctx context.Context, tx *sql.Tx, entity int64) ([]capturedCredit, map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT provider FROM credit_source_heads WHERE entity_id=? ORDER BY provider COLLATE BINARY`, entity)
	if err != nil {
		return nil, nil, err
	}
	providers := []string{}
	for rows.Next() {
		var provider string
		if err = rows.Scan(&provider); err != nil {
			rows.Close()
			return nil, nil, err
		}
		providers = append(providers, provider)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	out := []capturedCredit{}
	references := map[string]string{}
	for _, provider := range providers {
		head, e := ReadCreditSourceHead(ctx, tx, entity, provider)
		if e != nil {
			return nil, nil, e
		}
		references[provider] = head.RecordReference
		for _, member := range head.Members {
			out = append(out, capturedCredit{provider, member.SourceRowID, "current", member.Position, member.Original})
		}
	}
	sortCapturedCredits(out)
	return out, references, nil
}
func capturedCreditByID(ctx context.Context, tx *sql.Tx, entity int64, source string) (capturedCredit, error) {
	var c capturedCredit
	c.source = source
	args := []any{&c.provider}
	args = append(args, creditSourceTupleDest(&c.original)...)
	err := tx.QueryRowContext(ctx, `SELECT r.provider,`+creditSourceTupleColumns+` FROM credit_source_rows r WHERE r.entity_id=? AND r.source_row_id=? AND EXISTS(SELECT 1 FROM credit_source_publication_members m JOIN credit_source_publications p ON p.publication_id=m.publication_id WHERE m.source_row_id=r.source_row_id AND p.sealed=1)`, entity, source).Scan(args...)
	return c, err
}
func ownerCreditEntries(ctx context.Context, tx *sql.Tx, entity int64) ([]OwnerCreditEntry, error) {
	rows, err := tx.QueryContext(ctx, `SELECT entry_id,COALESCE(source_row_id,''),operation,credited_name,role,department FROM credit_owner_entries WHERE entity_id=? ORDER BY position`, entity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OwnerCreditEntry{}
	for rows.Next() {
		var row OwnerCreditEntry
		if err = rows.Scan(&row.EntryID, &row.SourceRowID, &row.Operation, &row.CreditedName, &row.Role, &row.Department); err != nil {
			return nil, err
		}
		if len(out) >= 2048 {
			return nil, errors.New("owner credit budget exceeded")
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func missingCreditStatus(original capturedCredit, references map[string]string) string {
	// A sealed head is meaningful even when its successful publication is empty.
	// No member/name/role comparison can establish record identity or a rebind.
	if reference, ok := references[original.provider]; ok && reference != original.original.RecordReference {
		return "replaced"
	}
	return "missing"
}

// RebuildEffectiveCreditsTx is the only normalized projection. Owner-saved order
// precedes new untouched source evidence; a lock freezes the complete sequence.
func RebuildEffectiveCreditsTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	return creditProjectionChange(ctx, tx, func() error { return rebuildEffectiveCreditsTx(ctx, tx, entity) })
}
func rebuildEffectiveCreditsTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	current, references, err := creditHeads(ctx, tx, entity)
	if err != nil {
		return err
	}
	var locked bool
	err = tx.QueryRowContext(ctx, `SELECT locked FROM credit_owner_families WHERE entity_id=?`, entity).Scan(&locked)
	decided := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	entries, err := ownerCreditEntries(ctx, tx, entity)
	if err != nil {
		return err
	}
	live := map[string]capturedCredit{}
	for _, c := range current {
		live[c.source] = c
	}
	used := map[string]bool{}
	out := []EffectiveCredit{}
	for _, entry := range entries {
		if entry.SourceRowID == "" {
			out = append(out, EffectiveCredit{OwnerEntryID: entry.EntryID, Provider: "manual", SourceStatus: "current", Original: AcceptedCreditTuple{CreditID: entry.EntryID, TargetKind: "scoped_credit", TargetID: entry.EntryID}, CreditedName: entry.CreditedName, Role: entry.Role, Department: entry.Department})
			continue
		}
		used[entry.SourceRowID] = true
		if entry.Operation == "exclude" {
			continue
		}
		c, present := live[entry.SourceRowID]
		if !present {
			if entry.Operation == "retain" && !locked {
				continue
			}
			c, err = capturedCreditByID(ctx, tx, entity, entry.SourceRowID)
			if err != nil {
				return err
			}
			c.status = missingCreditStatus(c, references)
		}
		out = append(out, EffectiveCredit{SourceRowID: c.source, OwnerEntryID: entry.EntryID, Provider: c.provider, SourceStatus: c.status, Original: c.original, CreditedName: entry.CreditedName, Role: entry.Role, Department: entry.Department})
	}
	if !locked {
		for _, c := range current {
			if !used[c.source] {
				out = append(out, EffectiveCredit{SourceRowID: c.source, Provider: c.provider, SourceStatus: "current", Original: c.original, CreditedName: c.original.CreditedName, Role: c.original.Role, Department: c.original.Department})
			}
		}
	}
	// Legacy seams without accepted custody remain unchanged ONLY before the first
	// normalized owner decision. Owner admission must capture all original seams.
	if !decided {
		legacy, e := ReadEffectiveCredits(ctx, tx, entity)
		if e != nil {
			return e
		}
		providers := map[string]bool{}
		for _, c := range current {
			providers[c.provider] = true
		}
		rows, e := tx.QueryContext(ctx, `SELECT provider FROM credit_source_heads WHERE entity_id=?`, entity)
		if e != nil {
			return e
		}
		for rows.Next() {
			var p string
			if e = rows.Scan(&p); e != nil {
				rows.Close()
				return e
			}
			providers[p] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, c := range legacy {
			if !providers[c.Provider] {
				out = append(out, c)
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_credits WHERE entity_id=?`, entity); err != nil {
		return err
	}
	for position, c := range out {
		var person any
		// Resolve only the exact captured key AND token. No alias/name adoption and no
		// person INSERT/UPDATE is possible when projecting an owner decision.
		if c.Original.PersonIdentityKey != "" && c.Original.PersonToken != "" {
			var id int64
			e := tx.QueryRowContext(ctx, `SELECT id FROM catalog_people WHERE identity_key=? AND token=?`, c.Original.PersonIdentityKey, c.Original.PersonToken).Scan(&id)
			if e == nil {
				person = id
			} else if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		role, e := creditLabel(ctx, tx, c.Role)
		if e != nil {
			return e
		}
		department, e := creditLabel(ctx, tx, c.Department)
		if e != nil {
			return e
		}
		var source, owner any
		if c.SourceRowID != "" {
			source = c.SourceRowID
		}
		if c.OwnerEntryID != "" {
			owner = c.OwnerEntryID
		}
		providerPerson := ""
		if person != nil {
			providerPerson = c.Original.ProviderPersonID
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO catalog_credits(entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,provider_person_id,source_ordinal,source_row_id,owner_entry_id,source_status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, entity, position, person, role, department, c.CreditedName, c.Provider, c.Original.CreditID, providerPerson, c.Original.SourceOrdinal, source, owner, c.SourceStatus); e != nil {
			return e
		}
	}
	if err = touchOwnTx(ctx, tx, entity); err != nil {
		return err
	}
	if decided {
		return publishOwnerCreditDerivationsTx(ctx, tx, entity)
	}
	return nil
}

// Owner decisions and refreshes of an owner-decided family publish the current
// search/person-related/rec facet membership in the same transaction, not a
// window of stale credit membership while the ordinary dirty queue catches up.
func publishOwnerCreditDerivationsTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	ids := []int64{entity}
	rows, err := tx.QueryContext(ctx, `SELECT show_id FROM catalog_episodes WHERE entity_id=? AND show_id IS NOT NULL UNION SELECT album_id FROM catalog_songs WHERE entity_id=? AND album_id IS NOT NULL UNION SELECT book_id FROM catalog_book_files WHERE entity_id=? AND book_id IS NOT NULL`, entity, entity, entity)
	if err != nil {
		return err
	}
	for rows.Next() {
		var parent int64
		if err = rows.Scan(&parent); err != nil {
			rows.Close()
			return err
		}
		if parent > 0 && parent != entity {
			ids = append(ids, parent)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err = deriveSearch(ctx, tx, mustJSON([]int64{entity})); err != nil {
		return err
	}
	if err = deriveRelated(ctx, tx, mustJSON([]int64{entity})); err != nil {
		return err
	}
	return deriveRecFacets(ctx, tx, ids)
}

// SaveOwnerCreditsTx writes only normalized decisions and their one effective
// projection. expectedRevision is the captured family revision inside the outer
// repair document CAS transaction. Exclusions survive ordinary list saves.
func SaveOwnerCreditsTx(ctx context.Context, tx *sql.Tx, entity, expectedRevision int64, entries []OwnerCreditEntry, locked bool, actor, observed string) error {
	captured := append([]OwnerCreditEntry(nil), entries...)
	return creditProjectionChange(ctx, tx, func() error {
		return saveOwnerCreditsTx(ctx, tx, entity, expectedRevision, captured, locked, actor, observed)
	})
}
func saveOwnerCreditsTx(ctx context.Context, tx *sql.Tx, entity, expectedRevision int64, entries []OwnerCreditEntry, locked bool, actor, observed string) error {
	if entity <= 0 || expectedRevision < 0 || len(entries) > 2048 || !creditCustodyText(actor, 256, true) || !creditCustodyText(observed, 64, true) {
		return errors.New("invalid owner credits")
	}
	var actual int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM credit_owner_families WHERE entity_id=?`, entity).Scan(&actual)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if actual != expectedRevision {
		return ErrCreditSourceStale
	}
	prior, err := ownerCreditEntries(ctx, tx, entity)
	if err != nil {
		return err
	}
	knownAdds := map[string]bool{}
	for _, e := range prior {
		if e.Operation == "add" {
			knownAdds[e.EntryID] = true
		}
	}
	seen := map[string]bool{}
	for n := range entries {
		e := &entries[n]
		if !creditCustodyText(e.CreditedName, CreditNameMaxBytes, true) || !creditCustodyText(e.Role, CreditRoleMaxBytes, false) || !creditCustodyText(e.Department, CreditDepartmentMaxBytes, false) {
			return errors.New("invalid owner credit content")
		}
		if e.Operation == "add" {
			if e.SourceRowID != "" || e.EntryID != "" && !knownAdds[e.EntryID] {
				return errors.New("invalid scoped credit")
			}
			if e.EntryID == "" {
				e.EntryID, err = newCreditCustodyID()
				if err != nil {
					return err
				}
			}
		} else {
			if e.Operation != "retain" && e.Operation != "edit" && e.Operation != "exclude" {
				return errors.New("invalid credit decision")
			}
			if _, err = capturedCreditByID(ctx, tx, entity, e.SourceRowID); err != nil {
				return err
			}
			e.EntryID, err = newCreditCustodyID()
			if err != nil {
				return err
			}
		}
		key := e.SourceRowID
		if key == "" {
			key = "add:" + e.EntryID
		}
		if seen[key] {
			return errors.New("duplicate owner credit")
		}
		seen[key] = true
	}
	for _, e := range prior {
		if e.Operation == "exclude" && !seen[e.SourceRowID] {
			entries = append(entries, e)
		}
	}
	if len(entries) > 2048 {
		return errors.New("owner credit budget exceeded")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO credit_owner_families(entity_id,revision,locked,actor,observed_at) VALUES(?,1,?,?,?) ON CONFLICT(entity_id) DO UPDATE SET revision=revision+1,locked=excluded.locked,actor=excluded.actor,observed_at=excluded.observed_at WHERE revision=?`, entity, locked, actor, observed, expectedRevision)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrCreditSourceStale
	}
	// Replacing rows happens in the same outer transaction as the projection and
	// derived invalidation. Clear old projection FKs before replacing owner rows.
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_credits WHERE entity_id=?`, entity); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM credit_owner_entries WHERE entity_id=?`, entity); err != nil {
		return err
	}
	for n, e := range entries {
		var source any
		if e.SourceRowID != "" {
			source = e.SourceRowID
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO credit_owner_entries(entry_id,entity_id,position,operation,source_row_id,credited_name,role,department) VALUES(?,?,?,?,?,?,?,?)`, e.EntryID, entity, n, e.Operation, source, e.CreditedName, e.Role, e.Department); err != nil {
			return err
		}
	}
	return RebuildEffectiveCreditsTx(ctx, tx, entity)
}

// Stable ordering is already provider/current-publication-position/source ID;
// explicit sorting avoids relying on incidental map iteration in future callers.
func sortCapturedCredits(rows []capturedCredit) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.provider != b.provider {
			return a.provider < b.provider
		}
		if a.position != b.position {
			return a.position < b.position
		}
		return a.source < b.source
	})
}

// Keep failed repository calls noncommittable even if an outer caller handles
// their error. Nested calls use SQLite's nearest same-name savepoint semantics.
func creditProjectionChange(ctx context.Context, tx *sql.Tx, change func() error) (err error) {
	if _, err = tx.ExecContext(ctx, `SAVEPOINT effective_credit_change`); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			cleanup := context.WithoutCancel(ctx)
			if _, e := tx.ExecContext(cleanup, `ROLLBACK TO effective_credit_change`); e != nil {
				err = errors.Join(err, e, tx.Rollback())
				return
			}
			if _, e := tx.ExecContext(cleanup, `RELEASE effective_credit_change`); e != nil {
				err = errors.Join(err, e, tx.Rollback())
			}
		}
	}()
	if err = change(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `RELEASE effective_credit_change`)
	return err
}
