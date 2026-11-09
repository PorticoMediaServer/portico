package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// ipSourceSeed is one legal accepted IP provider source on an embedded D/E
// schema: provider policy/consent, a live show subject, the applied operation,
// IP-sealed evidence with its receipt and build, and the selected accepted head.
// A second show has only a selected-but-pending observation to prove the read
// never falls back to it.
var ipSourceSeed = []string{
	`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1)`,
	`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled) VALUES(1,'tmdb',zeroblob(16),1,1,1,'US','modern','fill_missing',1)`,
	`INSERT INTO screen_metadata_consent(singleton,revision,confirmed,confirmed_at) VALUES(1,1,1,'2026-11-01T00:00:00Z')`,
	`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,1)`,
	`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2001,x'000000000000000000000000000007d1',1,2),(2002,x'000000000000000000000000000007d2',1,2)`,
	`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(2001,x'000000000000000000000000000007d1',1,2,'IP Show','ip show','IP Show','ip show','IP Show',2001,zeroblob(16),zeroblob(16)),(2002,x'000000000000000000000000000007d2',1,2,'Pending Show','pending show','Pending Show','pending show','Pending Show',2002,zeroblob(16),zeroblob(16))`,
	`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(1,'tmdb','series','tv-42','en','US','tmdb.series.detail.v1',zeroblob(16),1),(2,'tmdb','series','tv-99','en','US','tmdb.series.detail.v1',zeroblob(16),1)`,
	// op-1 is the applied accepted operation; op-2 is only staged/pending.
	`INSERT INTO metadata_provider_operations(id,entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,descriptive_revision,policy_incarnation,policy_revision,consent_revision,algorithm,refresh_mode,query_title,query_artist,query_year,query_digest,input_digest,result_digest,selected_id,manual,review_search,qualification,source_content_revision,created_at,command_incarnation) SELECT 'op-1',2001,'tmdb','applied','applied',1,'lease','2027-01-01T00:00:00Z',metadata_incarnation,1,1,1,1,1,1,zeroblob(16),1,1,'modern','fill_missing','','',0,'qd','id','rd','sel',0,0,'q',1,'2026-11-01T00:00:00Z',zeroblob(16) FROM catalog_entities WHERE id=2001`,
	`INSERT INTO metadata_provider_operations(id,entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,descriptive_revision,policy_incarnation,policy_revision,consent_revision,algorithm,refresh_mode,query_title,query_artist,query_year,query_digest,input_digest,result_digest,selected_id,manual,review_search,qualification,source_content_revision,created_at,command_incarnation) SELECT 'op-2',2002,'tmdb','staged','staged',1,'lease','2027-01-01T00:00:00Z',metadata_incarnation,1,1,1,1,1,1,zeroblob(16),1,1,'modern','fill_missing','','',0,'qd','id','rd','sel',0,0,'q',1,'2026-11-01T00:00:00Z',zeroblob(16) FROM catalog_entities WHERE id=2002`,
	`INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,title,sealed) VALUES('ev-1',1,'tmdb.series.v1','tmdb','series','tv-42','en','US','tmdb.series.detail.v1',printf('%064d',1),'2026-11-01T00:00:00Z','https://example.invalid/1','Show',1)`,
	`INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,title,sealed) VALUES('ev-2',2,'tmdb.series.v1','tmdb','series','tv-99','en','US','tmdb.series.detail.v1',printf('%064d',2),'2026-11-01T00:00:00Z','https://example.invalid/2','Pending',0)`,
	`INSERT INTO metadata_evidence_builds(id,evidence_id,generation,owner_operation,owner_incarnation,owner_generation,state,phase,cursor,expected_rows,materialized_rows,expected_shape_digest,materialized_digest,materialized_charge,expires_at,content_revision) VALUES('build-1','ev-1',1,'op-1',zeroblob(16),1,'ready','done',0,1,1,printf('%064d',3),printf('%064d',4),0,'2027-01-01T00:00:00Z',1)`,
	`INSERT INTO metadata_evidence_builds(id,evidence_id,generation,owner_operation,owner_incarnation,owner_generation,state,phase,cursor,expected_rows,materialized_rows,expected_shape_digest,materialized_digest,materialized_charge,expires_at,content_revision) VALUES('build-2','ev-2',1,'op-2',zeroblob(16),1,'ready','done',0,1,1,printf('%064d',5),printf('%064d',6),0,'2027-01-01T00:00:00Z',1)`,
	`INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,evidence_id,evidence_subject_id,parser_version,build_id,entity_id,provider,evidence_subject_kind,evidence_provider_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes) VALUES('op-1',zeroblob(16),1,randomblob(16),randomblob(32),'evidence_seal',1,1,randomblob(32),'sealed',0,-1,'',1,1,0,'ev-1',1,'tmdb.series.v1','build-1',2001,'tmdb','series','tv-42','sha256',1,randomblob(32),1,1)`,
	`UPDATE metadata_evidence SET ip_seal_owner='op-1',ip_seal_incarnation=zeroblob(16),ip_seal_sequence=1 WHERE id='ev-1'`,
	`INSERT INTO metadata_publications(id,operation_id,entity_id,provider,subject_kind,provider_id,evidence_id,selection_revision,generation,decision,observed_at,ip_proof_owner,ip_proof_incarnation,ip_proof_sequence) VALUES('pub-1','op-1',2001,'tmdb','series','tv-42','ev-1',1,1,'accepted','2026-11-01T00:00:00Z','op-1',zeroblob(16),1)`,
	`INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,requested_id,accepted_evidence_id,accepted_publication_id,status,observed_at) VALUES(2001,'tmdb','primary',zeroblob(16),1,'series','tv-42','tv-42','ev-1','pub-1','selected','2026-11-01T00:00:00Z')`,
	// A selected head with no accepted publication is pending, never accepted.
	`INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,requested_id,accepted_evidence_id,status,observed_at) VALUES(2002,'tmdb','primary',zeroblob(16),1,'series','tv-99','tv-99','ev-2','selected','2026-11-01T00:00:00Z')`,
	`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(printf('%064d',7),'image/jpeg',600,900,1234,'2026-11-01T00:00:00Z'),(printf('%064d',8),'image/png',1920,1080,5678,'2026-11-01T00:00:00Z')`,
	`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(printf('%064d',7),'image/jpeg',600,900,printf('%064d',7),printf('%064d',8),printf('%064d',7),1280,0,0,3,'2026-11-01T00:00:00Z')`,
	`INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,actor,observed_at,revision) VALUES('entity',2001,'poster','matched',printf('%064d',7),'tmdb','img-1','provider','credit','en','en','fence-1','owner','2026-11-01T00:00:00Z',2)`,
	`INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at,revision) VALUES('entity',2001,'backdrop','locked',NULL,'','','','','fence-2','owner','2026-11-01T00:00:00Z',3)`,
	`INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at,revision) VALUES('entity',2001,'logo','matched',printf('%064d',7),'tmdb','img-2','provider','credit','fence-1','owner','2026-11-01T00:00:00Z',1)`,
	`INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at,revision) VALUES('entity',2001,'logo','locked',printf('%064d',7),'owner','img-3','owner','','fence-3','owner','2026-11-01T00:00:00Z',2)`,
}

func ipSourceSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	for _, q := range ipSourceSeed {
		f.exec(t, q)
	}
	return f
}

func ipSourceFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := composeBrowseFixture(t, ipSourceSource(t), append(browseDefinitions(), append(IPSourceHarnessDefinitions(), IPHistoryHarnessDefinitions()...)...))
	return f, &ForegroundReader{snapshots: f.snapshots, pool: f.snapshots}
}

func ipSourceView(f *browseFixture) BrowseInput {
	v := f.input
	v.Kind = 2
	return v
}

func ipSourceInput(f *browseFixture) IPSourceInput {
	return IPSourceInput{View: ipSourceView(f), Entity: 2001, Provider: "tmdb", Role: "primary"}
}

func TestIPProviderSourceAccepted(t *testing.T) {
	f, r := ipSourceFixture(t)
	got, _, err := r.ipProviderSource(t.Context(), foregroundRequest(), time.Second, f.output, ipSourceInput(f))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Present || got.Entity.ID != 2001 || got.Entity.Title != "IP Show" || got.Entity.Revision != 1 ||
		got.Provider != "tmdb" || got.Role != "primary" || got.SubjectKind != "series" || got.ProviderID != "tv-42" ||
		got.HeadRevision != 1 || got.Publication.ID != "pub-1" || got.Publication.OperationID != "op-1" || got.Publication.SelectionRevision != 1 ||
		got.Evidence.ID != "ev-1" || !got.Evidence.Sealed || !got.Evidence.ProofSealed || got.Evidence.Parser != "tmdb.series.v1" ||
		got.Build.ID != "build-1" || got.Build.OwnerOperation != "op-1" || got.Build.ContentRevision != 1 ||
		!got.Policy.Enabled || !got.Policy.ProviderEnabled || !got.Policy.ScreenConfirmed || got.Policy.Algorithm != "modern" || got.Policy.RefreshMode != "fill_missing" || got.Policy.Region != "US" {
		t.Fatalf("accepted source facts: %+v", got)
	}
	// A read never mutates the accepted source.
	if _, _, err = r.ipProviderSource(t.Context(), foregroundRequest(), time.Second, f.output, ipSourceInput(f)); err != nil {
		t.Fatal("second read", err)
	}
}

