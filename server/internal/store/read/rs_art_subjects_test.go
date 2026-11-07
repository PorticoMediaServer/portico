package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed rs_art_subjects_test.go
var rsArtSubjectsJournal string

func rsArtSubjectsHash(seed string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(seed+rsArtSubjectsJournal+rsArtJournal)))
}

func TestRSArtTypedReadingEligibility(t *testing.T) {
	f := rsReadingSource(t)
	rsArtSelect(t, f, 1101, "cover", "matched", 40)
	defs := append(browseDefinitions(), ReadingFactsDefinitions()...)
	defs = append(defs, ArtworkDefinitions()...)
	f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, rsArtSubjectsHash(rsReadingHash(t)))
	r, err := NewRSReadingReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	in := ArtworkInput{View: f.input, Item: 1101, Role: "poster", PageSlot: true}
	in.View.Kind = 20
	for _, eligible := range []bool{true, false} {
		if !eligible {
			f.exec(t, `UPDATE catalog_entities SET has_reading_attribution_members=0 WHERE id=1101`)
		}
		fg, req, out := liveRequests("typed-reading-art")
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			fact, _, e := r.Artwork(ctx, req, time.Second, out, in)
			if eligible {
				if e != nil || fact.Owner != 1101 || fact.Digest != rsArtDigest(40) {
					t.Fatal(fact, e)
				}
			} else if !errors.Is(e, ErrDenied) || fact.Present {
				t.Fatal("ineligible Reading art escaped", fact, e)
			}
			return e
		})
		if eligible && err != nil || !eligible && !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
	}
}
func TestRSArtTypedMobiConfiguration(t *testing.T) {
	f := rsReadingSource(t)
	f.exec(t, `UPDATE reading_publications SET format='mobi' WHERE item_id=1101`)
	rsArtSelect(t, f, 1101, "cover", "matched", 43)
	defs := append(browseDefinitions(), ReadingFactsDefinitions()...)
	defs = append(defs, ArtworkDefinitions()...)
	f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, rsArtSubjectsHash(rsReadingHash(t)))
	ordinary, _ := NewRSReadingReader(f.snapshots)
	configured, _ := newRSReadingReaderWithMobi(f.snapshots)
	for _, c := range []struct {
		reader *RSReadingReader
		want   bool
	}{{ordinary, false}, {configured, true}} {
		in := ArtworkInput{View: f.input, Item: 1101, Role: "poster", PageSlot: true}
		in.View.Kind = 20
		fg, req, out := liveRequests("typed-mobi-art")
		_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			fact, _, e := c.reader.Artwork(ctx, req, time.Second, out, in)
			if c.want {
				if e != nil || !fact.Present {
					t.Fatal("configured Mobi", fact, e)
				}
			} else if !errors.Is(e, ErrDenied) || fact.Present {
				t.Fatal("unconfigured Mobi", fact, e)
			}
			return e
		})
		if c.want && err != nil || !c.want && !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
	}
}

func TestRSArtTypedAuthorAndContainer(t *testing.T) {
	f := newRSArtSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening)VALUES(13,'author',0,1,1,1,1)`)
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(1113,x'00800000000000000000000000000459',1,13)`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation)SELECT id,public_id,library_id,kind,'Author','author','a','author','a',0,public_id,public_id FROM catalog_subjects WHERE id=1113`)
	rsArtSelect(t, f, 1113, "portrait", "matched", 41)
	defs := append(HarnessDefinitions(), ArtworkDefinitions()...)
	defs = append(defs, rsDetailEligibilityDefinitions()...)
	f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, rsArtSubjectsHash(rsDetailHash(t)))
	r, _ := NewForegroundReader(f.snapshots)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=99,admission_member_count=1,admission_unrated_members=0,admission_min_known_age=8 WHERE id=1110`)
	for _, c := range []struct{ id, kind, owner int64 }{{1113, 13, 1113}, {1110, 10, 1110}} {
		fg, req, out := liveRequests("typed-subject-art")
		_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			fact, _, e := r.SubjectArtwork(ctx, req, time.Second, out, rsArtInput(f, c.id, c.kind, "poster", true))
			if e != nil || fact.Owner != c.owner {
				t.Fatal(fact, e)
			}
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestRSArtCompleteAutomaticCollection(t *testing.T) {
	f := newRSAutoSource(t)
	rsArtSelect(t, f, 1101, "poster", "matched", 42)
	defs := append(browseDefinitions(), AutomaticCollectionDefinitions()...)
	defs = append(defs, ArtworkDefinitions()...)
	f = composeReadFixture(t, f, defs, rsAutoConfig(), true, rsArtSubjectsHash(rsAutoHash(t)))
	r, _ := NewCollectionReader(f.snapshots)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`)
	for _, allowed := range []bool{true, false} {
		age := 8
		if !allowed {
			age = 18
		}
		f.exec(t, `UPDATE catalog_entities SET admission_own_age=? WHERE id IN(1,4)`, age)
		p := rsBeginCollection(t, f, r)
		rsCompleteCollection(t, f, p)
		fg, req, out := liveRequests("complete-collection-art")
		_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			fact, _, e := p.Artwork(ctx, req, time.Second, out, "poster", true)
			if allowed {
				if e != nil || fact.Owner != 1101 || fact.Digest != rsArtDigest(42) {
					t.Fatal(fact, e)
				}
			} else if !errors.Is(e, ErrDenied) || fact.Present {
				t.Fatal("hidden automatic root art escaped", fact, e)
			}
			return e
		})
		if allowed && err != nil || !allowed && !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
	}
}
func TestRSCollectionPollKeepsOriginalClock(t *testing.T) {
	f, r := newRSAutoFixture(t, nil)
	p := rsBeginCollection(t, f, r)
	f.exec(t, `UPDATE authorization_family_tokens SET expires_at=?`, f.input.Now.Add(500*time.Millisecond).Format(time.RFC3339Nano))
	p.started = time.Now().Add(-time.Second)
	fg, req, out := liveRequests("collection-expired-original-clock")
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
		step, _, e := p.Advance(ctx, req, time.Second, out)
		if !errors.Is(e, ErrDenied) || step.Ready {
			t.Fatal("poll rewound authorization clock", step, e)
		}
		return e
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}
