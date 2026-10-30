package catalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// keyDomains reports the domains a key actually retains, so a test asserts the
// dependency vector rather than only its mutation behaviour.
func keyDomains(key summaryKey) []int {
	out := []int{}
	for domain, entry := range key.Domains {
		if entry.Present {
			out = append(out, domain)
		}
	}
	return out
}

func sameDomains(got []int, want ...int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The cached core of a non-restricted summary reads synchronous entity facts and
// the static kind table only. No derivation domain may enter its key, so an
// unrelated rebuild or slot change must reuse the cached answer unchanged.
func TestLibrarySummaryKeyIgnoresDerivationDomainsWithoutRestrictions(t *testing.T) {
	_, s, _ := browseFixture(t)
	c := catalogtest.New(t, s.db)
	c.Drain()
	lib := Library{ID: "a", Kind: "movie"}
	v := Viewer{Profile: "p", Libraries: []string{"a"}}

	cached, scans := summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal("cold read", scans)
	}
	key := summaryKeyRead(t, s, v, lib)
	if got := keyDomains(key); len(got) != 0 {
		t.Fatal("non-restricted key retained domains", got)
	}

	// Unrelated and would-be-relevant domains are both irrelevant here: the
	// visibility clause is the literal "1", so 18/19 are not read either.
	for _, domain := range []int{33, 20, 18, 19} {
		var version, slot int
		if err := s.db.QueryRow(`SELECT version,active_slot FROM catalog_derivations WHERE domain=?`, domain).Scan(&version, &slot); err != nil {
			t.Fatalf("domain %d fixture: %v", domain, err)
		}
		c.Exec(`UPDATE catalog_derivations SET version=version+1,active_slot=1-active_slot WHERE domain=?`, domain)
		next := summaryKeyRead(t, s, v, lib)
		if next != key {
			t.Fatalf("domain %d changed a non-restricted key", domain)
		}
		reused, reusedScans := summaryRead(t, s, v, lib)
		if reusedScans != 0 {
			t.Fatalf("domain %d rebuild discarded a correct entry", domain)
		}
		summaryParity(t, reused, cached)
	}
}

// An active policy reads browse row existence (domain 18) and membership edges
// (domain 19), so those versions and slots stay in the key and a change
// recomputes. Every other domain is not an input: its version/slot change must
// keep the key and reuse the cached answer.
func TestLibrarySummaryKeyTracksOnlyRestrictedDerivationDomains(t *testing.T) {
	_, s, _ := browseFixture(t)
	c := catalogtest.New(t, s.db)
	c.Drain()
	lib := Library{ID: "a", Kind: "movie"}
	v := Viewer{Profile: "p", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{BlockUnrated: true}}

	cached, scans := summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal("cold read", scans)
	}
	key := summaryKeyRead(t, s, v, lib)
	if got := keyDomains(key); !sameDomains(got, 18, 19) {
		t.Fatal("restricted key domains", got)
	}
	if got, scans := summaryRead(t, s, v, lib); scans != 0 {
		t.Fatal("restricted read did not reuse", scans, got.Counts)
	}

	for _, domain := range []int{33, 20, 21, 25, 26, 27, 35, 41} {
		var version, slot int
		if err := s.db.QueryRow(`SELECT version,active_slot FROM catalog_derivations WHERE domain=?`, domain).Scan(&version, &slot); err != nil {
			t.Fatalf("domain %d fixture: %v", domain, err)
		}
		c.Exec(`UPDATE catalog_derivations SET version=version+1,active_slot=1-active_slot WHERE domain=?`, domain)
		if next := summaryKeyRead(t, s, v, lib); next != key {
			t.Fatalf("unrelated domain %d changed a restricted key", domain)
		}
		reused, reusedScans := summaryRead(t, s, v, lib)
		if reusedScans != 0 {
			t.Fatalf("unrelated domain %d discarded a correct entry", domain)
		}
		summaryParity(t, reused, cached)
	}

	// A pending rebuild of a domain outside the vector is no longer an input,
	// so it neither bypasses nor discards the entry. The global dirty probe is
	// deliberately unchanged; the next test pins it.
	for _, flag := range []string{"rebuilding", "cleaning", "first_build"} {
		c.Exec(`UPDATE catalog_derivations SET ` + flag + `=1 WHERE domain=33`)
		if next := summaryKeyRead(t, s, v, lib); next != key {
			t.Fatalf("unrelated %s changed a restricted key", flag)
		}
		reused, reusedScans := summaryRead(t, s, v, lib)
		if reusedScans != 0 {
			t.Fatalf("unrelated %s discarded a correct entry", flag)
		}
		summaryParity(t, reused, cached)
		c.Exec(`UPDATE catalog_derivations SET ` + flag + `=0 WHERE domain=33`)
	}

	// The two retained domains, in both of their key components, must still
	// invalidate and recompute to the same live answer.
	for _, change := range []string{
		`UPDATE catalog_derivations SET version=version+1 WHERE domain=18`,
		`UPDATE catalog_derivations SET active_slot=1-active_slot WHERE domain=18`,
		`UPDATE catalog_derivations SET version=version+1 WHERE domain=19`,
		`UPDATE catalog_derivations SET active_slot=1-active_slot WHERE domain=19`,
	} {
		prior := summaryKeyRead(t, s, v, lib)
		c.Exec(change)
		if next := summaryKeyRead(t, s, v, lib); next == prior {
			t.Fatalf("retained domain change ignored: %s", change)
		}
		got, freshScans := summaryRead(t, s, v, lib)
		if freshScans != 1 {
			t.Fatalf("retained domain change did not recompute: %s", change)
		}
		summaryParity(t, got, cached)
		summaryParity(t, got, summaryOracle(t, s, v, lib))
	}
}

