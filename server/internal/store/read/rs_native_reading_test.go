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
	// Publish only after every source mutation has been seeded.
	nativeFixtureImage(t, f, search.ManifestEntity, 1101, 2101, []nativeFixtureField{{1, rsReadingNativeTitle}})
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

func TestRSNativeReadingNegativeControls(t *testing.T) {
	age := rsReadingNativePositiveOptions(20)
	age.ownAge, age.maximumAge, age.allowUnrated = 30, 12, 0
	attribution := rsReadingNativePositiveOptions(20)
	attribution.attribution = 0
	mobi := rsReadingNativePositiveOptions(20)
	mobi.format = "mobi"
	for _, c := range []rsReadingNativeControl{
		{name: "age-over-profile", options: age, withRS: true},
		{name: "attribution-missing", options: attribution, withRS: true},
		{name: "converter-only-format", options: mobi, withRS: true},
		{name: "missing-rs-definitions", options: rsReadingNativePositiveOptions(20), withRS: false, requireErr: true},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := rsReadingNativeFixture(t, c.options, c.withRS)
				var after *FullSearchCursor
				for poll := 0; poll < 100; poll++ {
					page, err := rsReadingNativeRead(t, f, r, after, nil)
					if err != nil {
						want := ErrDenied
						if !c.withRS {
							want = sqliteconn.ErrPolicy
						}
						if !errors.Is(err, want) {
							t.Fatalf("unexpected refusal class: %v, want %v", err, want)
						}
						if !reflect.DeepEqual(page, FullSearchPage{}) {
							t.Fatalf("refusal leaked output: %+v (%v)", page, err)
						}
						return
					}
					switch page.Disposition {
					case SearchPending:
						if len(page.Hits) != 0 || page.Next == nil || page.Exhausted {
							t.Fatalf("partial match escaped: %+v", page)
						}
						after = page.Next
						continue
					case SearchUnavailable:
						t.Fatal("domain negative must not pass on unrelated native unavailability")
					case SearchResults:
						if len(page.Hits) != 0 {
							t.Fatalf("negative control granted a hit: %+v", page.Hits)
						}
						if page.Exhausted {
							if c.requireErr {
								t.Fatal("missing definitions were accepted")
							}
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
