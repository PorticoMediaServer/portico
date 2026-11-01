package enrichment

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func (f *releaseFixture) switchTo(t *testing.T, on bool) {
	t.Helper()
	f.c.Exec(`INSERT INTO server_settings(singleton,updated_ms,"recommendations.titleDataset") VALUES(1,0,?) ON CONFLICT(singleton) DO UPDATE SET "recommendations.titleDataset"=excluded."recommendations.titleDataset"`, on)
}

func (f *releaseFixture) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := f.c.DB.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func (f *releaseFixture) totalRequests() int {
	n := 0
	for _, v := range f.p.requests {
		n += v
	}
	return n
}

func TestSwitchedOffTheServerNeverAsks(t *testing.T) {
	f := newReleaseFixture(t)
	f.switchTo(t, false)
	for range 3 {
		if err := f.step(t); err != nil {
			t.Fatal(err)
		}
		f.now = f.now.Add(DefaultInterval + JitterWindow)
	}
	if f.totalRequests() != 0 || f.installed(t) != "" {
		t.Fatal("a switched-off server made requests", f.p.requests)
	}
	// Works that gain provider ids while off aren't left queued.
	if n := f.count(t, `SELECT count(*) FROM enrichment_jobs`); n != 0 {
		t.Fatal("jobs queued while off", n)
	}
}

func TestSwitchingOffRemovesEverythingTheDatasetBrought(t *testing.T) {
	f := newReleaseFixture(t)
	// More works than one purge batch.
	films := f.c.Library("m2", "More", "movie", "/m2")
	thief := f.c.Movie(films, "/m2/thief.mkv", "Thief", 1981)
	collateral := f.c.Movie(films, "/m2/collateral.mkv", "Collateral", 2004)
	f.c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie','11524'),(?,'tmdb','movie','1538')`, thief.ID, collateral.ID)
	f.c.Drain()
	if err := f.step(t); err != nil || f.installed(t) != "1" {
		t.Fatal(err)
	}
	f.c.Drain()
	if f.count(t, `SELECT count(*) FROM catalog_dataset_works`) != 3 || f.count(t, `SELECT count(*) FROM catalog_rec_facets WHERE facet LIKE 'x:%'`) == 0 {
		t.Fatal("the fixture didn't import")
	}
	var seed, sequence int64
	f.c.DB.QueryRow(`SELECT seed,sequence FROM enrichment_state`).Scan(&seed, &sequence)

	f.switchTo(t, false)
	before := f.totalRequests()
	batches := 0
	for more := true; more; batches++ {
		var err error
		if more, err = f.s.Step(context.Background(), 2); err != nil {
			t.Fatal(err)
		}
		if batches > 5 {
			t.Fatal("the purge doesn't finish")
		}
	}
	if batches < 2 {
		t.Fatal("the purge wasn't batched", batches)
	}
	f.c.Drain()
	if f.totalRequests() != before {
		t.Fatal("switching off made a request")
	}
	for _, q := range []string{
		`SELECT count(*) FROM catalog_dataset_works`,
		`SELECT count(*) FROM catalog_dataset_tags`,
		`SELECT count(*) FROM catalog_dataset_vocabulary`,
		`SELECT count(*) FROM catalog_similar WHERE source='portico-dataset'`,
		`SELECT count(*) FROM enrichment_domains`,
		`SELECT count(*) FROM enrichment_jobs`,
		`SELECT count(*) FROM catalog_rec_facets WHERE facet LIKE 'x:%'`,
	} {
		if n := f.count(t, q); n != 0 {
			t.Fatal("left behind:", q, n)
		}
	}
	left, _ := filepath.Glob(filepath.Join(f.dir, "*"))
	if len(left) != 0 {
		t.Fatal("files left behind", left)
	}
	// The install's seed and rollback guard stay.
	var seedAfter, sequenceAfter int64
	f.c.DB.QueryRow(`SELECT seed,sequence FROM enrichment_state`).Scan(&seedAfter, &sequenceAfter)
	if seedAfter != seed || sequenceAfter != sequence || sequence != 1 {
		t.Fatal("install state changed", seed, seedAfter, sequence, sequenceAfter)
	}

	// Back on: a first start, at once, with no waiting for the week.
	f.switchTo(t, true)
	f.now = f.now.Add(time.Minute)
	if err := f.step(t); err != nil || f.installed(t) != "1" {
		t.Fatal("switching back on didn't download at once", err)
	}
	if n := f.count(t, `SELECT count(*) FROM catalog_dataset_works`); n != 3 {
		t.Fatal("switching back on didn't re-import", n)
	}
}

func TestANewServerHasTheSwitchOn(t *testing.T) {
	f := newReleaseFixture(t)
	if n := f.count(t, `SELECT count(*) FROM server_settings`); n != 0 {
		t.Skip("fixture saved settings")
	}
	on, err := f.s.switchedOn(context.Background())
	if err != nil || !on {
		t.Fatal("a server that never saved the switch has it off", on, err)
	}
	f.c.Exec(`INSERT INTO server_settings(singleton,updated_ms) VALUES(1,0)`)
	if on, err = f.s.switchedOn(context.Background()); err != nil || !on {
		t.Fatal("an unset column reads as off", on, err)
	}
}
