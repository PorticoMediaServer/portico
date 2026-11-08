package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// This fixture is mechanical read-contract evidence only. It does not exercise
// producer acceptance, a MusicBrainz adapter, native qualification or network
// access. Every tuple below is seeded offline before the composed registry is
// captured and the pool is created.
const (
	rsAlbumProviderOpID         = "album-release-op"
	rsAlbumProviderPubID        = "album-release-pub"
	rsAlbumProviderEvidenceID   = "album-release-evidence"
	rsAlbumProviderSubjectID    = int64(7001)
	rsAlbumProviderParser       = "musicbrainz.release.header-complete-children.v1"
	rsAlbumProviderRequestShape = "musicbrainz.release.detail+artist-credits+recordings+release-groups.v1"
	rsAlbumProviderObservedAt   = "2020-01-02T03:04:05.000000000Z"
	rsAlbumProviderSourcePubID  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rsAlbumProviderAlgorithm    = "portico.normalized-evidence.sha256.v1"
	rsAlbumProviderDigest       = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rsAlbumProviderReleaseID    = "canonical-release"
	rsAlbumProviderRequestedID  = "original-redirect"
	rsAlbumProviderGroupID      = "different-release-group"
	rsAlbumProviderFixtureTag   = "rs-album-provider-abi-fixture"
)

//go:embed rs_artist_provider_test.go
var rsArtistProviderJournal string

func rsArtistProviderHash() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rsAudioRelationsJournal+rsDetailJournal+rsArtistProviderJournal+rsAlbumProviderFixtureTag)))
}

// rsAlbumProviderFixture derives the live artist/album/song graph from
// audioRelationsFixture and seeds one accepted release tuple for album 2001
// before actual Reader Capture and pool creation. mutate runs before that
// composition so negative controls never observe a cached acceptance.
func rsAlbumProviderFixture(t *testing.T, mutate func(*testing.T, *browseFixture)) (*browseFixture, *RSArtistAlbumReader) {
	t.Helper()
	source := audioRelationsFixture(t)
	// Same live primary artist, second album and featured song shape used by the
	// existing artist-album fixture.
	for _, x := range []struct {
		id, kind, template int64
		artist, album      any
	}{{1998, 5, 2006, nil, nil}, {1999, 6, 2001, int64(1998), nil}, {9902, 7, 4001, nil, int64(1999)}} {
		source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,randomblob(16),1,?)`, x.id, x.kind)
		source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id,album_id) SELECT id,public_id,library_id,kind,?,'x','x','x','x',0,public_id,public_id,?,? FROM catalog_subjects WHERE id=?`, "é\x00album", x.artist, x.album, x.id)
	}
	// 9902 makes 2006's featured album 1999 visible; 4001 is a featured song on
	// album 2001 for the independent appearance role.
	source.exec(t, `INSERT INTO catalog_song_artists(song_id,artist_id) VALUES(9902,2006)`)
	source.exec(t, `INSERT INTO catalog_song_artists(song_id,artist_id) VALUES(4001,1998)`)
	rsAlbumProviderSeed(t, source)
	if mutate != nil {
		mutate(t, source)
	}

	cfg := readfixture.LiveConfig(time.Now)
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 4 << 20, ProtectedBytes: 32 << 20}
	defs := append(browseDefinitions(), RSArtistAlbumDefinitions()...)
	defs = append(defs, TitleFactsDefinitions()...)
	defs = append(defs, ArtworkDefinitions()...)
	hash := rsArtistProviderHash()
	// Capture the actual closed registry in both native planner profiles.
	f := composeReadFixture(t, source, defs, cfg, false, hash)
	f.exec(t, "ANALYZE")
	analyzed := captureBrowse(t, f, defs, plan.Analyzed, hash)
	if _, err := stmt.New(defs, analyzed.Receipt()); err != nil {
		t.Fatal(err)
	}
	r, err := NewRSArtistAlbumReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