// A selected but unaccepted observation is never an accepted fallback.
func TestIPProviderSourcePendingNeverAccepted(t *testing.T) {
	f, r := ipSourceFixture(t)
	in := ipSourceInput(f)
	in.Entity = 2002
	got, _, err := r.ipProviderSource(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Present || got.Entity.ID != 0 || got.Entity.Title != "" || got.Evidence.ID != "" || got.Publication.ID != "" || got.Build.ID != "" {
		t.Fatalf("pending source escaped as accepted: %+v", got)
	}
	if !got.Policy.Enabled || got.Policy.Algorithm != "modern" {
		t.Fatalf("absent source must keep the current policy witness: %+v", got.Policy)
	}
}

// Acceptance advances the subject's metadata revision; a later revision is not a
// freshness gate and must not hide the already accepted source.
func TestIPProviderSourcePostAcceptRevision(t *testing.T) {
	f, r := ipSourceFixture(t)
	f.exec(t, `UPDATE catalog_entities SET metadata_revision=9, source_revision=4, identity_revision=3 WHERE id=2001`)
	got, _, err := r.ipProviderSource(t.Context(), foregroundRequest(), time.Second, f.output, ipSourceInput(f))
	if err != nil || !got.Present || got.Evidence.ID != "ev-1" {
		t.Fatal("post-accept revision hid the accepted source", got, err)
	}
}

// A later policy revision is also not a freshness gate; only a disabled provider
// or revoked provider-specific consent makes the source ineligible.
func TestIPProviderSourcePolicyAdvance(t *testing.T) {
	f, r := ipSourceFixture(t)
	f.exec(t, `UPDATE metadata_provider_policy SET revision=5 WHERE library_id=1 AND provider='tmdb'`)
	got, _, err := r.ipProviderSource(t.Context(), foregroundRequest(), time.Second, f.output, ipSourceInput(f))
	if err != nil || !got.Present || !got.Policy.Enabled || got.Policy.Revision != 5 {
		t.Fatal("policy advance hid the accepted source", got, err)
	}
}

func TestIPProviderSourceIneligible(t *testing.T) {
	for name, mutation := range map[string]string{
		"provider disabled": `UPDATE metadata_providers SET enabled=0 WHERE code='tmdb'`,
		"policy disabled":   `UPDATE metadata_provider_policy SET enabled=0 WHERE library_id=1 AND provider='tmdb'`,
		"consent revoked":   `UPDATE screen_metadata_consent SET confirmed=0`,
	} {
		t.Run(name, func(t *testing.T) {
			f, r := ipSourceFixture(t)
			f.exec(t, mutation)
			got, _, err := r.ipProviderSource(t.Context(), foregroundRequest(), time.Second, f.output, ipSourceInput(f))
			if err != nil || got.Present {
				t.Fatal("ineligible source", got, err)
			}
		})
	}
}

type ipBoundary struct {
	*governor.Snapshots
	phase  int
	mutate func(int)
}

func (s *ipBoundary) Read(ctx context.Context, r governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.phase++
	phase := s.phase
	return s.Snapshots.Read(ctx, r, budget, func(scope *governor.ReadScope) error { e := read(scope); s.mutate(phase); return e })
}

func (s *ipBoundary) ReadForecasted(ctx context.Context, r governor.Request, budget time.Duration, f governor.SnapshotForecast, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.phase++
	phase := s.phase
	return s.Snapshots.ReadForecasted(ctx, r, budget, f, func(scope *governor.ReadScope) error { e := read(scope); s.mutate(phase); return e })
}

// A forged or incoherent accepted tuple yields honest absence, never a pending
// or partial fallback. A policy fence ahead of the current policy is a refusal.
func TestIPProviderSourceForgedTuple(t *testing.T) {
	for name, tc := range map[string]struct {
		mutations []string
		present   bool
		refused   bool
	}{
		"decision":      {[]string{`UPDATE metadata_publications SET decision='superseded' WHERE id='pub-1'`}, false, false},
		"selection":     {[]string{`UPDATE metadata_publications SET selection_revision=2 WHERE id='pub-1'`}, false, false},
		"status":        {[]string{`UPDATE metadata_subject_providers SET status='pending' WHERE entity_id=2001`}, false, false},
		"operation":     {[]string{`UPDATE metadata_provider_operations SET status='staged' WHERE id='op-1'`}, false, false},
		"unseal":        {[]string{`UPDATE metadata_publications SET ip_proof_owner=NULL,ip_proof_incarnation=NULL,ip_proof_sequence=NULL WHERE id='pub-1'`, `UPDATE metadata_evidence SET ip_seal_owner=NULL,ip_seal_incarnation=NULL,ip_seal_sequence=NULL,sealed=0 WHERE id='ev-1'`, `DELETE FROM metadata_ip_receipts`}, false, false},
		"build removal": {[]string{`UPDATE metadata_publications SET ip_proof_owner=NULL,ip_proof_incarnation=NULL,ip_proof_sequence=NULL WHERE id='pub-1'`, `UPDATE metadata_evidence SET ip_seal_owner=NULL,ip_seal_incarnation=NULL,ip_seal_sequence=NULL,sealed=0 WHERE id='ev-1'`, `DELETE FROM metadata_ip_receipts`, `DELETE FROM metadata_evidence_builds WHERE id='build-1'`}, false, false},
		"policy ahead":  {[]string{`UPDATE metadata_provider_operations SET policy_revision=7 WHERE id='op-1'`}, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			f, r := ipSourceFixture(t)
			for _, mutation := range tc.mutations {
				f.exec(t, mutation)
			}
			got, _, err := r.ipProviderSource(t.Context(), foregroundRequest(), time.Second, f.output, ipSourceInput(f))
			if tc.refused {
				if !errors.Is(err, ErrUnqualified) || got.Present {
					t.Fatal("forged policy fence escaped", got, err)
				}
				return
			}
			if err != nil || got.Present {
				t.Fatal("forged head escaped", got, err)
			}
		})
	}
}

