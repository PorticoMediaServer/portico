package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group E is not embedded, so owner selection is qualified only against the
// exact draft fixtures: IP-19's material custody, IP-20's source/blob custody and
// this task's E selection replacement plus normalized history-artwork pins. No
// Capture/golden claim is made here. The commands do no file or provider IO.
func ipArtworkSelectionFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	for _, name := range []string{
		"testdata/ip-artwork-material-draft.sql",
		"testdata/ip-artwork-material-publish-draft.sql",
		"testdata/ip-artwork-selection-draft.sql",
	} {
		ddl, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, query := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',1,0,1,1,1),(7,'song',1,0,1,1,1)`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,artwork_language,artwork_policy_revision,default_language,online_lookups,lookup_epoch) VALUES(1,'same',1,'en',1,3) ON CONFLICT(singleton) DO UPDATE SET artwork_language='same',artwork_policy_revision=1,default_language='en',online_lookups=1,lookup_epoch=3`,
	} {
		ipPolicyExec(t, w, query)
	}
	ipArtworkSelectionSeedEntity(t, w, 778, 1, [16]byte{4}, 1, 0, 0)
	ipArtworkSelectionSeedEntity(t, w, 781, 5, [16]byte{11}, 1, 0, 0)
	ipArtworkSelectionSeedEntity(t, w, 779, 6, [16]byte{7}, 1, 781, 0)
	ipArtworkSelectionSeedEntity(t, w, 780, 7, [16]byte{8}, 1, 0, 779)
	ipPolicyExec(t, w, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,artwork_revision) VALUES(778,'person-token','person:key','Person','person',X'05000000000000000000000000000000',2)`)
	leaseUntil := int64(1) << 50
	for _, row := range []struct {
		id, subject int64
		column      string
		incarnation []byte
		revision    int64
		command     []byte
	}{
		{901, 778, "entity_id", ipArtworkSelectionBytes(4), 1, ipArtworkSelectionBytes(1)},
		{902, 778, "person_id", ipArtworkSelectionBytes(5), 2, ipArtworkSelectionBytes(6)},
		{903, 779, "entity_id", ipArtworkSelectionBytes(7), 1, ipArtworkSelectionBytes(9)},
		{904, 780, "entity_id", ipArtworkSelectionBytes(8), 1, ipArtworkSelectionBytes(10)},
	} {
		ipPolicyExec(t, w, `INSERT INTO artwork_subject_jobs(id,`+row.column+`,source_incarnation,source_revision,phase,job_revision,job_lease_owner,job_lease_generation,job_lease_expires_ms,command_incarnation) VALUES(?,?,?,?,'publish',1,7,1,?,?)`,
			row.id, row.subject, row.incarnation, row.revision, leaseUntil, row.command)
	}
	defs := append(usAuthorityDefinitions(), ipArtworkMaterialDefinitions()...)
	defs = append(defs, ipArtworkMaterialPublishDefinitions()...)
	defs = append(defs, ipArtworkSelectionDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{r, keeper}, p
}

func ipArtworkSelectionSeedEntity(t *testing.T, w *executor, id, kind int64, incarnation [16]byte, revision int64, artistID, albumID int64) {
	t.Helper()
	public := make([]byte, 16)
	public[8], public[9] = byte(id>>8), byte(id&0xff)
	ipPolicyExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,777,?)`, id, public, kind)
	ipPolicyExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,artist_id,album_id,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision,artwork_revision) VALUES(?,?,777,?,NULLIF(?,0),NULLIF(?,0),'Title','title','t','title','t',0,X'03000000000000000000000000000000',?,1,1,1,?)`,
		id, public, kind, artistID, albumID, incarnation[:], revision)
}

func ipArtworkSelectionMovieOwner() ipArtworkMaterialOwner {
	return ipArtworkMaterialOwner{id: 901, incarnation: [16]byte{1}, entityID: 778,
		sourceIncarnation: [16]byte{4}, sourceRevision: 1, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

func ipArtworkSelectionPersonOwner() ipArtworkMaterialOwner {
	return ipArtworkMaterialOwner{id: 902, incarnation: [16]byte{6}, personID: 778,
		sourceIncarnation: [16]byte{5}, sourceRevision: 2, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

func ipArtworkSelectionAlbumOwner() ipArtworkMaterialOwner {
	return ipArtworkMaterialOwner{id: 903, incarnation: [16]byte{9}, entityID: 779,
		sourceIncarnation: [16]byte{7}, sourceRevision: 1, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

func ipArtworkSelectionSongOwner() ipArtworkMaterialOwner {
	return ipArtworkMaterialOwner{id: 904, incarnation: [16]byte{10}, entityID: 780,
		sourceIncarnation: [16]byte{8}, sourceRevision: 1, revision: 1,
		leaseOwner: 7, leaseGeneration: 1, leaseUntilMS: 1 << 50}
}

func ipArtworkSelectionHex(n int) string { return strings.Repeat(fmt.Sprintf("%x", n%16), 64) }

func ipArtworkSelectionBytes(n byte) []byte {
	value := make([]byte, 16)
	value[0] = n
	return value
}

func ipArtworkSelectionBytes32(n byte) []byte {
	value := make([]byte, 32)
	value[0] = n
	return value
}

// One accepted source with its three ready slots and (optionally) the retained
// original upload bytes. It is content custody only, not a selection.
func seedIPArtworkSelectionSource(t *testing.T, w *executor, seed int, original bool) (string, [3]string) {
	t.Helper()
	digest := ipArtworkSelectionHex(seed)
	var blobs [3]string
	for n := 0; n < 3; n++ {
		blobs[n] = ipArtworkSelectionHex(seed*10 + n + 1)
		ipPolicyExec(t, w, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,?,?,?,?,?)`,
			blobs[n], "image/jpeg", int64(500+n), int64(750), int64(40000+n), "2026-01-01T00:00:00Z")
	}
	var originalBytes int64
	if original {
		originalBytes = 2 << 20
	}
	ipPolicyExec(t, w, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,'image/jpeg',3000,2000,?,?,?,1920,0,0,3,'2026-01-01T00:00:00Z',1,?,0,NULL)`,
		digest, blobs[0], blobs[1], blobs[2], originalBytes)
	return digest, blobs
}

func ipArtworkSelectionFactsFor(digest string) ipArtworkSelectionFacts {
	return ipArtworkSelectionFacts{digest: digest, provider: "tmdb", imageID: "image-001",
		origin: "https://example.invalid/art", attribution: "Example", locale: "",
		language: "en", sourceFence: "provider-fence"}
}

func ipArtworkSelectionUploadFacts(digest string) ipArtworkSelectionFacts {
	return ipArtworkSelectionFacts{digest: digest, provider: "upload", imageID: "upload-001",
		origin: "upload", attribution: "Upload", locale: "", language: "en", sourceFence: "upload-fence"}
}

// A matched row is what discovery may write beneath the owner's lock. It is
// never the owner's choice and must survive every select/clear here.
func ipArtworkSelectionSeedMatched(t *testing.T, w *executor, owner ipArtworkMaterialOwner, role, digest string) {
	t.Helper()
	kind, subject := ipArtworkSelectionNamespace(owner)
	ipPolicyExec(t, w, `INSERT INTO artwork_selections(kind,entity_id,role,choice,revision,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,actor,observed_at) VALUES(?,?,?,'matched',1,?,'tmdb','image-001','https://example.invalid/art','Example','','en','provider-fence','provider','2026-01-01T00:00:00Z')`,
		kind, subject, role, digest)
}

func ipArtworkSelectionCommandFor(t *testing.T, kind CommandID, sequence int64, operation byte, owner ipArtworkMaterialOwner, role string, before ipArtworkSelectionBefore, after ipArtworkSelectionFacts) *ipArtworkSelectionCommand {
	t.Helper()
	c, err := newIPArtworkSelectionCommand(kind, CommandIdentity{Incarnation: owner.incarnation, Sequence: sequence, OperationID: [16]byte{operation}}, ipArtworkSelectionRequest{
		owner: owner, role: role, language: "en", policyRevision: 1, actor: "owner",
		before: before, after: after,
	})
	if err != nil {
		t.Fatal(kind, err)
	}
	return c
}

func applyIPArtworkSelectionCommand(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, c *ipArtworkSelectionCommand) (ipArtworkSelectionCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, c.kind)
	out, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("selection", out, err)
	}
	return out, turn, tx, b
}

// The next fresh command requires the previous sequence ACKed and the subject
// job re-snapshotted at the subject's current artwork revision.
func ipArtworkSelectionAdvance(t *testing.T, w *executor, o ipArtworkMaterialOwner, acked int64) ipArtworkMaterialOwner {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=? WHERE id=?`, acked, o.id)
	var revision int64
	if o.entityID > 0 {
		revision = ipPolicyQueryInt(t, w, `SELECT artwork_revision FROM catalog_entities WHERE id=?`, o.entityID)
	} else {
		revision = ipPolicyQueryInt(t, w, `SELECT artwork_revision FROM catalog_people WHERE id=?`, o.personID)
	}
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET source_revision=? WHERE id=?`, revision, o.id)
	o.revision++
	o.sourceRevision = revision
	return o
}

// The exact current locked row as the caller last observed it.
func ipArtworkSelectionBeforeFor(t *testing.T, w *executor, owner ipArtworkMaterialOwner, role string) ipArtworkSelectionBefore {
	t.Helper()
	kind, subject := ipArtworkSelectionNamespace(owner)
	var revision int64
	var digest, provider, imageID, origin, attribution, locale, language, fence, objectID, objectRevision sql.NullString
	var actor string
	err := w.db.QueryRowContext(t.Context(), `SELECT revision,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,actor,source_object_id,source_object_revision FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND choice='locked'`,
		kind, subject, role).Scan(&revision, &digest, &provider, &imageID, &origin, &attribution,
		&locale, &language, &fence, &actor, &objectID, &objectRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return ipArtworkSelectionBefore{}
	}
	if err != nil {
		t.Fatal(err)
	}
	fenceValue := fence.String
	if !digest.Valid && fenceValue == ipArtworkSelectionEmptyFence {
		fenceValue = ""
	}
	return ipArtworkSelectionBefore{present: true, revision: revision, actor: actor,
		facts: ipArtworkSelectionFacts{digest: digest.String, provider: provider.String,
			imageID: imageID.String, origin: origin.String, attribution: attribution.String,
			locale: locale.String, language: language.String, sourceFence: fenceValue,
			objectID:       EvidenceText{Value: objectID.String, Present: objectID.Valid},
			objectRevision: EvidenceText{Value: objectRevision.String, Present: objectRevision.Valid}}}
}

func ipArtworkSelectionExpect(t *testing.T, w *executor, want int64, query string, args ...any) {
	t.Helper()
	if got := ipPolicyQueryInt(t, w, query, args...); got != want {
		t.Fatal(query, got, want)
	}
}

// A refused command retains nothing and moves no counter.
func ipArtworkSelectionClean(t *testing.T, w *executor, selections int64) {
	t.Helper()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM artwork_selections`, selections},
		{`SELECT count(*) FROM metadata_owner_history`, 0},
		{`SELECT count(*) FROM metadata_owner_history_artwork`, 0},
		{`SELECT count(*) FROM artwork_command_receipts`, 0},
		{`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`, 0},
		{`SELECT acknowledged_command_sequence FROM artwork_subject_jobs WHERE id=901`, 0},
		{`SELECT artwork_revision FROM catalog_entities WHERE id=778`, 1},
	} {
		ipArtworkSelectionExpect(t, w, check.want, check.query)
	}
}

