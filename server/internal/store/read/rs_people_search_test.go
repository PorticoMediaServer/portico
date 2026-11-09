package read

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// newRSPeopleSearchFixture composes the actual native bank, role/count facts,
// credit images and portrait points. It is an explicit test composition, not a
// default registration or qualification claim.
func newRSPeopleSearchFixture(t *testing.T, seed func(*testing.T, *browseFixture)) (*browseFixture, *RSPeopleSearchReader) {
	t.Helper()
	gc := readfixture.LiveConfig(time.Now)
	gc.Preparation = &governor.PreparationProfile{MaxActive: 1, MaxBytes: 8 << 20, MaxJobBytes: 4 << 20, ProtectedBytes: 32 << 20}
	f := newNativeSource(t)
	if seed != nil {
		seed(t, f)
	}
	f = composeReadFixture(t, f, RSPeopleSearchDefinitions(), gc, false, nativeFixtureHash(t))
	r, e := NewRSPeopleSearchReader(f.snapshots, f.authority)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}

func peopleSearchView(f *browseFixture, library, kind int64) BrowseInput {
	v := f.input
	v.Library, v.Kind = library, kind
	return v
}

func beginPeopleSearch(t *testing.T, f *browseFixture, r *RSPeopleSearchReader, in RSPeopleSearchInput) *RSPeopleSearchPreparation {
	t.Helper()
	if len(in.Views) == 0 {
		in.Views = []BrowseInput{peopleSearchView(f, 1, 4)}
	}
	if in.Order == "" {
		in.Order = "relevance"
	}
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	t.Cleanup(func() {
		if e := p.Close(); e != nil {
			t.Error(e)
		}
	})
	return p
}

// runPeopleSearch polls the public Publish path under one LIVE/output grant at
// a time. calls, when non-nil, proves the one-native-call-per-poll contract.
func runPeopleSearch(t *testing.T, f *browseFixture, r *RSPeopleSearchReader, p *RSPeopleSearchPreparation, calls *int, encodeHook func(RSPeopleSearchPage) error, consumeErr error) (RSPeopleSearchPage, int, error) {
	t.Helper()
	consumed := 0
	for poll := 0; poll < 4000; poll++ {
		q, req, out := liveRequests("rs-people-search")
		req.Budget.RawRows = 4096
		before := 0
		if calls != nil {
			before = *calls
		}
		var page RSPeopleSearchPage
		done := false
		_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, out BrowseOutput) error {
			var err error
			done, _, err = r.Publish(ctx, req, time.Second, out, p, func(ctx context.Context, got RSPeopleSearchPage) ([]byte, error) {
				page = got
				if encodeHook != nil {
					if e := encodeHook(got); e != nil {
						return nil, e
					}
				}
				return []byte("owned people search"), ctx.Err()
			}, func(ctx context.Context, payload []byte) error {
				consumed++
				if string(payload) != "owned people search" || p.search.closed || p.work == nil || f.authority.ForegroundUsage().Live != 1 {
					return errors.New("lost joined custody")
				}
				if consumeErr != nil {
					return consumeErr
				}
				return ctx.Err()
			})
			return err
		})
		if calls != nil && *calls-before != 1 {
			t.Fatalf("poll %d native calls %d", poll, *calls-before)
		}
		if e != nil {
			return RSPeopleSearchPage{}, consumed, e
		}
		if done {
			return page, consumed, nil
		}
	}
	return RSPeopleSearchPage{}, consumed, errors.New("people search did not terminate")
}

func countPeopleSearchCalls(r *RSPeopleSearchReader) *int {
	calls := 0
	native := r.search.read
	r.search.read = func(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in FullSearchInput, query search.Query) (FullSearchPage, *BrowseReadResult, error) {
		calls++
		return native(ctx, req, budget, out, in, query)
	}
	return &calls
}

func seedPeopleSearchImage(t *testing.T, f *browseFixture, image, entity int64) {
	t.Helper()
	f.exec(t, `INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) SELECT ?,id,metadata_incarnation,1,1,1,1,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16),1,zeroblob(32) FROM catalog_entities WHERE id=?`, image, entity)
	f.exec(t, `UPDATE catalog_entities SET credit_image_id=? WHERE id=?`, image, entity)
}