// rsAlbumProviderSeed writes the schema-required cyclic publication/evidence/
// receipt tuple. BEGIN plus deferred foreign keys permits the cycle; foreign
// keys stay enabled and are validated at COMMIT. No skip or FK bypass is used.
func rsAlbumProviderSeed(t *testing.T, f *browseFixture) {
	t.Helper()
	var fk int
	if err := f.conn.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatal("foreign keys disabled")
	}
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	f.exec(t, `INSERT INTO metadata_providers(code,adapter,enabled) VALUES('musicbrainz','fixture',1)`)
	f.exec(t, `INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode) VALUES(1,'musicbrainz',x'22222222222222222222222222222222',1,1,0,'','fixture-algorithm','fill_missing')`)
	f.exec(t, `INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(?,?,?,?,?,?,?,x'33333333333333333333333333333333',1)`, rsAlbumProviderSubjectID, "musicbrainz", "release", rsAlbumProviderReleaseID, "en", "", rsAlbumProviderRequestShape)
	f.exec(t, `INSERT INTO credit_source_publications(publication_id,entity_id,provider,accepted_record_reference,observed_at,member_count,sealed) VALUES(?,2001,'musicbrainz','fixture://musicbrainz/release/canonical-release',?,4,0)`, rsAlbumProviderSourcePubID, rsAlbumProviderObservedAt)
	// The original operation carries metadata_revision 1; the entity is moved to
	// 2 after COMMIT to exercise the required owner-edit case.
	f.exec(t, `INSERT INTO metadata_provider_operations(id,entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,descriptive_revision,policy_incarnation,policy_revision,consent_revision,algorithm,refresh_mode,query_title,query_artist,query_year,query_digest,input_digest,selected_id,manual,review_search,qualification,created_at,command_incarnation,last_command_sequence,acknowledged_command_sequence,source_content_revision,source_publication_id,source_owner_generation,source_evidence_id,source_subject_id,source_parser_version,source_next_position,source_expected_members,source_expected_bytes,source_algorithm,source_version,source_expected_commitment,source_evidence_owner,source_evidence_incarnation,source_evidence_sequence)
SELECT ?,id,'musicbrainz','accept','applied',3,'fixture','later',metadata_incarnation,source_revision,identity_revision,1,1,1,1,x'22222222222222222222222222222222',1,5,'fixture-algorithm','fill_missing','Album','Artist',0,'query-digest','input-digest',?,0,0,'fixture',?,x'11111111111111111111111111111111',1,1,1,?,3,?,?,?,4,4,1234,?,1,x'4444444444444444444444444444444444444444444444444444444444444444',?,x'11111111111111111111111111111111',1 FROM catalog_entities WHERE id=2001`,
		rsAlbumProviderOpID, rsAlbumProviderReleaseID, rsAlbumProviderObservedAt, rsAlbumProviderSourcePubID, rsAlbumProviderEvidenceID, rsAlbumProviderSubjectID, rsAlbumProviderParser, rsAlbumProviderAlgorithm, rsAlbumProviderOpID)
	f.exec(t, `INSERT INTO enrichment_command_receipts(provider_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,entity_id,after_item,after_ordinal,result_count,result_digest,seal_version,seal_evidence_id,seal_subject_id,seal_parser_version,seal_provider,seal_subject_kind,seal_provider_id,seal_language,seal_region,seal_request_shape,seal_observation_digest,seal_owner_generation,seal_build_id,seal_build_generation,seal_content_revision,seal_algorithm,seal_commitment,seal_verified_rows,seal_canonical_bytes,seal_retained_bytes,proof_pin_count)
VALUES(?,x'11111111111111111111111111111111',1,x'55555555555555555555555555555555',x'6666666666666666666666666666666666666666666666666666666666666666','evidence_seal',0,0,2001,'',4,0,x'7777777777777777777777777777777777777777777777777777777777777777',1,?,?,?,?,?,?,?,?,?,?,3,'album-release-build',1,1,?,x'4444444444444444444444444444444444444444444444444444444444444444',4,1234,4096,0)`,
		rsAlbumProviderOpID, rsAlbumProviderEvidenceID, rsAlbumProviderSubjectID, rsAlbumProviderParser, "musicbrainz", "release", rsAlbumProviderReleaseID, "en", "", rsAlbumProviderRequestShape, rsAlbumProviderDigest, rsAlbumProviderAlgorithm)
	f.exec(t, `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,release_group_id,primary_type,sealed,seal_proof_owner,seal_proof_incarnation,seal_proof_sequence)
VALUES(?,?,?,'musicbrainz','release',?,'en','',?,?,?,?,?,?,1,?,x'11111111111111111111111111111111',1)`,
		rsAlbumProviderEvidenceID, rsAlbumProviderSubjectID, rsAlbumProviderParser, rsAlbumProviderReleaseID, rsAlbumProviderRequestShape, rsAlbumProviderDigest, rsAlbumProviderObservedAt, "fixture://musicbrainz/release/canonical-release", rsAlbumProviderGroupID, "EP", rsAlbumProviderOpID)
	f.exec(t, `INSERT INTO metadata_publications(id,operation_id,entity_id,provider,subject_kind,provider_id,evidence_id,selection_revision,generation,decision,observed_at) VALUES(?,?,2001,'musicbrainz','release',?,?,2,3,'accepted',?)`,
		rsAlbumProviderPubID, rsAlbumProviderOpID, rsAlbumProviderReleaseID, rsAlbumProviderEvidenceID, rsAlbumProviderObservedAt)
	f.exec(t, `INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,requested_id,status,observed_at,accepted_evidence_id,accepted_publication_id) VALUES(2001,'musicbrainz','primary',x'33333333333333333333333333333333',2,'release',?,?,'selected',?,?,?)`,
		rsAlbumProviderReleaseID, rsAlbumProviderRequestedID, rsAlbumProviderObservedAt, rsAlbumProviderEvidenceID, rsAlbumProviderPubID)
	f.exec(t, `INSERT INTO metadata_evidence_terms(evidence_id,vocabulary,ordinal,value) VALUES(?,'release_group_secondary_type',0,'Live'),(?,'release_group_secondary_type',1,'Compilation'),(?,'release_group_secondary_type',2,'Future Type'),(?,'release_group_secondary_type',3,'')`, rsAlbumProviderEvidenceID, rsAlbumProviderEvidenceID, rsAlbumProviderEvidenceID, rsAlbumProviderEvidenceID)
	f.exec(t, `INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,5)`)
	f.exec(t, "COMMIT")
	f.exec(t, `UPDATE catalog_entities SET metadata_revision=2 WHERE id=2001`)
}

