package writer

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// The MusicBrainz fixture mirrors the recorded release the provider adapter
// normalizes (`providers/testdata/musicbrainz_release.json`): one medium, two
// tracks with their own pre-staged recording headers, release- and
// track-scoped artist credits, and the language/region-independent empty locale
// the adapter stages. The writer is provider-agnostic; these rows only exercise
// the evidence shape the adapter emits.
const (
	ipMBReleaseMBID    = "b1a9c0fe-9c2e-4f5a-9d1a-2f7f3b6c1d20"
	ipMBTrackOneMBID   = "d5c2c3a4-3f40-4a51-8b1c-2d3e4f506172"
	ipMBTrackTwoMBID   = "a8f5f6d7-6273-4d84-9e4f-506172839405"
	ipMBRecordingOne   = "f7e4e5c6-5162-4c73-8d3e-4f5061728394"
	ipMBRecordingTwo   = "b9a6a7e8-7384-4e95-8f50-617283940516"
	ipMBArtistMBID     = "b3a0a1e2-1d2c-4d3e-8f9a-0b1c2d3e4f50"
	ipMBFeaturedMBID   = "e6d3d4b5-4051-4b62-9c2d-3e4f50617283"
	ipMBReleaseSubject = 1
	ipMBRecordOneSub   = 21
	ipMBRecordTwoSub   = 22
)

type ipEntityMBContent struct {
	trackTitle string
	mediaTitle string
	alias      string
	rating     float64
}

func ipEntityMBDefaultContent() ipEntityMBContent {
	return ipEntityMBContent{trackTitle: "Safe from Harm", mediaTitle: "", alias: "Collected (remaster)", rating: 4}
}

func int64Pointer(value int64) *int64 { return &value }

func ipEntityMBHeader(evidenceID string) EvidenceHeaderInput {
	parser, shape := ipEntityEvidencePair("musicbrainz", "release")
	// The adapter never sets Language or Region: MusicBrainz observations are
	// language/region independent.
	return EvidenceHeaderInput{ID: evidenceID, SubjectID: ipMBReleaseSubject, ParserVersion: parser,
		Provider: "musicbrainz", SubjectKind: "release", ProviderID: ipMBReleaseMBID,
		RequestShape: shape, ObservationDigest: ipEntityEvidenceDigest(evidenceID),
		ObservedAt: "2026-01-01T00:00:00Z",
		SourceURL:  "https://musicbrainz.org/ws/2/release/" + ipMBReleaseMBID,
		Title:      "Collected", Artist: "Massive Attack", ReleaseDate: "1991-04-08", Year: 1991,
		Country: "GB", Barcode: "0077778622821"}
}

func ipEntityMBRecordingHeader(evidenceID, recordingID string, subjectID int64) EvidenceHeaderInput {
	parser, shape := ipEntityEvidencePair("musicbrainz", "recording")
	return EvidenceHeaderInput{ID: evidenceID, SubjectID: subjectID, ParserVersion: parser,
		Provider: "musicbrainz", SubjectKind: "recording", ProviderID: recordingID,
		RequestShape: shape, ObservationDigest: ipEntityEvidenceDigest(evidenceID),
		ObservedAt: "2026-01-01T00:00:00Z",
		SourceURL:  "https://musicbrainz.org/ws/2/recording/" + recordingID,
		Title:      "Safe from Harm", Artist: "Massive Attack feat. Shara Nelson",
		Disambiguation: "album version", LengthMillis: EvidenceInteger{Present: true, Value: 341000}}
}

