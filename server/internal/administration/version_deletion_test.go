package administration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

func TestDeleteVersionKeepsTitleAndOtherFilesAndBindsPreview(t *testing.T) {
	s, db, _, item, paths := deletionFixture(t)
	ctx := context.Background()
	var asset string
	if err := db.QueryRow(`SELECT token FROM catalog_assets WHERE path=?`, paths[0]).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewDeleteVersion(ctx, allow, item, asset)
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalFiles != 1 || p.Targets[0].Files[0].Path != paths[0] {
		t.Fatal(p)
	}
	in := VersionDeleteRequest{Confirmation: p.Confirmation, PreviewKey: p.PreviewKey, OperationID: "delete-version"}
	bad := in
	bad.PreviewKey = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err = s.DeleteVersion(ctx, allow, item, asset, bad); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	r, err := s.DeleteVersion(ctx, allow, item, asset, in)
	if err != nil || r.Removed != 1 || r.Receipts[0].FilesDeleted != 1 {
		t.Fatal(r, err)
	}
	if _, err = os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err = os.Stat(paths[1]); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	replay, err := s.DeleteVersion(ctx, allow, item, asset, in)
	if err != nil || replay.Removed != 1 {
		t.Fatal(replay, err)
	}
	if err = db.QueryRow(`SELECT token FROM catalog_assets WHERE path=?`, paths[1]).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	p, err = s.PreviewDeleteVersion(ctx, allow, item, asset)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DeleteVersion(ctx, allow, item, asset, VersionDeleteRequest{Confirmation: p.Confirmation, PreviewKey: p.PreviewKey, OperationID: "delete-last-version"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM catalog_missing_titles m JOIN catalog_entities e ON e.id=m.entity_id WHERE e.public_id=pid_blob(?)`, item).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestDeleteVersionReconcilesDiskDeletionBeforeALaterConflict(t *testing.T) {
	s, db, _, item, paths := deletionFixture(t)
	ctx := context.Background()
	var asset string
	if err := db.QueryRow(`SELECT token FROM catalog_assets WHERE path=?`, paths[0]).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewDeleteVersion(ctx, allow, item, asset)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	auth := func(ctx context.Context, tx *sql.Tx) error {
		calls++
		if calls == 2 {
			_, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET title='Renamed' WHERE public_id=pid_blob(?)`, item)
			return err
		}
		return nil
	}
	_, err = s.DeleteVersion(ctx, auth, item, asset, VersionDeleteRequest{Confirmation: p.Confirmation, PreviewKey: p.PreviewKey, OperationID: "rename-during-delete"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want later conflict, got %v", err)
	}
	if _, err = os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("approved file was not deleted: %v", err)
	}
	var available bool
	if err = db.QueryRow(`SELECT available FROM catalog_assets WHERE token=?`, asset).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available {
		t.Fatal("physically deleted file is still available after conflict")
	}
}

func TestDeleteVersionReconcilesDiskDeletionBeforeRevocation(t *testing.T) {
	s, db, _, item, paths := deletionFixture(t)
	ctx := context.Background()
	var asset string
	if err := db.QueryRow(`SELECT token FROM catalog_assets WHERE path=?`, paths[0]).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewDeleteVersion(ctx, allow, item, asset)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	auth := func(context.Context, *sql.Tx) error {
		calls++
		if calls == 2 {
			return ErrDenied
		}
		return nil
	}
	_, err = s.DeleteVersion(ctx, auth, item, asset, VersionDeleteRequest{Confirmation: p.Confirmation, PreviewKey: p.PreviewKey, OperationID: "revoked-during-delete"})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("want denial, got %v", err)
	}
	var available bool
	var links int
	if err = db.QueryRow(`SELECT available,(SELECT count(*) FROM catalog_asset_links WHERE asset_id=catalog_assets.id) FROM catalog_assets WHERE token=?`, asset).Scan(&available, &links); err != nil {
		t.Fatal(err)
	}
	if available || links != 1 {
		t.Fatalf("availability %v, links %d: reconcile must record absence without unlink authority", available, links)
	}
}

func TestDeleteVersionCheckedReplaysBeforeRemovedSelectionProof(t *testing.T) {
	s, db, _, item, paths := deletionFixture(t)
	var asset string
	if e := db.QueryRow(`SELECT token FROM catalog_assets WHERE path=?`, paths[0]).Scan(&asset); e != nil {
		t.Fatal(e)
	}
	preview, e := s.PreviewDeleteVersion(context.Background(), allow, item, asset)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	selection := func(ctx context.Context, tx *sql.Tx) error {
		calls++
		var live bool
		e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?) AND a.token=?)`, item, asset).Scan(&live)
		if e != nil {
			return e
		}
		if !live {
			return ErrConflict
		}
		return nil
	}
	in := VersionDeleteRequest{Confirmation: preview.Confirmation, PreviewKey: preview.PreviewKey, OperationID: "file-info-replay"}
	first, e := s.DeleteVersionChecked(context.Background(), allow, selection, item, asset, in)
	if e != nil || first.Removed != 1 || calls != 1 {
		t.Fatal(first, e, calls)
	}
	replay, e := s.DeleteVersionChecked(context.Background(), allow, selection, item, asset, in)
	if e != nil || replay.Removed != 1 || calls != 1 {
		t.Fatal("receipt re-proved its own removed link", replay, e, calls)
	}
	deny := func(context.Context, *sql.Tx) error { return ErrDenied }
	if _, e = s.DeleteVersionChecked(context.Background(), deny, selection, item, asset, in); !errors.Is(e, ErrDenied) {
		t.Fatal("replay skipped live authority", e)
	}
}
