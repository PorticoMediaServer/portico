package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// These fixtures are owner-local offline reads of the actual compiled native
// recipe. They are not producer acceptance, archive qualification or a bank /
// native-format certification.
const rsReadingNativeTitle = "Native Reading Accepted"

func TestRSNativeReadingDomainSelection(t *testing.T) {
	for _, kind := range []int64{20, 21} {
		binding, err := bindNativeSearchPolicy(kind, 1101)
		if err != nil || binding.recipe != rsReadingNativePolicy || binding.subject != 1101 || !binding.nativeSearch || binding.state != nil {
			t.Fatal(kind, binding, err)
		}
	}
	readPolicies.Lock()
	converter := readPolicies.recipes[rsReadingMobiPolicy]
	readPolicies.Unlock()
	if converter.nativeSearch {
		t.Fatal("native hit acquired converter permission")
	}
}

type rsReadingNativeOptions struct {
	kind         int64
	format       string
	ownAge       int64
	attribution  int64
	maximumAge   int64 // -1 means no profile restriction
	allowUnrated int64
	// extraTitle seeds a second Reading item that matches the same query. It is
	// the hidden neighbour in the skip tests; empty means a single-item fixture.
	extraTitle  string
	extraAge    int64
	extraFormat string
}

func rsReadingNativePositiveOptions(kind int64) rsReadingNativeOptions {
	format := "epub"
	if kind == 21 {
		format = "cbz"
	}
	return rsReadingNativeOptions{kind: kind, format: format, attribution: 1, maximumAge: -1, allowUnrated: 1}
}

func rsReadingNativeDefinitions(withRS bool) []stmt.Definition {
	defs := append([]stmt.Definition(nil), NativeFullSearchDefinitions()...)
	if withRS {
		defs = append(defs, RSNativeSearchDefinitions()...)
	}
	return defs
}

func rsReadingNativeFixtureHash(t *testing.T, o rsReadingNativeOptions) string {
	t.Helper()
	data, err := os.ReadFile("rs_native_reading_test.go")
	if err != nil {
		t.Fatal(err)
	}
	journal := append([]byte(nativeFixtureHash(t)), data...)
	journal = append(journal, []byte(fmt.Sprintf("|%+v", o))...)
	return fmt.Sprintf("%x", sha256.Sum256(journal))
}

