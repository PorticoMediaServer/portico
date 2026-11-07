package read

import (
	"bytes"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Values-only DRAFT fold for parent PersonalState/detail/continue composition.
// These helpers do not authorize, admit snapshots or publish a public DTO.
// Compose under GV's existing exact source/root and current-viewer fences.
type uoPersonalWatchedFact struct {
	subject            int64
	public             [16]byte
	generation, action int64
	watched            bool
}
type uoPersonalAncestorIntent struct {
	uoPersonalWatchedFact
	// Maximum accepted membership along this <=2-edge typed ancestry path.
	membership, watermark int64
}
type uoPersonalEffectiveWatched struct {
	watched         bool
	action, subject int64
}

// Explicit unwatch participates in the SAME action ordering as watched. A late
// member is ineligible, not an implicit unwatch. Undo is a new parent action;
// it cannot erase a later explicit child action unless its own action is newer.
// Never infer watched from flag presence, lastPlayed or manufactured play time.
func foldUOPersonalWatched(subject int64, public [16]byte, generation int64, leaf uoPersonalWatchedFact, ancestors []uoPersonalAncestorIntent) (uoPersonalEffectiveWatched, error) {
	if subject <= 0 || public == [16]byte{} || generation <= 0 || len(ancestors) > 2 {
		return uoPersonalEffectiveWatched{}, ErrInput
	}
	if leaf.subject != subject || leaf.public != public || leaf.generation < 0 || leaf.action < 0 {
		return uoPersonalEffectiveWatched{}, ErrUnqualified
	}
	out := uoPersonalEffectiveWatched{subject: subject}
	if leaf.generation == generation {
		out.watched, out.action = leaf.watched, leaf.action
	}
	seen := map[int64]bool{subject: true}
	for _, a := range ancestors {
		if a.subject <= 0 || a.public == [16]byte{} || seen[a.subject] || a.generation <= 0 || a.action < 0 || a.membership <= 0 || a.watermark < 0 {
			return uoPersonalEffectiveWatched{}, ErrUnqualified
		}
		seen[a.subject] = true
		if a.generation != generation || a.action == 0 || a.membership > a.watermark {
			continue
		}
		if a.action == out.action && a.action != 0 {
			return uoPersonalEffectiveWatched{}, ErrUnqualified
		}
		if a.action > out.action {
			out = uoPersonalEffectiveWatched{a.watched, a.action, a.subject}
		}
	}
	return out, nil
}

// Direct public-ID equality guard, deliberately separate for corruption-only
// controls. A valid retained FK normally prevents mismatches; do not weaken
// this check merely because native constraints make the bad fixture impossible.
func uoPersonalPublicIDMatches(got []byte, want [16]byte) bool {
	return len(got) == 16 && want != [16]byte{} && bytes.Equal(got, want[:])
}

// Reads one sparse numeric fact. Variable snapshots/fences are not selected.
// A missing fact still has the caller's exact retained identity, no guessed ID.
func readUOPersonalWatchedFact(q browseQuery, profile string, subject int64, public [16]byte) (uoPersonalWatchedFact, error) {
	out := uoPersonalWatchedFact{subject: subject, public: public}
	rows, err := q("uo.personal.intent.fact", profile, subject)
	if err != nil {
		return uoPersonalWatchedFact{}, err
	}
	if len(rows) == 0 {
		return out, nil
	}
	if len(rows) != 1 || len(rows[0]) != 5 {
		return uoPersonalWatchedFact{}, ErrUnqualified
	}
	row := rows[0]
	got, ok := row[0].([]byte)
	if !ok || !uoPersonalPublicIDMatches(got, public) {
		return uoPersonalWatchedFact{}, ErrUnqualified
	}
	out.generation = row[1].(int64)
	out.action = row[2].(int64)
	out.watched = row[3].(int64) == 1
	// Dismissal/flag mutations may advance action_sequence, but only an explicit
	// watched intent advances container_intent_sequence (also used on leaves).
	if row[4] != nil {
		out.action = row[4].(int64)
	} else {
		out.action = 0
	}
	return out, nil
}

// Load a chosen applicable container's exact membership and intent. The typed
// caller supplies a native parent/season/show identity from its source fence;
// this query proves membership (<=9 fixed source categories), not permission.
// For a two-edge path, call per edge then use max(edge1,edge2), never arbitrary
// recursion or an all-descendants scan. At most two ancestors enter the fold.
func readUOPersonalAncestor(q browseQuery, profile string, parent, child int64, public [16]byte) (uoPersonalAncestorIntent, error) {
	var out uoPersonalAncestorIntent
	rows, err := q("uo.personal.intent.ancestor", child, profile, parent)
	if err != nil {
		return out, err
	}
	if len(rows) == 0 {
		return out, ErrNotReady
	}
	if len(rows) != 1 || len(rows[0]) != 6 {
		return out, ErrUnqualified
	}
	r := rows[0]
	got, ok := r[0].([]byte)
	if !ok || !uoPersonalPublicIDMatches(got, public) {
		return out, ErrUnqualified
	}
	if r[1] == nil || r[2] == nil || r[4] == nil || r[5] == nil {
		return out, ErrNotReady
	}
	out = uoPersonalAncestorIntent{uoPersonalWatchedFact: uoPersonalWatchedFact{subject: parent, public: public, generation: r[1].(int64), action: r[2].(int64), watched: r[3].(int64) == 1}, watermark: r[4].(int64), membership: r[5].(int64)}
	return out, nil
}
func UOPersonalIntentDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	out := []stmt.Definition{
		{ID: readStatementID("uo.personal.intent.fact"), Kind: stmt.Read, SQL: `SELECT item_public_id,activity_generation,action_sequence,watched,container_intent_sequence FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=?`, Params: []stmt.Param{s("profile"), i("subject")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}}},
		{ID: readStatementID("uo.personal.intent.ancestor"), Kind: stmt.Read, SQL: `SELECT s.public_id,p.activity_generation,p.container_intent_sequence,p.watched,p.container_membership_sequence,(SELECT min(c.membership_sequence) FROM catalog_scalar_contributions c INDEXED BY catalog_scalar_contributor WHERE c.parent_id=s.id AND c.contributor_id=?) FROM catalog_subjects s CROSS JOIN catalog_kinds k ON k.id=s.kind AND k.container=1 CROSS JOIN personal_items p INDEXED BY personal_current_live ON p.item_id=s.id AND p.item_public_id=s.public_id WHERE p.profile_id=? AND s.id=?`, Params: []stmt.Param{i("child"), s("profile"), i("parent")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}, PrimaryKeys: []string{"s"}}},
	}
	// Argument order of the inner membership point is fixed in SQL.
	for n := range out {
		out[n].Owner = "store/read UO personal draft"
		out[n].CallSites = []string{"store/read/uo_personal_intent.go"}
		args := []any{"profile-one", int64(2111)}
		if n == 1 {
			args = []any{int64(2111), "profile-one", int64(2112)}
		}
		out[n].Cohorts = []stmt.Cohort{{Name: fmt.Sprintf("point_%d", n), Bindings: args}}
	}
	return out
}
