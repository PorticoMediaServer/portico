package writer

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func searchReclaimNativeSeed(t *testing.T) (*executor, *searchFragmentReclaimCommand) {
	t.Helper()
	w, head := searchHeadNativeSeed(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}

	// Prepopulate an already-applied head before FULL startup Capture. Running
	// a NORMAL head turn here would change the held connection policy before
	// qualification; no PRAGMA reset or policy waiver is used.
	h := head.request
	st := h.snapshot.stage
	seal := st.seal
	exec(`INSERT INTO search_own_prefix(search_own_prefix,rowid,title_prefix_tokens,initial_prefix_tokens) VALUES('delete',41,?,?)`, h.old.prefix, h.old.initial)
	exec(`INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(41,?,?)`, h.next.prefix, h.next.initial)
	exec(`UPDATE search_publish_jobs SET published_document_id=307,published_generation=5,document_id=NULL,phase='idle',job_next_attempt_ms=NULL,job_revision=20,last_command_sequence=8 WHERE id=29`)
	exec(`UPDATE search_revision SET revision=20 WHERE singleton=1`)
	exec(`UPDATE search_generations SET pending_subjects=2 WHERE id=7`)
	exec(`UPDATE search_reclaim_jobs SET phase='ready',job_revision=2,job_next_attempt_ms=1000 WHERE id=2`)
	exec(`INSERT INTO search_publish_receipts(publish_job_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,input_generation,image_id,fragment_id,after_field,after_ordinal,after_fragment_id,seal_chain,published_generation,staged_fragments,staged_bytes,verified_fragments,verified_bytes,seal_algorithm) VALUES(29,?,8,?,?,'head_publish',20,1000,5,307,NULL,?,?,?,?,5,?,?,?,?,?)`, head.identity.Incarnation[:], head.identity.OperationID[:], head.digest[:], seal.afterField, seal.afterOrdinal, seal.afterFragmentID, seal.chain[:], seal.stagedFragments, seal.stagedBytes, seal.verifiedFragments, seal.verifiedBytes, seal.algorithm)
	rows := []searchmanifest.Row{{Field: 1, Ordinal: 0, Revision: "09", Prefix: "00006f00006c000064", Lexical: "old", Normalized: "old"}, {Field: 1, Ordinal: 1, Revision: "09"}}
	in := head.request.snapshot.stage.image.input
	in.InputGeneration = 3
	in.SourceRevision = 9
	m, err := searchmanifest.Prepare(in, rows)
	if err != nil {
		t.Fatal(err)
	}
	seed, chain := m.Seed(), m.Digest()
	exec(`INSERT INTO search_image_seals(publication_id,algorithm,seed,expected_digest,expected_fragments,expected_bytes,staged_fragments,staged_bytes,verified_fragments,verified_bytes,chain,after_field,after_ordinal,after_fragment_id,sealed) VALUES(13,?,?,?,?,?,?,?,?,?,?,1,1,602,1)`, searchmanifest.Algorithm, seed[:], chain[:], m.Count(), m.Bytes(), m.Count(), m.Bytes(), m.Count(), m.Bytes(), chain[:])
	for n, row := range rows {
		id := int64(601 + n)
		exec(`INSERT INTO search_fragments(id,owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id) VALUES(?,41,NULL,?,?,?,?,?,?,3,7,13)`, id, int64(row.Field), row.Ordinal, row.Revision, row.Prefix, row.Lexical, row.Normalized)
		exec(`INSERT INTO search_fragment_prefix(rowid,prefix_tokens) VALUES(?,?)`, id, row.Prefix)
		exec(`INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(?,?)`, id, row.Lexical)
		exec(`INSERT INTO search_fragment_grams(rowid,normalized) VALUES(?,?)`, id, row.Normalized)
	}
	exec(`UPDATE search_reclaim_jobs SET job_lease_owner=1,job_lease_generation=2,job_lease_expires_ms=2100000000000 WHERE id=2`)
	o := searchFragmentReclaimOwner{id: 2, publicationID: 13, livePublicationID: sql.NullInt64{Int64: 13, Valid: true}, phase: "ready", incarnation: [16]byte{61}, revision: 2, leaseOwner: 1, leaseGeneration: 2, leaseExpiresMS: 2100000000000, nextAttempt: sql.NullInt64{Int64: 1000, Valid: true}}
	r := searchFragmentReclaimRequest{variant: searchReclaimFragmentKind, bankKey: "v1", engineVersion: searchpolicy.EngineVersion, snapshot: searchFragmentReclaimSnapshot{owner: o, fragment: searchRetainedFragment{601, 13, 7, 3, 41, 3, 1, rows[0]}, currentHead: sql.NullInt64{Int64: 307, Valid: true}}}
	c, err := newSearchFragmentReclaimCommand(CommandIdentity{o.incarnation, 1, [16]byte{91}}, r)
	if err != nil {
		t.Fatal(err)
	}
	return w, c
}