// Every v1 owner select writes choice='locked', one row per category, and the
// selected source keeps all three ready slots plus a normalized history pin.
func TestIPArtworkSelectionLockedChoiceAcrossSourceSlots(t *testing.T) {
	w, r, p := ipArtworkSelectionFixture(t)
	digest, blobs := seedIPArtworkSelectionSource(t, w, 1, false)
	owner := ipArtworkSelectionMovieOwner()
	for n, role := range []string{"poster", "backdrop", "still"} {
		cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, int64(n+1), byte(n+2), owner, role,
			ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
		out, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, cmd)
		if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.Count != 1 ||
			out.Value.Selected != digest || out.Value.Previous != "" ||
			out.Value.SelectionRevision != 1 || !out.Value.History.Present || out.Digest == (ResultDigest{}) {
			t.Fatal(role, out)
		}
		ipPolicyCommitTurn(t, turn, tx)
		if n < 2 {
			owner = ipArtworkSelectionAdvance(t, w, owner, int64(n+1))
		}
	}
	ipArtworkSelectionExpect(t, w, 3, `SELECT count(*) FROM artwork_selections`)
	ipArtworkSelectionExpect(t, w, 3, `SELECT count(*) FROM artwork_selections WHERE choice='locked' AND digest=? AND provider='tmdb' AND effective_language='en'`, digest)
	ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM artwork_selections WHERE choice<>'locked'`)
	ipArtworkSelectionExpect(t, w, 3, `SELECT count(*) FROM metadata_owner_history_artwork WHERE choice='locked' AND digest=?`, digest)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=?`, digest)
	ipArtworkSelectionExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
	for _, blob := range blobs {
		ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_blobs WHERE digest=? AND status='ready' AND deletion_token IS NULL`, blob)
	}
	ipArtworkSelectionExpect(t, w, 3, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='select' AND result_count=1 AND choice='locked'`)
	ipArtworkSelectionExpect(t, w, 4, `SELECT artwork_revision FROM catalog_entities WHERE id=778`)
	ipArtworkSelectionExpect(t, w, 4, `SELECT job_revision FROM artwork_subject_jobs WHERE id=901`)
	ipArtworkSelectionExpect(t, w, 3, `SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`)
}

