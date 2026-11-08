package read

import (
	"math/bits"
	"slices"
)

func relatedAddFeature(s *relatedState, f relatedFeature) error {
	for _, old := range s.features {
		if old.key == f.key {
			return nil
		}
	}
	if len(s.features) == rsRelatedFeatures {
		return ErrRelatedCapacity
	}
	s.features = append(s.features, f)
	return nil
}
func relatedRead(q browseQuery, s *relatedState, name string, args ...any) ([][]any, error) {
	rows, err := q("rs.related."+name, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) > MaxRaw {
		return nil, ErrUnqualified
	}
	s.rawTotal += len(rows)
	return rows, nil
}
func relatedQuantum(q browseQuery, s *relatedState, windows *rsByteWindow) error {
	switch s.phase {
	case -1:
		return relatedSeedAvailability(q, s)
	case 0:
		return relatedEnumerate(q, s)
	case 1:
		return relatedNormalize(q, s)
	case 2:
		return relatedCandidates(q, s, windows)
	case 3:
		return relatedHydrate(q, s)
	case 6:
		return relatedCollectionOrder(q, s)
	default:
		return nil
	}
}
func relatedEnumerate(q browseQuery, s *relatedState) error {
	if s.rootIndex == len(s.roots) {
		s.phase = 1
		s.duplicate = make([]bool, len(s.features))
		s.normalizeI = 0
		s.normalizeJ = 0
		return nil
	}
	e := s.roots[s.rootIndex]
	switch s.enumStage {
	case 0:
		rows, err := relatedRead(q, s, "terms", e.id(), s.after, int64(MaxRaw))
		if err != nil {
			return err
		}
		for _, row := range rows {
			id, vocab := row[0].(int64), row[1].(int64)
			s.after = id
			if vocab == 1 || vocab == 7 || vocab == 8 {
				if err := relatedAddFeature(s, relatedFeature{key: RelatedKey{Kind: 1, ID: id}}); err != nil {
					return err
				}
			}
		}
		if len(rows) < MaxRaw {
			s.enumStage = 1
			s.after = -1
		}
	case 1:
		if e.image() == 0 {
			s.enumStage = 2
			s.after = 0
			return nil
		}
		if e.row[16] != int64(1) {
			return ErrNotReady
		}
		rows, err := relatedRead(q, s, "credits", e.id(), e.image(), s.after, int64(MaxRaw))
		if err != nil {
			return err
		}
		for _, row := range rows {
			ord := row[0].(int64)
			s.after = ord
			mode := uint8(3)
			if row[1] != nil {
				mode = 1
			} else if row[4].(int64) > 0 {
				mode = 2
			}
			if err := relatedAddFeature(s, relatedFeature{key: RelatedKey{Kind: 2, ID: e.id(), Image: e.image(), Ordinal: ord}, mode: mode, provider: row[4].(int64) > 0, person: relatedInt(row[1])}); err != nil {
				return err
			}
		}
		if len(rows) < MaxRaw {
			s.enumStage = 2
			s.after = 0
		}
	case 2:
		rows, err := relatedRead(q, s, "collections", e.id(), s.after, int64(MaxRaw))
		if err != nil {
			return err
		}
		s.raw = rows
		s.rawIndex = 0
		s.enumStage = 4
	case 4:
		if s.rawIndex == len(s.raw) {
			if len(s.raw) < MaxRaw {
				s.enumStage = 3
				s.after = 0
			} else {
				s.enumStage = 2
			}
			s.raw = nil
			return nil
		}
		id := s.raw[s.rawIndex][0].(int64)
		collection, err := relatedEntityRead(q, s.fence.profile, id)
		if err != nil {
			return err
		}
		s.rawTotal++
		ok, err := relatedEligibility(q, collection, s.fence)
		if err != nil {
			return err
		}
		if ok && collection.kind() == 10 && collection.eligible(s.fence) {
			if err := relatedAddFeature(s, relatedFeature{key: RelatedKey{Kind: 3, ID: id}}); err != nil {
				return err
			}
		}
		s.after = id
		s.rawIndex++
	case 3:
		typed := []struct {
			col    int
			kind   uint8
			target int64
		}{{18, 4, 0}, {19, 5, 2}, {22, 6, 5}}
		if s.after < int64(len(typed)) {
			p := typed[s.after]
			s.after++
			if id := relatedInt(e.row[p.col]); id > 0 {
				owner, err := relatedEntityRead(q, s.fence.profile, id)
				if err != nil {
					return err
				}
				s.rawTotal++
				ok, err := relatedEligibility(q, owner, s.fence)
				if err != nil {
					return err
				}
				if ok && owner.eligible(s.fence) && relatedInt(owner.row[7]) != 0 && (p.target == 0 || owner.kind() == p.target) {
					return relatedAddFeature(s, relatedFeature{key: RelatedKey{Kind: p.kind, ID: id}})
				}
			}
			return nil
		}
		if err := relatedAddFeature(s, relatedFeature{key: RelatedKey{Kind: 7, ID: e.id()}}); err != nil {
			return err
		}
		s.rootIndex++
		s.enumStage = 0
		s.after = 0
	}
	return nil
}
func relatedNormalize(q browseQuery, s *relatedState) error {
	if s.normalizeI == len(s.features) {
		kept := s.features[:0]
		for i, f := range s.features {
			if !s.duplicate[i] {
				kept = append(kept, f)
			}
		}
		s.features = kept
		s.duplicate = nil
		s.phase = 2
		return nil
	}
	i, j := s.normalizeI, s.normalizeJ
	if s.features[i].key.Kind != 2 || j == i || s.duplicate[i] {
		s.normalizeI++
		s.normalizeJ = 0
		return nil
	}
	s.normalizeJ++
	if s.features[j].key.Kind != 2 || s.duplicate[j] {
		return nil
	}
	a, b := s.features[i], s.features[j]
	if a.person > 0 && b.person > 0 {
		s.duplicate[i] = a.person == b.person
		return nil
	}
	name := "name"
	if a.provider && b.provider {
		name = "provider"
	}
	rows, err := relatedRead(q, s, "credit.equal."+name, b.key.Image, b.key.Ordinal, a.key.Image, a.key.Ordinal)
	if err != nil {
		return err
	}
	if len(rows) > 1 {
		return ErrUnqualified
	}
	s.duplicate[i] = len(rows) == 1
	return nil
}
func relatedStream(q browseQuery, s *relatedState, f relatedFeature) ([][]any, error) {
	p := s.position
	switch f.key.Kind {
	case 1:
		return relatedRead(q, s, "term.members", f.key.ID, p[0], int64(MaxRaw))
	case 2:
		name := map[uint8]string{1: "person", 2: "provider", 3: "name"}[f.mode]
		var out [][]any
		seeks := []struct {
			name string
			args []any
		}{{"image_tail", []any{p[0], p[1], p[2]}}, {"ordinal_tail", []any{p[0], p[1]}}, {"next", []any{p[0]}}}
		if p[0] == 0 {
			seeks = seeks[2:]
		}
		for _, seek := range seeks {
			prefix := []any{f.key.Image, f.key.Ordinal}
			if f.mode == 2 {
				prefix = []any{f.providerName, f.providerID}
			}
			args := append(prefix, seek.args...)
			args = append(args, int64(MaxRaw-len(out)))
			rows, err := relatedRead(q, s, "credit."+name+"."+seek.name, args...)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
			if len(out) == MaxRaw {
				break
			}
		}
		return out, nil
	case 3:
		return relatedRead(q, s, "collection.members", f.key.ID, p[0], int64(MaxRaw))
	case 4:
		return relatedRead(q, s, "parent.members", f.key.ID, p[0], int64(MaxRaw))
	case 5:
		return relatedRead(q, s, "show.members", f.key.ID, p[0], int64(MaxRaw))
	case 6:
		return relatedRead(q, s, "artist.members", f.key.ID, p[0], int64(MaxRaw))
	case 7:
		return relatedRead(q, s, "local.members", f.key.ID, p[0], int64(MaxRaw))
	}
	return nil, ErrInput
}
func relatedCandidates(q browseQuery, s *relatedState, windows *rsByteWindow) error {
	if s.featureIndex == len(s.features) {
		s.ids = make([]int64, 0, len(s.candidates))
		for id := range s.candidates {
			s.ids = append(s.ids, id)
		}
		slices.Sort(s.ids)
		s.phase = 3
		return nil
	}
	f := s.features[s.featureIndex]
	if f.mode == 2 && f.providerID == "" {
		return relatedCreditBytes(q, s, windows)
	}
	if s.raw == nil {
		rows, err := relatedStream(q, s, f)
		if err != nil {
			return err
		}
		s.raw = rows
		s.rawIndex = 0
		if len(rows) == 0 {
			s.featureIndex++
			s.position = [3]int64{}
			s.raw = nil
		}
		return nil
	}
	if s.rawIndex == len(s.raw) {
		if len(s.raw) < MaxRaw {
			s.featureIndex++
			s.position = [3]int64{}
		}
		s.raw = nil
		return nil
	}
	row := s.raw[s.rawIndex]
	id := row[0].(int64)
	if s.candidateID > 0 {
		id = s.candidateID
	}
	e, err := relatedEntityRead(q, s.fence.profile, id)
	if err != nil {
		return err
	}
	s.rawTotal++
	ok, err := relatedEligibility(q, e, s.fence)
	if err != nil {
		return err
	}
	if ok {
		if f.key.Kind == 2 && s.candidateID == 0 && (e.image() != row[2].(int64) || e.row[16] != int64(1)) {
			e = relatedEntity{}
		}
		if e.row != nil && s.candidateID == 0 && e.work() != e.id() {
			s.candidateID = e.work()
			return nil
		}
		if e.row != nil && e.kind() == s.roots[len(s.roots)-1].kind() && e.id() != s.roots[len(s.roots)-1].id() {
			c := s.candidates[e.id()]
			if c == nil {
				if len(s.candidates) == rsRelatedCandidates {
					return ErrRelatedCapacity
				}
				c = &relatedCandidate{entity: e}
				s.candidates[e.id()] = c
			}
			if f.key.Kind != 7 {
				c.bits[s.featureIndex/64] |= uint64(1) << uint(s.featureIndex%64)
			}
		}
	}
	s.candidateID = 0
	s.position = [3]int64{row[0].(int64), row[1].(int64), row[2].(int64)}
	s.rawIndex++
	return nil
}
func relatedShared(c *relatedCandidate) int {
	n := 0
	for _, v := range c.bits {
		n += bits.OnesCount64(v)
	}
	return n
}
func relatedHas(c *relatedCandidate, n int) bool { return c.bits[n/64]&(uint64(1)<<uint(n%64)) != 0 }
func relatedProviderEqual(q browseQuery, s *relatedState, a, b int64) (bool, error) {
	rows, err := relatedRead(q, s, "provider.equal", a, b)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row[0] != nil {
			return true, nil
		}
	}
	// No guessed exhaustion of a full raw window, even for optional evidence.
	if len(rows) == MaxRaw {
		return false, ErrRelatedCapacity
	}
	return false, nil
}
func relatedHydrate(q browseQuery, s *relatedState) error {
	if s.hydrate == len(s.ids) {
		s.phase = 6
		return nil
	}
	id := s.ids[s.hydrate]
	c := s.candidates[id]
	if s.current == nil {
		e, err := relatedEntityRead(q, s.fence.profile, id)
		if err != nil {
			return err
		}
		s.rawTotal++
		ok, err := relatedEligibility(q, e, s.fence)
		if err != nil {
			return err
		}
		if !ok || e.negative() || relatedShared(c) == 0 {
			s.hydrate++
			return nil
		}
		c.entity = e
		s.current = c
		s.enumStage = 0
		s.after = 0
		return nil
	}
	// Exclude provider/local editions of the seed independently of enrichment.
	seed := s.roots[len(s.roots)-1].id()
	switch s.enumStage {
	case 0:
		rows, err := relatedRead(q, s, "local.equal", id, seed)
		if err != nil {
			return err
		}
		if len(rows) > 1 {
			return ErrUnqualified
		}
		if len(rows) == 1 {
			s.current = nil
			s.hydrate++
			return nil
		}
		s.enumStage = 1
		return nil
	case 1:
		equal, err := relatedProviderEqual(q, s, id, seed)
		if err != nil {
			return err
		}
		if equal {
			s.current = nil
			s.hydrate++
			return nil
		}
		s.enumStage = 2
		s.after = 0
		s.normalizeJ = 0
		return nil
	case 2:
		if int(s.after) == s.hydrate {
			s.enumStage = 3
			s.probe = []relatedProbe{{id: id, entity: c.entity, stage: 1}}
			s.probeSeen = map[int64]bool{id: true}
			return nil
		}
		prior := s.candidates[s.ids[s.after]]
		if !prior.visible {
			s.after++
			s.normalizeJ = 0
			return nil
		}
		equal := false
		if s.normalizeJ == 0 {
			rows, err := relatedRead(q, s, "local.equal", id, prior.entity.id())
			if err != nil {
				return err
			}
			if len(rows) > 1 {
				return ErrUnqualified
			}
			equal = len(rows) == 1
			s.normalizeJ = 1
		} else {
			var err error
			equal, err = relatedProviderEqual(q, s, id, prior.entity.id())
			if err != nil {
				return err
			}
			s.after++
			s.normalizeJ = 0
		}
		if equal {
			for n := range prior.bits {
				prior.bits[n] |= c.bits[n]
			}
			s.current = nil
			s.hydrate++
		}
		return nil
	}
	visible, done, err := relatedProbeStep(q, s)
	if err != nil {
		return err
	}
	if done {
		c.visible = visible
		s.current = nil
		s.hydrate++
		s.probe = nil
		s.probeSeen = nil
	}
	return nil
}