// ipEntityMBPages is the complete adapter-shaped release cohort in the closed
// family order. Every non-credit family is parameterized so a delta test can
// change exactly one row.
func ipEntityMBPages(c ipEntityMBContent) []ipEntitySealPage {
	return []ipEntitySealPage{
		{Family: "alias", AfterOrdinal: 0, rows: 1, aliases: []ipEvidenceAlias{
			{Ordinal: 0, RequestedID: "requested-release", CanonicalID: ipMBReleaseMBID, Name: c.alias},
		}},
		{Family: "term", AfterOrdinal: 0, rows: 1, terms: []ipEvidenceTerm{
			{Ordinal: 0, Vocabulary: "genre", Value: "Drama"},
		}},
		{Family: "rating", AfterOrdinal: 0, rows: 1, ratings: []ipEvidenceRating{
			{Role: "user", Value: c.rating, Scale: 10, Votes: 5,
				SourceURL: "https://musicbrainz.org/ws/2/release/" + ipMBReleaseMBID},
		}},
		{Family: "media", AfterOrdinal: 0, rows: 1, media: []ipEvidenceMedia{
			{Position: 1, Format: "CD", Title: c.mediaTitle},
		}},
		{Family: "track", AfterOrdinal: 0, rows: 2, tracks: []ipEvidenceTrack{
			{TrackID: ipMBTrackOneMBID, MediumPosition: 1, Position: 1, Number: "1",
				Title: c.trackTitle, Artist: "Massive Attack feat. Shara Nelson",
				LengthMillis:        int64Pointer(341000),
				RecordingEvidenceID: "rec-evidence-one",
				Recording:           ipEntityMBRecordingHeader("rec-evidence-one", ipMBRecordingOne, ipMBRecordOneSub)},
			{TrackID: ipMBTrackTwoMBID, MediumPosition: 1, Position: 2, Number: "2",
				Title: "Unfinished Sympathy", Artist: "Massive Attack",
				LengthMillis:        int64Pointer(0),
				RecordingEvidenceID: "rec-evidence-two",
				Recording:           ipEntityMBRecordingHeader("rec-evidence-two", ipMBRecordingTwo, ipMBRecordTwoSub)},
		}},
		{Family: "credit", AfterOrdinal: 0, rows: 3, credits: []ipEvidenceCredit{
			{Ordinal: 0, Class: "artist", ProviderPersonID: ipMBArtistMBID, Name: "Massive Attack",
				ArtistName: "Massive Attack", SortName: "Massive Attack", CreditID: "rel-credit-one",
				SourceOrdinal: 0, SourceOrdinalPresent: true},
			{Ordinal: 1, Class: "artist", Scope: ipMBTrackOneMBID, ProviderPersonID: ipMBFeaturedMBID,
				Name: "Shara Nelson", ArtistName: "Shara Nelson", SortName: "Nelson, Shara",
				Disambiguation: "British singer", JoinPhrase: " feat. ", CreditID: "trk-credit-one",
				SourceOrdinal: 1, SourceOrdinalPresent: true},
			{Ordinal: 2, Class: "artist", Scope: ipMBTrackOneMBID, ProviderPersonID: ipMBArtistMBID,
				Name: "Massive Attack", ArtistName: "Massive Attack", SortName: "Massive Attack",
				CreditID: "trk-credit-two", SourceOrdinal: 2, SourceOrdinalPresent: true},
		}},
	}
}

// ipEntityMBArtistCohort builds n release-scoped artist credits split into
// sealed pages at the IP-06 credit page bound. IP-06 allows 1024 evidence
// credits; 200 is well inside that bound but far above the old 64-row accept cap.
func ipEntityMBArtistCohort(n int) []ipEntitySealPage {
	rows := make([]ipEvidenceCredit, n)
	for i := range rows {
		person := fmt.Sprintf("artist-%03d", i)
		creditID := fmt.Sprintf("credit-%03d", i)
		rows[i] = ipEvidenceCredit{Ordinal: int64(i), Class: "artist", ProviderPersonID: person,
			CreditID: creditID, Name: fmt.Sprintf("Artist %d", i),
			SourceOrdinal: int64(i), SourceOrdinalPresent: true}
	}
	var pages []ipEntitySealPage
	for start := 0; start < n; start += 64 {
		end := min(start+64, n)
		pages = append(pages, ipEntitySealPage{Family: "credit", AfterOrdinal: int64(start),
			rows: int64(end - start), credits: rows[start:end]})
	}
	return pages
}