func seedPeopleSearchPortrait(t *testing.T, f *browseFixture, person int64) {
	t.Helper()
	const digest = "0000000000000000000000000000000000000000000000000000000000000001"
	f.exec(t, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/png',1,1,1,'2026-11-06T00:00:00Z')`, digest)
	f.exec(t, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/png',1,1,?,?,?,1280,0,0,1,'2026-11-06T00:00:00Z')`, digest, digest, digest, digest)
	f.exec(t, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('person',?,'portrait','matched',?,'local','portrait','nfo','','f','test','2026-11-06T00:00:00Z')`, person, digest)
}

func seedPeopleSearchLabels(t *testing.T, f *browseFixture) {
	t.Helper()
	f.exec(t, `INSERT OR IGNORE INTO catalog_credit_labels(id,label) VALUES(3,'Director'),(4,'Directing')`)
}

func seedPeopleSearchCrewCredit(t *testing.T, f *browseFixture) {
	t.Helper()
	seedPeopleSearchLabels(t, f)
	// A kind-2 (show) view is a container, and the catalogue policy admits a
	// container only when the profile's age policy does. The explicit default
	// restriction keeps that proof real instead of relying on an absent row.
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',-1,1)`)
	seedPeopleSearchImage(t, f, 5001, 1)
	f.exec(t, `UPDATE catalog_entities SET kind=2,retired=0 WHERE id=1`)
	f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(5001,1,0,1,3,4,'Keanu Direction','fixture','crew',0)`)
}

func TestRSPeopleSearchNativeSummaryDelivery(t *testing.T) {
	for _, mode := range []string{"ready", "person-art-during-encode", "revoke-during-encode", "consumer-error"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := newRSPeopleSearchFixture(t, func(t *testing.T, f *browseFixture) {
					seedPeopleSearchCrewCredit(t, f)
					seedPeopleSearchPortrait(t, f, 1)
				})
				query, e := search.PlanQuery("keanu", true)
				if e != nil {
					t.Fatal(e)
				}
				p := beginPeopleSearch(t, f, r, RSPeopleSearchInput{
					Views: []BrowseInput{peopleSearchView(f, 1, 2), peopleSearchView(f, 1, 4)},
					Query: query,
				})
				if p.work.lease != p.search.lease {
					t.Fatal("hydration did not borrow the native lease")
				}
				calls := countPeopleSearchCalls(r)
				consumerErr := errors.New("consumer stopped")
				var want error
				if mode == "consumer-error" {
					want = consumerErr
				}
				hook := func(RSPeopleSearchPage) error {
					switch mode {
					case "person-art-during-encode":
						f.exec(t, `UPDATE catalog_people SET artwork_revision=artwork_revision+1 WHERE id=1`)
					case "revoke-during-encode":
						f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
					}
					return nil
				}
				page, consumed, e := runPeopleSearch(t, f, r, p, calls, hook, func() error {
					if mode == "consumer-error" {
						return consumerErr
					}
					return nil
				}())
				if *calls >= 4000 {
					t.Fatal("native call census")
				}
				switch mode {
				case "ready":
					if e != nil || consumed != 1 || !page.Complete || page.Pending || page.Next != nil || page.CountStatus != SearchCountUnknown || len(page.People) != 1 {
						t.Fatal(page, consumed, e)
					}
					v := page.People[0]
					if v.ID != 1 || v.Token != "person" || v.Name != "Keanu" || v.SortName != "keanu" || v.CreditCount != 2 || !reflect.DeepEqual(v.Roles, []string{"Acting", "Directing"}) {
						t.Fatal("exact summary", v)
					}
					if v.Portrait == nil || v.Portrait.MIME != "image/png" || v.Portrait.Revision == 0 {
						t.Fatal("portrait", v.Portrait)
					}
					if len(v.KnownFor) != 2 || v.KnownFor[0].ID != 1 || v.KnownFor[1].ID != 2 {
						t.Fatal("known-for", v.KnownFor)
					}
					if !p.search.closed || p.work != nil {
						t.Fatal("delivery did not retire custody")
					}
				case "person-art-during-encode":
					if !errors.Is(e, ErrStale) || consumed != 0 {
						t.Fatal("summary-art drift escaped", consumed, e)
					}
				case "revoke-during-encode":
					if !errors.Is(e, ErrDenied) || consumed != 0 {
						t.Fatal("revoked summary escaped", consumed, e)
					}
				case "consumer-error":
					if !errors.Is(e, want) || consumed != 1 {
						t.Fatal("joined consumer error", consumed, e)
					}
				}
				assertLiveUsage(t, f, 0, 0)
			})
		})
	}
}

func TestRSPeopleSearchMissingCreditScopeRefusesRankedHit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRSPeopleSearchFixture(t, nil)
		query, e := search.PlanQuery("keanu", true)
		if e != nil {
			t.Fatal(e)
		}
		// Native People ranking is library-scoped but not kind-scoped; the
		// credit only exists on the kind 4 entity, so complete kind 1 hydration
		// has no visible credit and must refuse instead of dropping the hit.
		p := beginPeopleSearch(t, f, r, RSPeopleSearchInput{Views: []BrowseInput{peopleSearchView(f, 1, 1)}, Query: query})
		page, consumed, e := runPeopleSearch(t, f, r, p, nil, nil, nil)
		if !errors.Is(e, ErrUnqualified) || consumed != 0 || len(page.People) != 0 {
			t.Fatal("ranked hit without credit scope", page, consumed, e)
		}
		assertLiveUsage(t, f, 0, 0)
	})
}

func TestRSPeopleSearchCloseRetiresTheOneLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRSPeopleSearchFixture(t, nil)
		query, e := search.PlanQuery("keanu", true)
		if e != nil {
			t.Fatal(e)
		}
		p := beginPeopleSearch(t, f, r, RSPeopleSearchInput{Views: []BrowseInput{peopleSearchView(f, 1, 4)}, Query: query})
		q, req, out := liveRequests("rs-people-search-close")
		req.Budget.RawRows = 4096
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, out BrowseOutput) error {
			done, _, err := r.Publish(ctx, req, time.Second, out, p, func(context.Context, RSPeopleSearchPage) ([]byte, error) {
				return nil, errors.New("must not encode")
			}, func(context.Context, []byte) error {
				return errors.New("must not consume")
			})
			if done || err != nil {
				t.Fatal("unexpected delivery", done, err)
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if e = p.Close(); e != nil {
			t.Fatal(e)
		}
		// MaxActive1 proves the first lease physically retired: a second
		// preparation can now be admitted without a leaked job.
		p2 := beginPeopleSearch(t, f, r, RSPeopleSearchInput{Views: []BrowseInput{peopleSearchView(f, 1, 4)}, Query: query})
		if p2.work.lease != p2.search.lease {
			t.Fatal("second preparation borrowed a distinct lease")
		}
		if e = p2.Close(); e != nil {
			t.Fatal(e)
		}
		assertLiveUsage(t, f, 0, 0)
	})
}

func seedPeopleSearchSecondLibrary(t *testing.T, f *browseFixture) {
	t.Helper()
	seedPeopleSearchLabels(t, f)
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
	// 1040 becomes a legal episode of a show in its own (second) library: the
	// schema requires every kind-4 row to carry show_id, numbering and
	// episode_number, and the show reference is library-scoped. The entity and
	// its subject row reference each other's identity tuple, so both kind
	// changes are deferred to COMMIT exactly as the base native seed does.
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(1200,x'000000000000000000000000000004b0',2,2)`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(1200,x'000000000000000000000000000004b0',2,2,'Other Show','other show','o','other show','o',0,zeroblob(16),zeroblob(16))`)
	f.exec(t, `UPDATE catalog_subjects SET kind=4 WHERE id=1040`)
	f.exec(t, `UPDATE catalog_entities SET kind=4,show_id=1200,numbering='seasonal',episode_number=1,retired=0 WHERE id=1040`)
	f.exec(t, "COMMIT")
	seedPeopleSearchImage(t, f, 6001, 1040)
	f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(6001,1040,0,1,1,2,'Keanu','fixture','other-library',0)`)
	// The episode's show container takes part in the native walk as a related
	// source, so it must have its own current native publication exactly like
	// every other entity in this fixture. Without one the walk reports an
	// incomplete proof forever instead of returning the person page.
	nativeFixtureImage(t, f, search.ManifestEntity, 1200, 6003, []nativeFixtureField{{1, "Other Show"}})
	nativeFixtureImage(t, f, search.ManifestEntity, 1040, 6002, []nativeFixtureField{{1, "Title 1040"}})
}

func TestRSPeopleSearchDuplicateCrossLibraryPersonHydratesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRSPeopleSearchFixture(t, seedPeopleSearchSecondLibrary)
		query, e := search.PlanQuery("keanu", true)
		if e != nil {
			t.Fatal(e)
		}
		p := beginPeopleSearch(t, f, r, RSPeopleSearchInput{
			Views: []BrowseInput{peopleSearchView(f, 1, 4), peopleSearchView(f, 2, 4)},
			Query: query,
		})
		page, consumed, e := runPeopleSearch(t, f, r, p, nil, nil, nil)
		if e != nil || consumed != 1 || len(page.People) != 1 || page.Next != nil {
			t.Fatal(page, consumed, e)
		}
		v := page.People[0]
		if v.ID != 1 || v.CreditCount != 2 || !reflect.DeepEqual(v.Roles, []string{"Acting"}) {
			t.Fatal("deduplicated cross-library summary", v)
		}
		if len(v.KnownFor) != 2 || v.KnownFor[0].ID != 2 || v.KnownFor[1].ID != 1040 {
			t.Fatal("known-for over both libraries", v.KnownFor)
		}
		assertLiveUsage(t, f, 0, 0)
	})
}