func searchReclaimNativeFixture(t *testing.T) (*executor, *stmt.Registry, *searchFragmentReclaimCommand) {
	t.Helper()
	w, c := searchReclaimNativeSeed(t)
	r := cachedNativeInventoryFixtureRegistry(t, w, searchFragmentReclaimDefinitions(), "search_reclaim_native_test.go", "search_reclaim_statements.go")
	return w, r, c
}

func TestSearchReclaimCompleteWireAndOpaqueOriginal(t *testing.T) {
	e := requestEncoder{h: sha256.New()}
	e.value(reflect.ValueOf(searchFragmentReclaimRequest{}), 0)
	fixed := e.written + uint64(len(canonicalRequestCodec)+4+16+8+16)
	if e.err != nil || fixed > searchFragmentReclaimFixedBytes {
		t.Fatal("reclaim preflight undercounts shared wire", fixed, e.err)
	}
	s, rows := searchFragmentStageSnapshotFixture(t)
	r := searchFragmentReclaimRequest{variant: searchReclaimFragmentKind, bankKey: "v1", engineVersion: searchpolicy.EngineVersion,
		snapshot: searchFragmentReclaimSnapshot{owner: searchFragmentReclaimOwner{id: 2, publicationID: s.image.id, livePublicationID: sql.NullInt64{Int64: s.image.id, Valid: true}, phase: "ready", incarnation: [16]byte{61}, revision: 2, leaseOwner: 1, leaseGeneration: 2, leaseExpiresMS: 2100000000000}, fragment: searchRetainedFragment{601, s.image.id, 7, 5, 41, 3, 1, rows[0].row}}}
	r.snapshot.fragment.row.Revision = "09"
	identity := CommandIdentity{r.snapshot.owner.incarnation, 1, [16]byte{91}}
	c, err := newSearchFragmentReclaimCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	if c.request.snapshot.fragment.row.Revision != "09" {
		t.Fatal("original TEXT revision parsed/reformatted")
	}
	addresses := map[uintptr]bool{}
	for _, p := range searchReclaimTexts(&r) {
		addresses[uintptr(unsafe.Pointer(p))] = true
	}
	found := 0
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String {
			found++
			if !addresses[v.UnsafeAddr()] {
				t.Fatal("retained string outside preflight")
			}
		}
	})
	if found != len(addresses) {
		t.Fatal("preflight string inventory")
	}
}