func ipEntityMBFixture(t *testing.T, pages []ipEntitySealPage) (*executor, *ipPolicyRegistry,
	*usOwnerAuthorityProof, ipEntitySealStaged) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	for _, name := range []string{
		"testdata/ip-policy-draft.sql",
		"testdata/ip-entity-evidence-draft.sql",
		"testdata/ip-entity-seal-draft.sql",
		"testdata/ip-tv-set-draft.sql",
		"testdata/ip-entity-accept-draft.sql",
	} {
		ddl, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
			t.Fatal(name, err)
		}
	}
	incarnation := ipEntityJobIncarnation(ipMBReleaseSubject)
	ipPolicyExec(t, w, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(2,'show',1,0,1,1,0),(5,'artist',0,0,1,1,1),(6,'album',0,0,1,1,1)`)
	ipPolicyExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`)
	ipPolicyExec(t, w, `INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`)
	ipPolicyExec(t, w, `INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider,command_incarnation) VALUES(777,'musicbrainz',X'0a000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'tmdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000')`)
	ipPolicyExec(t, w, `INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`)
	ipPolicyExec(t, w, `INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`)
	binaryID := make([]byte, 16)
	binaryID[0] = 0x33
	artistID := make([]byte, 16)
	artistID[0] = 0x34
	artistIncarnation := ipEntityJobIncarnation(2)
	ipPolicyExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(1,?,777,6)`, binaryID)
	ipPolicyExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2,?,777,5)`, artistID)
	ipPolicyExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(2,?,777,5,'Massive Attack','massive attack','m','massive attack','m',1,?,?,1,1,1)`,
		artistID, artistIncarnation[:], artistIncarnation[:])
	ipPolicyExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision,artist_id) VALUES(1,?,777,6,'MB Entity','mb entity','m','mb entity','m',1,?,?,1,1,1,2)`,
		binaryID, incarnation[:], incarnation[:])
	ipPolicyExec(t, w, `INSERT INTO metadata_acquisition_jobs(entity_id,provider,purpose,generation,revision,status,selected_id,manual,review_search,query_override,year_override,force_refresh,episode_order,parent_publication,attempts,next_attempt_ms,lease,lease_until_ms,error,page,after_item,staged_bytes,observed_at) VALUES(1,'musicbrainz','metadata',1,1,'pending',?,0,0,'',0,0,'','',0,0,'',0,'',0,'',0,'2026-01-01T00:00:00.000000000Z')`, ipMBReleaseMBID)
	subjectIncarnation := make([]byte, 16)
	subjectIncarnation[0] = 0x2a
	for _, row := range []struct {
		id         int64
		kind       string
		providerID string
	}{
		{ipMBReleaseSubject, "release", ipMBReleaseMBID},
		{ipMBRecordOneSub, "recording", ipMBRecordingOne},
		{ipMBRecordTwoSub, "recording", ipMBRecordingTwo},
	} {
		_, shape := ipEntityEvidencePair("musicbrainz", row.kind)
		// The adapter stages the empty locale; the subject binding must agree.
		ipPolicyExec(t, w, `INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(?, 'musicbrainz', ?, ?, '', '', ?, ?, 1)`,
			row.id, row.kind, row.providerID, shape, subjectIncarnation)
	}
	defs := append(append(append(append(append(ipPolicyDefinitions(), ipEntityJobDefinitions()...),
		ipEntityEvidenceDefinitions()...), ipEntitySealDefinitions()...), ipTVSetDefinitions()...),
		ipEntityAcceptDefinitions()...)
	defs = append(defs, ipEntityDeltaDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	r := &ipPolicyRegistry{registry, keeper}

	identity := ipEntityJobIdentity(0x21, 1, 0x31)
	q := ipEntityJobClaimFor(1, "musicbrainz")
	q.selectedID, q.consentRevision = ipMBReleaseMBID, 3
	c, err := newIPEntityClaimCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	out, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, c)
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, out.Value, 1)
	owner := out.Value

	staged, _ := ipEntityMBStage(t, w, r, p, owner, identity, 0, "evidence-one", "build-one",
		ipEntityMBHeader("evidence-one"), pages, 2)
	return w, r, p, staged
}

