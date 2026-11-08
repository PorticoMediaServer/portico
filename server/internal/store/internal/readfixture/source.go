// Package readfixture factors existing test-only foreground seeds. Production
// packages must not import it. No issuer, grant or command authority is supplied.
package readfixture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

//go:embed source.go
var journal string

type browseFact struct {
	id        int64
	key, keep string
	retired   bool
	age       *int64
}
type browseFixture struct {
	sharedMovieKind     bool
	existingLibraryKeys []string
	temporalOrigin      time.Time
	temporalShift       time.Duration
	borrowed            bool
	path                string
	conn                *sql.Conn
	db                  *sql.DB
	facts               []browseFact
	credit              *creditAttachment
	creditReceipt       CreditReceipt
	closed              bool
}

// creditAttachment is set only when the attached database already owns a
// credit dictionary or credit image rows. The unchanged standalone journal then
// runs against resolved ids instead of its literal reserved ones.
type creditAttachment struct {
	labels  map[int64]int64 // fixture id -> actual catalog_credit_labels.id
	imageID int64
}
type Fact struct {
	ID        int64
	Key, Keep string
	Retired   bool
	Age       *int64
}
type Source struct{ source *browseFixture }

// Credit label/image collisions are refusals, never repair. A paired canonical
// search artifact may already own reserved ids; the fixture then reuses an
// exact label identity or allocates a fresh one, and never changes a meaning.
var ErrCreditLabelCollision = errors.New("readfixture: catalog credit label collision")

// ErrSearchBankStale means a search bank present in the attached database no
// longer describes its current catalogue source rows or engine/fingerprint.
var ErrSearchBankStale = errors.New("readfixture: search bank does not match the catalogue")

// CreditLabelCollisionError names the reserved id whose meaning differs.
type CreditLabelCollisionError struct {
	ID       int64
	Existing string
	Required string
}

func (e *CreditLabelCollisionError) Error() string {
	return fmt.Sprintf("readfixture: catalog_credit_labels id %d is %q, fixture requires %q", e.ID, e.Existing, e.Required)
}
func (e *CreditLabelCollisionError) Unwrap() error { return ErrCreditLabelCollision }

// SearchBankStaleError names the source witness that no longer matches.
type SearchBankStaleError struct{ Reason string }

func (e *SearchBankStaleError) Error() string {
	return "readfixture: search bank stale: " + e.Reason
}
func (e *SearchBankStaleError) Unwrap() error { return ErrSearchBankStale }

// CreditResolution is one fixture label id resolved into the attached database.
type CreditResolution struct {
	FixtureID int64
	Label     string
	ActualID  int64
	Inserted  bool
}

// CreditReceipt records the exact dictionary identity the attached fixture
// used. It is not authority, a repair or a production credit semantic.
type CreditReceipt struct {
	Resolutions        []CreditResolution
	CreditImageID      int64
	DictionarySHA256   string
	JournalSeededLabel bool
}

func (s *Source) CreditReceipt() CreditReceipt { return s.source.creditReceipt }

func (s *Source) TemporalOrigin() time.Time { return s.source.temporalOrigin }
func (s *Source) Path() string              { return s.source.path }
func (s *Source) Conn() *sql.Conn           { return s.source.conn }
func (s *Source) Facts() []Fact {
	out := make([]Fact, len(s.source.facts))
	for i, f := range s.source.facts {
		out[i] = Fact{f.id, f.key, f.keep, f.retired, f.age}
	}
	return out
}
func (s *Source) CloseSetup() error { return s.source.close() }
func (f *browseFixture) close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	if f.borrowed {
		return nil
	}
	return errors.Join(f.conn.Close(), f.db.Close())
}

// One bounded setup attachment, consumed once by the unchanged seed journals.
// Keying by the exact test owner prevents another parallel fixture borrowing it.
var attachment struct {
	sync.Mutex
	owner  *testing.T
	source *browseFixture
}