// A changed accepted head between the business window and fresh publication
// retries once and serves the new facts; persistent drift refuses.
func TestIPProviderSourcePublicationDrift(t *testing.T) {
	f, r := ipSourceFixture(t)
	r.snapshots = &ipBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
		if phase == 2 {
			f.exec(t, `UPDATE catalog_entities SET title='Renamed Show' WHERE id=2001`)
		}
	}}
	got, result, err := r.ipProviderSource(t.Context(), foregroundRequest(), time.Second, f.output, ipSourceInput(f))
	if err != nil || !got.Present || got.Entity.Title != "Renamed Show" || result.Attempts != 2 {
		t.Fatalf("retry title=%q attempts=%d err=%v", got.Entity.Title, result.Attempts, err)
	}
	f, r = ipSourceFixture(t)
	r.snapshots = &ipBoundary{Snapshots: f.snapshots, mutate: func(int) {
		f.exec(t, `UPDATE catalog_entities SET revision=revision+1 WHERE id=2001`)
	}}
	got, result, err = r.ipProviderSource(t.Context(), foregroundRequest(), time.Second, f.output, ipSourceInput(f))
	if !errors.Is(err, ErrStale) || !errors.Is(err, governor.ErrBusy) || got.Present || result.Attempts != 2 {
		t.Fatal("persistent drift must refuse", got, result, err)
	}
}