func TestSearchReclaimNativeOneFragmentAndSavedReplay(t *testing.T) {
	w, r, c := searchReclaimNativeFixture(t)
	turn := searchPublisherComponentTurn(t, w, r, SearchFragmentReclaim)
	native, err := searchFragmentReclaimFreshFacts(t.Context(), turn, c.request)
	if err != nil {
		t.Fatal(err)
	}
	before, proof := searchImageSharedFacts(t, turn), searchHeadGDriverProof(t, turn)
	started := time.Now()
	out, err := writeSearchFragmentReclaim(t.Context(), turn, c, native, 1000)
	bodyEnd := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	if out.AcceptedJobRevision != 3 || out.FragmentID != 601 || out.PublicationID != 13 || out.AfterFragmentID != 601 || out.Complete || out.Outcome != (ReceiptOutcome{}) {
		t.Fatal("wrong native saved result", out)
	}
	var remaining int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM search_fragments WHERE publication_id=13`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal("reclaim exceeded one fragment", remaining, err)
	}
	for _, q := range []string{`SELECT rowid FROM search_fragment_prefix WHERE search_fragment_prefix MATCH '00006f00006c000064'`, `SELECT rowid FROM search_fragment_lexical WHERE search_fragment_lexical MATCH 'old'`, `SELECT rowid FROM search_fragment_grams WHERE search_fragment_grams MATCH 'old'`} {
		var id int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), q).Scan(&id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("retained native tokens survived", id, err)
		}
	}
	var seals, images int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM search_image_seals WHERE publication_id=13),(SELECT count(*) FROM search_publications WHERE id=13)`).Scan(&seals, &images); err != nil || seals != 1 || images != 1 {
		t.Fatal("native retirement lost immutable image custody", seals, images, err)
	}
	if after := searchImageSharedFacts(t, turn); after != before {
		t.Fatal("reclaim changed source/global/Gdriver accounting", before, after)
	}
	if searchHeadGDriverProof(t, turn) != proof {
		t.Fatal("Gdriver proof changed")
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	commitStart := time.Now()
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	returned := time.Now()
	if w.db.Stats().InUse != 0 {
		t.Fatal("physical return not joined")
	}
	t.Logf("component only reclaimJob=%d image=%d fragment=%d retainedBytes=%d revisionBytes=%d body=%s commit+return=%s whole=%s; NOT native Work/HOLD/CP qualification", out.JobID, out.PublicationID, out.FragmentID, len(native.snapshot.fragment.row.Prefix)+len(native.snapshot.fragment.row.Lexical)+len(native.snapshot.fragment.row.Normalized), len(native.snapshot.fragment.row.Revision), bodyEnd.Sub(started), returned.Sub(commitStart), returned.Sub(started))
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_reclaim_jobs SET job_revision=9223372036854775807,job_lease_expires_ms=0 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	turn = searchPublisherComponentTurn(t, w, r, SearchFragmentReclaim)
	for id := range turn.steps {
		if id != searchReclaimOwner && id != searchReclaimIdentity && id != searchReclaimResult {
			delete(turn.steps, id)
		}
	}
	again, err := applySearchFragmentReclaim(t.Context(), turn, c)
	if err != nil || again != out {
		t.Fatal("deleted row/live cursor substituted for saved result", again, out, err)
	}
}

func TestSearchReclaimLateCASRollsBackAllOriginalTokens(t *testing.T) {
	w, r, c := searchReclaimNativeFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_reclaim_jobs SET job_revision=3 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	turn := searchPublisherComponentTurn(t, w, r, SearchFragmentReclaim)
	out, err := writeSearchFragmentReclaim(t.Context(), turn, c, c.request, 1000)
	if !errors.Is(err, ErrConflict) || out != (searchFragmentReclaimResult{}) {
		t.Fatal(out, err)
	}
	if _, e := turn.transaction.rollback(err); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var revision string
	if err := w.db.QueryRowContext(t.Context(), `SELECT source_revision FROM search_fragments WHERE id=601`).Scan(&revision); err != nil || revision != "09" {
		t.Fatal("rollback lost original content", revision, err)
	}
	for _, q := range []string{`SELECT rowid FROM search_fragment_prefix WHERE search_fragment_prefix MATCH '00006f00006c000064'`, `SELECT rowid FROM search_fragment_lexical WHERE search_fragment_lexical MATCH 'old'`, `SELECT rowid FROM search_fragment_grams WHERE search_fragment_grams MATCH 'old'`} {
		var id int64
		if err := w.db.QueryRowContext(t.Context(), q).Scan(&id); err != nil || id != 601 {
			t.Fatal("rollback lost old native bytes", id, err)
		}
	}
}