func rsReadingNativeSource(t *testing.T, o rsReadingNativeOptions) *browseFixture {
	t.Helper()
	f := newNativeSource(t)
	library := "book"
	if o.kind == 21 {
		library = "comic"
	}
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'ebook',0,0,1,1,0),(21,'comic',0,0,1,1,0)`)
	f.exec(t, `UPDATE libraries SET kind=? WHERE id='movies'`, library)
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(1,'reading-asset','/media/reading',1000,1,'reading','','',0,0,0,1)`)
	public := []byte{0xf0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(1101,?,1,?)`, public, o.kind)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age,has_reading_attribution_members,reading_numbered_order,reading_number_value,reading_author_value,reading_author_key,reading_author_source)VALUES(1101,?,1,?,?,?,?,?,?,0,?,?,0,?,?,1,0,'Accepted author','accepted author','owner')`,
		public, o.kind, rsReadingNativeTitle, "native reading accepted", "native reading accepted", "native reading accepted", "native reading accepted", public, public, o.ownAge, o.attribution)
	if o.maximumAge >= 0 {
		f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',?,?)`, o.maximumAge, o.allowUnrated)
	}
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id)VALUES(1101,1,1)`)
	f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format)VALUES(1101,1,?)`, o.format)
	if o.extraTitle != "" {
		extraPublic := []byte{0xf0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(2,'reading-asset-hidden','/media/reading-hidden',1000,1,'reading','','',0,0,0,1)`)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(1102,?,1,?)`, extraPublic, o.kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age,has_reading_attribution_members,reading_numbered_order,reading_number_value,reading_author_value,reading_author_key,reading_author_source)VALUES(1102,?,1,?,?,?,?,?,?,0,?,?,0,?,1,1,0,'Hidden author','hidden author','owner')`,
			extraPublic, o.kind, o.extraTitle, "native reading hidden", "native reading hidden", "native reading hidden", "native reading hidden", extraPublic, extraPublic, o.extraAge)
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id)VALUES(1102,2,1)`)
		f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format)VALUES(1102,2,?)`, o.extraFormat)
	}
	// Publish only after every source mutation has been seeded.
	nativeFixtureImage(t, f, search.ManifestEntity, 1101, 2101, []nativeFixtureField{{1, rsReadingNativeTitle}})
	if o.extraTitle != "" {
		nativeFixtureImage(t, f, search.ManifestEntity, 1102, 2102, []nativeFixtureField{{1, o.extraTitle}})
	}
	return f
}

func rsReadingNativeFixture(t *testing.T, o rsReadingNativeOptions, withRS bool) (*browseFixture, *NativeFullSearchReader) {
	t.Helper()
	f := composeReadFixture(t, rsReadingNativeSource(t, o), rsReadingNativeDefinitions(withRS), readfixture.LiveConfig(time.Now), false, rsReadingNativeFixtureHash(t, o))
	r, err := NewNativeFullSearchReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	f.input.Kind = 0
	f.input.Limit = 20
	return f, r
}

// Each call is one fresh foreground admission and exactly one native Read.
func rsReadingNativeRead(t *testing.T, f *browseFixture, r *NativeFullSearchReader, after *FullSearchCursor, retained []*FullSearchReadStamp) (FullSearchPage, error) {
	t.Helper()
	query, err := search.PlanQuery("native reading", true)
	if err != nil {
		t.Fatal(err)
	}
	live, request, output := liveRequests("rs-native-reading")
	request.Budget.RawRows = 4096
	var page FullSearchPage
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, _, e = r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input, After: after, Retained: retained}, query)
		return e
	})
	return page, err
}

func rsReadingNativeComplete(t *testing.T, f *browseFixture, r *NativeFullSearchReader) FullSearchPage {
	t.Helper()
	var after *FullSearchCursor
	for poll := 0; poll < 100; poll++ {
		page, err := rsReadingNativeRead(t, f, r, after, nil)
		if err != nil {
			t.Fatalf("poll %d: %v", poll, err)
		}
		if page.Disposition == SearchPending {
			if len(page.Hits) != 0 || page.Next == nil || page.Exhausted {
				t.Fatalf("partial match escaped: %+v", page)
			}
			after = page.Next
			continue
		}
		if page.Disposition != SearchResults {
			t.Fatalf("poll %d: unexpected disposition %+v", poll, page)
		}
		return page
	}
	t.Fatal("bounded continuation did not finish")
	return FullSearchPage{}
}

// rsReadingNativePageShape is the viewer-visible results page plus its
// continuation shape. Custody, stamps and internal candidate work are
// deliberately excluded: a hidden match must not change any of them, but those
// values also carry fixture-local identities.
type rsReadingNativePageShape struct {
	disposition        SearchDisposition
	exhausted, partial bool
	hasNext            bool
	hits               []rsReadingNativeHitShape
}

type rsReadingNativeHitShape struct {
	subject        search.ManifestSubject
	id, library    int64
	kind           int64
	tier           uint8
	title, sortKey string
}

func rsReadingNativeShape(page FullSearchPage) rsReadingNativePageShape {
	shape := rsReadingNativePageShape{disposition: page.Disposition, exhausted: page.Exhausted, partial: page.Partial, hasNext: page.Next != nil}
	for _, hit := range page.Hits {
		shape.hits = append(shape.hits, rsReadingNativeHitShape{hit.Subject, hit.ID, hit.Library, hit.Kind, hit.Tier, hit.Item.Title, hit.SortKey})
	}
	return shape
}

func assertRSNativeReadingHit(t *testing.T, page FullSearchPage, kind int64) {
	t.Helper()
	if page.Stamp == nil || !page.Exhausted {
		t.Fatal("complete page lacks custody", page)
	}
	if len(page.Hits) != 1 {
		t.Fatalf("want exactly the reading hit, got %+v", page.Hits)
	}
	hit := page.Hits[0]
	want := readPolicyBinding{recipe: rsReadingNativePolicy, subject: 1101, nativeSearch: true}
	if hit.Subject != search.ManifestEntity || hit.ID != 1101 || hit.Kind != kind || hit.Library != 1 ||
		hit.Item.ID != 1101 || hit.Item.Title != rsReadingNativeTitle || !hit.permission.valid ||
		hit.permission.binding != want {
		t.Fatalf("kind%d native reading hit: %+v permission %+v", kind, hit, hit.permission)
	}
	if hit.permission.binding.recipe == rsReadingMobiPolicy {
		t.Fatal("private converter recipe granted a native hit")
	}
}

func TestRSNativeReadingCompiledRecipeAcceptedKinds(t *testing.T) {
	for _, kind := range []int64{20, 21} {
		kind := kind
		t.Run(fmt.Sprintf("kind%d", kind), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := rsReadingNativeFixture(t, rsReadingNativePositiveOptions(kind), true)
				assertRSNativeReadingHit(t, rsReadingNativeComplete(t, f, r), kind)
				assertLiveUsage(t, f, 0, 0)
			})
		})
	}
}

type rsReadingNativeControl struct {
	name       string
	options    rsReadingNativeOptions
	withRS     bool
	requireErr bool
}

// Per-item ineligibility is a skip, never a refusal: an unattributed Reading
// item simply is not a hit and the viewer gets an ordinary empty page. Missing
// compiled definitions are not an item fact, so they still refuse.
func TestRSNativeReadingNegativeControls(t *testing.T) {
	attribution := rsReadingNativePositiveOptions(20)
	attribution.attribution = 0
	for _, c := range []rsReadingNativeControl{
		{name: "attribution-missing", options: attribution, withRS: true},
		{name: "missing-rs-definitions", options: rsReadingNativePositiveOptions(20), withRS: false, requireErr: true},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := rsReadingNativeFixture(t, c.options, c.withRS)
				var after *FullSearchCursor
				for poll := 0; poll < 100; poll++ {
					page, err := rsReadingNativeRead(t, f, r, after, nil)
					if c.requireErr {
						if !errors.Is(err, sqliteconn.ErrPolicy) || !reflect.DeepEqual(page, FullSearchPage{}) {
							t.Fatalf("missing definitions: page %+v err %v", page, err)
						}
						return
					}
					if err != nil {
						t.Fatalf("ineligible item refused the page: %v", err)
					}
					switch page.Disposition {
					case SearchPending:
						if len(page.Hits) != 0 || page.Next == nil || page.Exhausted {
							t.Fatalf("partial match escaped: %+v", page)
						}
						after = page.Next
						continue
					case SearchResults:
						if len(page.Hits) != 0 {
							t.Fatalf("ineligible item granted a hit: %+v", page.Hits)
						}
						if page.Exhausted {
							return
						}
						if page.Next == nil {
							t.Fatalf("non-exhausted zero page without continuation: %+v", page)
						}
						after = page.Next
						continue
					default:
						t.Fatalf("unexpected disposition: %+v", page)
					}
				}
				t.Fatal("bounded negative continuation did not finish")
			})
		})
	}
}

// A restricted profile must not learn that an over-age matching item exists.
// The results page, its hits and its continuation shape are identical to the
// same fixture without the hidden item, and no poll refuses.
func TestRSNativeReadingHiddenAgeSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		visible := rsReadingNativePositiveOptions(20)
		visible.maximumAge = 12
		hidden := visible
		hidden.extraTitle = "Native Reading Hidden"
		hidden.extraAge = 30
		hidden.extraFormat = "epub"
		f, r := rsReadingNativeFixture(t, hidden, true)
		withHidden := rsReadingNativeComplete(t, f, r)
		assertRSNativeReadingHit(t, withHidden, 20)
		g, s := rsReadingNativeFixture(t, visible, true)
		withoutHidden := rsReadingNativeComplete(t, g, s)
		assertRSNativeReadingHit(t, withoutHidden, 20)
		if !reflect.DeepEqual(rsReadingNativeShape(withHidden), rsReadingNativeShape(withoutHidden)) {
			t.Fatalf("hidden item changed the viewer-visible page shape: %+v vs %+v", rsReadingNativeShape(withHidden), rsReadingNativeShape(withoutHidden))
		}
	})
}

// A converter-only edition is not a native hit for any viewer; the other match
// is still returned and no poll refuses.
func TestRSNativeReadingConverterOnlySkipped(t *testing.T) {
	for _, maximumAge := range []int64{-1, 12} {
		maximumAge := maximumAge
		t.Run(fmt.Sprintf("maximumAge%d", maximumAge), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := rsReadingNativePositiveOptions(20)
				o.maximumAge = maximumAge
				o.extraTitle = "Native Reading Hidden"
				o.extraFormat = "mobi"
				f, r := rsReadingNativeFixture(t, o, true)
				assertRSNativeReadingHit(t, rsReadingNativeComplete(t, f, r), 20)
			})
		})
	}
}

func TestRSNativeReadingRetainedSourceRechecked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := rsReadingNativeFixture(t, rsReadingNativePositiveOptions(20), true)
		prior := rsReadingNativeComplete(t, f, r)
		assertRSNativeReadingHit(t, prior, 20)
		// Deny the previously accepted domain source AFTER its valid hit/stamp.
		f.exec(t, `UPDATE reading_publications SET format='mobi' WHERE item_id=1101`)
		var after *FullSearchCursor
		for poll := 0; poll < 100; poll++ {
			page, err := rsReadingNativeRead(t, f, r, after, []*FullSearchReadStamp{prior.Stamp})
			if err != nil {
				if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(page, FullSearchPage{}) {
					t.Fatalf("retained source drift released output: %+v (%v)", page, err)
				}
				return
			}
			if page.Disposition != SearchPending {
				t.Fatalf("retained source drift produced output: %+v", page)
			}
			if len(page.Hits) != 0 || page.Next == nil || page.Exhausted {
				t.Fatalf("partial match escaped: %+v", page)
			}
			after = page.Next
		}
		t.Fatal("bounded retained continuation did not finish")
	})
}

func TestRSNativeReadingCaptureProfiles(t *testing.T) {
	for _, statistics := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		statistics := statistics
		t.Run(string(statistics), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := rsReadingNativePositiveOptions(20)
				f := rsReadingNativeSource(t, o)
				if statistics == plan.Analyzed {
					f.exec(t, "ANALYZE")
				}
				defs := rsReadingNativeDefinitions(true)
				golden := captureBrowse(t, f, defs, statistics, rsReadingNativeFixtureHash(t, o))
				if _, err := stmt.New(defs, golden.Receipt()); err != nil {
					t.Fatalf("actual %s Capture receipt: %v", statistics, err)
				}
			})
		})
	}
}