func TestIPProviderSourceRefusals(t *testing.T) {
	var nilReader *ForegroundReader
	if _, _, err := nilReader.IPProviderSource(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, IPSourceInput{}); !errors.Is(err, ErrInput) {
		t.Fatal("nil reader", err)
	}
	r := &ForegroundReader{}
	for name, in := range map[string]IPSourceInput{
		"zero entity":   {View: BrowseInput{Library: 1, Kind: 2}, Provider: "tmdb", Role: "primary"},
		"zero library":  {View: BrowseInput{Kind: 2}, Entity: 1, Provider: "tmdb", Role: "primary"},
		"movie":         {View: BrowseInput{Library: 1, Kind: 1}, Entity: 1, Provider: "tmdb", Role: "primary"},
		"provider":      {View: BrowseInput{Library: 1, Kind: 2}, Entity: 1, Provider: "mb", Role: "primary"},
		"role":          {View: BrowseInput{Library: 1, Kind: 2}, Entity: 1, Provider: "tmdb", Role: "release_track"},
		"descending":    {View: BrowseInput{Library: 1, Kind: 2, Descending: true}, Entity: 1, Provider: "tmdb", Role: "primary"},
		"keep articles": {View: BrowseInput{Library: 1, Kind: 2, KeepArticles: true}, Entity: 1, Provider: "tmdb", Role: "primary"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := r.ipProviderSource(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrInput) {
				t.Fatal(err)
			}
		})
	}
}

func TestIPSourceCancelledLifetime(t *testing.T) {
	f, r := ipSourceFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, _, err := r.ipProviderSource(ctx, foregroundRequest(), time.Second, f.output, ipSourceInput(f)); err == nil || got.Present {
		t.Fatal("cancelled source read", got, err)
	}
	if got, _, err := r.ipArtworkMaterial(ctx, foregroundRequest(), time.Second, f.output, IPMaterialInput{View: ipSourceView(f), Entity: 2001, Role: "poster"}); err == nil || got.Present {
		t.Fatal("cancelled material read", got, err)
	}
}