// rsAlbumProviderComplete drives the public Begin/Page contract under
// ConsumeForeground with the original preparation until Complete.
func rsAlbumProviderComplete(t *testing.T, f *browseFixture, r *RSArtistAlbumReader, artist int64, limit int) RSArtistAlbumPage {
	t.Helper()
	v := f.input
	v.Kind = 5
	p, ref, err := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: artist, Limit: limit})
	if err != nil || ref != nil {
		t.Fatal(err, ref)
	}
	defer p.Close()
	for i := 0; i < 400; i++ {
		q, req, out := liveRequests("artist-provider")
		var page RSArtistAlbumPage
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
			var pageErr error
			page, _, pageErr = r.Page(ctx, req, time.Second, o, p)
			return pageErr
		})
		if err != nil {
			t.Fatal(i, err)
		}
		if page.Complete {
			return page
		}
		if !page.Pending || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
			t.Fatal("partial output", i, page)
		}
	}
	t.Fatal("did not complete")
	return RSArtistAlbumPage{}
}

func rsAlbumProviderCard(t *testing.T, page RSArtistAlbumPage, id int64) RSArtistAlbumCard {
	t.Helper()
	for _, card := range page.Cards {
		if card.Facts.Item.ID == id {
			return card
		}
	}
	t.Fatalf("album %d absent from %+v", id, page.Cards)
	return RSArtistAlbumCard{}
}

func rsAlbumProviderWant() RSAlbumProviderFact {
	return RSAlbumProviderFact{
		ReleaseID:          rsAlbumProviderReleaseID,
		RequestedReleaseID: rsAlbumProviderRequestedID,
		ReleaseGroupID:     rsAlbumProviderGroupID,
		ObservedAt:         rsAlbumProviderObservedAt,
		RawPrimaryType:     "EP",
		DisplayPrimaryType: "Compilation",
		SecondaryTypes:     []string{"Live", "Compilation", "Future Type", ""},
	}
}

