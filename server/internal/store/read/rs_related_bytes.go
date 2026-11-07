package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Parent owns plan/rs_octets.go registration of this exact point. No SQL
// permission is minted by the private read gateway's projection mapping.
const rsRelatedCreditOctetsSQL = `SELECT octet_length(provider),octet_length(provider_person_id) FROM catalog_credits WHERE image_id=? AND ord=?`

const rsRelatedCardOctetsSQL = `SELECT octet_length(e.public_id),octet_length(e.title),octet_length(e.artwork_incarnation) FROM catalog_entities AS e WHERE e.id=?`

func init() {
	registerRSBytePoint("rs.related.credit_identity", rsBytePoint{lengths: "rs.related.credit_identity_octets", columns: []int{2, 3}})
	registerRSBytePoint("rs.related.card", rsBytePoint{lengths: "rs.related.card_octets", columns: []int{1, 2, 4}})
}
func rsRelatedByteDefinitions() []stmt.Definition {
	params := []stmt.Param{{Name: "item", Type: stmt.Integer}}
	cohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(999999)}}}
	makePoint := func(name, sql string) stmt.Definition {
		return stmt.Definition{ID: statementID("rs.related." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_related_bytes.go:relatedCardQuantum"}, Cohorts: cohorts, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}}
	}
	out := []stmt.Definition{makePoint("card_octets", rsRelatedCardOctetsSQL), makePoint("card", `SELECT e.id,e.public_id,e.title,e.revision,e.artwork_incarnation,e.artwork_revision FROM catalog_entities AS e WHERE e.id=?`)}
	for _, p := range []struct{ name, sql string }{{"credit_identity_octets", rsRelatedCreditOctetsSQL}, {"credit_identity", `SELECT image_id,ord,provider,provider_person_id FROM catalog_credits WHERE image_id=? AND ord=?`}} {
		d := makePoint(p.name, p.sql)
		d.Params = []stmt.Param{{Name: "image", Type: stmt.Integer}, {Name: "ordinal", Type: stmt.Integer}}
		d.Cohorts = []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1)}}}
		d.Plan.PrimaryKeys = []string{"catalog_credits"}
		out = append(out, d)
	}
	return out
}
func relatedCardQuantum(q browseQuery, s *relatedState, in RelatedInput, similar bool, windows *rsByteWindow) error {
	if s.cards == nil {
		s.cards = make(map[int64]string)
		rows, err := relatedRows(s, in, similar)
		if err != nil {
			return err
		}
		seen := map[int64]bool{}
		for _, row := range rows {
			for _, entry := range row.Entries {
				if !seen[entry.ID] {
					s.cardIDs = append(s.cardIDs, entry.ID)
					seen[entry.ID] = true
				}
			}
		}
	}
	if s.cardIndex == len(s.cardIDs) {
		s.done = true
		return nil
	}
	id := s.cardIDs[s.cardIndex]
	if s.phase == 4 {
		rows, err := q("rs.related.card_octets", id)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return ErrStale
		}
		actual, err := rsPointBytes(rows[0])
		if err != nil {
			return err
		}
		if err = windows.arm(actual, 2); err != nil {
			return err
		}
		s.phase = 5
		s.waitByte = true
		return nil
	}
	rows, err := q("rs.related.card", id)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return ErrStale
	}
	row := rows[0]
	e := s.candidates[id].entity
	if row[0] != id || row[3] != e.row[5] || row[5] != e.row[30] || !equalSource([]any{row[1]}, []any{e.row[1]}) {
		return ErrStale
	}
	title := row[2].(string)
	// Actual retained text, not column maxima. The RB reservation covers
	// <=one MiB text/copies plus bounded normalized reducer/map state.
	if s.cardBytes+2*int64(len(title)) > 1<<20 {
		return ErrRelatedCapacity
	}
	s.cardBytes += 2 * int64(len(title))
	s.cards[id] = title
	s.cardIndex++
	s.phase = 4
	// The next card must measure its bytes in a separate conservative window;
	// a forecast may never be widened by a later point in the same snapshot.
	s.waitByte = true
	return nil
}

func relatedCreditBytes(q browseQuery, s *relatedState, windows *rsByteWindow) error {
	f := &s.features[s.featureIndex]
	if !s.sourceByte {
		rows, err := q("rs.related.credit_identity_octets", f.key.Image, f.key.Ordinal)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return ErrStale
		}
		actual, err := rsPointBytes(rows[0])
		if err != nil {
			return err
		}
		if err = windows.arm(actual, 2); err != nil {
			return err
		}
		s.sourceByte = true
		s.waitByte = true
		return nil
	}
	rows, err := q("rs.related.credit_identity", f.key.Image, f.key.Ordinal)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return ErrStale
	}
	row := rows[0]
	if row[0] != f.key.Image || row[1] != f.key.Ordinal {
		return ErrStale
	}
	provider, id := row[2].(string), row[3].(string)
	if id == "" {
		return ErrStale
	}
	if s.cardBytes+2*int64(len(provider)+len(id)) > 1<<20 {
		return ErrRelatedCapacity
	}
	s.cardBytes += 2 * int64(len(provider)+len(id))
	f.providerName = provider
	f.providerID = id
	s.sourceByte = false
	s.waitByte = true
	return nil
}
