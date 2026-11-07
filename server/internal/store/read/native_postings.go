package read

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

var nativeSlots = [...]stmt.FTSReadSlot{stmt.SearchOwnPrefixReadV1, stmt.SearchFragmentPrefixReadV1, stmt.SearchFragmentLexicalReadV1, stmt.SearchFragmentGramsReadV1, stmt.CatalogPeopleNamesReadV1}

func nativeFTSDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for slotIndex, slot := range nativeSlots {
		for _, shape := range []stmt.FTSReadShape{stmt.FTSFirst, stmt.FTSNext, stmt.FTSPoint} {
			descriptor := stmt.FTSRead{Slot: slot, Shape: shape}
			spec, ok := descriptor.Specification()
			if !ok {
				panic("unrecognized compiled native read slot")
			}
			params := []stmt.Param{{Name: "query", Type: stmt.Text}}
			term := "matrix"
			if slotIndex == 4 {
				term = "keanu"
			}
			match, _ := nativeMatchText(term, uint8(slotIndex+1))
			bindings := []any{match}
			if shape == stmt.FTSNext {
				params = append(params, stmt.Param{Name: "after_rowid", Type: stmt.Integer})
				bindings = append(bindings, int64(0))
			}
			if shape == stmt.FTSPoint {
				params = append(params, stmt.Param{Name: "rowid", Type: stmt.Integer})
				bindings = append(bindings, int64(1))
			} else {
				params = append(params, stmt.Param{Name: "limit", Type: stmt.Integer})
				bindings = append(bindings, int64(64))
			}
			empty := append([]any(nil), bindings...)
			empty[0] = "\"absent\""
			defs = append(defs, stmt.Definition{ID: stmt.ID("read.full.native.fts." + spec.Table + "." + string(shape)), SQL: spec.SQL, Params: params, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/native_postings.go:nativePosting"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, NativeRead: &descriptor}})
		}
	}
	return defs
}

// NativeFullSearchDefinitions is inert. Trusted composition captures the exact
// combined catalogue on its actual Reader before making the pool. Nothing here
// registers an executable statement or installs a production search route.
func NativeFullSearchDefinitions() []stmt.Definition {
	defs := append([]stmt.Definition(nil), browseDefinitions()...)
	defs = append(defs, FullSearchDefinitions()...)
	defs = append(defs, nativeSearchSourceDefinitions()...)
	defs = append(defs, nativeEngineDefinition())
	defs = append(defs, nativeAutomaticDefinitions()...)
	return append(defs, nativeFTSDefinitions()...)
}

func nativeMatchText(term string, slot uint8) (string, error) {
	if term == "" || slot < 1 || slot > 5 || !utf8.ValidString(term) {
		return "", ErrInput
	}
	candidate := term
	if slot <= 2 {
		// Scalar-hex clipping follows the already compiled preparation recipe.
		// For a CJK-containing term, any accepted substring contains its first CJK
		// unigram. That broad posting is only a candidate; EVERY query term and the
		// complete logical sibling field are still verified before counting/emission.
		if strings.ContainsFunc(term, facettext.CJK) {
			for _, r := range term {
				if facettext.CJK(r) {
					candidate = string(r)
					break
				}
			}
		}
		candidate = search.PrepareTitle(candidate).PrefixTokens()
		if at := strings.IndexByte(candidate, ' '); at >= 0 {
			candidate = candidate[:at]
		}
	}
	if slot == 4 && utf8.RuneCountInString(term) < 3 {
		return "", nil
	} // trigram cannot discover a shorter term
	if candidate == "" {
		return "", nil
	}
	query := "\"" + strings.ReplaceAll(candidate, "\"", "\"\"") + "\""
	if slot != 4 {
		query += "*"
	}
	if len(query) > 1539 {
		return "", ErrInput
	}
	return query, nil
}

func nativePosting(query browseQuery, term string, slot uint8, after int64) (int64, bool, error) {
	match, err := nativeMatchText(term, slot)
	if err != nil {
		return 0, false, err
	}
	if match == "" {
		return 0, true, nil
	}
	spec, _ := (stmt.FTSRead{Slot: nativeSlots[slot-1], Shape: stmt.FTSNext}).Specification()
	rows, err := query("full.native.fts."+spec.Table+".next", match, after, int64(1))
	if err != nil {
		return 0, false, err
	}
	if len(rows) == 0 {
		return 0, true, nil
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return 0, false, ErrUnqualified
	}
	id, ok := rows[0][0].(int64)
	if !ok || id <= after {
		return 0, false, ErrStale
	}
	return id, false, nil
}
func nativePostingPoint(query browseQuery, term string, slot uint8, id int64) (bool, error) {
	match, err := nativeMatchText(term, slot)
	if err != nil || match == "" {
		return false, err
	}
	spec, _ := (stmt.FTSRead{Slot: nativeSlots[slot-1], Shape: stmt.FTSPoint}).Specification()
	rows, err := query("full.native.fts."+spec.Table+".point", match, id)
	if err != nil {
		return false, err
	}
	if len(rows) > 1 || len(rows) == 1 && (len(rows[0]) != 1 || rows[0][0] != id) {
		return false, ErrUnqualified
	}
	return len(rows) == 1, nil
}

type nativeSource struct {
	scope                *fullQueryScope
	key                  search.FieldKey
	title, fold, sortKey string
	kind, library        int64
	item                 []any
	permission           nativePolicyWitness
	personSource         [32]byte
}

// The accepted point uses the current entity's full revision, not its narrower
// source_revision; no pending input or stale native posting is promoted.
func nativeEntitySource(query browseQuery, scope *fullQueryScope, id int64) (*nativeSource, error) {
	rows, err := query("full.native.entity.accepted", id, scope.bank)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// A live source with a raw posting but no matching accepted head is
		// pending, not proof of a zero-match population. Missing/retired sources
		// are ordinary rejected raw positions.
		item, e := query("full.native.item", id)
		if e != nil {
			return nil, e
		}
		if len(item) == 0 || len(item) == 1 && len(item[0]) == 19 && item[0][4] == int64(1) {
			return nil, nil
		}
		return nil, errFullSearchPending
	}
	if len(rows) != 1 || len(rows[0]) != 12 {
		return nil, ErrUnqualified
	}
	p := rows[0]
	if p[0] != p[10] || p[1] != p[2] || p[1] != p[11] {
		return nil, ErrUnqualified
	}
	item, err := query("full.native.item", id)
	if err != nil {
		return nil, err
	}
	if len(item) != 1 || len(item[0]) != 19 {
		return nil, ErrUnqualified
	}
	inc, ok := p[3].([]byte)
	nativeInc, ok2 := item[0][9].([]byte)
	if !ok || !ok2 || len(inc) != 16 || string(inc) != string(nativeInc) || p[4] != item[0][5] {
		return nil, ErrStale
	}
	if err := nativeSealed(query, p[0].(int64)); err != nil {
		return nil, err
	}
	return &nativeSource{scope: scope, key: search.FieldKey{Subject: search.ManifestEntity, SubjectID: id, PublicationID: p[0].(int64), BankID: scope.bank, InputGeneration: p[1].(int64)}, title: p[6].(string), fold: p[7].(string), kind: p[8].(int64), library: p[9].(int64), item: item[0]}, nil
}
func nativePostingSource(query browseQuery, scope *fullQueryScope, slot uint8, id int64) (*nativeSource, uint16, error) {
	if slot == 5 {
		source, err := nativePersonSource(query, scope, id)
		return source, 1, err
	}
	if slot == 1 {
		s, err := nativeEntitySource(query, scope, id)
		return s, 1, err
	}
	rows, err := query("full.native.fragment", id)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return nil, 0, nil
	}
	if len(rows) != 1 || len(rows[0]) != 8 {
		return nil, 0, ErrUnqualified
	}
	v := rows[0]
	if v[7] != scope.bank {
		return nil, 0, nil
	}
	var s *nativeSource
	if v[2] != nil {
		person, ok := v[2].(int64)
		if !ok || v[1] != nil {
			return nil, 0, ErrUnqualified
		}
		s, err = nativePersonSource(query, scope, person)
	} else {
		entity, ok := v[1].(int64)
		if !ok {
			return nil, 0, ErrUnqualified
		}
		s, err = nativeEntitySource(query, scope, entity)
	}
	if err != nil || s == nil {
		return s, 0, err
	}
	if v[0] != s.key.PublicationID || v[6] != s.key.InputGeneration {
		return nil, 0, nil
	} // old/staging publication still consumes its raw position
	field, ok := v[3].(int64)
	if !ok || field < 1 || field > 9 || s.key.Subject == search.ManifestPerson && field > 4 {
		return nil, 0, ErrUnqualified
	}
	return s, uint16(field), nil
}