// A locked-empty choice is a persistent locked row with a NULL digest, not a
// deletion. It keeps a matched candidate beneath it, can be replaced by a real
// digest, and clear removes only the lock.
func TestIPArtworkSelectionLockedEmptyPersistsAndUpdates(t *testing.T) {
	w, r, p := ipArtworkSelectionFixture(t)
	matched, _ := seedIPArtworkSelectionSource(t, w, 2, false)
	digest, blobs := seedIPArtworkSelectionSource(t, w, 3, false)
	owner := ipArtworkSelectionMovieOwner()
	ipArtworkSelectionSeedMatched(t, w, owner, "poster", matched)

	empty := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, owner, "poster",
		ipArtworkSelectionBefore{}, ipArtworkSelectionFacts{})
	out, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, empty)
	if out.Outcome.State != Applied || out.Value.Count != 1 || out.Value.Selected != "" ||
		out.Value.Previous != "" || out.Value.SelectionRevision != 1 || !out.Value.History.Present {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE choice='locked' AND digest IS NULL AND provider='' AND image_id='' AND source_fence='owner-empty' AND effective_language='' AND revision=1`)
	ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM metadata_owner_history_artwork`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM metadata_owner_history`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE choice='matched' AND digest=? AND revision=1`, matched)
	ipArtworkSelectionExpect(t, w, 1, `SELECT CASE choice WHEN 'locked' THEN 1 ELSE 0 END FROM artwork_selections WHERE role='poster' ORDER BY choice LIMIT 1`)
	ipArtworkSelectionExpect(t, w, 2, `SELECT artwork_revision FROM catalog_entities WHERE id=778`)

	owner = ipArtworkSelectionAdvance(t, w, owner, 1)
	again := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 2, 3, owner, "poster",
		ipArtworkSelectionBeforeFor(t, w, owner, "poster"), ipArtworkSelectionFacts{})
	out, turn, tx, _ = applyIPArtworkSelectionCommand(t, w, r, p, again)
	if out.Outcome.State != Applied || out.Value.Count != 0 || out.Value.History.Present ||
		out.Value.Selected != "" || out.Value.Previous != "" || out.Value.SelectionRevision != 1 {
		t.Fatal("repeated locked-empty was a new effect", out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM metadata_owner_history`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT revision FROM artwork_selections WHERE role='poster' AND choice='locked'`)
	ipArtworkSelectionExpect(t, w, 2, `SELECT artwork_revision FROM catalog_entities WHERE id=778`)

	owner = ipArtworkSelectionAdvance(t, w, owner, 2)
	real := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 3, 4, owner, "poster",
		ipArtworkSelectionBeforeFor(t, w, owner, "poster"), ipArtworkSelectionFactsFor(digest))
	out, turn, tx, _ = applyIPArtworkSelectionCommand(t, w, r, p, real)
	if out.Outcome.State != Applied || out.Value.Count != 1 || out.Value.Selected != digest ||
		out.Value.Previous != "" || out.Value.SelectionRevision != 2 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE choice='locked' AND digest=? AND revision=2`, digest)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM metadata_owner_history_artwork WHERE digest=?`, digest)

	owner = ipArtworkSelectionAdvance(t, w, owner, 3)
	clear := ipArtworkSelectionCommandFor(t, IPArtworkClear, 4, 5, owner, "poster",
		ipArtworkSelectionBeforeFor(t, w, owner, "poster"), ipArtworkSelectionFacts{})
	out, turn, tx, _ = applyIPArtworkSelectionCommand(t, w, r, p, clear)
	if out.Outcome.State != Applied || out.Value.Count != 1 || out.Value.Selected != "" ||
		out.Value.Previous != digest || out.Value.SelectionRevision != 3 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM artwork_selections WHERE choice='locked'`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE choice='matched' AND digest=?`, matched)
	ipArtworkSelectionExpect(t, w, 2, `SELECT count(*) FROM metadata_owner_history_artwork`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=?`, digest)
	for _, blob := range blobs {
		ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_blobs WHERE digest=?`, blob)
	}
}