func seedPeopleSearchUbiquitous(t *testing.T, f *browseFixture) {
	t.Helper()
	seedPeopleSearchLabels(t, f)
	// Entity and subject identity tuples reference each other, so the kind
	// changes are deferred to COMMIT exactly as the base native seed does.
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	for _, id := range []int64{100, 101, 102, 103, 104, 105} {
		// Legal episodes of the library-1 show seeded above: kind 4 rows must
		// carry show_id, numbering and episode_number.
		f.exec(t, `UPDATE catalog_subjects SET kind=4 WHERE id=?`, id)
		f.exec(t, `UPDATE catalog_entities SET kind=4,show_id=1,numbering='seasonal',episode_number=?,retired=0 WHERE id=?`, id, id)
	}
	f.exec(t, "COMMIT")
	for n, id := range []int64{100, 101, 102, 103, 104, 105} {
		seedPeopleSearchImage(t, f, 7000+id, id)
		role, department := int64(1), int64(2)
		if n%2 == 1 {
			role, department = 3, 4
		}
		f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(?,?,0,1,?,?,'Keanu','fixture','ubiquitous',0)`, 7000+id, id, role, department)
		nativeFixtureImage(t, f, search.ManifestEntity, id, 7300+id, []nativeFixtureField{{1, fmt.Sprintf("Work %d", id)}})
	}
	// An orphan person with a current name publication but no visible credit is
	// never selected; it cannot stand in for an empty successful page.
	f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,search_incarnation) VALUES(200,'ghost','local:ghost','Ghost','ghost',zeroblob(16),zeroblob(16))`)
	nativeFixtureImage(t, f, search.ManifestPerson, 200, 7200, []nativeFixtureField{{1, "Ghost"}})
}