// A container needs a current eligible available descendant, not a five-child
// sample. No unbounded EXISTS or recursive query hides that work from custody.
func relatedProbeStep(q browseQuery, s *relatedState) (bool, bool, error) {
	if len(s.probe) == 0 {
		return false, true, nil
	}
	p := &s.probe[len(s.probe)-1]
	if p.stage == 0 {
		e, err := relatedEntityRead(q, s.fence.profile, p.id)
		if err != nil {
			return false, false, err
		}
		s.rawTotal++
		if e.row == nil || !e.eligible(s.fence) {
			s.probe = s.probe[:len(s.probe)-1]
			return false, false, nil
		}
		p.entity = e
		p.stage = 1
		return false, false, nil
	}
	if p.stage == 1 {
		if p.entity.kind() < 1 || p.entity.kind() > 13 {
			return false, false, ErrUnqualified
		}
		f := s.fence
		f.container = relatedInt(p.entity.row[7])
		_, err := rsDetailSubjectEligibility(q, BrowseInput{Library: f.libraryID, Kind: p.entity.kind(), policy: readPolicyBinding{subject: p.id}}, f)
		if err == ErrDenied {
			s.probe = s.probe[:len(s.probe)-1]
			return false, false, nil
		}
		if err != nil {
			return false, false, err
		}
		p.stage = 2
		return false, false, nil
	}
	if relatedInt(p.entity.row[7]) == 0 {
		return relatedInventoryStep(q, s, p)
	}

	// Only these hierarchy/member walks have a complete source proof here.
	// Other container kinds need their own closed traversal, even when their
	// accepted admission band is supported by the general subject recipe.
	switch p.entity.kind() {
	case 2, 3, 5, 6, 8, 10:
	default:
		return false, false, ErrUnqualified
	}
	// Read one child per tick. Full windows are not necessary here: the cursor
	// position is always an actual raw child and an empty seek proves exhaustion.
	var rows [][]any
	var err error
	switch p.entity.kind() {
	case 2:
		rows, err = relatedRead(q, s, "show.members", p.id, p.child, int64(1))
	case 5:
		rows, err = relatedRead(q, s, "artist.members", p.id, p.child, int64(1))
	case 6, 8:
		if len(p.members) > 0 {
			id := p.members[0]
			p.members = p.members[1:]
			return false, false, relatedPushProbe(s, id)
		}
		if p.exhausted {
			s.probe = s.probe[:len(s.probe)-1]
			return false, false, nil
		}
		name, kind := "album", int64(7)
		if p.entity.kind() == 8 {
			name = "book"
			kind = 9
		}
		rows, err = relatedRead(q, s, "children."+name, s.fence.libraryID, kind, p.child, int64(MaxRaw))
		if err != nil {
			return false, false, err
		}
		for _, row := range rows {
			p.child = row[0].(int64)
			if row[1] != nil && row[1].(int64) == p.id {
				p.members = append(p.members, p.child)
			}
		}
		p.exhausted = len(rows) < MaxRaw
		return false, false, nil
	case 10:
		rows, err = relatedRead(q, s, "collection.members", p.id, p.child, int64(1))
	default:
		rows, err = relatedRead(q, s, "children", p.id, p.child, int64(1))
	}
	if err != nil {
		return false, false, err
	}

	if len(rows) == 0 {
		s.probe = s.probe[:len(s.probe)-1]
		return false, false, nil
	}
	child := rows[0][0].(int64)
	p.child = child
	return false, false, relatedPushProbe(s, child)
}
func relatedPushProbe(s *relatedState, child int64) error {
	if s.probeSeen[child] {
		return ErrUnqualified
	}
	if len(s.probeSeen) == rsRelatedCandidates || len(s.probe) == 8 {
		return ErrRelatedCapacity
	}
	s.probeSeen[child] = true
	s.probe = append(s.probe, relatedProbe{id: child})
	return nil
}