// The accepted-source positive covers both the same-primary Compilation role
// and the relationship-first Appearance role. It is a read ABI assertion, not
// producer or MusicBrainz acceptance.
func TestRSArtistProviderAlbumAcceptedSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := rsAlbumProviderFixture(t, nil)
		want := rsAlbumProviderWant()

		same := rsAlbumProviderComplete(t, f, r, 2006, 2)
		card := rsAlbumProviderCard(t, same, 2001)
		if card.Provider == nil || !reflect.DeepEqual(*card.Provider, want) {
			t.Fatalf("accepted provider tuple %+v want %+v", card.Provider, want)
		}
		if card.Role != RSArtistAlbumCompilation {
			t.Fatal("same-primary role", card.Role)
		}
		if card.PrimaryArtist.ID != 2006 {
			t.Fatal("primary artist", card.PrimaryArtist)
		}
		if !slices.Equal(card.MissingCanonicalFacts, []string{"catalog_albums.provider_match_status"}) {
			t.Fatal("invented provider status", card.MissingCanonicalFacts)
		}

		appearance := rsAlbumProviderComplete(t, f, r, 1998, 2)
		other := rsAlbumProviderCard(t, appearance, 2001)
		if other.Provider == nil || !reflect.DeepEqual(*other.Provider, want) {
			t.Fatalf("appearance provider tuple %+v want %+v", other.Provider, want)
		}
		if other.Role != RSArtistAlbumAppearance {
			t.Fatal("appearance role", other.Role)
		}
		if other.PrimaryArtist.ID != 2006 {
			t.Fatal("actual primary artist", other.PrimaryArtist)
		}
		if !slices.Equal(other.MissingCanonicalFacts, []string{"catalog_albums.provider_match_status"}) {
			t.Fatal("invented provider status", other.MissingCanonicalFacts)
		}
	})
}