// ForegroundOn is the fatal convenience form of AttachForegroundOn.
func ForegroundOn(t *testing.T, path string, conn *sql.Conn, existingLibraryKeys ...string) *Source {
	t.Helper()
	source, err := AttachForegroundOn(t, path, conn, existingLibraryKeys...)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// AttachForegroundOn attaches the unchanged foreground journal to an existing
// owned database. A paired canonical search artifact may already contain a
// credit dictionary and search bank; this entry point refuses a stale bank and
// resolves the credit collision by exact label identity before seeding. It
// never deletes/overwrites an existing row and never changes a label's meaning.
func AttachForegroundOn(t *testing.T, path string, conn *sql.Conn, existingLibraryKeys ...string) (*Source, error) {
	t.Helper()
	if conn == nil || path == "" {
		return nil, errors.New("missing owned fixture attachment")
	}
	if len(existingLibraryKeys) > 1 || (len(existingLibraryKeys) == 1 && existingLibraryKeys[0] != "qualification-movies") {
		return nil, errors.New("closed canonical foreground library attachment required")
	}
	if err := verifySearchBankCatalogue(t.Context(), conn); err != nil {
		return nil, err
	}
	credit, receipt, err := resolveCreditDictionary(t.Context(), conn)
	if err != nil {
		return nil, err
	}
	origin := time.Now().UTC()
	f := &browseFixture{existingLibraryKeys: append([]string(nil), existingLibraryKeys...), path: path, conn: conn, borrowed: true, sharedMovieKind: true, temporalOrigin: origin, temporalShift: origin.Sub(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)), credit: credit, creditReceipt: receipt}
	attachment.Lock()
	if attachment.owner != nil {
		attachment.Unlock()
		return nil, errors.New("fixture attachment already owned")
	}
	attachment.owner, attachment.source = t, f
	attachment.Unlock()
	defer func() {
		attachment.Lock()
		if attachment.owner == t {
			attachment.owner, attachment.source = nil, nil
		}
		attachment.Unlock()
	}()
	return &Source{newForegroundSource(t)}, nil
}

// verifySearchBankCatalogue refuses a bank that no longer describes the
// catalogue rows it indexes, or that was built for another engine/fingerprint.
// A database with no search publications is the ordinary foreground fixture and
// is left alone.
func verifySearchBankCatalogue(ctx context.Context, conn *sql.Conn) error {
	var present bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM search_publications)`).Scan(&present); err != nil {
		return err
	}
	if !present {
		return nil
	}
	profile := searchpolicy.Compiled()
	var recipe, engine, fingerprint, state string
	var pending, indexedRows, indexedBytes int64
	err := conn.QueryRowContext(ctx, `SELECT recipe_version,engine_version,unicode_fingerprint,state,pending_subjects,indexed_rows,indexed_bytes FROM search_generations WHERE id=1 AND bank_key='v1'`).Scan(&recipe, &engine, &fingerprint, &state, &pending, &indexedRows, &indexedBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return &SearchBankStaleError{Reason: "one v1 generation is required"}
	}
	if err != nil {
		return err
	}
	if recipe != profile.RecipeVersion || engine != profile.EngineVersion || fingerprint != profile.UnicodeFingerprint {
		return &SearchBankStaleError{Reason: "engine/fingerprint differs from the compiled search profile"}
	}
	if state != "active" || pending != 0 {
		return &SearchBankStaleError{Reason: fmt.Sprintf("generation state=%q pending=%d", state, pending)}
	}
	var entityMismatch, personMismatch, missingEntity, missingPerson, publications, fragments int64
	if err := conn.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM search_publications p LEFT JOIN catalog_entities e ON e.id=p.entity_id
  WHERE p.entity_id IS NOT NULL AND (e.id IS NULL OR p.title<>e.title OR p.library_id<>e.library_id OR p.kind<>e.kind OR p.source_revision<>e.revision OR p.source_incarnation<>e.metadata_incarnation)),
 (SELECT count(*) FROM search_publications p LEFT JOIN catalog_people q ON q.id=p.person_id
  WHERE p.person_id IS NOT NULL AND (q.id IS NULL OR p.title<>q.name OR p.source_revision<>q.revision OR p.source_incarnation<>q.search_incarnation)),
 (SELECT count(*) FROM catalog_entities e WHERE e.retired=0 AND NOT EXISTS (SELECT 1 FROM search_publications p WHERE p.entity_id=e.id)),
 (SELECT count(*) FROM catalog_people q WHERE NOT EXISTS (SELECT 1 FROM search_publications p WHERE p.person_id=q.id)),
 (SELECT count(*) FROM search_publications),
 (SELECT count(*) FROM search_fragments)`).Scan(&entityMismatch, &personMismatch, &missingEntity, &missingPerson, &publications, &fragments); err != nil {
		return err
	}
	if entityMismatch != 0 || personMismatch != 0 {
		return &SearchBankStaleError{Reason: fmt.Sprintf("publication/source mismatch entities=%d people=%d", entityMismatch, personMismatch)}
	}
	if missingEntity != 0 || missingPerson != 0 {
		return &SearchBankStaleError{Reason: fmt.Sprintf("current source without publication entities=%d people=%d", missingEntity, missingPerson)}
	}
	if indexedRows != fragments || publications == 0 || indexedBytes <= 0 {
		return &SearchBankStaleError{Reason: fmt.Sprintf("indexed rows=%d fragments=%d bytes=%d", indexedRows, fragments, indexedBytes)}
	}
	return nil
}

