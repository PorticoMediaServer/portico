package mediaanalysis

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func readFactsTx(ctx context.Context, tx *sql.Tx, source, object, revision, item, asset string) (ReadFacts, error) {
	f := ReadFacts{ObjectID: object, ObjectRevision: revision, ItemID: item, AssetID: asset}
	s := &f.Source
	err := tx.QueryRowContext(ctx, `SELECT src.id,src.library_id,src.kind,src.root,src.generation,src.incarnation,src.root_identity,src.enabled,src.identity_confirmed,o.size,o.modified_ns
 FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id
 WHERE o.id=? AND o.revision=? AND o.asset_id=? AND src.id=? AND src.enabled=1 AND src.identity_confirmed=1
 AND o.retired=0 AND o.state='available' AND o.root_incarnation=src.incarnation`, object, revision, asset, source).
		Scan(&s.ID, &s.LibraryID, &s.Kind, &s.ResolvedPath, &s.Generation, &s.Incarnation, &s.RootIdentity, &s.Enabled, &s.IdentityConfirmed, &f.Size, &f.ModifiedNS)
	if err != nil {
		return f, err
	}
	f.Consent, err = catalog.SourceAnalysisConsentTx(ctx, tx, source)
	if err == nil && !f.Consent.Allowed {
		err = storage.ErrRemoteAnalysisDisabled
	}
	return f, err
}

func checkReadFactsTx(ctx context.Context, tx *sql.Tx, original ReadFacts) error {
	now, err := readFactsTx(ctx, tx, original.Source.ID, original.ObjectID, original.ObjectRevision, original.ItemID, original.AssetID)
	if err != nil {
		return err
	}
	if now != original {
		return context.Canceled
	}
	return nil
}

func (s *Service) scanReadContext(ctx context.Context, b binding) (context.Context, *ReadInvocation) {
	facts := ReadFacts{Source: b.Source, Consent: b.Consent, ObjectID: b.ObjectID, ObjectRevision: b.SourceRevision, ItemID: b.ItemID, AssetID: b.AssetID, Size: b.SourceSize, ModifiedNS: b.SourceModifiedNS}
	// Use only physical fields returned by readFactsTx, not mutable progress/name
	// settings from LibrarySource. They cannot authorize another byte source.
	facts.Source = catalog.LibrarySource{ID: b.Source.ID, LibraryID: b.Source.LibraryID, Kind: b.Source.Kind, ResolvedPath: b.Source.ResolvedPath, Generation: b.Source.Generation, Incarnation: b.Source.Incarnation, RootIdentity: b.Source.RootIdentity, Enabled: b.Source.Enabled, IdentityConfirmed: b.Source.IdentityConfirmed}
	return s.readContext(ctx, facts, func(check context.Context, tx *sql.Tx) error {
		if err := checkReadFactsTx(check, tx, facts); err != nil {
			return err
		}
		source, err := catalog.InventoryFenceTx(check, tx, b.JobID)
		if err != nil {
			return err
		}
		if source.ID != b.Source.ID || source.Generation != b.Source.Generation || source.Incarnation != b.Source.Incarnation {
			return context.Canceled
		}
		policy, err := catalog.ScanPolicyTx(check, tx, source.LibraryID)
		if err != nil {
			return err
		}
		if policy.Revision != b.PolicyRevision {
			return context.Canceled
		}
		var active bool
		err = tx.QueryRowContext(check, `SELECT EXISTS(SELECT 1 FROM inventory_analysis_queue WHERE job_id=? AND object_id=? AND revision=? AND policy_revision=? AND state='pending')`, b.JobID, b.ObjectID, b.SourceRevision, b.PolicyRevision).Scan(&active)
		if err == nil && !active {
			err = context.Canceled
		}
		return err
	})
}

func (s *Service) segmentReadContext(ctx context.Context, f segmentFile, attempt int) (context.Context, *ReadInvocation) {
	return s.readContext(ctx, f.ReadFacts, func(check context.Context, tx *sql.Tx) error {
		if err := checkReadFactsTx(check, tx, f.ReadFacts); err != nil {
			return err
		}
		mode, err := effectiveSegmentModeTx(check, tx, f.ObjectID, f.LibraryID)
		if err != nil {
			return err
		}
		if mode != f.Mode || mode == SegmentModeOff {
			return context.Canceled
		}
		planning, err := segmentPlanningTx(check, tx)
		if err != nil {
			return err
		}
		if planning.digest() != f.Planning.digest() {
			return context.Canceled
		}
		var active bool
		err = tx.QueryRowContext(check, `SELECT EXISTS(SELECT 1 FROM segment_files WHERE object_id=? AND source_revision=? AND mode=? AND planning_policy=? AND attempt=? AND state='pending')`, f.ObjectID, f.Revision, f.Mode, f.Planning.digest(), attempt).Scan(&active)
		if err == nil && !active {
			err = context.Canceled
		}
		return err
	})
}
