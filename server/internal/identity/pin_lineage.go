package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PIN attempts are counted per approving device chain (JUSTIN-ANSWERS #10,
// IDENT-04). A lineage begins where someone proved the account on a device of
// their own (password, Portico Account, setup, the reset file). Every session
// that descends from one keeps it: choosing a profile, a device approved
// through Quick Connect, a paired Cast screen and an access token. Pairing a
// new device therefore never starts a fresh PIN budget, and forgetting the
// first device does not clear what its descendants owe.

type pinLineageKey struct{}

type inheritedPINLineage struct{ id string }

// withPINLineage marks the family about to be issued as continuing lineage.
func withPINLineage(ctx context.Context, lineage string) context.Context {
	return context.WithValue(ctx, pinLineageKey{}, inheritedPINLineage{id: lineage})
}

// withPINLineageOf marks the family about to be issued as a descendant of
// parent, which keeps parent's lineage.
func withPINLineageOf(ctx context.Context, tx *sql.Tx, parent string) (context.Context, error) {
	lineage, err := familyPINLineageTx(ctx, tx, parent)
	if err != nil {
		return ctx, err
	}
	return withPINLineage(ctx, lineage), nil
}

// familyPINLineageTx is the lineage a family's PIN attempts count against. A
// family without one has no PIN budget at all, never a fresh one.
func familyPINLineageTx(ctx context.Context, tx *sql.Tx, family string) (string, error) {
	if family == "" {
		return "", ErrUnauthorized
	}
	var lineage string
	err := tx.QueryRowContext(ctx, `SELECT lineage_id FROM identity_family_pin_lineages WHERE family_id=?`, family).Scan(&lineage)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnauthorized
	}
	return lineage, err
}

// bindPINLineageTx records the new family's lineage: the one it inherits, or
// otherwise the issuing device's own, made the first time that device signs in
// to the account.
func bindPINLineageTx(ctx context.Context, tx *sql.Tx, account, device, family string) error {
	lineage := ""
	if inherited, ok := ctx.Value(pinLineageKey{}).(inheritedPINLineage); ok {
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT account_id FROM identity_pin_lineages WHERE id=?`, inherited.id).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) || err == nil && owner != account {
			return ErrUnauthorized
		}
		if err != nil {
			return err
		}
		lineage = inherited.id
	} else {
		var err error
		if lineage, err = originPINLineageTx(ctx, tx, account, device); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO identity_family_pin_lineages(family_id,lineage_id) VALUES(?,?)`, family, lineage)
	return err
}

func originPINLineageTx(ctx context.Context, tx *sql.Tx, account, device string) (string, error) {
	if device == "" {
		return "", ErrUnauthorized
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) VALUES(?,?,?,?) ON CONFLICT(account_id,origin_device_id) DO NOTHING`, "pln_"+Token(), account, device, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return "", err
	}
	var lineage string
	err := tx.QueryRowContext(ctx, `SELECT id FROM identity_pin_lineages WHERE account_id=? AND origin_device_id=?`, account, device).Scan(&lineage)
	return lineage, err
}
