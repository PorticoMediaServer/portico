package read

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestUOPersonalEffectiveWatchedSequenceMembershipAndUndo(t *testing.T) {
	public := [16]byte{1}
	leaf := uoPersonalWatchedFact{subject: 1, public: public, generation: 1}
	season := uoPersonalAncestorIntent{uoPersonalWatchedFact: uoPersonalWatchedFact{subject: 2, public: [16]byte{2}, generation: 1, action: 2, watched: true}, membership: 5, watermark: 10}
	show := uoPersonalAncestorIntent{uoPersonalWatchedFact: uoPersonalWatchedFact{subject: 3, public: [16]byte{3}, generation: 1, action: 1, watched: true}, membership: 7, watermark: 10}
	fold := func(l uoPersonalWatchedFact, as []uoPersonalAncestorIntent, want bool, action, subject int64) {
		t.Helper()
		got, e := foldUOPersonalWatched(1, public, 1, l, as)
		if e != nil || got.watched != want || got.action != action || got.subject != subject {
			t.Fatal(got, e, want, action, subject)
		}
	}
	fold(leaf, []uoPersonalAncestorIntent{season, show}, true, 2, 2)
	late := season
	late.membership = 11
	fold(leaf, []uoPersonalAncestorIntent{late}, false, 0, 1)
	// A show cannot inherit across a season reparent after its watermark. Caller
	// supplies max(membership episode->season, membership season->show).
	reparented := show
	reparented.membership = 12
	fold(leaf, []uoPersonalAncestorIntent{reparented}, false, 0, 1)
	explicitUnwatch := leaf
	explicitUnwatch.action = 3
	fold(explicitUnwatch, []uoPersonalAncestorIntent{season, show}, false, 3, 1)
	undo := season
	undo.action = 4
	undo.watched = false
	fold(leaf, []uoPersonalAncestorIntent{undo, show}, false, 4, 2)
	// Restoring an original parent intent preserves its original order, so a
	// newer explicit child edit remains authoritative after undo.
	restored := season
	restored.action = 1
	fold(explicitUnwatch, []uoPersonalAncestorIntent{restored}, false, 3, 1)
	newerLeaf := leaf
	newerLeaf.action = 5
	newerLeaf.watched = true
	fold(newerLeaf, []uoPersonalAncestorIntent{undo, show}, true, 5, 1)
	// Privacy reset invalidates all stale watched evidence.
	got, e := foldUOPersonalWatched(1, public, 2, newerLeaf, []uoPersonalAncestorIntent{season, show})
	if e != nil || got.watched || got.action != 0 {
		t.Fatal(got, e)
	}
	if _, e = foldUOPersonalWatched(1, public, 1, leaf, []uoPersonalAncestorIntent{season, season}); e == nil {
		t.Fatal("duplicate logical ancestor accepted")
	}
}
func TestUOPersonalPublicIDGuardCorruptionOnly(t *testing.T) {
	want := [16]byte{1}
	bad := [16]byte{2}
	// Direct unit control, deliberately does not pretend a valid composite-FK
	// fixture can contain a mismatched retained subject/public UUID.
	if !uoPersonalPublicIDMatches(want[:], want) || uoPersonalPublicIDMatches(bad[:], want) || uoPersonalPublicIDMatches(want[:15], want) {
		t.Fatal("public-ID equality guard failed")
	}
	query := func(_ string, _ ...any) ([][]any, error) {
		return [][]any{{bad[:], int64(1), int64(3), int64(1), int64(3)}}, nil
	}
	if got, e := readUOPersonalWatchedFact(query, "p", 1, want); e == nil || got != (uoPersonalWatchedFact{}) {
		t.Fatal("corrupt personal row escaped", got, e)
	}
}
func uoPersonalReadDefinitions() []stmt.Definition {
	out := UOPersonalIntentDefinitions()
	out = append(out, UOPersonalMembershipDefinitions()...)
	return append(out, UOPersonalHistoryDefinitions()...)
}
func newUOPersonalReadSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	// Test-only UO-001 DRAFT DDL. No canonical schema or product registry edits.
	for _, ddl := range []string{`CREATE INDEX personal_current_watchlist ON personal_items(profile_id,item_public_id) WHERE watchlisted=1`, `CREATE INDEX personal_current_favorite ON personal_items(profile_id,item_public_id) WHERE favorite=1`, `CREATE INDEX personal_current_not_interested ON personal_items(profile_id,item_public_id) WHERE not_interested=1`} {
		f.exec(t, ddl)
	}
	return f
}

// Component-only bounded native query adapter. Production composition uses the
// existing registered GV query scope; no raw handles escape production code.
func uoPersonalNativeQuery(t *testing.T, f *browseFixture) browseQuery {
	t.Helper()
	defs := map[stmt.ID]stmt.Definition{}
	for _, d := range uoPersonalReadDefinitions() {
		defs[d.ID] = d
	}
	return func(name string, args ...any) ([][]any, error) {
		d, ok := defs[readStatementID(name)]
		if !ok {
			t.Fatalf("undefined personal statement %s", name)
		}
		rows, e := f.conn.QueryContext(t.Context(), d.SQL, args...)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		columns, e := rows.Columns()
		if e != nil {
			return nil, e
		}
		var out [][]any
		for rows.Next() {
			row := make([]any, len(columns))
			dest := make([]any, len(columns))
			for n := range row {
				dest[n] = &row[n]
			}
			if e = rows.Scan(dest...); e != nil {
				return nil, e
			}
			out = append(out, row)
			if len(out) > MaxRaw {
				t.Fatal("unbounded component rows")
			}
		}
		return out, rows.Err()
	}
}
func TestUOPersonalWatchedNativeMembershipDeduplicatesSources(t *testing.T) {
	f := newUOPersonalReadSource(t)
	q := uoPersonalNativeQuery(t, f)
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2200,x'03800000000000000000000000000000',1,2)`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,'Show','show','s','show','s',0,public_id,public_id FROM catalog_subjects WHERE id=2200`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,watched,action_sequence,container_intent_sequence,container_membership_sequence) SELECT 'profile-one',public_id,id,library_id,1,10,10,20 FROM catalog_subjects WHERE id=2200`)
	f.exec(t, `INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence) VALUES(1,2200,2,0,1,5),(1,2200,2,1,1,5)`)
	var pub []byte
	if e := f.conn.QueryRowContext(t.Context(), `SELECT public_id FROM catalog_subjects WHERE id=2200`).Scan(&pub); e != nil {
		t.Fatal(e)
	}
	var public [16]byte
	copy(public[:], pub)
	a, e := readUOPersonalAncestor(q, "profile-one", 2200, 2, public)
	if e != nil || a.membership != 5 || a.watermark != 20 || a.action != 10 {
		t.Fatal(a, e)
	}
	l, e := readUOPersonalWatchedFact(q, "profile-one", 2200, public)
	if e != nil || !l.watched || l.action != 10 {
		t.Fatal(l, e)
	}
}