func TestRSPeopleSearchUbiquitousCreditsAndOrphan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRSPeopleSearchFixture(t, seedPeopleSearchUbiquitous)
		query, e := search.PlanQuery("keanu", true)
		if e != nil {
			t.Fatal(e)
		}
		p := beginPeopleSearch(t, f, r, RSPeopleSearchInput{Views: []BrowseInput{peopleSearchView(f, 1, 4)}, Query: query})
		page, consumed, e := runPeopleSearch(t, f, r, p, nil, nil, nil)
		if e != nil || consumed != 1 || len(page.People) != 1 {
			t.Fatal(page, consumed, e)
		}
		v := page.People[0]
		if v.ID != 1 || v.CreditCount != 7 || !reflect.DeepEqual(v.Roles, []string{"Acting", "Directing"}) || len(v.KnownFor) != 7 {
			t.Fatal("ubiquitous person", v)
		}
		var ids []int64
		for _, known := range v.KnownFor {
			ids = append(ids, known.ID)
		}
		if !reflect.DeepEqual(ids, []int64{2, 100, 101, 102, 103, 104, 105}) {
			t.Fatal("known-for billing/id order", ids)
		}
		// The orphan has no visible credit, so a query for only its name is an
		// empty complete page, not an orphan card or a refusal.
		ghost, e := search.PlanQuery("ghost", true)
		if e != nil {
			t.Fatal(e)
		}
		p = beginPeopleSearch(t, f, r, RSPeopleSearchInput{Views: []BrowseInput{peopleSearchView(f, 1, 4)}, Query: ghost})
		page, consumed, e = runPeopleSearch(t, f, r, p, nil, nil, nil)
		if e != nil || consumed != 1 || !page.Complete || len(page.People) != 0 || page.Next != nil {
			t.Fatal("orphan leaked", page, consumed, e)
		}
		assertLiveUsage(t, f, 0, 0)
	})
}

