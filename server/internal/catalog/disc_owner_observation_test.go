package catalog

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

// discOwnerObservationScenario drives one asset that carries more than one
// current inventory observation, which is the shape the owner-disc chooser has
// to resolve. The fixture's own observation retires, so insertion order below is
// this test's order; its asset, source, job, revision and probe stay the real
// fixture values. Added observation IDs start with '!' so they sort below every
// base64url identity token the fixture's observation could hold.
type discOwnerObservationScenario struct {
	service  *Service
	db       *sql.DB
	origin   string
	asset    string
	job      string
	policy   int64
	probe    disc.Probe
	revision string
}

func newDiscOwnerObservationScenario(t *testing.T) *discOwnerObservationScenario {
	t.Helper()
	service, db, _, job, object, policy, probe := discPublicationFixture(t)
	o := &discOwnerObservationScenario{service: service, db: db, origin: object, job: job, policy: policy, probe: probe}
	if err := db.QueryRow(`SELECT asset_id,revision FROM inventory_objects WHERE id=?`, object).Scan(&o.asset, &o.revision); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE inventory_objects SET retired=1 WHERE id=?`, object); err != nil {
		t.Fatal(err)
	}
	return o
}

// observe adds one further current observation of the same asset. Insertion
// order is this call's order; the object ID, not that order, decides.
func (o *discOwnerObservationScenario) observe(t *testing.T, id string) {
	t.Helper()
	if _, err := o.db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job)
 SELECT ?,source_id,asset_id,root_incarnation,?,object_identity,revision,evidence_json,size,modified_ns,seen_job
 FROM inventory_objects WHERE id=?`, id, "VIDEO_TS/"+id, o.origin); err != nil {
		t.Fatal(err)
	}
}

func (o *discOwnerObservationScenario) publish(t *testing.T, id string) {
	t.Helper()
	receipt, err := acceptDiscProbeForTest(t, o.service, context.Background(), o.job, id, o.revision, o.policy, o.probe)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("structure source receipt", receipt, err)
	}
	catalogtest.New(t, o.db).Drain()
}

func (o *discOwnerObservationScenario) storedFacts(t *testing.T, id string) int {
	t.Helper()
	var count int
	if err := o.db.QueryRow(`SELECT count(*) FROM inventory_disc_facts WHERE object_id=?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (o *discOwnerObservationScenario) readableProbe(t *testing.T, id string) disc.Probe {
	t.Helper()
	tx, err := o.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := ReadInventoryDiscProbeTx(context.Background(), tx, id, o.revision)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// chosen is the premise under test: the lowest current object ID of this asset.
func (o *discOwnerObservationScenario) chosen(t *testing.T) string {
	t.Helper()
	var id string
	if err := o.db.QueryRow(`SELECT id FROM inventory_objects WHERE asset_id=? AND state='available' AND retired=0 ORDER BY id LIMIT 1`, o.asset).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// discBlock reads the owner-only block through the same authorized snapshot
// FileInfo binds for one request.
func (o *discOwnerObservationScenario) discBlock(t *testing.T) *FileInfoDisc {
	t.Helper()
	var got *FileInfoDisc
	err := dbwork.WithReadSnapshot(context.Background(), o.db, func(ctx context.Context) error {
		got = o.service.WithContext(ctx).discTitles([]TitleFile{{ID: o.asset}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDiscOwnerObservationChoosesLowestCurrentObjectID(t *testing.T) {
	t.Run("facts-free lowest ID suppresses a later facts-bearing observation", func(t *testing.T) {
		o := newDiscOwnerObservationScenario(t)
		o.observe(t, "!1-observation")
		o.observe(t, "!2-observation")
		o.publish(t, "!2-observation")
		if got := o.chosen(t); got != "!1-observation" {
			t.Fatalf("lowest current object ID is %q", got)
		}
		if got := o.storedFacts(t, "!1-observation"); got != 0 {
			t.Fatalf("lowest observation has %d stored fact rows", got)
		}
		if got := o.storedFacts(t, "!2-observation"); got != 1 {
			t.Fatalf("later observation has %d stored fact rows", got)
		}
		if got := o.readableProbe(t, "!2-observation"); !reflect.DeepEqual(got, o.probe) {
			t.Fatalf("later observation is not readable: %+v", got)
		}
		if got := o.discBlock(t); got != nil {
			t.Fatalf("later facts-bearing observation stood in for the facts-free lowest: %+v", got)
		}
	})

	t.Run("ID order decides when the facts-bearing observation was inserted first", func(t *testing.T) {
		o := newDiscOwnerObservationScenario(t)
		o.observe(t, "!2-observation")
		o.publish(t, "!2-observation")
		o.observe(t, "!1-observation")
		if got := o.chosen(t); got != "!1-observation" {
			t.Fatalf("lowest current object ID is %q", got)
		}
		if got := o.storedFacts(t, "!2-observation"); got != 1 {
			t.Fatalf("first inserted observation has %d stored fact rows", got)
		}
		// Without the ID ordering the chooser meets the earlier inserted,
		// readable observation first and its probe supplies the block.
		if got := o.readableProbe(t, "!2-observation"); !reflect.DeepEqual(got, o.probe) {
			t.Fatalf("earlier inserted observation is not readable: %+v", got)
		}
		if got := o.discBlock(t); got != nil {
			t.Fatalf("insertion order beat object ID order: %+v", got)
		}
	})

	t.Run("chosen readable observation supplies the block", func(t *testing.T) {
		o := newDiscOwnerObservationScenario(t)
		o.observe(t, "!1-observation")
		o.observe(t, "!2-observation")
		o.publish(t, "!1-observation")
		if got := o.chosen(t); got != "!1-observation" {
			t.Fatalf("lowest current object ID is %q", got)
		}
		if got := o.storedFacts(t, "!2-observation"); got != 0 {
			t.Fatalf("later observation has %d stored fact rows", got)
		}
		want := &FileInfoDisc{FileID: o.asset, Kind: FileInfoDiscKind(o.probe.Kind), Titles: make([]FileInfoDiscTitle, 0, len(o.probe.Titles))}
		for position, title := range o.probe.OrderedTitles() {
			want.Titles = append(want.Titles, FileInfoDiscTitle{Number: position + 1, DurationMs: title.DurationMs, Chapters: len(title.Chapters), Angles: title.AngleCount})
		}
		if got := o.discBlock(t); !reflect.DeepEqual(got, want) {
			t.Fatalf("chosen observation block = %+v, want %+v", got, want)
		}
	})
}