func TestIPArtworkMaterialChoices(t *testing.T) {
	f, r := ipSourceFixture(t)
	in := IPMaterialInput{View: ipSourceView(f), Entity: 2001, Role: "poster"}
	got, _, err := r.ipArtworkMaterial(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || !got.Present || got.Locked || got.Choice != "matched" || got.SelectionRevision != 2 || got.SourceRevision != 1 ||
		got.Small.Digest != fmt.Sprintf("%064d", 7) || got.Large.Digest != fmt.Sprintf("%064d", 8) || got.Small.Status != "ready" || got.Provider != "tmdb" || got.ImageID != "img-1" {
		t.Fatalf("matched material %+v err=%v", got, err)
	}
	in.Role = "backdrop"
	got, _, err = r.ipArtworkMaterial(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || got.Present || !got.Locked || got.Choice != "locked" || got.Small.Digest != "" {
		t.Fatalf("locked clear %+v err=%v", got, err)
	}
	in.Role = "logo"
	got, _, err = r.ipArtworkMaterial(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || !got.Present || !got.Locked {
		t.Fatalf("owner lock must win over matched: %+v err=%v", got, err)
	}
	in.Role = "banner"
	got, _, err = r.ipArtworkMaterial(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || got.Present || got.Locked {
		t.Fatalf("absent material %+v err=%v", got, err)
	}
}

func TestIPArtworkMaterialScope(t *testing.T) {
	f, r := ipSourceFixture(t)
	in := IPMaterialInput{View: ipSourceView(f), Entity: 2001, Role: "poster"}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2001`)
	if _, _, err := r.ipArtworkMaterial(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrDenied) {
		t.Fatal("retired subject", err)
	}
	f, r = ipSourceFixture(t)
	f.exec(t, `UPDATE artwork_sources SET retiring=1,retirement_token=randomblob(16) WHERE digest=printf('%064d',7)`)
	if _, _, err := r.ipArtworkMaterial(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrUnqualified) {
		t.Fatal("retiring source", err)
	}
	f, r = ipSourceFixture(t)
	f.exec(t, `UPDATE artwork_blobs SET status='deleting',deletion_token=randomblob(16) WHERE digest=printf('%064d',8)`)
	if _, _, err := r.ipArtworkMaterial(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrUnqualified) {
		t.Fatal("deleting blob", err)
	}
}

func TestIPArtworkMaterialDrift(t *testing.T) {
	f, r := ipSourceFixture(t)
	r.snapshots = &ipBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
		if phase == 2 {
			f.exec(t, `UPDATE artwork_selections SET revision=9 WHERE kind='entity' AND entity_id=2001 AND role='poster' AND choice='matched'`)
		}
	}}
	got, result, err := r.ipArtworkMaterial(t.Context(), foregroundRequest(), time.Second, f.output, IPMaterialInput{View: ipSourceView(f), Entity: 2001, Role: "poster"})
	if err != nil || !got.Present || got.SelectionRevision != 9 || result.Attempts != 2 {
		t.Fatalf("material retry %+v attempts=%d err=%v", got, result.Attempts, err)
	}
}

func TestIPArtworkMaterialRefusals(t *testing.T) {
	var nilReader *ForegroundReader
	if _, _, err := nilReader.IPArtworkMaterial(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, IPMaterialInput{}); !errors.Is(err, ErrInput) {
		t.Fatal("nil reader", err)
	}
	r := &ForegroundReader{}
	for name, in := range map[string]IPMaterialInput{
		"zero entity":  {View: BrowseInput{Library: 1, Kind: 2}, Role: "poster"},
		"zero library": {View: BrowseInput{Kind: 2}, Entity: 1, Role: "poster"},
		"bad role":     {View: BrowseInput{Library: 1, Kind: 2}, Entity: 1, Role: "thumbnail"},
		"descending":   {View: BrowseInput{Library: 1, Kind: 2, Descending: true}, Entity: 1, Role: "poster"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := r.ipArtworkMaterial(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrInput) {
				t.Fatal(err)
			}
		})
	}
}

// The fixture's shared foreground source has a poster selection for entity 1;
// the plan cohorts are bound to that accepted schema shape.
func ipSourcePlanFixtureHash(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.ForegroundHash(t)+"|ip-source|"+fmt.Sprint(len(ipSourceSeed)))))
}