func TestSearchReclaimUsesRetainedSubjectAndAllowsStaleNormalizer(t *testing.T) {
	w, r, c := searchReclaimNativeFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM catalog_entities WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	p := searchpolicy.Compiled()
	last := "0"
	if p.UnicodeFingerprint[len(p.UnicodeFingerprint)-1] == '0' {
		last = "1"
	}
	p.UnicodeFingerprint = p.UnicodeFingerprint[:len(p.UnicodeFingerprint)-1] + last
	if !searchpolicy.StoredWellFormed(p) {
		t.Fatal("fixture stale profile malformed")
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_generations SET unicode_fingerprint=? WHERE id=7`, p.UnicodeFingerprint); err != nil {
		t.Fatal(err)
	}
	turn := searchPublisherComponentTurn(t, w, r, SearchFragmentReclaim)
	native, err := searchFragmentReclaimFreshFacts(t.Context(), turn, c.request)
	if err != nil {
		t.Fatal("reclaim depended on live source/current normalizer", err)
	}
	out, err := writeSearchFragmentReclaim(t.Context(), turn, c, native, 1000)
	if err != nil || out.FragmentID != 601 {
		t.Fatal(out, err)
	}
}

func TestSearchReclaimRejectsSkippingNextFragment(t *testing.T) {
	w, r, c := searchReclaimNativeFixture(t)
	request := c.request
	request.snapshot.fragment.id = 602
	request.snapshot.fragment.row.Ordinal = 1
	request.snapshot.fragment.row.Prefix = ""
	request.snapshot.fragment.row.Lexical = ""
	request.snapshot.fragment.row.Normalized = ""
	if _, err := newSearchFragmentReclaimCommand(c.identity, request); err != nil {
		t.Fatal("preparation is not the native next-row proof", err)
	}
	turn := searchPublisherComponentTurn(t, w, r, SearchFragmentReclaim)
	out, err := searchFragmentReclaimFreshFacts(t.Context(), turn, request)
	if !errors.Is(err, ErrConflict) || out != (searchFragmentReclaimRequest{}) {
		t.Fatal("skipped earlier retained fragment", out, err)
	}
}

func TestSearchReclaimEmptyLastFragmentStillRetainsImage(t *testing.T) {
	w, r, c := searchReclaimNativeFixture(t)
	turn := searchPublisherComponentTurn(t, w, r, SearchFragmentReclaim)
	if _, err := writeSearchFragmentReclaim(t.Context(), turn, c, c.request, 1000); err != nil {
		t.Fatal(err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	next := c.request
	o := &next.snapshot.owner
	o.revision = 3
	o.phase = "reclaim"
	o.afterFragmentID = 601
	o.lastSequence = 1
	next.snapshot.fragment.id = 602
	next.snapshot.fragment.row.Ordinal = 1
	next.snapshot.fragment.row.Prefix = ""
	next.snapshot.fragment.row.Lexical = ""
	next.snapshot.fragment.row.Normalized = ""
	identity := CommandIdentity{o.incarnation, 2, [16]byte{92}}
	if _, err := newSearchFragmentReclaimCommand(identity, next); err == nil {
		t.Fatal("advanced beyond unacknowledged original receipt")
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_reclaim_jobs SET acknowledged_command_sequence=1 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	o.acknowledgedSequence = 1
	second, err := newSearchFragmentReclaimCommand(identity, next)
	if err != nil {
		t.Fatal(err)
	}
	turn = searchPublisherComponentTurn(t, w, r, SearchFragmentReclaim)
	native, err := searchFragmentReclaimFreshFacts(t.Context(), turn, next)
	if err != nil {
		t.Fatal(err)
	}
	out, err := writeSearchFragmentReclaim(t.Context(), turn, second, native, 1001)
	if err != nil || out.FragmentID != 602 || out.AcceptedJobRevision != 4 || out.Complete {
		t.Fatal("empty final fragment skipped or inferred image completion", out, err)
	}
	for _, q := range []string{`SELECT count(*) FROM search_fragment_prefix_docsize WHERE id=602`, `SELECT count(*) FROM search_fragment_lexical_docsize WHERE id=602`, `SELECT count(*) FROM search_fragment_grams_docsize WHERE id=602`} {
		var count int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), q).Scan(&count); err != nil || count != 0 {
			t.Fatal("empty native document not retired", count, err)
		}
	}
	var fragments, images, seals int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM search_fragments WHERE publication_id=13),(SELECT count(*) FROM search_publications WHERE id=13),(SELECT count(*) FROM search_image_seals WHERE publication_id=13)`).Scan(&fragments, &images, &seals); err != nil || fragments != 0 || images != 1 || seals != 1 {
		t.Fatal("last fragment erased original image/seal", fragments, images, seals, err)
	}
	delete(turn.steps, searchReclaimResult)
	retired, decision, err := lookupSearchFragmentReclaim(t.Context(), turn, 2, ReceiptReference{c.identity, c.digest})
	if err != nil || decision != receiptRetired || retired != (searchFragmentReclaimResult{}) {
		t.Fatal("ACK floor invented historical Applied", retired, decision, err)
	}
}

func TestSearchReclaimFreshLivePointersRefuse(t *testing.T) {
	for _, q := range []string{`UPDATE search_publish_jobs SET published_document_id=13,published_generation=3 WHERE id=29`, `UPDATE search_publish_jobs SET document_id=13 WHERE id=29`} {
		w, r, c := searchReclaimNativeFixture(t)
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
		turn := searchPublisherComponentTurn(t, w, r, SearchFragmentReclaim)
		out, err := searchFragmentReclaimFreshFacts(t.Context(), turn, c.request)
		if !errors.Is(err, ErrConflict) || out != (searchFragmentReclaimRequest{}) {
			t.Fatal("fresh live image pointer accepted", out, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
}
