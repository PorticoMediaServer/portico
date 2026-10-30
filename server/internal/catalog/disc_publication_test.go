package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// This is a publication/transaction fixture, not evidence that the empty local
// test directory is a playable disc or that a real scan/helper has run. Its
// supplied structure is the actual committed procedural DVD helper observation.
func discPublicationFixture(t *testing.T) (*Service, *sql.DB, LibrarySource, string, string, int64, disc.Probe) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := New(db)
	library, err := s.Create("Disc publication unit", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := scanLibrarySource(db.QueryRow(`SELECT `+sourceColumns+` FROM library_sources WHERE library_id=? ORDER BY id LIMIT 1`, library.ID))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.ScanPolicy(context.Background(), library.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "VIDEO_TS")
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	observation, err := storage.LocalInventoryDirectory(storage.InventoryRequest{Root: root, RootIdentity: source.RootIdentity, RelativePath: "VIDEO_TS"})
	if err != nil {
		t.Fatal(err)
	}
	revision, evidence, err := ObservationRevision(source, observation)
	if err != nil {
		t.Fatal(err)
	}
	job, object, asset := identity.Token(), identity.Token(), identity.Token()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = compactcatalog.CreateAssetTx(context.Background(), tx, asset, compactcatalog.Asset{Path: path, Size: observation.Size, ModifiedNS: observation.ModifiedNS, Container: "disc"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running','unit');`, job, library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES(?,?,?,?,?,?)`, job, source.ID, source.Generation, source.Incarnation, source.RootIdentity, policy.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, object, source.ID, asset, source.Incarnation, "VIDEO_TS", observation.ObjectIdentity, revision, evidence, observation.Size, observation.ModifiedNS, job); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "discs", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Fixtures []struct {
			ID       string
			Expected json.RawMessage
		}
	}
	if err = json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	var probe disc.Probe
	for _, f := range inventory.Fixtures {
		if f.ID == "dvd-two-title-video-ts" {
			probe, err = disc.DecodeProbe(f.Expected)
		}
	}
	if err != nil || len(probe.Titles) != 2 {
		t.Fatalf("actual source fixture absent: %v", err)
	}
	return s, db, source, job, object, policy.Revision, probe
}

// A sealed input waits for the real finite A5 directory pass before acceptance.
// Drive that existing worker between retries, just as ingestion does; never
// forge an accepted target pin or drain a still-unaccepted producer to EOF.
func acceptDiscProbeForTest(t *testing.T, s *Service, ctx context.Context, job, object, revision string, policy int64, probe disc.Probe) (compactcatalog.InfoAcceptReceipt, error) {
	t.Helper()
	worker := compactcatalog.NewWorker(s.db)
	for i := 0; i < 1000; i++ {
		r, err := s.AcceptInventoryDiscProbe(ctx, job, object, revision, policy, probe)
		if !errors.Is(err, compactcatalog.ErrInfoInputDeferred) && !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
			return r, err
		}
		if _, err = worker.Step(ctx, 8); err != nil {
			return r, err
		}
	}
	return compactcatalog.InfoAcceptReceipt{}, compactcatalog.ErrInfoInputDeferred
}

func TestDiscStructurePublicationStableIdentitiesReorderAndCurrentRead(t *testing.T) {
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	ctx := context.Background()
	var revision string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	publish := func(p disc.Probe) compactcatalog.InfoAcceptReceipt {
		t.Helper()
		receipt, err := acceptDiscProbeForTest(t, s, ctx, job, object, revision, policy, p)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.State != compactcatalog.InfoInputAcceptedPending && receipt.State != compactcatalog.InfoInputApplied {
			t.Fatal("structure source receipt", receipt)
		}
		catalogtest.New(t, db).Drain()
		return receipt
	}
	read := func() disc.Probe {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		got, err := ReadInventoryDiscProbeTx(ctx, tx, object, revision)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	tokens := func() map[string]string {
		t.Helper()
		rows, err := db.Query(`SELECT title_key,selection_token FROM inventory_disc_titles WHERE object_id=?`, object)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var key, token string
			if err = rows.Scan(&key, &token); err != nil {
				t.Fatal(err)
			}
			out[key] = token
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	publish(probe)
	if got := read(); !reflect.DeepEqual(got, probe) {
		t.Fatalf("normalized source changed: %+v", got)
	}
	before := tokens()
	if receipt := publish(probe); receipt.State != compactcatalog.InfoInputApplied || receipt.ChangeID != 0 {
		t.Fatal("unchanged structure was not an applied no-op", receipt)
	}
	if !reflect.DeepEqual(before, tokens()) {
		t.Fatal("unchanged probe replaced title identity")
	}
	// The real sorted numbering changes while disc order/selection identity
	// remain stable. A UNIQUE ordinal must not fail halfway through a swap.
	probe.Titles[1].DurationMs = probe.Titles[0].DurationMs + 1000
	publish(probe)
	if !reflect.DeepEqual(before, tokens()) || !reflect.DeepEqual(read(), probe) {
		t.Fatal("reorder changed identity or facts")
	}
	var key string
	if err := db.QueryRow(`SELECT title_key FROM inventory_disc_titles WHERE object_id=? AND title_number=1`, object).Scan(&key); err != nil || key != probe.Titles[1].Key(probe.Kind) {
		t.Fatalf("wrong longest title: %s %v", key, err)
	}
	// A vanished title and its dependent rows retire, without renaming the
	// surviving title's private selector. No synthetic file row is inserted.
	probe.Titles = probe.Titles[:1]
	publish(probe)
	if got := tokens(); len(got) != 1 || got[probe.Titles[0].Key(probe.Kind)] != before[probe.Titles[0].Key(probe.Kind)] {
		t.Fatal("retirement lost surviving identity")
	}
}

func TestDiscStructureSourceFencesAndCorruptionStayClosed(t *testing.T) {
	s, db, source, job, object, policy, probe := discPublicationFixture(t)
	ctx := context.Background()
	var revision string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	receipt, err := acceptDiscProbeForTest(t, s, ctx, job, object, revision, policy, probe)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatalf("structure source receipt: %+v %v", receipt, err)
	}
	catalogtest.New(t, db).Drain()
	if _, err := s.AcceptInventoryDiscProbe(ctx, job, object, revision+"stale", policy, probe); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale object published: %v", err)
	}
	if _, err := s.AcceptInventoryDiscProbe(ctx, job, object, revision, policy+1, probe); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale policy published: %v", err)
	}
	// A material normalized-row mutation cannot become a plausible partial
	// projection: the complete observed fingerprint must still match.
	if _, err := db.Exec(`UPDATE inventory_disc_chapters SET start_ms=start_ms+1 WHERE object_id=? AND chapter_order=1`, object); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ReadInventoryDiscProbeTx(ctx, tx, object, revision)
	tx.Rollback()
	if !errors.Is(err, disc.ErrProtocol) {
		t.Fatalf("corrupted rows accepted: %v", err)
	}
	if _, err = db.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInventoryDiscProbe(ctx, job, object, revision, policy, probe); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale source published: %v", err)
	}
	tx, err = db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ReadInventoryDiscProbeTx(ctx, tx, object, revision)
	tx.Rollback()
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale source silently refreshed: %v", err)
	}
}