// resolveCreditDictionary returns a resolution only when the database already
// owns credit state. Reserved fixture ids 1..4 must either be absent or carry
// exactly the fixture's meaning; any other meaning is a named refusal. Labels
// found at another id are reused exactly, and absent labels are appended with
// fresh ids. The stored receipt hashes the final dictionary, not a plan.
func resolveCreditDictionary(ctx context.Context, conn *sql.Conn) (*creditAttachment, CreditReceipt, error) {
	receipt := CreditReceipt{}
	rows, err := conn.QueryContext(ctx, `SELECT id,label FROM catalog_credit_labels ORDER BY id`)
	if err != nil {
		return nil, receipt, err
	}
	type label struct {
		id    int64
		label string
	}
	var existing []label
	var maxID int64
	byLabel := map[string]int64{}
	for rows.Next() {
		var item label
		if err := rows.Scan(&item.id, &item.label); err != nil {
			rows.Close()
			return nil, receipt, err
		}
		existing = append(existing, item)
		byLabel[item.label] = item.id
		if item.id > maxID {
			maxID = item.id
		}
	}
	if err := rows.Close(); err != nil {
		return nil, receipt, err
	}
	var creditImages int64
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM catalog_credit_images`).Scan(&creditImages); err != nil {
		return nil, receipt, err
	}
	if len(existing) == 0 && creditImages == 0 {
		receipt.CreditImageID = 1
		receipt.JournalSeededLabel = true
		return nil, receipt, nil
	}
	required := []struct {
		id    int64
		label string
	}{{1, "Actor"}, {2, "Acting"}, {3, "Director"}, {4, "Directing"}}
	resolved := make(map[int64]int64, len(required))
	for _, want := range required {
		if actual, present := byLabel[want.label]; present {
			resolved[want.id] = actual
		}
	}
	for _, want := range required {
		for _, item := range existing {
			if item.id == want.id && item.label != want.label {
				return nil, receipt, &CreditLabelCollisionError{ID: want.id, Existing: item.label, Required: want.label}
			}
		}
	}
	next := maxID
	inserted := make(map[int64]bool, len(required))
	for _, want := range required {
		if _, ok := resolved[want.id]; ok {
			continue
		}
		next++
		if _, err := conn.ExecContext(ctx, `INSERT INTO catalog_credit_labels(id,label) VALUES(?,?)`, next, want.label); err != nil {
			return nil, receipt, err
		}
		existing = append(existing, label{id: next, label: want.label})
		byLabel[want.label] = next
		resolved[want.id] = next
		inserted[want.id] = true
	}
	for _, want := range required {
		receipt.Resolutions = append(receipt.Resolutions, CreditResolution{FixtureID: want.id, Label: want.label, ActualID: resolved[want.id], Inserted: inserted[want.id]})
	}
	imageID := int64(1)
	if creditImages != 0 {
		if err := conn.QueryRowContext(ctx, `SELECT coalesce(max(id),0)+1 FROM catalog_credit_images`).Scan(&imageID); err != nil {
			return nil, receipt, err
		}
	}
	receipt.CreditImageID = imageID
	receipt.JournalSeededLabel = false
	sort.Slice(existing, func(i, j int) bool { return existing[i].id < existing[j].id })
	digest := sha256.New()
	for _, item := range existing {
		fmt.Fprintf(digest, "%d:%s\n", item.id, item.label)
	}
	receipt.DictionarySHA256 = fmt.Sprintf("%x", digest.Sum(nil))
	return &creditAttachment{labels: resolved, imageID: imageID}, receipt, nil
}

func newBrowseSource(t *testing.T) *browseFixture {
	t.Helper()
	attachment.Lock()
	var attached *browseFixture
	if attachment.owner == t {
		attached = attachment.source
		attachment.owner, attachment.source = nil, nil
	}
	attachment.Unlock()
	if attached != nil {
		seedBrowse(t, attached)
		return attached
	}
	f := &browseFixture{path: schematest.Clone(t)}
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.db = db
	f.conn, err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.close(); err != nil {
			t.Error(err)
		}
	})
	seedBrowse(t, f)
	return f
}
func (f *browseFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	// N1 already seeded this exact movie kind. The paired cell exercises only
	// kind1; omit the existing two-kind seed statement, never repair/delete rows.
	if f.sharedMovieKind && strings.HasPrefix(q, "INSERT INTO catalog_kinds(") {
		return
	}
	// The same trusted fixture membership can also select the exact canonical
	// parent. Keep the standalone journal/overlay unchanged; no production authority.
	if len(f.existingLibraryKeys) != 0 && strings.HasPrefix(q, "INSERT INTO direct_memberships(") {
		if strings.Count(q, `'["movies"]'`) != 1 {
			t.Fatal("canonical fixture membership journal literal missing or ambiguous")
		}
		allowed, err := json.Marshal(append([]string{"movies"}, f.existingLibraryKeys...))
		if err != nil {
			t.Fatal(err)
		}
		q = strings.Replace(q, `'["movies"]'`, "'"+strings.ReplaceAll(string(allowed), "'", "''")+"'", 1)
	}
	// A paired canonical search artifact already owns catalog_credit_labels and
	// catalog_credit_images. Keep its rows exactly: skip only the journal's
	// reserved-id dictionary insert after resolveCreditDictionary proved/created
	// the exact meanings, and route the fixture's own credit image and credit
	// role/department ids through the resolved ids. Never INSERT OR IGNORE.
	if f.credit != nil {
		switch {
		case strings.HasPrefix(q, "INSERT INTO catalog_credit_labels("):
			return
		case strings.HasPrefix(q, "INSERT INTO catalog_credit_images("):
			q = strings.Replace(q, "SELECT 1,id,", fmt.Sprintf("SELECT %d,id,", f.credit.imageID), 1)
		case strings.HasPrefix(q, "UPDATE catalog_entities SET credit_image_id=1 "):
			q = strings.Replace(q, "credit_image_id=1", fmt.Sprintf("credit_image_id=%d", f.credit.imageID), 1)
		case strings.HasPrefix(q, "INSERT INTO catalog_credits("):
			if len(args) != 6 {
				t.Fatal("unexpected fixture credit row shape")
			}
			q = strings.Replace(q, "VALUES(1,1,", fmt.Sprintf("VALUES(%d,1,", f.credit.imageID), 1)
			for index := 0; index < 2; index++ {
				id, ok := args[index+1].(int64)
				actual, resolved := f.credit.labels[id]
				if !ok || !resolved {
					t.Fatalf("fixture credit label id %v has no exact resolution", args[index+1])
				}
				args[index+1] = actual
			}
		}
	}
	// Preserve the existing synthetic authority/data time offsets, but rebase
	// them ONCE before paired preparation to the actual clock. Standalone RD2
	// seeds remain byte-for-byte unchanged; failed inputs are never repaired.
	if !f.temporalOrigin.IsZero() {
		shift := func(value string) string {
			stamp, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return value
			}
			return stamp.Add(f.temporalShift).Format(time.RFC3339Nano)
		}
		for _, literal := range []string{"2026-11-05T00:00:00Z", "2026-11-06T00:00:00Z", "2026-11-07T00:00:00Z", "2026-11-06T12:00:00.000000000Z"} {
			q = strings.ReplaceAll(q, literal, shift(literal))
		}
		for i, arg := range args {
			if value, ok := arg.(string); ok {
				args[i] = shift(value)
			}
		}
	}
	if _, err := f.conn.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func Browse(t *testing.T) *Source     { return &Source{newBrowseSource(t)} }
func Foreground(t *testing.T) *Source { return &Source{newForegroundSource(t)} }
func sourcePart(t *testing.T, start, end string) []byte {
	t.Helper()
	a, b := strings.Index(journal, "\n"+start)+1, strings.Index(journal, "\n"+end)+1
	if a <= 0 || b <= a {
		t.Fatal("missing factored source journal")
	}
	return []byte(journal[a:b])
}
func BrowseHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256(sourcePart(t, "func seedBrowse(", "// END_BROWSE_JOURNAL")))
}
func ForegroundHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(BrowseHash(t)), []byte(strings.TrimSuffix(string(sourcePart(t, "func newForegroundSource(", "// END_FOREGROUND_JOURNAL")), "\n"))...)))
}

func seedBrowse(t *testing.T, f *browseFixture) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`,
		`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('a','alice',x'00','p'),('b','bob',x'00','q')`,
		`INSERT INTO direct_memberships(account_id,role,allowed_libraries) VALUES('a','member','["movies"]'),('b','member','["other"]')`,
		`INSERT INTO direct_profiles(id,account_id,name) VALUES('p','a','Alice'),('q','b','Bob')`,
		`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('d','a','i','fixture','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`,
		`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation) SELECT 'f','s','a','p','local','member',1,'2026-11-07T00:00:00Z',1,'d',m.incarnation,p.lifetime,x'00800000000000000000000000000001' FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id='a' AND p.id='p'`,
		`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('token','f',1,'2026-11-07T00:00:00Z')`,
		`INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movie','/media'),('other','Other','movie','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(1,'movies','/media'),(2,'other','/media')`,
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(2,'show',0,1,1,1,0)`,
	} {
		f.exec(t, q)
	}
	f.exec(t, "BEGIN")
	for id := int64(1); id <= 1100; id++ {
		key, keep := "a", "The A"
		if id%2 == 0 {
			key = "A"
			keep = "the a"
		}
		if id > 1050 {
			key = "b"
			keep = "B"
		}
		if id <= 5 {
			key = ""
			keep = ""
		}
		retired := id%9 == 0
		var age any
		var agePtr *int64
		if id%3 != 0 {
			v := int64(id % 20)
			age = v
			agePtr = &v
		}
		f.facts = append(f.facts, browseFact{id: id, key: key, keep: keep, retired: retired, age: agePtr})
		library := int64(1)
		if id == 1040 {
			library = 2
		}
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,1)`, id, public, library)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age) VALUES(?,?,?,1,?,?,?,?,?,0,?,?,?,?)`, id, public, library, fmt.Sprintf("Title %d", id), key, key, keep, keep, public, public, retired, age)
	}
	f.exec(t, "COMMIT")
}

// END_BROWSE_JOURNAL

func newForegroundSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	for _, q := range []string{
		`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(1,'asset','/media/title.mkv',1000,1,'matroska','h264','aac',1920,1080,120.0,1)`,
		`INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1,1,1)`,
		`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) VALUES('asset',1,1000,1,'f')`,
		`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES('asset',1,1000,1,'ready','f')`,
		`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES('asset',0,'video','h264','','',0,'',1,0),('asset',1,'audio','aac','eng','English',2,'stereo',1,0)`,
		`INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES('asset',0,'Opening',0.0,60.0),('asset',1,'End',60.0,120.0)`,
		`INSERT INTO catalog_credit_labels(id,label) VALUES(1,'Actor'),(2,'Acting'),(3,'Director'),(4,'Directing')`,
		`INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) SELECT 1,id,metadata_incarnation,1,1,1,1,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16),1,zeroblob(32) FROM catalog_entities WHERE id=1`,
		`UPDATE catalog_entities SET credit_image_id=1 WHERE id=1`,
		`UPDATE catalog_entities SET parent_id=1 WHERE id BETWEEN 2 AND 100`,
	} {
		f.exec(t, q)
	}
	for _, q := range []string{
		`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(printf('%064d',1),'image/png',1,1,1,'2026-11-06T00:00:00Z')`,
		`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT printf('%064d',1),'image/png',1,1,digest,digest,digest,1280,0,0,1,created_at FROM artwork_blobs`,
		`INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) SELECT 'entity',1,'poster','matched',digest,'provider','matched','provider','','f','test',created_at FROM artwork_sources`,
		`INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) SELECT 'entity',1,'poster','locked',digest,'owner','locked','owner','','f','test',created_at FROM artwork_sources`,
	} {
		f.exec(t, q)
	}
	f.exec(t, "BEGIN")
	for id := int64(0); id < 150; id++ {
		role, dept := int64(1), int64(2)
		if id >= 100 {
			role, dept = 3, 4
		}
		f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(1,1,?,?,?,?,'test',?,?)`, id, role, dept, fmt.Sprintf("Person %d", id), fmt.Sprint(id), id)
	}
	for id := int64(1); id <= 200; id++ {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		at := "2026-11-05T12:00:00.000000000Z"
		if id > 100 {
			at = "2026-11-05T11:00:00.000000000Z"
		}
		f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,resume_position,activity_at,activity_state) VALUES('p',?,?,1,500,?,'in_progress')`, public, id, at)
	}
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,resume_position,activity_at,activity_state) SELECT 'q',public_id,id,library_id,999,'2026-11-06T12:00:00.000000000Z','in_progress' FROM catalog_entities WHERE id=1`)
	f.exec(t, "COMMIT")
	return f
}

// END_FOREGROUND_JOURNAL