// The global pending probe is deliberately unchanged. Its retained-domain
// reasons still bypass without inserting an entry, and a clean state afterwards
// reuses the entry that was cached before the unsafe state.
func TestLibrarySummaryKeyRestrictedPendingStaysGlobalForRetainedDomains(t *testing.T) {
	_, s, _ := browseFixture(t)
	c := catalogtest.New(t, s.db)
	c.Drain()
	lib := Library{ID: "a", Kind: "movie"}
	v := Viewer{Profile: "p", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"blocked"}}}

	cached, scans := summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal("cold read", scans)
	}
	key := summaryKeyRead(t, s, v, lib)
	entries := len(s.state.summaries.entries)

	for _, check := range []struct {
		setup, cleanup string
	}{
		{`UPDATE catalog_derivations SET rebuilding=1 WHERE domain=18`, `UPDATE catalog_derivations SET rebuilding=0 WHERE domain=18`},
		{`UPDATE catalog_derivations SET cleaning=1 WHERE domain=19`, `UPDATE catalog_derivations SET cleaning=0 WHERE domain=19`},
		{`UPDATE catalog_derivations SET first_build=1 WHERE domain=18`, `UPDATE catalog_derivations SET first_build=0 WHERE domain=18`},
		{`INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(19,999999,1)`, `DELETE FROM catalog_dirty WHERE domain=19 AND entity_id=999999`},
	} {
		c.Exec(check.setup)
		for i := 0; i < 2; i++ {
			live, liveScans := summaryRead(t, s, v, lib)
			if liveScans != 1 {
				t.Fatalf("pending state reused: %s", check.setup)
			}
			summaryParity(t, live, cached)
		}
		if len(s.state.summaries.entries) != entries {
			t.Fatalf("pending state inserted an entry: %s", check.setup)
		}
		c.Exec(check.cleanup)
		reused, reusedScans := summaryRead(t, s, v, lib)
		if reusedScans != 0 {
			t.Fatalf("clean state did not reuse: %s", check.setup)
		}
		summaryParity(t, reused, cached)
		if next := summaryKeyRead(t, s, v, lib); next != key {
			t.Fatalf("pending cycle changed the key: %s", check.setup)
		}
	}
}

