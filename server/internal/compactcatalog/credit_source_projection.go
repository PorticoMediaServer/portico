package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// creditRecordReference uses real accepted record evidence, never a name,
// ordinal or generated person identity. Missing legacy evidence is not invented.
func creditRecordReference(ctx context.Context, tx *sql.Tx, entity int64, provider string) (string, error) {
	var reference string
	var accepted string
	e := tx.QueryRowContext(ctx, `SELECT p.provider||':'||p.provider_type||':'||p.provider_id FROM screen_metadata_work w JOIN screen_metadata_publications p ON p.id=w.accepted_publication WHERE w.target_kind='item' AND w.target_id=? AND p.provider=? AND p.decision='accepted'`, entity, provider).Scan(&accepted)
	if e == nil {
		return accepted, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	if provider == "manual" {
		err := tx.QueryRowContext(ctx, `SELECT observed_at FROM metadata_relationship_decisions WHERE kind='item' AND entity_id=? AND relationship='credit'`, entity).Scan(&reference)
		if err != nil {
			return "", err
		}
		return "accepted-owner:" + reference, nil
	}
	err := tx.QueryRowContext(ctx, `SELECT pe.provider||':'||CASE WHEN json_extract(CASE WHEN json_valid(pe.payload) THEN pe.payload ELSE '{}' END,'$.identity.type') IN('movie','episode','anime') THEN json_extract(CASE WHEN json_valid(pe.payload) THEN pe.payload ELSE '{}' END,'$.identity.type') WHEN pe.provider='anilist' THEN 'anime' WHEN e.kind=4 THEN 'episode' ELSE 'movie' END||':'||CAST(pe.provider_id AS TEXT) FROM provider_evidence pe JOIN catalog_entities e ON e.id=pe.item_id WHERE pe.item_id=? AND pe.provider=? AND pe.provider_id>0 AND e.kind IN(1,4)`, entity, provider).Scan(&reference)
	if err == nil {
		return reference, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	err = tx.QueryRowContext(ctx, `SELECT source_url FROM screen_metadata_fields WHERE target_kind='item' AND target_id=? AND provider=? AND field IN('credits','creditsOnline') AND source_url<>'' ORDER BY field LIMIT 1`, entity, provider).Scan(&reference)
	if err == nil {
		return reference, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return "", sql.ErrNoRows
}
func captureCreditProviderTx(ctx context.Context, tx *sql.Tx, entity int64, provider, record, observed string) error {
	rows, err := ReadEffectiveCredits(ctx, tx, entity)
	if err != nil {
		return err
	}
	tuples := []AcceptedCreditTuple{}
	for _, row := range rows {
		if row.Provider == provider {
			tuple := row.Original
			tuple.RecordReference = record
			tuples = append(tuples, tuple)
		}
	}
	var revision int64
	head, err := ReadCreditSourceHead(ctx, tx, entity, provider)
	if err == nil {
		revision = head.Revision
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = PublishCreditSourceTx(ctx, tx, entity, provider, record, observed, revision, tuples)
	return err
}

// SetCreditsTx publishes raw accepted evidence, then the one effective family.
// The preexisting catalogue writer remains the fresh-provider identity admission
// path; rebuilding owner overrides never invokes it or mutates canonical people.
func SetCreditsTx(ctx context.Context, tx *sql.Tx, entity int64, provider string, credits []Credit) error {
	if provider == "manual" {
		var decided bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credit_owner_families WHERE entity_id=?)`, entity).Scan(&decided); e != nil {
			return e
		}
		if decided {
			return errors.New("normalized owner credits require owner writer")
		}
		return setCreditProjectionProviderTx(ctx, tx, entity, provider, credits)
	}
	if len(credits) > CreditSourcePublicationMax {
		var decided bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credit_owner_families WHERE entity_id=?)`, entity).Scan(&decided); e != nil {
			return e
		}
		if decided {
			return errors.New("credit source publication exceeds bound")
		}
		return creditProjectionChange(ctx, tx, func() error {
			if _, e := tx.ExecContext(ctx, `DELETE FROM credit_source_heads WHERE entity_id=? AND provider=?`, entity, provider); e != nil {
				return e
			}
			return setCreditProjectionProviderTx(ctx, tx, entity, provider, credits)
		})
	}
	record, err := creditRecordReference(ctx, tx, entity, provider)
	if errors.Is(err, sql.ErrNoRows) {
		if len(credits) == 0 {
			head, e := ReadCreditSourceHead(ctx, tx, entity, provider)
			if e == nil {
				return SetCreditsFromRecordTx(ctx, tx, entity, provider, head.RecordReference, time.Now().UTC().Format(time.RFC3339Nano), credits)
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			var rows int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider=?`, entity, provider).Scan(&rows); e != nil {
				return e
			}
			if rows == 0 {
				return nil
			}
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM credit_owner_families WHERE entity_id=?`, entity).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("accepted credit record reference unavailable")
		}
		// Old catalogue/fixture seams with no accepted source record stay legacy;
		// they are not advertised as editable normalized provider references.
		return creditProjectionChange(ctx, tx, func() error {
			if _, e := tx.ExecContext(ctx, `DELETE FROM credit_source_heads WHERE entity_id=? AND provider=?`, entity, provider); e != nil {
				return e
			}
			return setCreditProjectionProviderTx(ctx, tx, entity, provider, credits)
		})
	}
	if err != nil {
		return err
	}
	return SetCreditsFromRecordTx(ctx, tx, entity, provider, record, time.Now().UTC().Format(time.RFC3339Nano), credits)
}
func SetCreditsFromRecordTx(ctx context.Context, tx *sql.Tx, entity int64, provider, record, observed string, credits []Credit) error {
	return creditProjectionChange(ctx, tx, func() error { return setCreditsFromRecordTx(ctx, tx, entity, provider, record, observed, credits) })
}
func setCreditsFromRecordTx(ctx context.Context, tx *sql.Tx, entity int64, provider, record, observed string, credits []Credit) error {
	if len(credits) > CreditSourcePublicationMax {
		return errors.New("credit source publication exceeds bound")
	}
	if err := setCreditProjectionProviderTx(ctx, tx, entity, provider, credits); err != nil {
		return err
	}
	if err := captureCreditProviderTx(ctx, tx, entity, provider, record, observed); err != nil {
		return err
	}
	return RebuildEffectiveCreditsTx(ctx, tx, entity)
}

// CaptureLegacyCreditSourcesTx is an explicit owner-write prerequisite in the
// same repair CAS transaction. Without truthful original record evidence it
// refuses the cutover rather than inventing or relabeling a provider identity.
func CaptureLegacyCreditSourcesTx(ctx context.Context, tx *sql.Tx, entity int64, observed string) error {
	return creditProjectionChange(ctx, tx, func() error { return captureLegacyCreditSourcesTx(ctx, tx, entity, observed) })
}
func captureLegacyCreditSourcesTx(ctx context.Context, tx *sql.Tx, entity int64, observed string) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT provider FROM catalog_credits WHERE entity_id=? AND source_row_id IS NULL AND owner_entry_id IS NULL ORDER BY provider COLLATE BINARY`, entity)
	if err != nil {
		return err
	}
	providers := []string{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		providers = append(providers, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, provider := range providers {
		reference, e := creditRecordReference(ctx, tx, entity, provider)
		if e != nil {
			return e
		}
		if e = captureCreditProviderTx(ctx, tx, entity, provider, reference, observed); e != nil {
			return e
		}
	}
	return RebuildEffectiveCreditsTx(ctx, tx, entity)
}

// CanCaptureCreditProviderTx is a bounded eligibility read, not a grant or write.
func CanCaptureCreditProviderTx(ctx context.Context, tx *sql.Tx, entity int64, provider string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider=?`, entity, provider).Scan(&count); err != nil {
		return false, err
	}
	if count > CreditSourcePublicationMax {
		return false, nil
	}
	reference, err := creditRecordReference(ctx, tx, entity, provider)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return creditCustodyText(reference, CreditSourceRecordReferenceMax, true), nil
}

// CaptureCreditProviderFromReferenceTx is used only after the metadata layer's
// canonical accepted local-source resolver has supplied the exact record.
func CaptureCreditProviderFromReferenceTx(ctx context.Context, tx *sql.Tx, entity int64, provider, reference, observed string) error {
	return creditProjectionChange(ctx, tx, func() error {
		if err := captureCreditProviderTx(ctx, tx, entity, provider, reference, observed); err != nil {
			return err
		}
		return RebuildEffectiveCreditsTx(ctx, tx, entity)
	})
}