// Clear restores the automatic matched choice; a matched row is never the
// owner's lock, and clearing an absent lock is not an ownership change.
func TestIPArtworkSelectionAutoCandidateBeneathLockAndClear(t *testing.T) {
	w, r, p := ipArtworkSelectionFixture(t)
	matched, _ := seedIPArtworkSelectionSource(t, w, 2, false)
	digest, _ := seedIPArtworkSelectionSource(t, w, 3, false)
	owner := ipArtworkSelectionMovieOwner()
	ipArtworkSelectionSeedMatched(t, w, owner, "poster", matched)

	selectCmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, owner, "poster",
		ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
	out, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, selectCmd)
	if out.Outcome.State != Applied || out.Value.Count != 1 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE choice='matched' AND digest=? AND revision=1`, matched)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE choice='locked' AND digest=?`, digest)

	owner = ipArtworkSelectionAdvance(t, w, owner, 1)
	clearCmd := ipArtworkSelectionCommandFor(t, IPArtworkClear, 2, 3, owner, "poster",
		ipArtworkSelectionBeforeFor(t, w, owner, "poster"), ipArtworkSelectionFacts{})
	out, turn, tx, _ = applyIPArtworkSelectionCommand(t, w, r, p, clearCmd)
	if out.Outcome.State != Applied || out.Value.Count != 1 || out.Value.Previous != digest ||
		out.Value.Selected != "" {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM artwork_selections WHERE choice='locked'`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT CASE choice WHEN 'matched' THEN 1 ELSE 0 END FROM artwork_selections WHERE role='poster' ORDER BY choice LIMIT 1`)

	owner = ipArtworkSelectionAdvance(t, w, owner, 2)
	noop := ipArtworkSelectionCommandFor(t, IPArtworkClear, 3, 4, owner, "poster",
		ipArtworkSelectionBefore{}, ipArtworkSelectionFacts{})
	out, turn, tx, _ = applyIPArtworkSelectionCommand(t, w, r, p, noop)
	if out.Outcome.State != Applied || out.Value.Count != 0 || out.Value.History.Present ||
		out.Value.Previous != "" || out.Value.Selected != "" {
		t.Fatal("matched row was treated as owner ownership", out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE choice='matched' AND digest=? AND revision=1`, matched)
	ipArtworkSelectionExpect(t, w, 2, `SELECT count(*) FROM metadata_owner_history`)
}

// Selection stays on the exact subject: an album or song choice is never copied
// to its container or children, and a person choice never lands in the entity
// namespace with the same numeric ID.
func TestIPArtworkSelectionOwnerRevisionAndNoContainerCopy(t *testing.T) {
	w, r, p := ipArtworkSelectionFixture(t)
	digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)

	album := ipArtworkSelectionAlbumOwner()
	cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, album, "cover",
		ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
	if out, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, cmd); true {
		if out.Outcome.State != Applied || out.Value.Count != 1 {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
	}
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE kind='entity' AND entity_id=779 AND role='cover' AND choice='locked'`)
	ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM artwork_selections WHERE entity_id<>779`)
	ipArtworkSelectionExpect(t, w, 2, `SELECT artwork_revision FROM catalog_entities WHERE id=779`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT artwork_revision FROM catalog_entities WHERE id=778`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT artwork_revision FROM catalog_entities WHERE id=780`)

	song := ipArtworkSelectionSongOwner()
	cmd = ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 3, song, "poster",
		ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
	if out, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, cmd); true {
		if out.Outcome.State != Applied || out.Value.Count != 1 {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
	}
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE kind='entity' AND entity_id=780 AND role='poster'`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE entity_id=779 AND role='cover'`)
	ipArtworkSelectionExpect(t, w, 2, `SELECT count(*) FROM artwork_selections`)
	ipArtworkSelectionExpect(t, w, 2, `SELECT artwork_revision FROM catalog_entities WHERE id=780`)
	ipArtworkSelectionExpect(t, w, 2, `SELECT artwork_revision FROM catalog_entities WHERE id=779`)
	ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM artwork_selections WHERE entity_id=778`)

	person := ipArtworkSelectionPersonOwner()
	cmd = ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 4, person, "portrait",
		ipArtworkSelectionBefore{}, ipArtworkSelectionUploadFacts(digest))
	if out, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, cmd); true {
		if out.Outcome.State != Applied || out.Value.Count != 1 {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
	}
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE kind='person' AND entity_id=778 AND role='portrait' AND choice='locked' AND provider='upload'`)
	ipArtworkSelectionExpect(t, w, 3, `SELECT artwork_revision FROM catalog_people WHERE id=778`)
	ipArtworkSelectionExpect(t, w, 3, `SELECT count(*) FROM artwork_selections`)
	ipArtworkSelectionExpect(t, w, 2, `SELECT artwork_revision FROM catalog_entities WHERE id=780`)
}

// Clear never deletes an installed, unpublished or retiring original: only the
// locked row leaves, while the source, its slots, the installed material and the
// history pin all stay charged.
func TestIPArtworkSelectionClearKeepsOriginalCustody(t *testing.T) {
	const intent = "job-entity-poster"
	w, r, p := ipArtworkSelectionFixture(t)
	digest, blobs := seedIPArtworkSelectionSource(t, w, 1, true)
	ipPolicyExec(t, w, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES(?,901,X'01000000000000000000000000000000',1,2,?,'image/jpeg',3000,2000,1920,3,'installed',4,4)`,
		intent, digest)
	for n := 0; n < 3; n++ {
		ipPolicyExec(t, w, `INSERT INTO artwork_material_slots(intent_id,slot,digest,mime,width,height,bytes,outlier) VALUES(?,?,?,?,?,?,?,0)`,
			intent, int64(n), blobs[n], "image/jpeg", int64(500+n), int64(750), int64(40000+n))
	}
	ipPolicyExec(t, w, `INSERT INTO artwork_material_slots(intent_id,slot,digest,mime,width,height,bytes,outlier) VALUES(?,3,?,'image/jpeg',3000,2000,?,0)`,
		intent, digest, int64(2<<20))
	owner := ipArtworkSelectionMovieOwner()
	cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, owner, "poster",
		ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
	if out, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, cmd); true {
		if out.Outcome.State != Applied || out.Value.Count != 1 {
			t.Fatal(out)
		}
		ipPolicyCommitTurn(t, turn, tx)
	}
	owner = ipArtworkSelectionAdvance(t, w, owner, 1)
	clear := ipArtworkSelectionCommandFor(t, IPArtworkClear, 2, 3, owner, "poster",
		ipArtworkSelectionBeforeFor(t, w, owner, "poster"), ipArtworkSelectionFacts{})
	out, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, clear)
	if out.Outcome.State != Applied || out.Value.Count != 1 || out.Value.Selected != "" ||
		out.Value.Previous != digest {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM artwork_selections`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=0 AND original_bytes=?`, digest, int64(2<<20))
	ipArtworkSelectionExpect(t, w, 3, `SELECT count(*) FROM artwork_blobs`)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_material_intents WHERE id=? AND state='installed' AND staged_slots=4`, intent)
	ipArtworkSelectionExpect(t, w, 4, `SELECT count(*) FROM artwork_material_slots WHERE intent_id=?`, intent)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_material_slots WHERE intent_id=? AND slot=3 AND digest=? AND bytes=?`, intent, digest, int64(2<<20))
	ipArtworkSelectionExpect(t, w, 2, `SELECT count(*) FROM metadata_owner_history_artwork WHERE digest=?`, digest)

	// A source already marked retiring by another owner is still not deleted by
	// our clear; the writer never infers physical exit from a selection removal.
	ipPolicyExec(t, w, `UPDATE artwork_sources SET retiring=1,retirement_token=X'0f000000000000000000000000000000' WHERE digest=?`, digest)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=1 AND original_bytes=?`, digest, int64(2<<20))
}

// Every fresh witness is required before a selection row is written.
func TestIPArtworkSelectionFences(t *testing.T) {
	cases := []struct {
		name   string
		sql    []string
		seed   bool
		mutate func(*ipArtworkSelectionRequest)
	}{
		{"stale_entity_revision", []string{`UPDATE catalog_entities SET artwork_revision=2 WHERE id=778`}, false, nil},
		{"retired_entity", []string{`UPDATE catalog_entities SET retired=1 WHERE id=778`}, false, nil},
		{"retired_library", []string{`UPDATE catalog_libraries SET retired=1 WHERE id=777`}, false, nil},
		{"stale_language", []string{`UPDATE metadata_runtime_settings SET artwork_language='textless'`}, false, nil},
		{"stale_policy", []string{`UPDATE metadata_runtime_settings SET artwork_policy_revision=2`}, false, nil},
		{"stale_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_owner=8 WHERE id=901`}, false, nil},
		{"expired_owner_lease", []string{`UPDATE artwork_subject_jobs SET job_lease_expires_ms=1 WHERE id=901`}, false, nil},
		{"wrong_phase", []string{`UPDATE artwork_subject_jobs SET phase='providers' WHERE id=901`}, false, nil},
		{"stale_owner_revision", nil, false, func(q *ipArtworkSelectionRequest) { q.owner.revision = 2 }},
		{"missing_source", nil, false, func(q *ipArtworkSelectionRequest) { q.after = ipArtworkSelectionFactsFor(ipArtworkSelectionHex(9)) }},
		{"retiring_source", []string{`UPDATE artwork_sources SET retiring=1,retirement_token=X'0f000000000000000000000000000000' WHERE digest=?`}, false, nil},
		{"deleting_blob", []string{`UPDATE artwork_blobs SET status='deleting',deletion_token=X'0f000000000000000000000000000000' WHERE digest=?`}, false, nil},
		{"locked_revision_changed", nil, true, func(q *ipArtworkSelectionRequest) { q.before.revision = 2 }},
		{"locked_digest_changed", nil, true, func(q *ipArtworkSelectionRequest) {
			q.before.facts = ipArtworkSelectionFactsFor(ipArtworkSelectionHex(9))
		}},
		{"locked_actor_changed", nil, true, func(q *ipArtworkSelectionRequest) { q.before.actor = "other" }},
		{"matched_is_not_lock", nil, true, func(q *ipArtworkSelectionRequest) {
			// The matched row is not the locked row: claiming it as the owner's
			// previous choice is a conflict, not ownership.
			q.before = ipArtworkSelectionBefore{present: true, revision: 1, actor: "provider",
				facts: ipArtworkSelectionFactsFor(ipArtworkSelectionHex(1))}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipArtworkSelectionFixture(t)
			digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)
			next, nextBlobs := seedIPArtworkSelectionSource(t, w, 2, false)
			owner := ipArtworkSelectionMovieOwner()
			if tc.seed {
				if tc.name == "matched_is_not_lock" {
					ipArtworkSelectionSeedMatched(t, w, owner, "poster", digest)
				} else {
					ipPolicyExec(t, w, `INSERT INTO artwork_selections(kind,entity_id,role,choice,revision,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,actor,observed_at) VALUES('entity',778,'poster','locked',1,?,'tmdb','image-001','https://example.invalid/art','Example','','en','provider-fence','owner','2026-01-01T00:00:00Z')`, digest)
				}
			}
			for _, query := range tc.sql {
				if strings.Contains(query, "?") {
					bind := any(next)
					if tc.name == "deleting_blob" {
						bind = nextBlobs[0]
					}
					ipPolicyExec(t, w, query, bind)
				} else {
					ipPolicyExec(t, w, query)
				}
			}
			cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, owner, "poster",
				ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(next))
			if tc.seed {
				cmd = ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, owner, "poster",
					ipArtworkSelectionBeforeFor(t, w, owner, "poster"), ipArtworkSelectionFactsFor(next))
			}
			if tc.mutate != nil {
				tc.mutate(&cmd.request)
				d, err := canonicalRequestDigest(cmd.kind, 1, cmd.identity, cmd.request)
				if err != nil {
					t.Fatal(err)
				}
				cmd.digest = d
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkSelect)
			out, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipArtworkSelectionCommandResult{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("stale selection admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			want := int64(0)
			if tc.seed {
				want = 1
			}
			ipArtworkSelectionExpect(t, w, want, `SELECT count(*) FROM artwork_selections`)
			ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM metadata_owner_history`)
			ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM metadata_owner_history_artwork`)
			ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM artwork_command_receipts`)
			ipArtworkSelectionExpect(t, w, 1, `SELECT artwork_revision FROM catalog_entities WHERE id=778`)
		})
	}
}

// Sequence, identity and request-digest integrity: no implicit allocation, no
// second owner choice for the same original result, no foreign owner.
func TestIPArtworkSelectionSequenceAndIdentity(t *testing.T) {
	t.Run("sequence_gap", func(t *testing.T) {
		w, r, p := ipArtworkSelectionFixture(t)
		digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)
		cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 2, 3, ipArtworkSelectionMovieOwner(), "poster",
			ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkSelect)
		out, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, cmd)
		if out != (ipArtworkSelectionCommandResult{}) || !errors.Is(err, ErrConflict) {
			t.Fatal("sequence gap admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM artwork_command_receipts`)
		ipArtworkSelectionExpect(t, w, 0, `SELECT count(*) FROM artwork_selections`)
	})
	t.Run("conflicting_request", func(t *testing.T) {
		w, r, p := ipArtworkSelectionFixture(t)
		digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)
		other, _ := seedIPArtworkSelectionSource(t, w, 2, false)
		first := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, ipArtworkSelectionMovieOwner(), "poster",
			ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
		if _, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, first); true {
			ipPolicyCommitTurn(t, turn, tx)
		}
		conflicting := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, ipArtworkSelectionMovieOwner(), "poster",
			ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(other))
		conflicting.digest = first.digest
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkSelect)
		out, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, conflicting)
		if out != (ipArtworkSelectionCommandResult{}) || err == nil {
			t.Fatal("tampered request admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
		ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_command_receipts`)
		ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE digest=?`, digest)
	})
	t.Run("missing_owner", func(t *testing.T) {
		w, r, p := ipArtworkSelectionFixture(t)
		digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)
		owner := ipArtworkSelectionMovieOwner()
		owner.id = 999
		cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, owner, "poster",
			ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkSelect)
		out, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, cmd)
		if out != (ipArtworkSelectionCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
			t.Fatal("missing owner admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
	t.Run("foreign_incarnation", func(t *testing.T) {
		w, r, p := ipArtworkSelectionFixture(t)
		digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)
		owner := ipArtworkSelectionMovieOwner()
		owner.incarnation = [16]byte{9}
		cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, owner, "poster",
			ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
		turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkSelect)
		out, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, cmd)
		if out != (ipArtworkSelectionCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
			t.Fatal("foreign incarnation admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

// Current authority and the real turn lifetime gate every selection before any
// custody byte.
func TestIPArtworkSelectionCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership", "cancelled_lifetime"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipArtworkSelectionFixture(t)
			digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)
			cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, ipArtworkSelectionMovieOwner(), "poster",
				ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
			lifetime := context.Background()
			if mode == "cancelled_lifetime" {
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				lifetime = cancelled
			} else {
				p = ipPolicyRevoke(t, w, p, mode)
			}
			turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkSelect)
			out, err := applyIPArtworkSelection(t.Context(), turn, p, lifetime, b, cmd)
			if out != (ipArtworkSelectionCommandResult{}) || err == nil {
				t.Fatal("stale authority/cancelled lifetime admitted", out, err)
			}
			if mode != "cancelled_lifetime" && !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			ipArtworkSelectionClean(t, w, 0)
		})
	}
}

