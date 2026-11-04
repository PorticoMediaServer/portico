package derivedtest

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestBrowseRowsIncrementalEqualsSourceRebuild(t *testing.T) {
	c := catalogtest.Open(t)
	corpus := catalogtest.DerivedSeed(c)
	check := func(t *testing.T) {
		c.Drain()
		source, err := catalogtest.DerivedCapture(context.Background(), c.DB, "catalog_entities", "catalog_assets", "catalog_asset_links")
		if err != nil {
			t.Fatal(err)
		}
		catalogtest.DerivedCheck(t, c, []string{"catalog_browse_rows"}, func() error {
			_, err := c.DB.Exec(`DELETE FROM catalog_browse_rows`)
			return err
		}, func() error {
			return compactcatalog.NewWorker(c.DB).Rebuild(context.Background(), compactcatalog.DomainBrowseRows)
		})
		after, err := catalogtest.DerivedCapture(context.Background(), c.DB, "catalog_entities", "catalog_assets", "catalog_asset_links")
		if err != nil {
			t.Fatal(err)
		}
		if err = catalogtest.DerivedDifference(source, after); err != nil {
			t.Fatalf("rebuild changed source: %v", err)
		}
	}
	t.Run("initial", check)
	for _, stage := range corpus.Stages {
		t.Run(stage.Name, func(t *testing.T) { stage.Apply(); check(t) })
	}
}

func TestParityDetectsMissingExtraAndWrongValues(t *testing.T) {
	c := catalogtest.Open(t)
	c.Exec(`CREATE TABLE parity_values(id INTEGER PRIMARY KEY,title TEXT,value ANY) STRICT`)
	c.Exec(`INSERT INTO parity_values VALUES(1,'Title',NULL),(2,'Other',x'61')`)
	original, err := catalogtest.DerivedCapture(context.Background(), c.DB, "parity_values")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`DELETE FROM parity_values WHERE id=1`,
		`INSERT INTO parity_values VALUES(3,'Extra',NULL)`,
		`UPDATE parity_values SET title='Wrong title' WHERE id=1`,
		`UPDATE parity_values SET value='' WHERE id=1`,
		`UPDATE parity_values SET value='a' WHERE id=2`,
	} {
		c.Exec(`DELETE FROM parity_values`)
		c.Exec(`INSERT INTO parity_values VALUES(1,'Title',NULL),(2,'Other',x'61')`)
		c.Exec(query)
		got, err := catalogtest.DerivedCapture(context.Background(), c.DB, "parity_values")
		if err != nil {
			t.Fatal(err)
		}
		if catalogtest.DerivedDifference(original, got) == nil {
			t.Fatalf("parity missed %s", query)
		}
	}
}

func TestParityPreservesInvalidUTF8TextBytes(t *testing.T) {
	c := catalogtest.Open(t)
	c.Exec(`CREATE TABLE parity_text(value TEXT) STRICT`)
	c.Exec(`INSERT INTO parity_text VALUES(?)`, string([]byte{0xff}))
	a, err := catalogtest.DerivedCapture(context.Background(), c.DB, "parity_text")
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE parity_text SET value=?`, string([]byte{0xfe}))
	b, err := catalogtest.DerivedCapture(context.Background(), c.DB, "parity_text")
	if err != nil {
		t.Fatal(err)
	}
	if catalogtest.DerivedDifference(a, b) == nil {
		t.Fatal("distinct invalid UTF-8 TEXT bytes compared equal")
	}
}

func TestRebuildRepairsAnIncorrectTitle(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("films", "Films", "movie", "/films")
	film := c.Movie(lib, "/films/a.mkv", "Correct title", 2026)
	c.Drain()
	want, err := catalogtest.DerivedCapture(context.Background(), c.DB, "catalog_browse_rows")
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE catalog_browse_rows SET title='Incorrect' WHERE entity_id=?`, film.ID)
	wrong, err := catalogtest.DerivedCapture(context.Background(), c.DB, "catalog_browse_rows")
	if err != nil {
		t.Fatal(err)
	}
	if catalogtest.DerivedDifference(want, wrong) == nil {
		t.Fatal("corruption was not observed")
	}
	if err = compactcatalog.NewWorker(c.DB).Rebuild(context.Background(), compactcatalog.DomainBrowseRows); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	got, err := catalogtest.DerivedCapture(context.Background(), c.DB, "catalog_browse_rows")
	if err != nil {
		t.Fatal(err)
	}
	if err = catalogtest.DerivedDifference(want, got); err != nil {
		t.Fatal(err)
	}
}

func TestRebuildRejectsUnknownDomainBeforeWriting(t *testing.T) {
	c := catalogtest.Open(t)
	if err := compactcatalog.NewWorker(c.DB).Rebuild(context.Background(), 999); err == nil {
		t.Fatal("unknown domain accepted")
	}
	var n int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_derivations`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
