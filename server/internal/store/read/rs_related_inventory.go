package read

// Inventory is reduced a point at a time under the work's global preparation
// lease. It retains no connection, and uses the Reading canonical primitives.
type relatedInventory struct {
	stage, source int
	variants      int
	asset         int64
	value         []any
	facts         rsReadingInventory
	bytes         int64
}

func (v *relatedInventory) rearm() {
	// Projection and final authorization can consume an arm before another poll.
	if v.stage == 2 {
		v.stage = 1
	}
	if v.stage == 4 {
		v.stage = 3
	}
}

func relatedInventoryStep(q browseQuery, s *relatedState, p *relatedProbe) (bool, bool, error) {
	if p.inventory == nil {
		p.inventory = &relatedInventory{facts: rsReadingInventory{sources: map[string][]any{}, assets: map[int64][]any{}, states: map[int64][2]bool{}}}
	}
	v := p.inventory
	library := s.fence.library
	switch v.stage {
	case 0:
		ids, err := q("rs.reading.source_ids", library, int64(MaxRaw+1))
		if err != nil {
			return false, false, err
		}
		if len(ids) > MaxRaw {
			return false, false, ErrRelatedCapacity
		}
		for _, id := range ids {
			if len(id[0].(string)) > 256 {
				return false, false, ErrRelatedCapacity
			}
		}
		v.facts.ids = ids
		gone, err := q("rs.reading.retired", p.id)
		if err != nil {
			return false, false, err
		}
		v.facts.retired = len(gone) > 0
		v.stage = 3
	case 3:
		if v.source == len(v.facts.ids) {
			v.stage = 5
			break
		}
		n, err := rsReadingOctets(q, "rs.reading.library_source_octets", v.facts.ids[v.source][0], library)
		if err != nil {
			return false, false, err
		}
		if 2*n > 1<<20-s.cardBytes {
			return false, false, ErrRelatedCapacity
		}
		if err = s.windows.arm(max(n, 1), 2); err != nil {
			return false, false, err
		}
		v.stage = 4
		s.waitByte = true
	case 4:
		id := v.facts.ids[v.source][0].(string)
		rows, err := q("rs.reading.library_source", id, library)
		if err != nil {
			return false, false, err
		}
		if len(rows) != 1 {
			return false, false, ErrStale
		}
		for _, col := range []int{0, 1, 3, 4} {
			v.bytes += int64(len(rows[0][col].(string)))
			s.cardBytes += 2 * int64(len(rows[0][col].(string)))
		}
		v.facts.sources[id] = rows[0]
		v.source++
		v.stage = 3
		s.waitByte = true
	case 5:
		rows, err := relatedRead(q, s, "assets", p.id, p.asset, int64(1))
		if err != nil {
			return false, false, err
		}
		if len(rows) == 0 {
			s.probe = s.probe[:len(s.probe)-1]
			return false, false, nil
		}
		v.variants++
		if v.variants > MaxRaw {
			return false, false, ErrRelatedCapacity
		}
		v.asset = rows[0][0].(int64)
		p.asset = v.asset
		v.stage = 1
	case 1:
		n, err := rsReadingOctets(q, "rs.reading.asset_octets", v.asset, s.fence.libraryID)
		if err != nil {
			return false, false, err
		}
		if 2*n > 1<<20-s.cardBytes {
			return false, false, ErrRelatedCapacity
		}
		if err = s.windows.arm(max(n, 1), 2); err != nil {
			return false, false, err
		}
		v.stage = 2
		s.waitByte = true
	case 2:
		rows, err := q("rs.reading.asset", v.asset, s.fence.libraryID)
		if err != nil {
			return false, false, err
		}
		if len(rows) != 1 {
			return false, false, ErrStale
		}
		v.facts.assets = map[int64][]any{v.asset: rows[0]}
		v.stage = 6
		s.waitByte = true
	case 6:
		ok, err := v.facts.available(q, v.asset, library)
		ok = true
		if err != nil {
			return false, false, err
		}
		if ok {
			return true, true, nil
		}
		v.facts.assets = nil
		v.facts.states = map[int64][2]bool{}
		v.stage = 5
	}
	return false, false, nil
}

// Feature owners and candidates use the same accepted subject recipe as probes.
func relatedEligibility(q browseQuery, e relatedEntity, f browseFence) (bool, error) {
	if e.row == nil || !e.eligible(f) {
		return false, nil
	}
	if e.kind() < 1 || e.kind() > 13 {
		return false, ErrUnqualified
	}
	f.container = relatedInt(e.row[7])
	_, err := rsDetailSubjectEligibility(q, BrowseInput{Library: f.libraryID, Kind: e.kind(), policy: readPolicyBinding{subject: e.id()}}, f)
	if err == ErrDenied {
		return false, nil
	}
	return err == nil, err
}
