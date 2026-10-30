package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

type smallForYouCandidate struct {
	id       int64
	title    string
	added    string
	eligible bool
	data     recWorkMetadata
}
type smallForYouOrder struct {
	profile, library      string
	libraryID             int64
	now                   time.Time
	stage, index          int
	taste                 recTaste
	epoch, dl, ds         float64
	known                 bool
	afterFacet            string
	facetStarted          bool
	jobs                  []int64
	afterJob              int64
	activityCount         int
	activityItem          int64
	activityAt            string
	activityAfter         string
	activityAfterID       int64
	work                  int64
	contribution          float64
	position, maxDuration float64
	afterAsset            int64
	signal                smallRecSignal
	candidates            []smallForYouCandidate
	afterPostingFacet     string
	afterPostingLibrary   int64
	afterPostingQuality   float64
	postingStarted        bool
	idf                   map[string]float64
	keys                  []string
	total                 float64
	memoRow, memoPolicy   string
	activityFence         string
}

// NewSmallForYouOrder reproduces the <=2000 exact branch of recForYouValued
// for COMPLETE, prefiltered movie identities. Other ForYou pivots are container
// selections and remain outside DB002's leaf-conversion contract. The instant
// is part of the caller's canonical preparation key and must not change later.
func NewSmallForYouOrder(ids []int64, profile, library, basis string, now time.Time) (*SmallSelectionOrder, error) {
	p, err := newSmallSelectionOrder(ids, basis)
	if err != nil {
		return nil, err
	}
	if library == "" || now.IsZero() {
		return nil, ErrSelectionBatchUnsupported
	}
	p.forYou = &smallForYouOrder{profile: profile, library: library, now: now, taste: recTaste{values: map[string]float64{}, engaged: map[int64]bool{}, hidden: map[int64]bool{}, loaded: map[int64]bool{}}, idf: map[string]float64{}, candidates: make([]smallForYouCandidate, len(ids))}
	p.done = len(ids) == 0
	if p.Bytes() > SelectionBatchMaximumBytes {
		return nil, ErrSelectionBatchCapacity
	}
	return p, nil
}
func (f *smallForYouOrder) bytes() int {
	n := int(unsafe.Sizeof(*f)) + len(f.profile) + len(f.library) + len(f.afterFacet) + len(f.activityAt) + len(f.activityAfter) + len(f.afterPostingFacet) + len(f.memoRow) + len(f.memoPolicy) + len(f.activityFence) + cap(f.jobs)*8 + cap(f.keys)*16 + cap(f.candidates)*int(unsafe.Sizeof(smallForYouCandidate{})) + f.signal.bytes()
	for k := range f.taste.values {
		n += 128 + len(k)
	}
	n += 96 * (len(f.taste.engaged) + len(f.taste.hidden) + len(f.taste.loaded))
	for k := range f.idf {
		n += 128 + len(k)
	}
	for _, c := range f.candidates {
		n += len(c.title) + len(c.added) + cap(c.data.facets)*16
		for _, v := range c.data.facets {
			n += len(v)
		}
	}
	return n
}
func (f *smallForYouOrder) clone() *smallForYouOrder {
	n := *f
	n.taste.values = cloneSmallMap(f.taste.values)
	n.taste.engaged = cloneSmallMap(f.taste.engaged)
	n.taste.hidden = cloneSmallMap(f.taste.hidden)
	n.taste.loaded = cloneSmallMap(f.taste.loaded)
	n.idf = cloneSmallMap(f.idf)
	n.keys = slices.Clone(f.keys)
	n.jobs = slices.Clone(f.jobs)
	n.candidates = slices.Clone(f.candidates)
	for i := range n.candidates {
		n.candidates[i].data.facets = slices.Clone(f.candidates[i].data.facets)
	}
	return &n
}
func cloneSmallMap[K comparable, V any](m map[K]V) map[K]V {
	n := make(map[K]V, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}
func (f *smallForYouOrder) addTaste(facet string, v float64, reserve func(int64) error) error {
	if _, ok := f.taste.values[facet]; !ok {
		if err := reserve(-(128 + int64(len(facet)))); err != nil {
			return err
		}
	}
	f.taste.values[facet] += v
	return nil
}

func (s *Service) smallForYouUnit(p *SmallSelectionOrder, reserve func(int64) error) error {
	f := p.forYou
	switch f.stage {
	case 0:
		if err := s.read().QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=? AND retired=0`, f.library).Scan(&f.libraryID); err != nil {
			return err
		}
		var err error
		f.epoch, f.taste.revision, f.known, err = compactcatalog.RecEpoch(s.Context(), s.read(), f.profile)
		if err != nil {
			return err
		}
		f.taste.stored = f.known
		if f.known {
			f.dl = compactcatalog.RecDecay(f.epoch, compactcatalog.UnixDays(f.now), compactcatalog.RecLongHalfLife)
			f.ds = compactcatalog.RecDecay(f.epoch, compactcatalog.UnixDays(f.now), compactcatalog.RecShortHalfLife)
		}
		f.stage = 1
		if f.profile == "" {
			f.stage = 10
		}
		return nil
	case 1: // Full profile taste, never a candidate-only approximation.
		if !f.known {
			f.stage = 2
			return nil
		}
		query := `SELECT facet AS v FROM rec_profile_taste WHERE profile_id=?`
		args := []any{f.profile}
		if f.facetStarted {
			query += ` AND facet>?`
			args = append(args, f.afterFacet)
		}
		query += ` ORDER BY facet LIMIT 1`
		facet, ok, err := s.smallOrderText(query, args, reserve)
		if err != nil {
			return err
		}
		if !ok {
			f.afterFacet = ""
			f.facetStarted = false
			f.stage = 2
			return nil
		}
		f.afterFacet, f.facetStarted = facet, true
		var long, short float64
		if err = s.read().QueryRow(`SELECT long,short FROM rec_profile_taste WHERE profile_id=? AND facet=?`, f.profile, facet).Scan(&long, &short); err != nil {
			return err
		}
		return f.addTaste(facet, long*f.dl+short*f.ds, reserve)
	case 2: // Same partial-index recent window as recAbandoned, before checks.
		if f.activityCount == 200 {
			f.stage = 5
			return nil
		}
		query := `SELECT item_id FROM progress_activity INDEXED BY progress_home_profile_recent WHERE profile_id=? AND state!='ended' AND updated_at<?`
		args := []any{f.profile, f.now.AddDate(0, 0, -recAbandonAfter).UTC().Format("2006-01-02T15:04:05.000Z")}
		if f.activityCount > 0 {
			query += ` AND (updated_at,item_id)<(?,?)`
			args = append(args, f.activityAfter, f.activityAfterID)
		}
		query += ` ORDER BY updated_at DESC,item_id DESC LIMIT 1`
		err := s.read().QueryRow(query, args...).Scan(&f.activityItem)
		if errors.Is(err, sql.ErrNoRows) {
			f.stage = 5
			return nil
		}
		if err != nil {
			return err
		}
		at, ok, err := s.smallOrderText(`SELECT updated_at AS v FROM progress_activity WHERE profile_id=? AND item_id=?`, []any{f.profile, f.activityItem}, reserve)
		if err != nil {
			return err
		}
		if !ok {
			return ErrSelectionBatchStale
		}
		f.activityAt, f.activityAfter, f.activityAfterID = at, at, f.activityItem
		f.activityCount++
		var completed int
		err = s.read().QueryRow(`SELECT completed,position FROM progress WHERE profile_id=? AND item_id=?`, f.profile, f.activityItem).Scan(&completed, &f.position)
		if err != nil {
			return err
		}
		if completed != 0 || f.position <= 0 {
			return nil
		}
		err = s.read().QueryRow(`SELECT COALESCE((SELECT show_id FROM catalog_episodes WHERE entity_id=?1),(SELECT album_id FROM catalog_songs WHERE entity_id=?1),(SELECT book_id FROM catalog_book_files WHERE entity_id=?1),?1)`, f.activityItem).Scan(&f.work)
		if err != nil {
			return err
		}
		var weight float64
		err = s.read().QueryRow(`SELECT COALESCE((SELECT weight FROM rec_profile_signals WHERE profile_id=? AND work_id=?),0)`, f.profile, f.work).Scan(&weight)
		if err != nil {
			return err
		}
		if weight > 0 {
			return nil
		}
		atTime, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil
		}
		f.contribution = recAbandonWeight * math.Exp2(-(compactcatalog.UnixDays(f.now)-compactcatalog.UnixDays(atTime))/compactcatalog.RecLongHalfLife)
		f.afterAsset = 0
		f.maxDuration = 0
		f.stage = 3
		return nil
	case 3: // All asset links, raw before the max-duration predicate.
		var asset int64
		err := s.read().QueryRow(`SELECT asset_id FROM catalog_asset_links WHERE entity_id=? AND asset_id>? ORDER BY asset_id LIMIT 1`, f.activityItem, f.afterAsset).Scan(&asset)
		if errors.Is(err, sql.ErrNoRows) {
			if f.position < recAbandonFraction*1000*f.maxDuration {
				f.afterFacet = ""
				f.facetStarted = false
				f.stage = 4
			} else {
				f.stage = 2
			}
			return nil
		}
		if err != nil {
			return err
		}
		f.afterAsset = asset
		var duration float64
		err = s.read().QueryRow(`SELECT duration FROM catalog_assets WHERE id=?`, asset).Scan(&duration)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		f.maxDuration = max(f.maxDuration, duration)
		return nil
	case 4:
		done, err := s.smallForYouContribution(f, reserve)
		if err != nil {
			return err
		}
		if done {
			f.stage = 2
		}
		return nil
	case 5: // Preserve the existing >256 backlog exclusion rule exactly.
		var work int64
		err := s.read().QueryRow(`SELECT work_id FROM rec_profile_jobs WHERE profile_id=? AND work_id>? ORDER BY work_id LIMIT 1`, f.profile, f.afterJob).Scan(&work)
		if errors.Is(err, sql.ErrNoRows) {
			f.index = 0
			f.stage = 6
			return nil
		}
		if err != nil {
			return err
		}
		f.afterJob = work
		if err = reserve(-16); err != nil {
			return err
		}
		f.jobs = append(f.jobs, work)
		f.taste.pending = true
		if len(f.jobs) > recOverlayJobs {
			f.taste.bulkPending = true
			f.jobs = nil
			f.stage = 10
			f.index = 0
		}
		return nil
	case 6:
		if f.index == len(f.jobs) {
			f.index = 0
			f.stage = 10
			return nil
		}
		f.work = f.jobs[f.index]
		f.signal = smallRecSignal{work: f.work, now: f.now}
		f.stage = 7
		return nil
	case 7:
		done, err := s.smallRecSignalUnit(f.profile, &f.signal, reserve)
		if err != nil {
			return err
		}
		if !done {
			return nil
		}
		next := f.signal.result()
		var oldLong, oldShort float64
		err = s.read().QueryRow(`SELECT COALESCE((SELECT long FROM rec_profile_signals WHERE profile_id=?1 AND work_id=?2),0),COALESCE((SELECT short FROM rec_profile_signals WHERE profile_id=?1 AND work_id=?2),0)`, f.profile, f.work).Scan(&oldLong, &oldShort)
		if err != nil {
			return err
		}
		if !f.known {
			f.epoch = math.Floor(next.At)
			f.known = true
			f.dl = compactcatalog.RecDecay(f.epoch, compactcatalog.UnixDays(f.now), compactcatalog.RecLongHalfLife)
			f.ds = compactcatalog.RecDecay(f.epoch, compactcatalog.UnixDays(f.now), compactcatalog.RecShortHalfLife)
		}
		long := next.Weight * math.Exp2((next.At-f.epoch)/compactcatalog.RecLongHalfLife)
		short := next.Weight * math.Exp2((next.At-f.epoch)/compactcatalog.RecShortHalfLife)
		f.contribution = (long-oldLong)*f.dl + (short-oldShort)*f.ds
		if err = reserve(-288); err != nil {
			return err
		}
		f.taste.engaged[f.work], f.taste.hidden[f.work], f.taste.loaded[f.work] = next.Engaged, next.Hidden, true
		f.afterFacet = ""
		f.facetStarted = false
		f.stage = 8
		return nil
	case 8:
		if f.contribution != 0 {
			done, err := s.smallForYouContribution(f, reserve)
			if err != nil {
				return err
			}
			if !done {
				return nil
			}
		}
		f.index++
		f.stage = 6
		return nil
	case 10: // Candidate source scalars, one pinned identity at a time.
		if f.index == len(p.ids) {
			f.index = 0
			f.stage = 13
			return nil
		}
		c := &f.candidates[f.index]
		c.id = p.ids[f.index]
		var kind int
		var library int64
		var retired, available bool
		err := s.read().QueryRow(`SELECT e.kind,e.library_id,e.retired,br.available FROM catalog_entities e JOIN catalog_browse_rows br ON br.entity_id=e.id WHERE e.id=?`, c.id).Scan(&kind, &library, &retired, &available)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSelectionBatchStale
		}
		if err != nil {
			return err
		}
		if compactcatalog.Kind(kind) != compactcatalog.Movie || library != f.libraryID {
			return ErrSelectionBatchUnsupported
		}
		c.eligible = !retired && available
		var ok bool
		c.title, ok, err = s.smallOrderText(`SELECT sort_key AS v FROM catalog_browse_rows WHERE entity_id=?`, []any{c.id}, reserve)
		if err != nil {
			return err
		}
		if !ok {
			return ErrSelectionBatchStale
		}
		c.added, ok, err = s.smallOrderText(`SELECT COALESCE(recent_text,added_text,'') AS v FROM catalog_browse_rows WHERE entity_id=?`, []any{c.id}, reserve)
		if err != nil {
			return err
		}
		if !ok {
			return ErrSelectionBatchStale
		}
		if !f.taste.loaded[c.id] {
			stored := f.profile
			if !f.taste.stored {
				stored = ""
			}
			var engaged, hidden, pending bool
			err = s.read().QueryRow(`SELECT COALESCE((SELECT engaged FROM rec_profile_signals WHERE profile_id=?1 AND work_id=?2),0),COALESCE((SELECT hidden FROM rec_profile_signals WHERE profile_id=?1 AND work_id=?2),0),CASE WHEN ?3 THEN EXISTS(SELECT 1 FROM rec_profile_jobs WHERE profile_id=?4 AND work_id=?2) ELSE 0 END`, stored, c.id, f.taste.bulkPending, f.profile).Scan(&engaged, &hidden, &pending)
			if err != nil {
				return err
			}
			if err = reserve(-288); err != nil {
				return err
			}
			f.taste.engaged[c.id], f.taste.hidden[c.id], f.taste.loaded[c.id] = engaged || pending, hidden || pending, true
		}
		if f.taste.hidden[c.id] {
			f.index++
			return nil
		}
		f.afterPostingFacet = ""
		f.afterPostingLibrary = 0
		f.afterPostingQuality = 0
		f.postingStarted = false
		f.stage = 11
		return nil
	case 11:
		c := &f.candidates[f.index]
		query := `SELECT facet AS v FROM catalog_rec_postings INDEXED BY catalog_rec_postings_entity WHERE entity_id=?`
		args := []any{c.id}
		if f.postingStarted {
			query += ` AND (facet,library_id,quality)>(?,?,?)`
			args = append(args, f.afterPostingFacet, f.afterPostingLibrary, f.afterPostingQuality)
		}
		query += ` ORDER BY facet,library_id,quality LIMIT 1`
		facet, ok, err := s.smallOrderText(query, args, reserve)
		if err != nil {
			return err
		}
		if !ok {
			f.index++
			f.stage = 10
			return nil
		}
		// A facet/entity has one posting in its owning library; pin the complete
		// physical key nevertheless, including unusual duplicate test fixtures.
		q := `SELECT library_id,quality FROM catalog_rec_postings WHERE entity_id=? AND facet=?`
		a := []any{c.id, facet}
		if f.postingStarted && facet == f.afterPostingFacet {
			q += ` AND (library_id,quality)>(?,?)`
			a = append(a, f.afterPostingLibrary, f.afterPostingQuality)
		}
		q += ` ORDER BY library_id,quality LIMIT 1`
		var library int64
		var quality float64
		if err = s.read().QueryRow(q, a...).Scan(&library, &quality); err != nil {
			return err
		}
		f.afterPostingFacet, f.afterPostingLibrary, f.afterPostingQuality, f.postingStarted = facet, library, quality, true
		c.data.quality, c.data.hasQuality = quality, true
		if facet != compactcatalog.RecAllFacet {
			if err = reserve(-(32 + int64(len(facet)))); err != nil {
				return err
			}
			c.data.facets = append(c.data.facets, facet)
		}
		return nil
	case 13:
		if err := reserve(-int64(16 * (len(f.taste.values) + 1))); err != nil {
			return err
		}
		for key := range f.taste.values {
			if recFacetWeight(key) > 0 {
				f.keys = append(f.keys, key)
			}
		}
		sort.Strings(f.keys)
		f.index = 0
		var total float64
		err := s.read().QueryRow(`SELECT COALESCE((SELECT works FROM catalog_rec_df WHERE facet=?),0)`, compactcatalog.RecAllFacet).Scan(&total)
		if err != nil {
			return err
		}
		f.total = math.Max(total, 1)
		f.stage = 14
		return nil
	case 14:
		if f.index == len(f.keys) {
			f.index = 0
			f.afterJob = 0
			f.stage = 15
			return nil
		}
		if err := s.smallForYouRarity(f, f.keys[f.index], reserve); err != nil {
			return err
		}
		f.index++
		return nil
	case 15:
		if f.index == len(f.candidates) {
			f.stage = 16
			return nil
		}
		c := &f.candidates[f.index]
		if int(f.afterJob) == len(c.data.facets) {
			f.index++
			f.afterJob = 0
			return nil
		}
		facet := c.data.facets[f.afterJob]
		f.afterJob++
		if _, ok := f.idf[facet]; !ok {
			return s.smallForYouRarity(f, facet, reserve)
		}
		return nil
	case 16:
		if reused, err := s.smallForYouReuseMemo(p, reserve); err != nil || reused {
			return err
		}
		return finishSmallForYou(p, reserve)
	}
	return ErrSelectionBatchIncomplete
}

func (s *Service) smallForYouContribution(f *smallForYouOrder, reserve func(int64) error) (bool, error) {
	query := `SELECT facet AS v FROM catalog_rec_facets WHERE entity_id=?`
	args := []any{f.work}
	if f.facetStarted {
		query += ` AND facet>?`
		args = append(args, f.afterFacet)
	}
	query += ` ORDER BY facet LIMIT 1`
	facet, ok, err := s.smallOrderText(query, args, reserve)
	if err != nil {
		return false, err
	}
	if !ok {
		f.afterFacet = ""
		f.facetStarted = false
		return true, nil
	}
	f.afterFacet, f.facetStarted = facet, true
	return false, f.addTaste(facet, f.contribution, reserve)
}
func (s *Service) smallForYouRarity(f *smallForYouOrder, facet string, reserve func(int64) error) error {
	if err := reserve(-(128 + int64(len(facet)))); err != nil {
		return err
	}
	var count float64
	if err := s.read().QueryRow(`SELECT COALESCE((SELECT works FROM catalog_rec_df WHERE facet=?),0)`, facet).Scan(&count); err != nil {
		return err
	}
	f.idf[facet] = math.Log(1 + f.total/math.Max(count, 1))
	return nil
}

// BindForYouMemoScope binds the exact ordinary-Browse memo identity without
// executing its WHERE. The scope comes from the original bound page; no second
// SQL compiler is substituted. An ordinary day-scoped cached rank must retain
// that same order even when a later preparation has a different wall instant.
type SmallForYouMemoScope struct {
	digest                   [12]byte
	policy, library, profile string
}

func (m SmallForYouMemoScope) Bytes() int {
	return 64 + len(m.policy) + len(m.library) + len(m.profile)
}
func NewSmallForYouMemoScope(bound BoundBrowseQuery, viewer Viewer) (SmallForYouMemoScope, error) {
	if bound.Where == "" {
		return SmallForYouMemoScope{}, ErrSelectionBatchUnsupported
	}
	// JSON's worst-case escaping is bounded BEFORE its temporary buffers are
	// allocated. Bound args are compiler scalars, never arbitrary object graphs.
	encodedBound := int64(32 + 6*len(bound.Where))
	for _, arg := range bound.Args {
		switch v := arg.(type) {
		case string:
			encodedBound += int64(16 + 6*len(v))
		case []byte:
			encodedBound += int64(16 + 6*len(v))
		case nil, bool, int, int32, int64, uint, uint32, uint64, float32, float64:
			encodedBound += 64
		default:
			return SmallForYouMemoScope{}, ErrSelectionBatchUnsupported
		}
	}
	if encodedBound > SelectionBatchMaximumBytes {
		return SmallForYouMemoScope{}, ErrSelectionBatchCapacity
	}
	raw, err := json.Marshal(append([]any{bound.Where}, bound.Args...))
	if err != nil {
		return SmallForYouMemoScope{}, err
	}
	if len(raw) > SelectionBatchMaximumBytes {
		return SmallForYouMemoScope{}, ErrSelectionBatchCapacity
	}

	digest := sha256.Sum256(raw)
	m := SmallForYouMemoScope{policy: RestrictionFence(viewer.EffectiveRestrictions()), library: bound.Library, profile: viewer.Profile}
	copy(m.digest[:], digest[:12])
	return m, nil
}
func (p *SmallSelectionOrder) BindForYouMemoScope(bound BoundBrowseQuery, viewer Viewer) error {
	scope, err := NewSmallForYouMemoScope(bound, viewer)
	if err != nil {
		return err
	}
	return p.bindForYouMemoScope(scope)
}
func (p *SmallSelectionOrder) bindForYouMemoScope(scope SmallForYouMemoScope) error {
	if p == nil || p.forYou == nil || scope.library != p.forYou.library || scope.profile != p.forYou.profile {
		return ErrSelectionBatchUnsupported
	}
	p.forYou.memoRow = "forYou:" + hex.EncodeToString(scope.digest[:]) + ":" + strconv.Itoa(len(p.ids))
	p.forYou.memoPolicy = scope.policy
	return nil
}

func (s *Service) smallForYouReuseMemo(p *SmallSelectionOrder, reserve func(int64) error) (bool, error) {
	f := p.forYou
	if f.memoRow == "" || f.taste.pending {
		return false, nil
	}
	revision, err := s.homeRevision([]string{f.library}, f.profile)
	if err != nil {
		return false, err
	}
	var data int64
	if err = s.read().QueryRow(`SELECT revision FROM rec_data_revision WHERE id=1`).Scan(&data); err != nil {
		return false, err
	}
	key := strings.Join([]string{f.profile, f.memoPolicy, idsJSON([]string{f.library}), itoa(revision.Catalog), itoa(revision.Viewer), itoa(f.taste.revision), itoa(data), f.now.In(time.Local).Format("2006-01-02")}, "\x00") + "\x00" + f.memoRow
	// Inspect without recMemo.get's unconditional full-result copy. Reserve
	// output/copy memory first; never import cached candidate facets.
	if err = reserve(-(int64(len(p.ids))*512 + 65536)); err != nil {
		return false, err
	}
	s.state.recMu.Lock()
	cached, ok := s.state.recMemo[key]
	if !ok {
		s.state.recMu.Unlock()
		return false, nil
	}
	if len(cached) > len(p.ids) {
		s.state.recMu.Unlock()
		return false, ErrSelectionBatchStale
	}
	ids := make([]int64, 0, len(cached))
	for _, candidate := range cached {
		id, parseErr := strconv.ParseInt(candidate.Work, 10, 64)
		if parseErr != nil {
			s.state.recMu.Unlock()
			return false, parseErr
		}
		ids = append(ids, id)
	}
	s.state.recMu.Unlock()
	byID := map[int64]*smallForYouCandidate{}
	for i := range f.candidates {
		byID[f.candidates[i].id] = &f.candidates[i]
	}
	seen := map[int64]bool{}
	for _, engaged := range []bool{false, true} {
		for _, id := range ids {
			if byID[id] != nil && !seen[id] && f.taste.engaged[id] == engaged {
				p.ordered = append(p.ordered, id)
				seen[id] = true
			}
		}
	}
	var rest []int64
	for _, id := range p.ids {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	slices.SortFunc(rest, func(a, b int64) int {
		c := smallOrderNoCase(byID[a].title, byID[b].title)
		if c == 0 {
			if a < b {
				c = -1
			} else if a > b {
				c = 1
			}
		}
		return c
	})
	p.ordered = append(p.ordered, rest...)
	p.done = true
	return true, nil
}

func finishSmallForYou(p *SmallSelectionOrder, reserve func(int64) error) error {
	f := p.forYou
	if err := reserve(-(int64(len(p.ids))*512 + int64(16*len(f.taste.values)) + 65536)); err != nil {
		return err
	}
	x := recSession{taste: f.taste, idf: f.idf, now: f.now, hydration: &recHydration{works: map[int64]*recWorkMetadata{}}}
	var scored []*recScored
	for i := range f.candidates {
		c := &f.candidates[i]
		x.hydration.works[c.id] = &c.data
		if !f.taste.hidden[c.id] {
			scored = append(scored, &recScored{work: c.id, quality: -1})
		}
	}
	// All candidate metadata/rarity is already hydrated by raw quanta. This
	// invokes the actual scorer, with no recSession/RecSignalFor/SQL census.
	if err := x.recRescore(scored, 0); err != nil {
		return err
	}
	byID := map[int64]*smallForYouCandidate{}
	for i := range f.candidates {
		byID[f.candidates[i].id] = &f.candidates[i]
	}
	fresh := f.now.AddDate(0, 0, -30).UTC().Format("2006-01-02")
	kept := scored[:0]
	for _, c := range scored {
		metadata := byID[c.work]
		if metadata.eligible {
			if metadata.added >= fresh {
				c.score += 0.05
			}
			kept = append(kept, c)
		}
	}
	scored = kept
	slices.SortFunc(scored, func(a, b *recScored) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		if a.work < b.work {
			return -1
		}
		if a.work > b.work {
			return 1
		}
		return 0
	})
	ranked := map[int64]bool{}
	for _, engaged := range []bool{false, true} {
		for _, c := range scored {
			if f.taste.engaged[c.work] == engaged {
				p.ordered = append(p.ordered, c.work)
				ranked[c.work] = true
			}
		}
	}
	var rest []int64
	for _, id := range p.ids {
		if !ranked[id] {
			rest = append(rest, id)
		}
	}
	slices.SortFunc(rest, func(a, b int64) int {
		left, right := smallOrderKey{}, smallOrderKey{}
		left.text(byID[a].title, true)
		right.text(byID[b].title, true)
		c := smallOrderCompare(left, right)
		if c == 0 {
			if a < b {
				c = -1
			} else if a > b {
				c = 1
			}
		}
		return c
	})
	p.ordered = append(p.ordered, rest...)
	p.done = true
	return nil
}

// The pending overlay's exact signal is a reduction over raw members and raw
// history, including OTHER profiles and incomplete events before rejection.
// There is no call to RecSignalFor's work-wide aggregate.
type smallRecSignal struct {
	work                                                                  int64
	now                                                                   time.Time
	stage, phase, kind                                                    int
	item, afterMember, afterHistory                                       int64
	finished, completions, favorite, watchlisted, notInterested, progress int64
	ratingSum                                                             float64
	ratingCount                                                           int64
	at                                                                    string
	memberDone                                                            bool
}

func (r *smallRecSignal) bytes() int { return int(unsafe.Sizeof(*r)) + len(r.at) }
func (r *smallRecSignal) result() compactcatalog.RecSignal {
	x := compactcatalog.RecSignal{At: compactcatalog.UnixDays(r.now)}
	if at, err := time.Parse(time.RFC3339Nano, r.at); err == nil {
		x.At = compactcatalog.UnixDays(at)
	}
	if r.finished > 0 {
		switch compactcatalog.Kind(r.kind) {
		case compactcatalog.Show:
			x.Weight += 1 + math.Min(1, .1*float64(r.finished-1))
		case compactcatalog.Album:
			x.Weight += .6 + math.Min(.6, .05*float64(r.completions-1))
		default:
			x.Weight++
			if r.completions > 1 {
				x.Weight += .5
			}
		}
	}
	if r.favorite == 1 {
		x.Weight += 2
	}
	if r.ratingCount > 0 {
		x.Weight += (r.ratingSum/float64(r.ratingCount)/2 - 3) * .8
	}
	if r.watchlisted == 1 {
		x.Weight += .5
	}
	if r.notInterested == 1 {
		x.Weight--
	}
	x.Engaged = r.finished > 0 || r.progress == 1 || r.favorite == 1 || r.ratingCount > 0 || r.watchlisted == 1
	x.Hidden = r.notInterested == 1
	x.Finished = r.finished > 0
	return x
}
func (s *Service) smallRecSignalUnit(profile string, r *smallRecSignal, reserve func(int64) error) (bool, error) {
	switch r.stage {
	case 0:
		err := s.read().QueryRow(`SELECT kind FROM catalog_entities WHERE id=?`, r.work).Scan(&r.kind)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		r.item = r.work
		r.stage = 2
		return false, nil
	case 1:
		if r.phase == 3 {
			return true, nil
		}
		// These parent indexes do not all order by entity_id alone. Use the
		// physical entity PK as the raw run; parent identity is tested afterwards.
		tables := []string{"catalog_episodes", "catalog_songs", "catalog_book_files"}
		parents := []string{"show_id", "album_id", "book_id"}
		var item, parent int64
		err := s.read().QueryRow(`SELECT entity_id,`+parents[r.phase]+` FROM `+tables[r.phase]+` WHERE entity_id>? ORDER BY entity_id LIMIT 1`, r.afterMember).Scan(&item, &parent)
		if errors.Is(err, sql.ErrNoRows) {
			r.phase++
			r.afterMember = 0
			return false, nil
		}
		if err != nil {
			return false, err
		}
		r.afterMember = item
		if parent != r.work {
			return false, nil
		}
		r.item = item
		r.stage = 2
		return false, nil
	case 2:
		// Watched-state precedence compares at most four container timestamps
		// and one explicit intent. Reserve those source scalars before the
		// shared production expression evaluates them, including rejected ones.
		var textBytes int64
		const parents = `SELECT 'season' AS kind,season_id AS id FROM catalog_episodes WHERE entity_id=?2
 UNION ALL SELECT 'show',show_id FROM catalog_episodes WHERE entity_id=?2
 UNION ALL SELECT 'album',album_id FROM catalog_songs WHERE entity_id=?2
 UNION ALL SELECT 'book',book_id FROM catalog_book_files WHERE entity_id=?2`
		if err := s.read().QueryRow(`SELECT COALESCE((SELECT sum(length(CAST(c.watermark AS BLOB))+length(CAST(c.cleared_through AS BLOB))) FROM (`+parents+`) parent JOIN container_personal_state c ON c.kind=parent.kind AND c.container_id=parent.id AND c.profile_id=?1),0)+COALESCE((SELECT length(CAST(authored_at AS BLOB)) FROM personal_watched_intents WHERE profile_id=?1 AND item_id=?2),0)`, profile, r.item).Scan(&textBytes); err != nil {
			return false, err
		}
		if err := reserve(textBytes); err != nil {
			return false, err
		}
		var favorite, watchlisted, hidden, progress, watched int64
		var rating sql.NullFloat64
		err := s.read().QueryRow(`SELECT COALESCE(p.favorite,0),COALESCE(p.watchlisted,0),COALESCE(p.not_interested,0),p.rating,EXISTS(SELECT 1 FROM progress WHERE profile_id=?1 AND item_id=?2 AND position>0),COALESCE(`+personalstate.SQL("?1", "?2")+`,0) FROM (SELECT 1) LEFT JOIN personal_items p ON p.profile_id=?1 AND p.item_id=?2`, profile, r.item).Scan(&favorite, &watchlisted, &hidden, &rating, &progress, &watched)
		if err != nil {
			return false, err
		}
		r.favorite = max(r.favorite, favorite)
		r.watchlisted = max(r.watchlisted, watchlisted)
		r.notInterested = max(r.notInterested, hidden)
		r.progress = max(r.progress, progress)
		if rating.Valid {
			r.ratingSum += rating.Float64
			r.ratingCount++
		}
		r.memberDone = watched == 1
		r.afterHistory = 0
		r.stage = 3
		// Both are singleton bounded scalars. Keep the original textual MAX
		// (including malformed timestamps); parse only once at result().
		for _, q := range []string{`SELECT last_played_at AS v FROM personal_items WHERE profile_id=? AND item_id=? AND last_played_at!=''`, `SELECT authored_at AS v FROM personal_watched_intents WHERE profile_id=? AND item_id=? AND watched=1`} {
			at, ok, err := s.smallOrderText(q, []any{profile, r.item}, reserve)
			if err != nil {
				return false, err
			}
			if ok && at > r.at {
				r.at = at
			}
		}
		return false, nil
	case 3:
		var row int64
		err := s.read().QueryRow(`SELECT rowid FROM personal_history INDEXED BY personal_history_item_id_fk WHERE item_id=? AND rowid>? ORDER BY rowid LIMIT 1`, r.item, r.afterHistory).Scan(&row)
		if errors.Is(err, sql.ErrNoRows) {
			if r.memberDone {
				r.finished++
			}
			r.stage = 1
			return false, nil
		}
		if err != nil {
			return false, err
		}
		r.afterHistory = row
		var owner bool
		var completed int
		var ownerBytes int64
		if err = s.read().QueryRow(`SELECT length(CAST(profile_id AS BLOB)) FROM personal_history WHERE rowid=?`, row).Scan(&ownerBytes); err != nil {
			return false, err
		}
		if err = reserve(ownerBytes); err != nil {
			return false, err
		}
		err = s.read().QueryRow(`SELECT profile_id=?,completed FROM personal_history WHERE rowid=?`, profile, row).Scan(&owner, &completed)
		if err != nil {
			return false, err
		}
		if owner && completed == 1 {
			r.completions++
			r.memberDone = true
			at, ok, err := s.smallOrderText(`SELECT updated_at AS v FROM personal_history WHERE rowid=?`, []any{row}, reserve)
			if err != nil {
				return false, err
			}
			if ok && at > r.at {
				r.at = at
			}
		}
		return false, nil
	}
	return false, ErrSelectionBatchIncomplete
}
