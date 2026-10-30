package subtitles

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"testing"
	"time"
)

type blockedScanExtractor struct{ entered, release chan struct{} }

func (b *blockedScanExtractor) Probe(context.Context, string, string) (*Inventory, string, error) {
	return nil, "", ErrUnsupported
}
func (b *blockedScanExtractor) Extract(ctx context.Context, item, source string, stream int, format string) ([]byte, string, error) {
	close(b.entered)
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	body, err := CanonicalAsset([]byte(plainSubtitle), nil, "srt", 60_000_000)
	return body, "scan-evidence", err
}
func TestScanTextDiscoveryDoesNotExtractOrHoldWriteGate(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = Persist(tx, f.item.Token, 1000, 1, &Inventory{SourceEvidence: "scan-evidence", Status: "known", TimingKnown: true, Tracks: []Track{{Origin: "embedded", StreamIndex: 2, Format: "srt", Language: "en", Title: "English"}}})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	extractor := &blockedScanExtractor{make(chan struct{}), make(chan struct{})}
	f.s.Extractor = extractor
	f.s.ffmpeg = "fixture-extractor"
	done := make(chan error, 1)
	workerDone := make(chan struct{})
	supervise.Go("test.scan-text-gate", func() {
		defer close(workerDone)
		done <- f.s.ImportScanText(ctx, f.item.Public, f.item.Token, func(ctx context.Context, tx *sql.Tx) error {
			var id string
			return tx.QueryRowContext(ctx, "SELECT token FROM catalog_assets WHERE token=?", f.item.Token).Scan(&id)
		})
	})
	defer func() {
		cancel()
		select {
		case <-workerDone:
		case <-time.After(time.Second):
			t.Error("scan import did not retire")
		}
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-extractor.entered:
		t.Fatal("scan extracted a track")
	default:
	}

	gate := dbwork.WriteGate()
	before := gate.Stats()
	if before.Active {
		t.Fatal("extraction holds the write gate", before)
	}
	// Keep the extractor stalled while a completely separate writer commits.
	writeCtx, writeCancel := context.WithTimeout(ctx, time.Second)
	defer writeCancel()
	w, err := dbwork.Begin(writeCtx, f.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal("extractor blocked another writer", err)
	}
	if _, err = w.Tx().ExecContext(writeCtx, `INSERT INTO configuration(key,value) VALUES('subtitle_scan_gate_test','true') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		w.Rollback()
		t.Fatal(err)
	}
	if err = w.Commit(); err != nil {
		t.Fatal(err)
	}
	afterWrite := gate.Stats()
	if afterWrite.Active || afterWrite.Acquired != before.Acquired+1 {
		t.Fatalf("gate counters %+v -> %+v", before, afterWrite)
	}
	// A blocked external operation must not accumulate any database hold time.
	select {
	case <-time.After(50 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stalled := gate.Stats()
	if stalled.Active || stalled.HeldMilli != afterWrite.HeldMilli || stalled.MaxHeldMilli != afterWrite.MaxHeldMilli {
		t.Fatalf("extractor accumulated write hold: %+v -> %+v", afterWrite, stalled)
	}
	var count int
	if err = f.db.QueryRowContext(ctx, "SELECT count(*) FROM subtitle_resources WHERE scope='shared'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("import not published: %d %v", count, err)
	}
}