// An uncertain COMMIT is durable truth only through a FRESH read-only
// connection; the quarantined pool serves no saved choice afterwards.
func TestIPArtworkSelectionUnknownCommitFreshRead(t *testing.T) {
	w, r, p := ipArtworkSelectionFixture(t)
	digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)
	cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, ipArtworkSelectionMovieOwner(), "poster",
		ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkSelect)
	out, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if again, e := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, cmd); again != (ipArtworkSelectionCommandResult{}) || e == nil {
		t.Fatal("uncertain frame served the saved selection", again, e)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM artwork_selections WHERE choice='locked' AND digest='` + digest + `' AND revision=1`, 1},
		{`SELECT count(*) FROM metadata_owner_history WHERE trigger='artwork_select'`, 1},
		{`SELECT count(*) FROM metadata_owner_history_artwork WHERE digest='` + digest + `'`, 1},
		{`SELECT count(*) FROM artwork_command_receipts WHERE command_kind='select' AND accepted_revision=1`, 1},
		{`SELECT count(*) FROM artwork_command_receipts`, 1},
		{`SELECT last_command_sequence FROM artwork_subject_jobs WHERE id=901`, 1},
		{`SELECT artwork_revision FROM catalog_entities WHERE id=778`, 2},
		{`SELECT count(*) FROM artwork_sources WHERE digest='` + digest + `'`, 1},
	} {
		if got := ipPolicyFreshInt(t, path, check.query); got != check.want {
			t.Fatal("fresh read", check.query, got, check.want)
		}
	}
}

// The original saved result replays exactly after source, policy, lease, history
// and automatic rows move; at or below ACK it is Retired instead.
func TestIPArtworkSelectionReplayAfterChangesAndRetired(t *testing.T) {
	w, r, p := ipArtworkSelectionFixture(t)
	digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)
	cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 1, 2, ipArtworkSelectionMovieOwner(), "poster",
		ipArtworkSelectionBefore{}, ipArtworkSelectionFactsFor(digest))
	out, turn, tx, _ := applyIPArtworkSelectionCommand(t, w, r, p, cmd)
	if out.Outcome.State != Applied || out.Value.Count != 1 {
		t.Fatal(out)
	}
	ipPolicyCommitTurn(t, turn, tx)
	original := out

	ipPolicyExec(t, w, `UPDATE catalog_entities SET artwork_revision=9 WHERE id=778`)
	ipPolicyExec(t, w, `UPDATE metadata_runtime_settings SET artwork_language='textless',artwork_policy_revision=9`)
	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET job_revision=99,job_lease_owner=9 WHERE id=901`)
	ipPolicyExec(t, w, `UPDATE artwork_sources SET retiring=1,revision=7,retirement_token=X'0f000000000000000000000000000000' WHERE digest=?`, digest)
	ipArtworkSelectionSeedMatched(t, w, ipArtworkSelectionMovieOwner(), "poster", digest)

	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkSelect)
	replay, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != original.Digest ||
		replay.Value != original.Value {
		t.Fatal("source change altered the original selection", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)

	ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=1 WHERE id=901`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkSelect)
	retired, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || retired.Outcome.State != ResultRetired ||
		retired.Value != (ipArtworkSelectionResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired selection served its result", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_selections WHERE choice='locked' AND digest=?`, digest)
	ipArtworkSelectionExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=1`, digest)
}

// A tampered selection receipt can never replay as an owner choice: over-long
// text, a foreign label, a non-locked choice, impossible count/history and clear
// rows carrying a digest are all refused.
func TestIPArtworkSelectionMalformedReceiptRefused(t *testing.T) {
	const insert = `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,lease_generation,lease_until_ms,role,choice,selection_revision,selected_digest,previous_digest,history_id) VALUES(901,X'01000000000000000000000000000000',2,?,?,1,?,?,1,?,1,?,?,?,?,?,?,?)`
	type malformed struct {
		name              string
		kind              CommandID
		label             string
		role              any
		choice            any
		count             int64
		selectionRevision int64
		selected          any
		previous          any
		history           any
	}
	cases := []malformed{
		{name: "over_long_role", kind: IPArtworkSelect, label: "select", role: strings.Repeat("r", 65),
			choice: "locked", count: 0, selectionRevision: 1},
		{name: "matched_choice", kind: IPArtworkSelect, label: "select", role: "poster",
			choice: "matched", count: 0, selectionRevision: 1},
		{name: "foreign_label", kind: IPArtworkSelect, label: "material_publish", role: "poster",
			choice: "locked", count: 0, selectionRevision: 1},
		{name: "count_without_history", kind: IPArtworkSelect, label: "select", role: "poster",
			choice: "locked", count: 1, selectionRevision: 1},
		{name: "history_without_count", kind: IPArtworkSelect, label: "select", role: "poster",
			choice: "locked", count: 0, selectionRevision: 1},
		{name: "selection_revision_mismatch", kind: IPArtworkSelect, label: "select", role: "poster",
			choice: "locked", count: 1, selectionRevision: 2},
		{name: "wrong_selected", kind: IPArtworkSelect, label: "select", role: "poster",
			choice: "locked", count: 1, selectionRevision: 1},
		{name: "clear_with_selected", kind: IPArtworkClear, label: "clear", role: "poster",
			choice: "locked", count: 0, selectionRevision: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, _ := ipArtworkSelectionFixture(t)
			digest, _ := seedIPArtworkSelectionSource(t, w, 1, false)
			other, _ := seedIPArtworkSelectionSource(t, w, 2, false)
			after := ipArtworkSelectionFactsFor(digest)
			if tc.kind == IPArtworkClear {
				after = ipArtworkSelectionFacts{}
			}
			cmd := ipArtworkSelectionCommandFor(t, tc.kind, 2, 7, ipArtworkSelectionMovieOwner(), "poster",
				ipArtworkSelectionBefore{}, after)
			ipPolicyExec(t, w, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('entity',778,'1','2','artwork_select','owner','2026-01-01T00:00:00Z','{}','{}')`)
			history := ipPolicyQueryInt(t, w, `SELECT max(id) FROM metadata_owner_history`)
			var historyArg any = history
			var selectedArg any = digest
			switch tc.name {
			case "count_without_history":
				historyArg = nil
			case "history_without_count":
				historyArg, selectedArg = history, nil
			case "wrong_selected":
				selectedArg = other
			}
			ipPolicyExec(t, w, insert, cmd.identity.OperationID[:], cmd.digest[:],
				ipArtworkSelectionBytes32(4),
				tc.label, tc.count, int64(1)<<50, tc.role, tc.choice, tc.selectionRevision,
				selectedArg, nil, historyArg)
			ipPolicyExec(t, w, `UPDATE artwork_subject_jobs SET last_command_sequence=2,acknowledged_command_sequence=1 WHERE id=901`)
			turn, tx, _ := ipPolicyTurn(t, w, r, tc.kind)
			if _, err := ipArtworkSelectionLookup(t.Context(), turn, tc.kind, cmd.request, cmd.identity, cmd.digest); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("malformed selection receipt decoded", err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

func TestIPArtworkSelectionBoundsAndDescriptors(t *testing.T) {
	owner := ipArtworkSelectionMovieOwner()
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 1, OperationID: [16]byte{2}}
	digest := ipArtworkSelectionHex(1)
	base := func() ipArtworkSelectionRequest {
		return ipArtworkSelectionRequest{owner: owner, role: "poster", language: "en", policyRevision: 1,
			actor: "owner", before: ipArtworkSelectionBefore{}, after: ipArtworkSelectionFactsFor(digest)}
	}
	for name, mutate := range map[string]func(*ipArtworkSelectionRequest){
		"both_namespaces":  func(q *ipArtworkSelectionRequest) { q.owner.personID = 778 },
		"no_namespace":     func(q *ipArtworkSelectionRequest) { q.owner.entityID = 0 },
		"zero_lease_owner": func(q *ipArtworkSelectionRequest) { q.owner.leaseOwner = 0 },
		"zero_lease_until": func(q *ipArtworkSelectionRequest) { q.owner.leaseUntilMS = 0 },
		"zero_source":      func(q *ipArtworkSelectionRequest) { q.owner.sourceIncarnation = [16]byte{} },
		"zero_revision":    func(q *ipArtworkSelectionRequest) { q.owner.revision = 0 },
		"foreign_incarn":   func(q *ipArtworkSelectionRequest) { q.owner.incarnation = [16]byte{9} },
		"bad_role":         func(q *ipArtworkSelectionRequest) { q.role = "thumbs" },
		"long_role":        func(q *ipArtworkSelectionRequest) { q.role = strings.Repeat("r", 65) },
		"long_language":    func(q *ipArtworkSelectionRequest) { q.language = strings.Repeat("a", 65) },
		"zero_policy":      func(q *ipArtworkSelectionRequest) { q.policyRevision = 0 },
		"empty_actor":      func(q *ipArtworkSelectionRequest) { q.actor = "" },
		"long_actor":       func(q *ipArtworkSelectionRequest) { q.actor = strings.Repeat("a", ipTextActorMax+1) },
		"short_digest":     func(q *ipArtworkSelectionRequest) { q.after.digest = "abc" },
		"upper_digest":     func(q *ipArtworkSelectionRequest) { q.after.digest = strings.Repeat("A", 64) },
		"bad_provider":     func(q *ipArtworkSelectionRequest) { q.after.provider = "plex" },
		"empty_image":      func(q *ipArtworkSelectionRequest) { q.after.imageID = "" },
		"long_image": func(q *ipArtworkSelectionRequest) {
			q.after.imageID = strings.Repeat("i", ipArtworkSelectionMaxField+1)
		},
		"empty_origin": func(q *ipArtworkSelectionRequest) { q.after.origin = "" },
		"long_attribution": func(q *ipArtworkSelectionRequest) {
			q.after.attribution = strings.Repeat("a", ipArtworkSelectionMaxText+1)
		},
		"bad_locale":  func(q *ipArtworkSelectionRequest) { q.after.locale = "not a locale!!" },
		"empty_fence": func(q *ipArtworkSelectionRequest) { q.after.sourceFence = "" },
		"long_fence": func(q *ipArtworkSelectionRequest) {
			q.after.sourceFence = strings.Repeat("f", ipArtworkSelectionMaxField+1)
		},
		"object_half": func(q *ipArtworkSelectionRequest) { q.after.objectID = EvidenceText{Value: "art", Present: true} },
		"object_long": func(q *ipArtworkSelectionRequest) {
			q.after.objectID = EvidenceText{Value: strings.Repeat("o", ipArtworkSelectionMaxField+1), Present: true}
			q.after.objectRevision = EvidenceText{Value: "r1", Present: true}
		},
		"after_language":      func(q *ipArtworkSelectionRequest) { q.after.language = "fr" },
		"empty_digest_facts":  func(q *ipArtworkSelectionRequest) { q.after.digest = "" },
		"before_absent_facts": func(q *ipArtworkSelectionRequest) { q.before.facts = ipArtworkSelectionFactsFor(digest) },
		"before_zero_rev": func(q *ipArtworkSelectionRequest) {
			q.before = ipArtworkSelectionBefore{present: true, actor: "owner", facts: ipArtworkSelectionFactsFor(digest)}
		},
		"before_empty_actor": func(q *ipArtworkSelectionRequest) {
			q.before = ipArtworkSelectionBefore{present: true, revision: 1, facts: ipArtworkSelectionFactsFor(digest)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			q := base()
			mutate(&q)
			if c, err := newIPArtworkSelectionCommand(IPArtworkSelect, identity, q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed selection admitted", c, err)
			}
		})
	}
	t.Run("clear_with_after", func(t *testing.T) {
		q := base()
		if c, err := newIPArtworkSelectionCommand(IPArtworkClear, identity, q); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("clear with after facts admitted", c, err)
		}
	})
	t.Run("wrong_kind", func(t *testing.T) {
		if c, err := newIPArtworkSelectionCommand(IPArtworkMaterialPublish, identity, base()); c != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("foreign kind admitted", c, err)
		}
	})
	// Bounded maximum: the longest admissible provenance for one locked choice.
	t.Run("bounded_maximum", func(t *testing.T) {
		q := base()
		q.actor = strings.Repeat("a", ipTextActorMax)
		q.after.imageID = strings.Repeat("i", ipArtworkSelectionMaxField)
		q.after.origin = strings.Repeat("o", ipArtworkSelectionMaxField)
		q.after.attribution = strings.Repeat("a", ipArtworkSelectionMaxText)
		q.after.sourceFence = strings.Repeat("f", ipArtworkSelectionMaxField)
		if _, err := newIPArtworkSelectionCommand(IPArtworkSelect, identity, q); err != nil {
			t.Fatal("bounded maximum selection refused", err)
		}
	})
	for id, want := range map[CommandID]Descriptor{
		IPArtworkSelect: {IPArtworkSelect, "ip_artwork_select", Normal},
		IPArtworkClear:  {IPArtworkClear, "ip_artwork_clear", Normal},
	} {
		got, err := Describe(id)
		if err != nil || got != want {
			t.Fatal(got, err)
		}
	}
	good := ipArtworkSelectionSavedResult{IPArtworkSelect, ipArtworkSelectionResult{
		OwnerID: 901, Role: "poster", Choice: "locked", SelectionRevision: 1,
		Selected: digest, History: EvidenceInteger{Value: 1, Present: true}, Count: 1}}
	if err := preflightIPArtworkSelectionResult(good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ipArtworkSelectionSavedResult){
		"wrong_kind":      func(v *ipArtworkSelectionSavedResult) { v.Kind = 300 },
		"zero_owner":      func(v *ipArtworkSelectionSavedResult) { v.Value.OwnerID = 0 },
		"bad_role":        func(v *ipArtworkSelectionSavedResult) { v.Value.Role = "thumbs" },
		"matched_choice":  func(v *ipArtworkSelectionSavedResult) { v.Value.Choice = "matched" },
		"zero_revision":   func(v *ipArtworkSelectionSavedResult) { v.Value.SelectionRevision = 0 },
		"max_revision":    func(v *ipArtworkSelectionSavedResult) { v.Value.SelectionRevision = 1<<63 - 1 },
		"bad_count":       func(v *ipArtworkSelectionSavedResult) { v.Value.Count = 2 },
		"negative_time":   func(v *ipArtworkSelectionSavedResult) { v.Value.AppliedMS = -1 },
		"short_previous":  func(v *ipArtworkSelectionSavedResult) { v.Value.Previous = "abc" },
		"short_selected":  func(v *ipArtworkSelectionSavedResult) { v.Value.Selected = "abc" },
		"missing_history": func(v *ipArtworkSelectionSavedResult) { v.Value.History = EvidenceInteger{} },
		"spurious_history": func(v *ipArtworkSelectionSavedResult) {
			v.Value.Count = 0
			v.Value.Selected = ""
			v.Value.History = EvidenceInteger{Value: 1, Present: true}
		},
		"noop_changed": func(v *ipArtworkSelectionSavedResult) {
			v.Value.Count = 0
			v.Value.History = EvidenceInteger{}
		},
		"clear_selected": func(v *ipArtworkSelectionSavedResult) {
			v.Kind = IPArtworkClear
			v.Value.Selected = digest
		},
	} {
		v := good
		mutate(&v)
		if err := preflightIPArtworkSelectionResult(v); !errors.Is(err, ErrInvalid) {
			t.Fatal(name, err)
		}
	}
}

// No filesystem, provider or HTTP access can happen inside the SQLite turn.
func TestIPArtworkSelectionIOStaysOutsideHold(t *testing.T) {
	for _, file := range []string{
		"ip_artwork_selection_native.go",
		"ip_artwork_selection_result.go",
		"ip_artwork_selection_statements.go",
	} {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(file, err)
		}
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"os", "io", "io/fs", "path/filepath", "net", "net/http", "mime/multipart"} {
				if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
					t.Fatal(file, "performs IO inside the hold:", path)
				}
			}
		}
	}
}