func seedPeopleSearchCrowd(t *testing.T, f *browseFixture, n int) {
	t.Helper()
	f.exec(t, "BEGIN")
	for p := int64(2); p <= int64(n+1); p++ {
		name := fmt.Sprintf("Crowd %03d", p)
		f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,search_incarnation) VALUES(?,?,?,?,?,zeroblob(16),zeroblob(16))`, p, fmt.Sprintf("crowd-%03d", p), fmt.Sprintf("local:crowd:%03d", p), name, fmt.Sprintf("crowd %03d", p))
		f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(1,2,?,?,1,2,?,'fixture','crowd',0)`, p, p, name)
		nativeFixtureImage(t, f, search.ManifestPerson, p, 2000+p, []nativeFixtureField{{1, name}})
	}
	f.exec(t, "COMMIT")
}

func TestRSPeopleSearchThreePagesInIndependentTitleOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const people = 81
		f, r := newRSPeopleSearchFixture(t, func(t *testing.T, f *browseFixture) {
			seedPeopleSearchCrowd(t, f, people)
		})
		query, e := search.PlanQuery("crowd", true)
		if e != nil {
			t.Fatal(e)
		}
		// Independent oracle: the native title order for people is sort_name,id.
		rows, e := f.conn.QueryContext(t.Context(), `SELECT id FROM catalog_people WHERE id BETWEEN 2 AND 82 ORDER BY sort_name,id`)
		if e != nil {
			t.Fatal(e)
		}
		var oracle []int64
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				t.Fatal(e)
			}
			oracle = append(oracle, id)
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			t.Fatal(e)
		}
		rows.Close()
		if len(oracle) != people {
			t.Fatal("oracle population", len(oracle))
		}
		var got []int64
		var after *RSPeopleSearchCursor
		pages := []int{}
		for {
			p := beginPeopleSearch(t, f, r, RSPeopleSearchInput{Views: []BrowseInput{peopleSearchView(f, 1, 4)}, Query: query, Order: "title", After: after})
			page, consumed, e := runPeopleSearch(t, f, r, p, nil, nil, nil)
			if e != nil || consumed != 1 || !page.Complete || page.Pending || page.CountStatus != SearchCountUnknown {
				t.Fatal(page, consumed, e)
			}
			pages = append(pages, len(page.People))
			for _, v := range page.People {
				got = append(got, v.ID)
			}
			if page.Next == nil {
				break
			}
			after = page.Next
			if len(pages) == 3 {
				t.Fatal("more than three pages", pages)
			}
		}
		if !reflect.DeepEqual(pages, []int{40, 40, 1}) || !reflect.DeepEqual(got, oracle) {
			t.Fatal("native keyset pages", pages, len(got), len(oracle))
		}
		assertLiveUsage(t, f, 0, 0)
	})
}
