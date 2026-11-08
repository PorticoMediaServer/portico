package read

// SCREENING ONLY (scratch branch, never merged): PB's v1 search bank builder
// (copied verbatim in zz_bank_screen_pb_test.go) over a canonical-shaped title
// population, read through the real native full-search reader, checked by RD's
// exact oracle. Directional evidence, not qualification.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// The canonical generator's phrase selection (SplitMix64, seed 0).
func screenTitle(seed uint32, ordinal int) string {
	x := uint64(seed)<<32 | uint64(ordinal)
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	words := [...]string{"The Amber River", "A Quiet Orbit", "An Autumn Map", "Blue Harbour", "Étoile", "9 Lanterns", "North & South", "The Glass Island"}
	return fmt.Sprintf("%s %07d", words[x%uint64(len(words))], ordinal)
}

func screenBankAtScale(t *testing.T, residents int) {
	t.Helper()
	f := newBrowseSource(t)
	started := time.Now()
	f.exec(t, "BEGIN")
	subject, err := f.conn.PrepareContext(t.Context(), `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	entity, err := f.conn.PrepareContext(t.Context(), `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age) VALUES(?,?,1,1,?,?,?,?,?,0,?,?,0,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	for o := 1; o <= residents; o++ {
		id := int64(1_000_000_000 + o)
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		title := screenTitle(0, o)
		key := fmt.Sprintf("%s", title)
		if _, err := subject.ExecContext(t.Context(), id, public); err != nil {
			t.Fatal(err)
		}
		if _, err := entity.ExecContext(t.Context(), id, public, title, key, key[:1], key, key[:1], public, public); err != nil {
			t.Fatal(err)
		}
	}
	f.exec(t, "COMMIT")
	catalogue := time.Since(started)

	started = time.Now()
	facts := seedQualificationSearchBank(t, f.conn)
	bank := time.Since(started)
	var pages, pageSize int64
	_ = f.conn.QueryRowContext(t.Context(), `PRAGMA page_count`).Scan(&pages)
	_ = f.conn.QueryRowContext(t.Context(), `PRAGMA page_size`).Scan(&pageSize)

	started = time.Now()
	f = composeReadFixture(t, f, NativeFullSearchDefinitions(), readfixture.LiveConfig(time.Now), false, nativeFixtureHash(t))
	compose := time.Since(started)
	reader, err := NewNativeFullSearchReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	f.input.Kind = 0
	f.input.Limit = 20

	oracle, err := scrNewFullSearchOracle([]string{screenTitle(0, 42), screenTitle(0, residents/2+7), screenTitle(0, residents)})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id,title FROM catalog_entities WHERE retired=0`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			t.Fatal(err)
		}
		oracle.Observe(id, title)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if err := oracle.Validate(); err != nil {
		t.Error("mix does not hold on this population:", err)
	}

	type termReport struct {
		Class      string
		Term       string
		Expected   int64
		Polls      int
		Hits       int
		Candidates int
		Exhausted  bool
		Partial    bool
		Count      uint64
		CountState int
		FirstNS    int64
		ReadyNS    int64
		ReadyPolls int
		TotalNS    int64
		MaxPollNS  int64
		Error      string
		Unavail    bool
		Distinct   int
		PollIDs    [][]int64
		CandSum    int
	}
	var reports []termReport
	for i, term := range oracle.Terms {
		r := termReport{Class: term.Class.String(), Term: term.Text, Expected: oracle.Expected(i)}
		var after *FullSearchCursor
		var pages []FullSearchPage
		seen := map[int64]int{}
		var walkStart = time.Now()
		for poll := 0; poll < 400; poll++ {
			begin := time.Now()
			page, err := nativePreviewRead(t, f, reader, term.Text, after, NativePreviewCandidates)
			took := time.Since(begin)
			r.Polls++
			r.MaxPollNS = max(r.MaxPollNS, took.Nanoseconds())
			if poll == 0 {
				r.FirstNS = took.Nanoseconds()
			}
			if err != nil {
				r.Error = err.Error()
				break
			}
			pages = append(pages, page)
			r.Hits += len(page.Hits)
			r.CandSum += page.NativeCandidates
			var ids []int64
			for _, h := range page.Hits {
				seen[h.ID]++
				ids = append(ids, h.ID)
			}
			if r.Class == "rare" || r.Class == "conjunction" {
				r.PollIDs = append(r.PollIDs, ids)
			}
			if r.ReadyNS == 0 && (len(page.Hits) > 0 || page.Exhausted && !page.Partial) {
				r.ReadyNS = time.Since(walkStart).Nanoseconds()
				r.ReadyPolls = r.Polls
			}
			r.Candidates, r.Exhausted, r.Partial = page.NativeCandidates, page.Exhausted, page.Partial
			r.Count, r.CountState = page.Count, int(page.CountStatus)
			if page.Next == nil {
				break
			}
			after = page.Next
		}
		r.TotalNS = time.Since(walkStart).Nanoseconds()
		r.Distinct = len(seen)
		if r.Error == "" {
			if cerr := oracle.CheckWalk(i, pages); cerr != nil {
				r.Error = cerr.Error()
				r.Unavail = cerr == scrErrFullSearchBankAbsent
			}
		}
		if r.Error != "" {
			t.Errorf("%s %q: %s", r.Class, r.Term, r.Error)
		}
		reports = append(reports, r)
	}
	out := map[string]any{"scope": "SCREENING: PB bank builder over canonical-shaped titles, read through the native reader; directional only", "residents": residents, "catalogue_seed_ns": catalogue.Nanoseconds(), "bank_build_ns": bank.Nanoseconds(), "compose_capture_ns": compose.Nanoseconds(), "db_bytes": pages * pageSize, "facts": facts, "terms": reports}
	data, _ := json.MarshalIndent(out, "", " ")
	t.Logf("%s", data)
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("bank-screen-%d.json", residents)), data, 0o600)
	}
}

func TestScreenBank30k(t *testing.T)  { screenBankAtScale(t, 30_000) }
func TestScreenBank200k(t *testing.T) { screenBankAtScale(t, 200_000) }
func TestScreenBank1M(t *testing.T)   { screenBankAtScale(t, 999_000) }