func relatedSeedAvailability(q browseQuery, s *relatedState) error {
	if s.rootIndex == len(s.roots) {
		s.phase = 0
		s.rootIndex = 0
		return nil
	}
	if s.probe == nil {
		e := s.roots[s.rootIndex]
		s.probe = []relatedProbe{{id: e.id(), entity: e, stage: 1}}
		s.probeSeen = map[int64]bool{e.id(): true}
	}
	visible, done, err := relatedProbeStep(q, s)
	if err != nil {
		return err
	}
	if done {
		if !visible {
			return ErrDenied
		}
		s.rootIndex++
		s.probe = nil
		s.probeSeen = nil
	}
	return nil
}

// Collection rows retain current year/title/ID order. Only native PK title
// comparisons are needed to sort; full candidate titles are never hydrated.
func relatedCollectionOrder(q browseQuery, s *relatedState) error {
	if s.orders == nil {
		s.orders = make(map[RelatedKey][]int64)
	}
	for s.orderFeature < len(s.features) && s.features[s.orderFeature].key.Kind != 3 {
		s.orderFeature++
	}
	if s.orderFeature == len(s.features) {
		s.orderCandidates = nil
		s.phase = 4
		return nil
	}
	key := s.features[s.orderFeature].key
	if s.orderCandidates == nil {
		s.orderCandidates = make([]int64, 0)
		for _, id := range s.ids {
			c := s.candidates[id]
			if c.visible && relatedHas(c, s.orderFeature) {
				s.orderCandidates = append(s.orderCandidates, id)
			}
		}
		if s.orderIDs+len(s.orderCandidates) > 65536 {
			return ErrRelatedCapacity
		}
		s.orderIDs += len(s.orderCandidates)
		s.orderIndex = 0
	}
	if s.orderIndex == len(s.orderCandidates) {
		s.orderFeature++
		s.orderCandidates = nil
		return nil
	}
	id := s.orderCandidates[s.orderIndex]
	order := s.orders[key]
	if !s.sortActive {
		s.sortLow = 0
		s.sortHigh = len(order)
		s.sortStage = 0
		s.sortActive = true
	}
	if s.sortLow == s.sortHigh {
		at := s.sortLow
		order = append(order, 0)
		copy(order[at+1:], order[at:])
		order[at] = id
		s.orders[key] = order
		s.orderIndex++
		s.sortActive = false
		return nil
	}
	mid := (s.sortLow + s.sortHigh) / 2
	other := order[mid]
	a, b := s.candidates[id].entity, s.candidates[other].entity
	less := false
	if a.row[8] != b.row[8] {
		less = a.row[8].(int64) < b.row[8].(int64)
	} else {
		x, y := id, other
		if s.sortStage == 1 {
			x, y = y, x
		}
		rows, err := relatedRead(q, s, "title.less", y, x)
		if err != nil {
			return err
		}
		if len(rows) > 1 {
			return ErrUnqualified
		}
		if s.sortStage == 0 && len(rows) == 0 {
			s.sortStage = 1
			return nil
		}
		if s.sortStage == 0 {
			less = true
		} else if len(rows) == 0 {
			less = id < other
		}
	}
	s.sortStage = 0
	if less {
		s.sortHigh = mid
	} else {
		s.sortLow = mid + 1
	}
	return nil
}
