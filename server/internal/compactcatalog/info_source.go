package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// InfoSourceFamily describes normalized producer input, not a query or cache.
// Row is replayable for this immutable probe and returns ordinal i. Only the
// current raw8/256KiB batch is constructed; callers need not duplicate a probe's
// complete technical arrays into another in-memory row graph.
type InfoSourceFamily struct {
	Number int
	Count  int64
	Parent bool
	Row    func(context.Context, *sql.Tx, int64) (InfoStageRow, error)
}

// InfoSourceCheck revalidates the producer's job/object/root/policy or physical
// snapshot in every accepting writer, including the final seal. It must not do
// network or file IO while the writer is held.
type InfoSourceCheck func(context.Context, *sql.Tx) error

type InfoSourcePrepare func(context.Context, *sql.Tx) (InfoInputSpec, []InfoSourceFamily, error)

// PublishInfoSource accepts one complete immutable source frame in bounded
// transactions. Its receipt explicitly distinguishes durable accepted-pending
// from applied. Canonical rows are installed later by the sole source41 owner.
// A resumable producer error keeps the committed prefix. Terminal cancellation
// abandons only its exact phase0 body for bounded NEW reclamation; accepted
// inputs always finish and retain their truthful pending/applied receipt.
func PublishInfoSource(ctx context.Context, db *sql.DB, check InfoSourceCheck, prepare InfoSourcePrepare) (out InfoAcceptReceipt, retErr error) {
	if db == nil || check == nil || prepare == nil {
		return InfoAcceptReceipt{}, ErrInfoInvariant
	}
	var input InfoInput
	var ownerWitness InfoWitness
	var ownerFingerprint [32]byte
	// Only terminal producer loss abandons its own exact unaccepted body.
	// Ordinary capacity/Busy errors and resumable producer errors keep the prefix.
	defer func() {
		if input.Ref.ChangeID == 0 || !(errors.Is(retErr, context.Canceled) || errors.Is(retErr, context.DeadlineExceeded) || errors.Is(retErr, ErrInfoInputDrift) || errors.Is(retErr, ErrInfoInvariant) || errors.Is(retErr, sql.ErrNoRows)) {
			return
		}
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		e := dbwork.WithWriteTxContext(clean, db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia), func(c context.Context, tx *sql.Tx) error {
			_, e := abandonInfoWitnessTx(c, tx, input.Ref, ownerWitness, ownerFingerprint)
			return e
		})
		if e != nil {
			retErr = errors.Join(retErr, e)
		}
	}()
	var families []InfoSourceFamily
	write := func(fn func(context.Context, *sql.Tx) error) error {
		return dbwork.WithWriteTxContext(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia), func(ctx context.Context, tx *sql.Tx) error {
			if err := check(ctx, tx); err != nil {
				return err
			}
			return fn(ctx, tx)
		})
	}
	err := write(func(ctx context.Context, tx *sql.Tx) error {
		spec, fs, err := prepare(ctx, tx)
		if err != nil {
			return err
		}
		var seen [19]bool
		if len(fs) > 18 {
			return ErrInfoInvariant
		}
		for _, f := range fs {
			if f.Number < 1 || f.Number > 18 || seen[f.Number] || f.Count < 0 || (f.Count > 0 && f.Row == nil) {
				return ErrInfoInvariant
			}
			seen[f.Number] = true
			changed := spec.FamilyMask&(1<<uint(f.Number-1)) != 0
			if changed == f.Parent {
				return ErrInfoInvariant
			}
		}
		for family := 1; family <= 18; family++ {
			if spec.FamilyMask&(1<<uint(family-1)) != 0 && !seen[family] {
				return ErrInfoInputIncomplete
			}
		}
		if spec.FamilyMask == 0 && len(fs) == 0 {
			var pending bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_changes WHERE asset_id=? AND asset_token=?)`, spec.AssetID, spec.Token).Scan(&pending); err != nil {
				return err
			}
			if !pending {
				input.Receipt = InfoInputReceipt{State: InfoInputApplied, ExpectedEpoch: spec.ExpectedEpoch}
				return nil
			}
		}
		if e := spec.validate(); e != nil {
			return e
		}
		ownerWitness, ownerFingerprint = spec.Witnesses[0], spec.Fingerprint
		input, err = BeginInfoInputTx(ctx, tx, spec)
		families = fs
		return err
	})
	if err != nil {
		return InfoAcceptReceipt{}, err
	}
	if input.Receipt.State != InfoInputStaging {
		return InfoAcceptReceipt{InfoInputReceipt: input.Receipt}, nil
	}
	for _, family := range families {
		done := false
		for !done {
			err = write(func(ctx context.Context, tx *sql.Tx) error {
				expected, staged, sealed, _, err := infoInputManifest(ctx, tx, input.Ref.ChangeID, family.Number)
				if errors.Is(err, sql.ErrNoRows) {
					expected, staged, sealed = family.Count, 0, 0
				} else if err != nil {
					return err
				}
				if expected != family.Count {
					return ErrInfoInputDrift
				}
				if sealed != 0 {
					done = true
					return nil
				}
				if staged < family.Count {
					rows := make([]InfoStageRow, 0, 8)
					bytes := 0
					started := time.Now()
					for at := staged; at < family.Count && len(rows) < 8; at++ {
						row, err := family.Row(ctx, tx, at)
						if err != nil {
							return err
						}
						if row == nil {
							return ErrInfoInvariant
						}
						number, values := row.infoStageRow()
						if number != family.Number || len(values) == 0 || values[0] != at {
							return ErrInfoInvariant
						}
						rows = append(rows, row)
						for _, v := range values {
							switch v := v.(type) {
							case string:
								bytes += len(v)
							case []byte:
								bytes += len(v)
							default:
								bytes += 8
							}
						}
						// One legal exceptional scalar can exceed the ordinary byte quantum.
						// Yield immediately afterwards; do not batch a second wide row.
						if bytes >= 256<<10 || time.Since(started) >= 8*time.Millisecond {
							break
						}
					}
					if family.Parent {
						_, err = StageInfoParentRowsTx(ctx, tx, input.Ref, family.Number, 0, family.Count, rows...)
					} else {
						_, err = StageInfoRowsTx(ctx, tx, input.Ref, family.Number, 0, family.Count, rows...)
					}
					return err
				}
				_, err = SealInfoFamilyTx(ctx, tx, input.Ref, family.Number, 0)
				done = err == nil
				return err
			})
			if err != nil {
				return InfoAcceptReceipt{}, err
			}
		}
	}
	var receipt InfoAcceptReceipt
	err = write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		receipt, err = AcceptInfoInputTx(ctx, tx, input.Ref)
		return err
	})
	return receipt, err
}

// NextInfoFactRevisionTx compares the newest accepted/staged source fact first,
// then the canonical point row. It visits at most two source headers. A queued
// replacement must not reuse NEW1's technical revision, even before NEW1 has
// materialized. An identical retry reuses its exact staged revision.
func NextInfoFactRevisionTx(ctx context.Context, tx *sql.Tx, asset int64, token string, family int, fingerprint string, size, modified int64) (revision int64, changed bool, err error) {
	var canonical, staged string
	switch family {
	case 1:
		canonical, staged = "asset_stream_facts", "catalog_info_new_stream_facts"
	case 4:
		canonical, staged = "asset_chapter_facts", "catalog_info_new_chapter_facts"
	case 6:
		canonical, staged = "asset_subtitle_facts", "catalog_info_new_subtitle_facts"
	default:
		return 0, false, ErrInfoInvariant
	}
	type fact struct {
		revision, size, modified int64
		fingerprint              string
	}
	var old fact
	err = tx.QueryRowContext(ctx, `SELECT revision,size,modified_ns,fingerprint FROM `+canonical+` WHERE asset_id=?`, token).Scan(&old.revision, &old.size, &old.modified, &old.fingerprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	maxRevision := old.revision
	rows, err := tx.QueryContext(ctx, `SELECT n.revision,n.size,n.modified_ns,n.fingerprint FROM catalog_info_changes c JOIN `+staged+` n ON n.change_id=c.id AND n.asset_id=c.asset_token WHERE c.asset_id=? AND c.asset_token=? AND c.phase<5 ORDER BY c.slot DESC`, asset, token)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	first := true
	var newest fact
	for rows.Next() {
		var f fact
		if err = rows.Scan(&f.revision, &f.size, &f.modified, &f.fingerprint); err != nil {
			return 0, false, err
		}
		if first {
			newest = f
			first = false
		}
		if f.revision > maxRevision {
			maxRevision = f.revision
		}
	}
	if err = rows.Err(); err != nil {
		return 0, false, err
	}
	if !first && newest.fingerprint == fingerprint && newest.size == size && newest.modified == modified {
		return newest.revision, true, nil
	}
	if first && old.revision > 0 && old.fingerprint == fingerprint && old.size == size && old.modified == modified {
		return old.revision, false, nil
	}
	if maxRevision == math.MaxInt64 {
		return 0, false, fmt.Errorf("%w: technical revision exhausted", ErrInfoInvariant)
	}
	return maxRevision + 1, true, nil
}