// Raw entity changes synchronously advance the existing library revision.
// Candidate cache entries must invalidate before any domain17 drain.
func TestLibrarySummaryRawCandidateInvalidatesBeforeDomain17(t *testing.T) {
	_, s, names := browseFixture(t)
	c := catalogtest.New(t, s.db)
	c.Drain()
	lib := Library{ID: "a", Kind: "movie"}
	v := Viewer{Libraries: []string{"a"}}
	first, scans := summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal("cold", scans)
	}
	prior := summaryKeyRead(t, s, v, lib)
	// Added order is a raw candidate input. The existing point trigger must
	// advance its revision without waiting for domain17 work to drain.
	c.Exec(`UPDATE catalog_entities SET added_ms=9999999999998 WHERE id=?`, names["m1"].ID)
	ordered := summaryKeyRead(t, s, v, lib)
	if ordered.Revision <= prior.Revision {
		t.Fatal("raw order dependency not fenced", prior.Revision, ordered.Revision)
	}
	if got, scans := summaryRead(t, s, v, lib); scans != 1 {
		t.Fatal("raw order reused", scans)
	} else {
		summaryParity(t, got, summaryOracle(t, s, v, lib))
	}
	prior = ordered
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,added_ms) VALUES(900000001,randomblob(16),?,1,'New raw candidate','new raw candidate','n',9999999999999)`, prior.Handle)
		return err
	})
	next := summaryKeyRead(t, s, v, lib)
	if next.Revision <= prior.Revision {
		t.Fatal("raw candidate did not stamp input")
	}
	got, scans := summaryRead(t, s, v, lib)
	if scans != 1 || summaryCount(got, "movie") != summaryCount(first, "movie")+1 {
		t.Fatal("raw candidate reused", scans, got.Counts)
	}
	summaryParity(t, got, summaryOracle(t, s, v, lib))
	if _, scans = summaryRead(t, s, v, lib); scans != 0 {
		t.Fatal("fresh candidate did not cache", scans)
	}
	c.Exec(`UPDATE catalog_entities SET retired=1 WHERE id=900000001`)
	got, scans = summaryRead(t, s, v, lib)
	if scans != 1 || summaryCount(got, "movie") != summaryCount(first, "movie") {
		t.Fatal("raw retirement reused", scans, got.Counts)
	}
	summaryParity(t, got, summaryOracle(t, s, v, lib))
}

// A credited artist may consume a song in another library. After drain, this
// library's local revisions are unchanged, so bind the existing policy epoch.
func TestLibrarySummaryCrossLibraryMemberAfterDrainInvalidates(t *testing.T) {
	c := catalogtest.Open(t)
	s := New(c.DB)
	a := c.Library("summary-a", "A", "music", "/summary-a")
	b := c.Library("summary-b", "B", "music", "/summary-b")
	artist := c.Artist(a, "Shared credit")
	other := c.Artist(b, "Album owner")
	album := c.Album(other, "Album", 2000)
	song := c.Song(album, 1, "/summary-b/song.flac", "Track")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetSongArtistsTx(ctx, tx, song.ID, []int64{artist.ID})
	})
	c.Drain()
	lib := Library{ID: "summary-a", Kind: "music"}
	v := Viewer{Libraries: []string{"summary-a", "summary-b"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"blocked"}}}
	first, scans := summaryRead(t, s, v, lib)
	if scans != 1 || summaryCount(first, "artist") != 1 {
		t.Fatal("initial artist", scans, first.Counts)
	}
	prior := summaryKeyRead(t, s, v, lib)
	c.Attributes(song.ID, "label", "blocked")
	c.Drain()
	next := summaryKeyRead(t, s, v, lib)
	if next.Revision != prior.Revision {
		t.Fatal("fixture altered consuming-library local inputs", prior.Revision, next.Revision)
	}
	if next.PolicyEpoch <= prior.PolicyEpoch {
		t.Fatal("cross-library label did not stamp authority")
	}
	got, scans := summaryRead(t, s, v, lib)
	if scans != 1 || summaryCount(got, "artist") != 0 {
		t.Fatal("cross-library visibility reused", scans, got.Counts)
	}
	summaryParity(t, got, summaryOracle(t, s, v, lib))
	if _, scans = summaryRead(t, s, v, lib); scans != 0 {
		t.Fatal("accepted authority did not cache", scans)
	}
}
