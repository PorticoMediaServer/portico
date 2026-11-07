package read

import "github.com/PorticoMediaServer/portico/server/internal/store/search"

// Full-key head and publication points share this physical read snapshot.
// Go repeats every original join fence; no tiny joined-table scan or hidden
// rowid witness is needed to prove a current published source.
func nativeAcceptedPublication(q browseQuery, scope *fullQueryScope, subject search.ManifestSubject, id int64) ([]any, error) {
	if scope == nil || scope.bank <= 0 || id <= 0 {
		return nil, ErrInput
	}
	name := "full.native.entity.head.raw"
	if subject == search.ManifestPerson {
		name = "full.native.person.head.raw"
	} else if subject != search.ManifestEntity {
		return nil, ErrInput
	}
	rows, err := q(name, id, scope.bank)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errFullSearchPending
	}
	if len(rows) != 1 || len(rows[0]) != 7 {
		return nil, ErrUnqualified
	}
	h := rows[0]
	publication, a := h[0].(int64)
	published, b := h[1].(int64)
	input, c := h[2].(int64)
	birth, d := h[3].([]byte)
	revision, e := h[4].(int64)
	job, f := h[5].(int64)
	recipe, g := h[6].(string)
	if h[0] == nil || a && publication == 0 {
		return nil, errFullSearchPending
	}
	if !a || !b || !c || !d || !e || !f || !g || len(birth) != 16 || job <= 0 {
		return nil, ErrUnqualified
	}
	if publication <= 0 || published <= 0 || input != published || revision <= 0 || recipe == "" {
		return nil, errFullSearchPending
	}
	rows, err = q("full.native.publication", publication)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errFullSearchPending
	}
	if len(rows) != 1 || len(rows[0]) != 12 {
		return nil, ErrUnqualified
	}
	p := rows[0]
	pubBirth, ok := p[9].([]byte)
	if !ok || len(pubBirth) != 16 {
		return nil, ErrUnqualified
	}
	if p[0] != publication || p[3] != scope.bank || p[8] != published || string(pubBirth) != string(birth) || p[10] != revision || p[11] != recipe {
		return nil, errFullSearchPending
	}
	if subject == search.ManifestEntity {
		if p[1] != id || p[2] != nil {
			return nil, errFullSearchPending
		}
	} else if p[2] != id || p[1] != nil || p[6] != nil || p[7] != nil {
		return nil, errFullSearchPending
	}
	if err = nativeSealed(q, publication); err != nil {
		return nil, err
	}
	return p, nil
}