// Complete physical siblings, not row-local conjunctions. The canonical first
// native posting is found with actual fixed-slot point reads; no raw fragment
// count or assumption about logical-key/native-rowid ordering enters a count.
func nativeLogicalField(ctx context.Context, query browseQuery, scope *fullQueryScope, source *nativeSource, field uint16, term int) (logicalSourceProof, fullPostingKey, error) {
	var empty logicalSourceProof
	if field < 1 || field > 9 || source == nil || term < 0 || term >= len(scope.terms) {
		return empty, fullPostingKey{}, ErrInput
	}
	after := int64(-1)
	var walk *logicalFieldWalk
	var first fullPostingKey
	total := 0
	if !scope.preview && field == 1 && source.key.Subject == search.ManifestEntity {
		hit, err := nativePostingPoint(query, scope.terms[term], 1, source.key.SubjectID)
		if err != nil {
			return empty, first, err
		}
		if hit {
			first = fullPostingKey{1, source.key.SubjectID}
		}
	}
	if !scope.preview && field == 1 && source.key.Subject == search.ManifestPerson {
		hit, err := nativePostingPoint(query, scope.terms[term], 5, source.key.SubjectID)
		if err != nil {
			return empty, first, err
		}
		if hit {
			first = fullPostingKey{5, source.key.SubjectID}
		}
	}
	for {
		name := "full.native.field.first"
		args := []any{source.key.PublicationID, int64(field)}
		if after >= 0 {
			name = "full.native.field.next"
			args = append(args, after)
		}
		rows, err := query(name, append(args, int64(64))...)
		if err != nil {
			return empty, first, err
		}
		if len(rows) > 64 {
			return empty, first, ErrUnqualified
		}
		for _, row := range rows {
			if len(row) != 6 {
				return empty, first, ErrUnqualified
			}
			id, ok := row[0].(int64)
			ord, ok2 := row[1].(int64)
			rev, ok3 := row[5].(string)
			if !ok || !ok2 || !ok3 || id <= 0 || ord <= after || !utf8.ValidString(rev) {
				return empty, first, ErrUnqualified
			}
			if walk == nil {
				key := source.key
				key.Field = field
				key.Revision = rev
				walk, err = newLogicalFieldWalk(scope, key, source.fold)
				if err != nil {
					return empty, first, err
				}
			}
			if rev != walk.key.Revision {
				return empty, first, ErrStale
			}
			if err = walk.observe(ctx, walk.key, row[3].(string), row[4].(string)); err != nil {
				return empty, first, err
			}
			for slot := uint8(2); !scope.preview && slot <= 4; slot++ {
				if first.slot != 0 && slot > first.slot {
					break
				}
				hit, err := nativePostingPoint(query, scope.terms[term], slot, id)
				if err != nil {
					return empty, first, err
				}
				pos := fullPostingKey{slot, id}
				if hit && (first.slot == 0 || comparePosting(pos, first) < 0) {
					first = pos
				}
			}
			total++
			if total > 512 {
				return empty, first, fmt.Errorf("%w: complete field exceeds bounded proof workspace", ErrUnqualified)
			}
			after = ord
		}
		if len(rows) < 64 {
			break
		}
	}
	if walk == nil {
		if field == 1 {
			return empty, first, fmt.Errorf("%w: accepted primary logical field missing", ErrUnqualified)
		}
		return empty, first, nil
	}
	proof, err := walk.finish(ctx, true)
	if err != nil {
		return empty, first, err
	}
	if !scope.preview && proof.matched[term] && first.slot == 0 {
		return empty, first, errors.Join(ErrUnqualified, errors.New("verified source has no native candidate witness"))
	}
	return proof, first, nil
}