// ipEntityMBStage stages, seals and ACKs one complete MusicBrainz evidence
// chain with the exact empty locale. salt 0 keeps the shared evidence identity;
// a non-zero salt gives a second chain in the same fixture its own globally
// unique operation IDs.
func ipEntityMBStage(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	owner ipEntityJobResult, claim CommandIdentity, salt byte, evidenceID, buildID string,
	header EvidenceHeaderInput, pages []ipEntitySealPage, startSequence int64) (ipEntitySealStaged, int64) {
	t.Helper()
	identity := func(sequence int64) CommandIdentity {
		if salt == 0 {
			return ipEntityEvidenceIdentity(claim, sequence)
		}
		return ipEntityEvidenceChainIdentity(claim, sequence, salt)
	}
	declarations := ipEntitySealDeclarations(pages)
	counts := ipEntitySealDeclaredCounts(declarations)
	bq := ipEntityEvidenceBeginFor(owner, evidenceID, buildID, counts)
	bq.header = header
	bq.families = declarations
	sequence := startSequence
	first, turn, tx := ipEntityEvidenceApplyBegin(t, w, r, p,
		mustEvidenceBegin(t, identity(sequence), bq))
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityEvidenceAck(t, w, first.Value, sequence)
	sequence++
	current, before := first.Value, int64(1)
	for _, page := range pages {
		familyStart := before - page.AfterOrdinal
		request := ipEntitySealPageRequest(owner, current, page, counts[page.Family], familyStart, before)
		out, turn, tx := ipEntityEvidenceApplyAppend(t, w, r, p,
			mustEvidenceAppend(t, identity(sequence), request))
		ipPolicyCommitTurn(t, turn, tx)
		ipEntityEvidenceAck(t, w, out.Value, sequence)
		current = out.Value
		before += page.rows
		sequence++
	}
	seal := ipEntitySealRequestFor(owner, current, declarations, pages)
	seal.language, seal.region = header.Language, header.Region
	seal.observationDigest = header.ObservationDigest
	staged := ipEntitySealStaged{owner: owner, claim: claim, pages: pages, declarations: declarations,
		begin: current, sequence: sequence, seal: seal}
	command, err := newIPEntitySealCommand(identity(sequence), seal)
	if err != nil {
		t.Fatal(err)
	}
	sealed, turn, tx := ipEntitySealApply(t, w, r, p, command)
	if sealed.Outcome.State != Applied {
		t.Fatal(sealed)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, ipEntityJobResult{OperationID: owner.OperationID, Provider: owner.Provider}, sequence)
	return staged, sequence
}

// ipEntityMBAcceptRequest projects the exact sealed cohort into the typed accept
// request. Terms and credits are derived from the sealed pages so the request
// can never drift from the evidence it must prove.
func ipEntityMBAcceptRequest(staged ipEntitySealStaged, publicationID string,
	selectionRevision int64) ipEntityAcceptRequest {
	q := ipEntityAcceptRequest{
		operationID: staged.owner.OperationID, provider: staged.owner.Provider,
		purpose: "metadata", selectedID: staged.owner.SelectedID, lease: staged.owner.Lease,
		entityID: staged.owner.EntityID, generation: 1, leaseUntilMS: staged.owner.LeaseUntilMS,
		evidenceID: staged.seal.evidenceID, buildID: staged.seal.buildID,
		publicationID: publicationID, buildGeneration: 1, contentRevision: staged.begin.Revision + 1,
		subjectID: ipMBReleaseSubject, subjectKind: "release", providerID: ipMBReleaseMBID,
		parserVersion: ipEntityEvidenceParser("musicbrainz", "release"), language: "", region: "",
		requestShape:         ipEntityEvidenceRequest("musicbrainz", "release"),
		observedAt:           "2026-01-01T00:00:00Z",
		selectionIncarnation: [16]byte{0x0d}, selectionRevision: selectionRevision, searchJobID: 1,
		fields: []ipEntityAcceptField{
			{Name: "title", Value: "Collected", Present: true},
			{Name: "language", Value: "en", Present: true},
			{Name: "releaseDate", Value: "1991-04-08", Present: true},
			{Name: "year", Value: "1991", Present: true},
		},
	}
	for _, page := range staged.pages {
		for _, row := range page.terms {
			q.terms = append(q.terms, ipEntityAcceptTerm{Vocabulary: row.Vocabulary,
				Ordinal: row.Ordinal, Value: row.Value})
		}
		for _, row := range page.external {
			q.external = append(q.external, ipEntityAcceptExternal{Provider: row.Provider,
				SubjectKind: row.SubjectKind, ProviderID: row.ProviderID})
		}
		for _, row := range page.credits {
			q.credits = append(q.credits, ipEntityAcceptCredit{Class: row.Class, Scope: row.Scope,
				ProviderPersonID: row.ProviderPersonID, CreditID: row.CreditID, Name: row.Name,
				Role: row.Role, Department: row.Department, Ordinal: row.Ordinal,
				Billing: row.Billing, BillingPresent: row.BillingPresent,
				SourceOrdinal: row.SourceOrdinal, SourceOrdinalPresent: row.SourceOrdinalPresent})
		}
	}
	return q
}

