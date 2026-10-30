package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Provider overrides are offered only when every existing raw seam can be
// captured from real accepted record evidence inside the repair transaction.
func creditOverridesAvailable(ctx context.Context, tx *sql.Tx, entity int64) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_credits WHERE entity_id=?`, entity).Scan(&count); err != nil {
		return false, err
	}
	if count > 512 {
		return false, nil
	}
	// A pre-cutover whole-list lock may hold credits from an older record
	// without ever recording their source reference. Current target identity is
	// not proof of that old record. Keep the legacy refusal until the owner
	// explicitly releases that lock and an actual accepted refresh supplies it.
	var unprovenLocked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_relationship_decisions d WHERE d.kind='item' AND d.entity_id=? AND d.relationship='credit' AND d.locked=1 AND NOT EXISTS(SELECT 1 FROM credit_owner_families f WHERE f.entity_id=d.entity_id) AND EXISTS(SELECT 1 FROM catalog_credits c WHERE c.entity_id=d.entity_id AND c.provider<>'manual' AND c.source_row_id IS NULL))`, entity).Scan(&unprovenLocked); err != nil {
		return false, err
	}
	if unprovenLocked {
		return false, nil
	}
	var ambiguousLegacy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_credits WHERE entity_id=? AND source_row_id IS NULL AND owner_entry_id IS NULL GROUP BY provider,credit_id,person_id HAVING count(*)>1)`, entity).Scan(&ambiguousLegacy); err != nil {
		return false, err
	}
	if ambiguousLegacy {
		return false, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT provider FROM catalog_credits WHERE entity_id=? AND source_row_id IS NULL AND owner_entry_id IS NULL`, entity)
	if err != nil {
		return false, err
	}
	providers := []string{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return false, err
		}
		providers = append(providers, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, provider := range providers {
		if provider == "nfo" {
			reference, _, e := legacyCreditNFOReference(ctx, tx, entity)
			if e != nil {
				return false, e
			}
			if reference == "" {
				return false, nil
			}
			continue
		}
		ok, e := compactcatalog.CanCaptureCreditProviderTx(ctx, tx, entity, provider)
		if e != nil {
			return false, e
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
func creditRelationshipKey(row RepairRelationship) string {
	return row.Provider + "\x00" + row.RecordID + "\x00" + row.TargetKind + "\x00" + row.TargetID
}
func applyEffectiveCreditSet(ctx context.Context, tx *sql.Tx, t RepairTarget, entity int64, values []RepairRelationship, locked bool, actor, now string, restore bool) error {
	if len(values) > 512 {
		return ErrRepairInput
	}
	if reference, _, e := legacyCreditNFOReference(ctx, tx, entity); e != nil {
		return e
	} else if reference != "" {
		if e = compactcatalog.CaptureCreditProviderFromReferenceTx(ctx, tx, entity, "nfo", reference, now); e != nil {
			return e
		}
	}
	if err := compactcatalog.CaptureLegacyCreditSourcesTx(ctx, tx, entity, now); err != nil {
		return err
	}
	current, err := readRepairRelationships(ctx, tx, t)
	if err != nil {
		return err
	}
	bySource := map[string]RepairRelationship{}
	byTuple := map[string][]RepairRelationship{}
	byAdd := map[string]RepairRelationship{}
	for _, row := range current {
		if row.Kind != "credit" {
			continue
		}
		if row.SourceRowID != "" {
			bySource[row.SourceRowID] = row
		}
		byTuple[creditRelationshipKey(row)] = append(byTuple[creditRelationshipKey(row)], row)
		if row.OwnerEntryID != "" && row.SourceRowID == "" {
			byAdd[row.OwnerEntryID] = row
		}
	}
	entries := []compactcatalog.OwnerCreditEntry{}
	included := map[string]bool{}
	for _, v := range values {
		if v.Kind != "credit" || !publicationTitle(v.Label, 300) || v.Label == "" || !publicationText(v.Role, 500) || !publicationTitle(v.Department, 200) {
			return ErrRepairInput
		}
		entry := compactcatalog.OwnerCreditEntry{CreditedName: v.Label, Role: v.Role, Department: v.Department}
		if v.RecordID == "" && v.SourceRowID == "" && v.OwnerEntryID == "" && len(byTuple[creditRelationshipKey(v)]) == 0 {
			entry.Operation = "add"
			entries = append(entries, entry)
			continue
		}
		if restore && v.SourceRowID == "" && v.OwnerEntryID != "" && v.Provider == "manual" && v.TargetKind == "scoped_credit" {
			if _, exists := byAdd[v.OwnerEntryID]; !exists {
				entry.Operation = "add"
				entries = append(entries, entry)
				continue
			}
		}
		var old RepairRelationship
		var found bool
		if v.SourceRowID != "" {
			old, found = bySource[v.SourceRowID]
		} else if v.OwnerEntryID != "" {
			old, found = byAdd[v.OwnerEntryID]
		} else {
			matches := byTuple[creditRelationshipKey(v)]
			if len(matches) == 1 {
				old = matches[0]
				found = true
			}
		}
		if !found && !(restore && v.SourceRowID != "") {
			return ErrRepairInput
		}
		if !restore && (!found || old.Provider != v.Provider || old.RecordID != v.RecordID || old.TargetKind != v.TargetKind || old.TargetID != v.TargetID) {
			return ErrRepairInput
		}
		source := old.SourceRowID
		if restore && source == "" {
			source = v.SourceRowID
		}
		if source == "" {
			if old.OwnerEntryID == "" {
				return ErrRepairInput
			}
			entry.Operation = "add"
			entry.EntryID = old.OwnerEntryID
			included["add:"+entry.EntryID] = true
		} else {
			var label, role, department string
			if err = tx.QueryRowContext(ctx, `SELECT original_credited_name,original_role,original_department FROM credit_source_rows WHERE entity_id=? AND source_row_id=?`, entity, source).Scan(&label, &role, &department); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrRepairInput
				}
				return err
			}
			entry.SourceRowID = source
			entry.Operation = "retain"
			if v.Label != label || v.Role != role || v.Department != department {
				entry.Operation = "edit"
			}
			included[source] = true
		}
		entries = append(entries, entry)
	}
	for _, row := range current {
		source := row.SourceRowID
		if row.Kind == "credit" && source != "" && !included[source] {
			entries = append(entries, compactcatalog.OwnerCreditEntry{SourceRowID: source, Operation: "exclude", CreditedName: row.Label, Role: row.Role, Department: row.Department})
		}
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM credit_owner_families WHERE entity_id=?`, entity).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = compactcatalog.SaveOwnerCreditsTx(ctx, tx, entity, revision, entries, locked, actor, now); err != nil {
		return err
	}
	// JSON is history/audit only; public rows were already materialized atomically.
	normalized, err := readRepairRelationships(ctx, tx, t)
	if err != nil {
		return err
	}
	credits := []RepairRelationship{}
	for _, row := range normalized {
		if row.Kind == "credit" {
			credits = append(credits, row)
		}
	}
	raw, err := json.Marshal(credits)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) VALUES(?,?,'credit',?,'manual',?,?,?) ON CONFLICT(kind,entity_id,relationship) DO UPDATE SET value_json=excluded.value_json,source=excluded.source,locked=excluded.locked,actor=excluded.actor,observed_at=excluded.observed_at`, t.Kind, entity, string(raw), locked, actor, now)
	return err
}

func normalizedCreditFamily(ctx context.Context, tx *sql.Tx, entity int64) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credit_owner_families WHERE entity_id=?)`, entity).Scan(&exists)
	return exists, err
}

// The first normalized decision can start from a legacy rendered document. Its
// undo snapshot receives only exact current-head references captured in that same
// CAS transaction; no historical/name/role search or later record rebinding.
func attachCreditHistorySources(ctx context.Context, tx *sql.Tx, entity int64, before *RepairSnapshot) error {
	normal, err := normalizedCreditFamily(ctx, tx, entity)
	if err != nil || !normal {
		return err
	}
	before.Relationships = append([]RepairRelationship(nil), before.Relationships...)
	for n := range before.Relationships {
		row := &before.Relationships[n]
		if row.Kind != "credit" || row.SourceRowID != "" || row.OwnerEntryID != "" {
			continue
		}
		rows, e := tx.QueryContext(ctx, `SELECT r.source_row_id FROM credit_source_heads h JOIN credit_source_publication_members m ON m.publication_id=h.publication_id JOIN credit_source_rows r ON r.source_row_id=m.source_row_id WHERE h.entity_id=? AND h.provider=? AND r.original_credit_id=? AND r.original_target_kind=? AND r.original_target_id=? ORDER BY m.position LIMIT 2`, entity, row.Provider, row.RecordID, row.TargetKind, row.TargetID)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(ids) != 1 {
			return ErrRepairInput
		}
		row.SourceRowID = ids[0]
		row.SourceStatus = "current"
	}
	return nil
}

// Include the exact accepted source publications in the existing repair CAS,
// even if two records happen to render identical credit/person presentation.
func creditRepairFence(ctx context.Context, tx *sql.Tx, entity int64) ([]any, error) {
	out := []any{}
	rows, err := tx.QueryContext(ctx, `SELECT provider,publication_id,revision FROM credit_source_heads WHERE entity_id=? ORDER BY provider COLLATE BINARY LIMIT 65`, entity)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var provider, publication string
		var revision int64
		if err = rows.Scan(&provider, &publication, &revision); err != nil {
			rows.Close()
			return nil, err
		}
		if len(out) >= 64 {
			rows.Close()
			return nil, ErrRepairInput
		}
		out = append(out, []any{provider, publication, revision})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var accepted string
	var selection int64
	err = tx.QueryRowContext(ctx, `SELECT accepted_publication,selection_revision FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, entity).Scan(&accepted, &selection)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out = append(out, []any{accepted, selection})
	rows, err = tx.QueryContext(ctx, `SELECT provider,provider_id,observed_at FROM provider_evidence WHERE item_id=? ORDER BY provider COLLATE BINARY LIMIT 65`, entity)
	if err != nil {
		return nil, err
	}
	count := 0
	for rows.Next() {
		var provider, observed string
		var id int64
		if err = rows.Scan(&provider, &id, &observed); err != nil {
			rows.Close()
			return nil, err
		}
		if count >= 64 {
			rows.Close()
			return nil, ErrRepairInput
		}
		count++
		out = append(out, []any{provider, id, observed})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM credit_owner_families WHERE entity_id=?`, entity).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out = append(out, revision)
	if reference, fence, e := legacyCreditNFOReference(ctx, tx, entity); e != nil {
		return nil, e
	} else if reference != "" {
		out = append(out, []any{reference, fence})
	}
	return out, nil
}

// Only the canonical local-source reader decides whether an NFO observation is
// current, locally allowed and coordinate-valid. Equal display names never do.
func legacyCreditNFOReference(ctx context.Context, tx *sql.Tx, entity int64) (string, string, error) {
	var needed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_credits WHERE entity_id=? AND provider='nfo' AND source_row_id IS NULL AND owner_entry_id IS NULL)`, entity).Scan(&needed); err != nil {
		return "", "", err
	}
	if !needed {
		return "", "", nil
	}
	var public, accepted string
	if err := tx.QueryRowContext(ctx, `SELECT pid(public_id) FROM catalog_entities WHERE id=?`, entity).Scan(&public); err != nil {
		return "", "", err
	}
	b, err := readScreenBase(ctx, tx, "item", public)
	if err != nil {
		return "", "", err
	}
	if !b.LocalAllowed {
		return "", "", nil
	}
	err = tx.QueryRowContext(ctx, `SELECT value FROM screen_metadata_fields WHERE target_kind='item' AND target_id=? AND field='credits' AND source_kind='nfo'`, entity).Scan(&accepted)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	reference := ""
	for _, n := range b.NFO {
		candidate := "nfo:" + n.Locator
		if n.Locator != "" && len(candidate) <= compactcatalog.CreditSourceRecordReferenceMax && screenRaw(n.Credits) == accepted && (reference == "" || candidate < reference) {
			reference = candidate
		}
	}
	if reference == "" {
		return "", "", nil
	}
	return reference, screenFence(b), nil
}