// Each control starts from an independent live fixture, mutates before Begin
// and requires absence of a provider fact rather than a false matched one.
func TestRSArtistProviderAlbumRejectsIncoherentSource(t *testing.T) {
	deferred := func(t *testing.T, f *browseFixture, queries ...string) {
		f.exec(t, "BEGIN")
		f.exec(t, "PRAGMA defer_foreign_keys=ON")
		for _, q := range queries {
			f.exec(t, q)
		}
		f.exec(t, "COMMIT")
	}
	for _, tc := range []struct {
		name  string
		apply func(*testing.T, *browseFixture)
	}{
		{"operation-ready", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE metadata_provider_operations SET status='ready' WHERE id=?`, rsAlbumProviderOpID)
		}},
		{"publication-generation", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE metadata_publications SET generation=4 WHERE id=?`, rsAlbumProviderPubID)
		}},
		{"publication-selection-revision", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE metadata_publications SET selection_revision=1 WHERE id=?`, rsAlbumProviderPubID)
		}},
		{"source-revision", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE catalog_entities SET source_revision=2 WHERE id=2001`)
		}},
		{"identity-revision", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE catalog_entities SET identity_revision=2 WHERE id=2001`)
		}},
		{"policy-incarnation", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE metadata_provider_policy SET incarnation=x'99999999999999999999999999999999' WHERE library_id=1 AND provider='musicbrainz'`)
		}},
		{"policy-revision", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE metadata_provider_policy SET revision=2 WHERE library_id=1 AND provider='musicbrainz'`)
		}},
		{"online-lookups-off", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE metadata_runtime_settings SET online_lookups=0 WHERE singleton=1`)
		}},
		{"lookup-epoch", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE metadata_runtime_settings SET lookup_epoch=6 WHERE singleton=1`)
		}},
		{"parser-version", func(t *testing.T, f *browseFixture) {
			deferred(t, f,
				`UPDATE enrichment_command_receipts SET seal_parser_version='fixture-parser' WHERE provider_operation_id='album-release-op'`,
				`UPDATE metadata_evidence SET parser_version='fixture-parser' WHERE id='album-release-evidence'`,
				`UPDATE metadata_provider_operations SET source_parser_version='fixture-parser' WHERE id='album-release-op'`,
			)
		}},
		{"request-shape-movie", func(t *testing.T, f *browseFixture) {
			deferred(t, f,
				`UPDATE enrichment_command_receipts SET seal_request_shape='movie.detail.v1' WHERE provider_operation_id='album-release-op'`,
				`UPDATE metadata_provider_subjects SET request_shape='movie.detail.v1' WHERE id=7001`,
				`UPDATE metadata_evidence SET request_shape='movie.detail.v1' WHERE id='album-release-evidence'`,
			)
		}},
		{"sealed-zero", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE metadata_evidence SET sealed=0,seal_proof_owner=NULL,seal_proof_incarnation=NULL,seal_proof_sequence=NULL WHERE id=?`, rsAlbumProviderEvidenceID)
		}},
		{"missing-current-proof", func(t *testing.T, f *browseFixture) {
			deferred(t, f,
				`UPDATE metadata_evidence SET seal_proof_owner=NULL,seal_proof_incarnation=NULL,seal_proof_sequence=NULL WHERE id='album-release-evidence'`,
				`UPDATE metadata_provider_operations SET source_publication_id=NULL,source_owner_generation=NULL,source_evidence_id=NULL,source_subject_id=NULL,source_parser_version=NULL,source_next_position=NULL,source_expected_members=NULL,source_expected_bytes=NULL,source_algorithm=NULL,source_version=NULL,source_expected_commitment=NULL,source_evidence_owner=NULL,source_evidence_incarnation=NULL,source_evidence_sequence=NULL WHERE id='album-release-op'`,
				`DELETE FROM enrichment_command_receipts WHERE provider_operation_id='album-release-op'`,
			)
		}},
		{"namespace-region", func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE metadata_provider_policy SET region='US' WHERE library_id=1 AND provider='musicbrainz'`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := rsAlbumProviderFixture(t, tc.apply)
				page := rsAlbumProviderComplete(t, f, r, 2006, 2)
				card := rsAlbumProviderCard(t, page, 2001)
				if card.Provider != nil {
					t.Fatalf("incoherent source admitted: %+v", *card.Provider)
				}
				if !slices.Contains(card.MissingCanonicalFacts, "catalog_albums.provider_match_status") {
					t.Fatal("missing provider status", card.MissingCanonicalFacts)
				}
			})
		})
	}
}

// A changed accepted source after the header was hydrated but before the fact
// became ready must refuse delivery; no partial or ready card may escape.
func TestRSArtistProviderAlbumStaleSourceAfterHeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := rsAlbumProviderFixture(t, nil)
		v := f.input
		v.Kind = 5
		p, ref, err := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: 2})
		if err != nil || ref != nil {
			t.Fatal(err, ref)
		}
		defer p.Close()
		mutated := false
		for i := 0; i < 400; i++ {
			q, req, out := liveRequests("artist-provider-stale")
			var page RSArtistAlbumPage
			_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
				var pageErr error
				page, _, pageErr = r.Page(ctx, req, time.Second, o, p)
				return pageErr
			})
			if err != nil {
				t.Fatal("pre-mutation failure", i, err)
			}
			if p.providerWork != nil && p.hydrate < len(p.providerWork) {
				work := &p.providerWork[p.hydrate]
				if work.header && !work.ready {
					f.exec(t, `UPDATE metadata_publications SET selection_revision=3 WHERE id=?`, rsAlbumProviderPubID)
					mutated = true
					break
				}
			}
			if page.Complete {
				t.Fatal("changed accepted source completed")
			}
			if !page.Pending || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
				t.Fatal("partial output", i, page)
			}
		}
		if !mutated {
			t.Fatal("header never hydrated")
		}
		// The next page must refuse the changed source rather than emit a card.
		q, req, out := liveRequests("artist-provider-stale-refusal")
		var page RSArtistAlbumPage
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
			var pageErr error
			page, _, pageErr = r.Page(ctx, req, time.Second, o, p)
			return pageErr
		})
		if !errors.Is(err, ErrStale) {
			t.Fatal("stale source not refused", err)
		}
		if len(page.Cards) != 0 || page.Total != 0 || page.Next != nil || page.Complete {
			t.Fatal("ready card escaped", page)
		}
	})
}