// ipEntityMBSeedAcceptedHead seeds the IA accepted-subject seam and search
// intent the accept proves; the owner fields stay absent because this fixture is
// a release with no descriptive owner rows.
func ipEntityMBSeedAcceptedHead(t *testing.T, w *executor) {
	t.Helper()
	ipPolicyExec(t, w, `INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,status,observed_at) VALUES(1,'musicbrainz','primary',X'0d000000000000000000000000000000',1,'release',?,'selected','2025-12-01T00:00:00Z')`, ipMBReleaseMBID)
	ipPolicyExec(t, w, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'bank-one',?,?,'unicode-fixture','active',0)`,
		searchpolicy.RecipeV1, searchpolicy.EngineVersion)
	ipPolicyExec(t, w, `INSERT INTO search_publish_jobs(id,entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,command_incarnation) VALUES(1,1,1,1,X'0e000000000000000000000000000000',1,?,'idle',X'0f000000000000000000000000000000')`,
		searchpolicy.RecipeV1)
}

func ipEntityMBAccept(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof,
	staged ipEntitySealStaged, publicationID string) ipEntityAcceptCommandResult {
	t.Helper()
	q := ipEntityMBAcceptRequest(staged, publicationID, 1)
	c, err := newIPEntityAcceptCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence+1), q)
	if err != nil {
		t.Fatal(err)
	}
	out, turn, tx := ipEntityAcceptApply(t, w, r, p, c)
	if out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	return out
}

// A full MusicBrainz release evidence — the adapter's empty locale plus the
// complete media/track/artist-credit families — begins, appends and SEALS; the
// same shape then accepts a release whose credits are all artist class, a
// 200-credit cohort, and refuses a cohort that no longer matches the sealed
// evidence.
func TestIPEntityMusicBrainzReleaseSealAndAccept(t *testing.T) {
	t.Run("adapter_shape_seals_with_empty_locale", func(t *testing.T) {
		w, _, _, staged := ipEntityMBFixture(t, ipEntityMBPages(ipEntityMBDefaultContent()))
		for query, want := range map[string]int64{
			`SELECT count(*) FROM metadata_evidence WHERE id='evidence-one' AND provider='musicbrainz' AND subject_kind='release' AND language='' AND region='' AND sealed=1`: 1,
			`SELECT count(*) FROM metadata_evidence_builds WHERE id='build-one' AND evidence_id='evidence-one' AND state='ready' AND phase='sealed'`:                          1,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='evidence_seal' AND evidence_id='evidence-one' AND build_id='build-one' AND terminal=1`:             1,
			`SELECT count(*) FROM metadata_evidence_media WHERE evidence_id='evidence-one' AND position=1 AND format='CD'`:                                                    1,
			`SELECT count(*) FROM metadata_evidence_tracks WHERE evidence_id='evidence-one'`:                                                                                  2,
			`SELECT count(*) FROM metadata_evidence_credits WHERE evidence_id='evidence-one' AND credit_class='artist'`:                                                       3,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
		if staged.seal.language != "" || staged.seal.region != "" {
			t.Fatal("seal request carried a locale", staged.seal.language, staged.seal.region)
		}
		// A fabricated/wrong locale or a foreign namespace on the same
		// MusicBrainz subject is refused, so the exception is bound to the exact
		// (provider, kind, parser, request shape) namespace.
		wrongLocale := staged.seal
		wrongLocale.language, wrongLocale.region = "en", "US"
		if c, err := newIPEntitySealCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence+1),
			wrongLocale); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("MusicBrainz non-empty locale admitted", c, err)
		}
		mixedNamespace := staged.seal
		_, mixedNamespace.requestShape = ipEntityEvidencePair("tmdb", "release")
		if c, err := newIPEntitySealCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence+1),
			mixedNamespace); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("mixed namespace admitted", c, err)
		}
	})
	t.Run("artist_credits_accept", func(t *testing.T) {
		pages := ipEntityMBArtistCohort(3)
		w, r, p, staged := ipEntityMBFixture(t, pages)
		ipEntityMBSeedAcceptedHead(t, w)
		out := ipEntityMBAccept(t, w, r, p, staged, "publication-one")
		if out.Value.CreditCount != 3 || out.Value.PublicationID != "publication-one" ||
			out.Value.SubjectKind != "release" || out.Value.ProviderID != ipMBReleaseMBID {
			t.Fatal(out)
		}
		for query, want := range map[string]int64{
			`SELECT count(*) FROM metadata_publications WHERE id='publication-one' AND provider='musicbrainz' AND subject_kind='release' AND provider_id='` + ipMBReleaseMBID + `' AND evidence_id='evidence-one'`: 1,
			`SELECT count(*) FROM metadata_subject_providers WHERE entity_id=1 AND provider='musicbrainz' AND accepted_publication_id='publication-one' AND accepted_evidence_id='evidence-one' AND revision=2`:    1,
			// Contributions stay evidence-pinned: no catalogue credit image.
			`SELECT count(*) FROM catalog_entities WHERE id=1 AND kind=6`: 1,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal(query, got)
			}
		}
	})
	t.Run("two_hundred_credits_accept", func(t *testing.T) {
		pages := ipEntityMBArtistCohort(200)
		w, r, p, staged := ipEntityMBFixture(t, pages)
		ipEntityMBSeedAcceptedHead(t, w)
		out := ipEntityMBAccept(t, w, r, p, staged, "publication-one")
		if out.Value.CreditCount != 200 {
			t.Fatal(out)
		}
		if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_evidence_credits WHERE evidence_id='evidence-one'`); got != 200 {
			t.Fatal("200-credit cohort", got)
		}
	})
	t.Run("sealed_cohort_mismatch_refuses", func(t *testing.T) {
		pages := ipEntityMBArtistCohort(3)
		w, r, p, staged := ipEntityMBFixture(t, pages)
		ipEntityMBSeedAcceptedHead(t, w)
		ipPolicyExec(t, w, `DELETE FROM metadata_evidence_credits WHERE evidence_id='evidence-one' AND ordinal=2`)
		q := ipEntityMBAcceptRequest(staged, "publication-one", 1)
		c, err := newIPEntityAcceptCommand(ipEntityEvidenceIdentity(staged.claim, staged.sequence+1), q)
		if err != nil {
			t.Fatal(err)
		}
		turn, tx, b := ipPolicyTurn(t, w, r, IPEntityAccept)
		out, err := applyIPEntityAccept(t.Context(), turn, p, t.Context(), b, c)
		if out != (ipEntityAcceptCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("mismatched sealed cohort admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		for query, want := range map[string]int64{
			`SELECT count(*) FROM metadata_publications`:                            0,
			`SELECT count(*) FROM metadata_ip_receipts WHERE command_kind='accept'`: 0,
		} {
			if got := ipPolicyQueryInt(t, w, query); got != want {
				t.Fatal("refused accept wrote state", query, got)
			}
		}
	})
	t.Run("tmdb_empty_locale_refuses_at_seal", func(t *testing.T) {
		// The empty pair is canonical only in the exact ip.musicbrainz.*
		// namespace. A TMDB evidence whose subject binding and observation carry
		// the empty pair still begins and appends, but its seal is refused.
		w, r, p, _ := ipEntitySealFixture(t)
		owner, claim := ipEntityJobClaimed(t, w, r, p, 1, "tmdb")
		incarnation := make([]byte, 16)
		incarnation[0] = 0x2b
		_, shape := ipEntityEvidencePair("tmdb", "release")
		ipPolicyExec(t, w, `INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(30,'tmdb','release','tmdb-1','','',?,?,1)`,
			shape, incarnation)
		pages := []ipEntitySealPage{{Family: "alias", AfterOrdinal: 0, rows: 1,
			aliases: []ipEvidenceAlias{{Ordinal: 0, RequestedID: "requested-release",
				CanonicalID: "tmdb-1", Name: "Alias"}}}}
		header := ipEntityEvidenceReleaseHeader("evidence-one", "tmdb", "tmdb-1", 30)
		header.Language, header.Region = "", ""
		staged, _ := ipEntitySealStageCustom(t, w, r, p, owner, claim, "evidence-one", "build-one",
			header, pages, 2)
		staged.seal.language, staged.seal.region = "", ""
		if c, err := newIPEntitySealCommand(
			ipEntityEvidenceIdentity(claim, staged.sequence), staged.seal); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("TMDB empty locale admitted at seal", c, err)
		}
	})
}

func TestIPEntityMBDescriptors(t *testing.T) {
	if ipEntityEvidenceParser("musicbrainz", "release") !=
		"ip.musicbrainz.release.evidence.complete-ordered-families.v2" {
		t.Fatal("musicbrainz release parser identity drifted")
	}
}

// ipEntityMBSecondClaim claims the replacement observation for the same album
// after the baseline acceptance moved the entity revision.
func ipEntityMBSecondClaim(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof) (ipEntityJobResult, CommandIdentity) {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='pending',generation=2,revision=2,lease='',lease_until_ms=0,next_attempt_ms=0,attempts=1,error='' WHERE entity_id=1 AND provider='musicbrainz'`)
	identity := ipEntityJobIdentity(0x41, 1, 0x51)
	q := ipEntityJobClaimFor(1, "musicbrainz")
	q.selectedID, q.consentRevision = ipMBReleaseMBID, 3
	q.jobGeneration, q.jobRevision, q.entityRevision, q.metadataRevision = 2, 2, 2, 2
	c, err := newIPEntityClaimCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	out, turn, tx, _ := ipEntityJobApplyClaim(t, w, r, p, c)
	if out.Outcome.State != Applied {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipEntityJobAck(t, w, out.Value, 1)
	return out.Value, identity
}

// ipEntityMBSecondEvidence stages and seals a replacement MusicBrainz
// observation for the album in one fixture, using a distinct operation-ID salt.
func ipEntityMBSecondEvidence(t *testing.T, w *executor, r *ipPolicyRegistry,
	p *usOwnerAuthorityProof, content ipEntityMBContent) ipEntitySealStaged {
	t.Helper()
	owner, claim := ipEntityMBSecondClaim(t, w, r, p)
	staged, _ := ipEntityMBStage(t, w, r, p, owner, claim, 0x6c, "evidence-two", "build-two",
		ipEntityMBHeader("evidence-two"), ipEntityMBPages(content), 2)
	return staged
}

// ipEntityMBDeltaRequest names the exact old accepted head and derives the new
// typed observation from the new sealed pages.
func ipEntityMBDeltaRequest(staged ipEntitySealStaged, accepted ipEntityAcceptResult) ipEntityDeltaRequest {
	return ipEntityDeltaRequest{
		observation: ipEntityMBAcceptRequest(staged, "publication-two",
			accepted.SelectionRevision+1),
		previousPublicationID: accepted.PublicationID,
		previousEvidenceID:    accepted.EvidenceID,
	}
}
