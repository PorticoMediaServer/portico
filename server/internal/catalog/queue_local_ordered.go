package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// LocalSearchQueueSupported is shared by the capability projection and the
// replay guard. Random/relative/Home/container exclusions remain unchanged.
func LocalSearchQueueSupported(node *BrowseNode, sorts []BrowseSortSelection) bool {
	if !browseQueryReplayable(node, sorts) || len(sorts) != 1 {
		return false
	}
	if sorts[0].Field == "groupOrder" {
		target := groupOrderTarget(node)
		return target != nil && target.Field != "homeRow"
	}
	_, ok := localQueueFactIndex(sorts[0].Field)
	d, found := browseSortByID(sorts[0].Field)
	return found && sorts[0].Seed == 0 && (ok || d.Profile && (d.ID == "personalRating" || d.ID == "lastPlayed" || d.ID == "playCount"))
}

func localQueueFactIndex(field string) (string, bool) {
	index, ok := map[string]string{
		"title": "catalog_browse_title", "added": "catalog_browse_added", "year": "catalog_browse_year",
		"duration": "catalog_browse_duration", "communityRating": "catalog_browse_rating", "aired": "catalog_browse_release_key",
		"releaseDate": "catalog_browse_release_key", "latestAired": "catalog_browse_latest_aired_key", "criticRating": "catalog_browse_critic_key",
		"episodeCount": "catalog_browse_episode_count", "album": "catalog_browse_album_key", "artist": "catalog_browse_artist_key",
		"trackNumber": "catalog_browse_track_key", "fileSize": "catalog_browse_file_size", "resolution": "catalog_browse_resolution_key",
	}[field]
	return index, ok
}

// Only bounded intent lives on the caller. Raw cursors and candidate reducer
// state live in the shared store; a delivered continuation stays delivery-leased.
// No cursor is durable and losing a handle restarts from accepted command keys.
type QueueLocalOrderedIterator struct {
	service      *Service
	spec         []byte
	caller       readprep.Caller
	handle       readprep.Handle
	delivery     readprep.Result
	cursor       localQueueCursor
	eof          bool
	sharedHandle bool
	expiry       time.Time
}

// QueueKeyIterator delivers bounded ordered windows with leased memory. A caller
// must validate basis/authority in each fresh accepting read and final writer.
type QueueKeyIterator interface {
	Next(context.Context) (QueueLocalOrderedBatch, error)
	Close()
	Pause()
	ReserveDelivery(int64) error
	CheckBasis(context.Context, *sql.Tx) error
}

type QueueLocalOrderedBatch struct {
	IDs   []string
	EOF   bool
	Ready bool
}

type localQueueSpec struct {
	Query  QueueQuery
	Viewer Viewer
	Basis  string
	Cursor localQueueCursor
	Limit  int
}

type localQueueKey struct {
	Type    int // 0 NULL, 1 integer, 2 real, 3 text
	Integer int64
	Real    float64
	Text    []byte
}

func (k localQueueKey) value() any {
	switch k.Type {
	case 1:
		return k.Integer
	case 2:
		return k.Real
	case 3:
		return string(k.Text)
	}
	return nil
}
func (k *localQueueKey) set(v any) {
	*k = localQueueKey{}
	switch n := v.(type) {
	case int64:
		k.Type = 1
		k.Integer = n
	case float64:
		k.Type = 2
		k.Real = n
	case string:
		k.Type = 3
		k.Text = []byte(n)
	case []byte:
		k.Type = 3
		k.Text = append([]byte{}, n...)
	}
}

type localQueuePosition struct {
	Set bool
	Key localQueueKey
	ID  int64
}
type localQueueCursor struct {
	Phase             int
	Current, Previous localQueuePosition
	Group             *GroupOrderWindowCursor
}
type localQueueOutput struct {
	IDs    []string
	EOF    bool
	Cursor localQueueCursor
}

func (s *Service) localOrderedQueue(ctx context.Context, tx *sql.Tx, viewer Viewer, query *QueueQuery, node *BrowseNode, pivot browsePivot, sorts []BrowseSortSelection, prefix, label, libraryName string) (QueueSelection, error) {
	if query.SelectionBasis == "" {
		return QueueSelection{}, ErrSelectionBatchUnsupported
	}
	if !selectionLeafPivot(pivot) || len(entityKindMembers(pivot)) != 1 || s.ReadPreparations() == nil {
		return QueueSelection{}, ErrSelectionBatchUnsupported
	}
	if err := selectionBatchSearchQualified(node); err != nil {
		return QueueSelection{}, err
	}
	bound, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return QueueSelection{}, err
	}
	read := s.WithContext(bound)
	basis, err := read.browseSelectionWitness(query.LibraryID, viewer.Profile, viewer.EffectiveRestrictions())
	if err != nil {
		return QueueSelection{}, err
	}
	if query.SelectionBasis != "" && query.SelectionBasis != basis {
		return QueueSelection{}, ErrStaleContinuation
	}
	revision, err := s.browseRevision(BrowseRequest{Library: query.LibraryID, Profile: viewer.Profile, ViewerFence: viewer.Fence, Restrictions: viewer.EffectiveRestrictions(), Query: node}, pivot, sorts)
	if err != nil {
		return QueueSelection{}, err
	}
	copy := *query
	copy.Query = node
	copy.Sort = sorts
	copy.Pivot = pivot.ID
	copy.SelectionBasis = basis
	spec, err := json.Marshal(localQueueSpec{Query: copy, Viewer: viewer, Basis: basis})
	if err != nil {
		return QueueSelection{}, err
	}
	root := *s
	root.readTx = nil
	root.rctx = nil
	root.recSources = nil
	root.recComposition = nil
	root.recRestrictions = Viewer{}.Restrictions
	it := &QueueLocalOrderedIterator{service: &root, spec: spec, caller: readprep.Caller("queue-local:" + operationHash([]any{string(spec)}))}
	if label == "" {
		label = libraryName
	}
	return QueueSelection{Iterator: it, Revision: prefix + revision + ":" + basis, Label: label, CheckBasis: s.selectionBasisCheck(viewer, query.LibraryID, basis)}, nil
}

func (it *QueueLocalOrderedIterator) Close() {
	if it == nil {
		return
	}
	if it.handle != "" && !it.sharedHandle {
		_ = it.service.ReadPreparations().Cancel(context.Background(), it.caller, it.handle)
		it.handle = ""
	}
	it.delivery.Release()
	it.delivery = readprep.Result{}
	it.cursor = localQueueCursor{}
	it.handle = ""
}

// Next never runs a cold walk on its caller. Preparing is observable even when
// every raw row rejects. Call again later; every completed window is <=128 IDs.
// IDs and cursor are valid until the next call or Close.
func (it *QueueLocalOrderedIterator) Next(ctx context.Context) (QueueLocalOrderedBatch, error) {
	if err := ctx.Err(); err != nil {
		return QueueLocalOrderedBatch{}, err
	}
	if it.eof {
		return QueueLocalOrderedBatch{Ready: true, EOF: true}, nil
	}
	store := it.service.ReadPreparations()
	if it.handle == "" {
		if err := it.delivery.ResizeDelivery(int64(8*len(it.spec) + 4*len(it.cursor.Current.Key.Text) + 4*len(it.cursor.Previous.Key.Text) + 16*it.cursor.Group.Bytes() + 32768)); err != nil && it.delivery.Key.Namespace != "" {
			return QueueLocalOrderedBatch{}, err
		}
		var input localQueueSpec
		if err := json.Unmarshal(it.spec, &input); err != nil {
			return QueueLocalOrderedBatch{}, err
		}
		input.Cursor = it.cursor
		first := input.Cursor.Phase == 0 && !input.Cursor.Current.Set && !input.Cursor.Previous.Set && input.Cursor.Group == nil
		input.Limit = 128
		if first {
			input.Limit = 1
		}
		spec, err := json.Marshal(input)
		if err != nil {
			return QueueLocalOrderedBatch{}, err
		}
		root := it.service
		factory := int64(8*cap(spec)+8*len(it.spec)) + int64(unsafe.Sizeof(*root)) + 8192
		plan := readprep.Plan{ReuseHandle: first, ColdSearch: true, ShouldYield: dbwork.ColdReadShouldYield, InitialBytes: factory, New: func(ctx context.Context, b *readprep.Budget) (readprep.Computation, error) {
			if err := b.Resize(factory + int64(8*len(spec)) + 2*SelectionBatchMaximumBytes); err != nil {
				return nil, err
			}
			var in localQueueSpec
			if err := json.Unmarshal(spec, &in); err != nil {
				return nil, err
			}
			c := &localQueueComputation{service: root, input: in, factory: factory}
			err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassMaintenance), root.db, func(inner context.Context) error {
				read := root.WithContext(inner)
				current, err := c.witness(read)
				if err != nil {
					return err
				}
				if current != in.Basis {
					return ErrSelectionBatchStale
				}
				bound := BoundBrowseQuery{Library: in.Query.LibraryID, Pivot: in.Query.Pivot, Query: in.Query.Query, Sorts: in.Query.Sort, snapshot: dbwork.Snapshot(inner)}
				c.template, err = read.NewBrowseSelectionBatch(BrowseRequest{}, in.Viewer, bound, in.Basis)
				return err
			})
			if err != nil {
				return nil, selectionPreparationError(err)
			}
			if in.Query.Sort[0].Field == "groupOrder" {
				c.group, err = NewGroupOrderWindow(c.template, groupOrderTarget(in.Query.Query), in.Query.Sort[0].Direction, in.Limit, in.Cursor.Group)
				if err != nil {
					return nil, selectionPreparationError(err)
				}
			}
			return c, nil
		}}
		h, err := store.SubmitUntil(ctx, it.caller, readprep.Key{Namespace: "queue-local-ordered-" + sqlite.SearchMatchVersion, Basis: input.Basis, Canonical: string(spec)}, plan, it.expiry)
		if err != nil {
			return QueueLocalOrderedBatch{}, err
		}
		it.handle = h
		it.sharedHandle = first
		it.delivery.Release()
		it.delivery = readprep.Result{}
		it.cursor = localQueueCursor{}
	}
	result, err := store.Poll(ctx, it.caller, it.handle)
	if err != nil {
		return QueueLocalOrderedBatch{}, err
	}
	if result.State != readprep.Ready {
		result.Release()
		return QueueLocalOrderedBatch{}, nil
	}
	if err = result.ResizeDelivery(int64(4*len(result.Payload) + len(it.spec) + 32768)); err != nil {
		result.Release()
		return QueueLocalOrderedBatch{}, err
	}
	var output localQueueOutput
	if err = json.Unmarshal(result.Payload, &output); err != nil {
		result.Release()
		return QueueLocalOrderedBatch{}, err
	}
	if err = dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassMaintenance), it.service.db, func(inner context.Context) error { return it.CheckBasis(inner, dbwork.Snapshot(inner)) }); err != nil {
		result.Release()
		return QueueLocalOrderedBatch{}, selectionPreparationError(err)
	}
	it.expiry = result.OriginalExpiry
	if len(output.IDs) > 128 {
		result.Release()
		return QueueLocalOrderedBatch{}, readprep.ErrInvalid
	}
	if !it.sharedHandle {
		_ = store.Cancel(context.WithoutCancel(ctx), it.caller, it.handle)
	}
	it.handle = ""
	it.delivery = result
	it.cursor = output.Cursor
	it.eof = output.EOF
	return QueueLocalOrderedBatch{IDs: output.IDs, EOF: output.EOF, Ready: true}, nil
}

type localQueueHead struct {
	ID      int64
	Key     localQueueKey
	Present bool
}
type localQueueComputation struct {
	service             *Service
	input               localQueueSpec
	template, candidate *BrowseSelectionBatchState
	heads               [2]localQueueHead
	stage, source       int
	ids                 [128]string
	count               int
	eof                 bool
	factory             int64
	payload             []byte
	group               *GroupOrderWindow
}

func (c *localQueueComputation) AccountedBytes() int64 {
	n := c.factory + int64(unsafe.Sizeof(*c)) + int64(c.template.Bytes()+c.candidate.Bytes()+cap(c.payload)+c.group.Bytes()+c.input.Cursor.Group.Bytes()) + 32768
	n += int64(8 * (len(c.input.Cursor.Current.Key.Text) + len(c.input.Cursor.Previous.Key.Text) + len(c.heads[0].Key.Text) + len(c.heads[1].Key.Text)))
	for _, id := range c.ids {
		n += int64(len(id))
	}
	return n
}
func (c *localQueueComputation) witness(read *Service) (string, error) {
	return read.browseSelectionWitness(c.input.Query.LibraryID, c.input.Viewer.Profile, c.input.Viewer.EffectiveRestrictions())
}
func (c *localQueueComputation) reserve(b *readprep.Budget, extra int64) error {
	return b.Resize(c.AccountedBytes() + int64(c.template.Bytes()+c.candidate.Bytes()) + max(int64(SelectionBatchMaximumBytes), extra))
}
func (c *localQueueComputation) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	if err := c.reserve(b, 0); err != nil {
		return readprep.StepResult{}, err
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	if c.group != nil {
		return c.stepLocalGroup(ctx, b)
	}
	if c.candidate != nil {
		next, work, err := c.service.StepBrowseSelectionBatch(ctx, c.candidate, c.witness, SelectionBatchLimits{ReserveTransient: func(n int64) error { return c.reserve(b, n) }})
		if err != nil {
			return readprep.StepResult{}, selectionPreparationError(err)
		}
		c.candidate = next
		if !next.Complete() {
			return readprep.StepResult{}, nil
		}
		c.candidate = nil
		if work.MatchCount > 0 {
			err = dbwork.WithReadSnapshot(ctx, c.service.db, func(inner context.Context) error {
				read := c.service.WithContext(inner)
				w, err := c.witness(read)
				if err != nil {
					return err
				}
				if w != c.input.Basis {
					return ErrSelectionBatchStale
				}
				var id string
				err = read.read().QueryRow(`SELECT pid(i.public_id) FROM catalog_entities i JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1 WHERE i.id=? AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id)`, work.Matches[0]).Scan(&id)
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				if err != nil {
					return err
				}
				c.ids[c.count] = id
				c.count++
				return nil
			})
			if err != nil {
				return readprep.StepResult{}, selectionPreparationError(err)
			}
		}
	} else if !c.eof {
		// Exactly one raw probe/ownership unit per step. Rejected and EOF probes
		// yield through the same shared scheduler as a matching candidate.
		err := dbwork.WithReadSnapshot(ctx, c.service.db, func(inner context.Context) error {
			read := c.service.WithContext(inner)
			w, err := c.witness(read)
			if err != nil {
				return err
			}
			if w != c.input.Basis {
				return ErrSelectionBatchStale
			}
			return c.rawUnit(inner, read, b)
		})
		if err != nil {
			return readprep.StepResult{}, selectionPreparationError(err)
		}
	}
	if !c.eof && c.count < c.input.Limit {
		return readprep.StepResult{}, nil
	}
	if err := c.reserve(b, int64(12*(len(c.input.Cursor.Current.Key.Text)+len(c.input.Cursor.Previous.Key.Text))+65536)); err != nil {
		return readprep.StepResult{}, err
	}
	output := localQueueOutput{IDs: c.ids[:c.count], EOF: c.eof, Cursor: c.input.Cursor}
	var err error
	c.payload, err = json.Marshal(output)
	if err != nil {
		return readprep.StepResult{}, err
	}
	return readprep.StepResult{Done: true, Payload: c.payload}, nil
}

func (c *localQueueComputation) phase() (personal, rest, desc bool, expression, index string) {
	sort := c.input.Query.Sort[0]
	desc = sort.Direction == "desc"
	definition, _ := browseSortByID(sort.Field)
	if definition.Profile {
		valued := c.input.Cursor.Phase == 1
		if desc {
			valued = !valued
		}
		if valued {
			column, _, ix := personalSortColumn(sort.Field)
			return true, false, desc, "e." + column, ix
		}
		return false, true, false, "e.sort_key COLLATE NOCASE", "catalog_browse_title"
	}
	index, _ = localQueueFactIndex(sort.Field)
	return false, false, desc, definition.Expression, index
}
func (c *localQueueComputation) rawUnit(ctx context.Context, read *Service, b *readprep.Budget) error {
	personal, rest, desc, expression, index := c.phase()
	if c.stage < 2 {
		source := c.stage
		c.stage++
		if personal && source == 1 {
			c.heads[source] = localQueueHead{}
			return nil
		}
		pos := c.input.Cursor.Current
		if source == 1 {
			pos = c.input.Cursor.Previous
		}
		head, err := c.probe(ctx, b, source, pos, personal, desc, expression, index)
		if err != nil {
			return err
		}
		c.heads[source] = head
		return nil
	}
	if c.stage == 2 {
		a, d := c.heads[0], c.heads[1]
		if a.ID == 0 && d.ID == 0 {
			definition, _ := browseSortByID(c.input.Query.Sort[0].Field)
			if definition.Profile && c.input.Cursor.Phase == 0 {
				c.input.Cursor = localQueueCursor{Phase: 1}
				c.stage = 0
				return nil
			}
			c.eof = true
			return nil
		}
		c.source = 0
		if a.ID == 0 {
			c.source = 1
		} else if d.ID != 0 {
			var previousFirst bool
			op := "<"
			if desc {
				op = ">"
			}
			// SQLite itself compares keys, including NOCASE, numeric affinity and ties.
			collate := ""
			if strings.Contains(expression, "COLLATE NOCASE") {
				collate = " COLLATE NOCASE"
			}
			err := dbwork.Snapshot(ctx).QueryRowContext(ctx, `SELECT CASE WHEN (? IS NULL)<>(? IS NULL) THEN (? IS NULL) ELSE (COALESCE(?`+collate+op+`?,0) OR ?`+collate+` IS ? AND ?`+op+`?) END`, d.Key.value(), a.Key.value(), func() any {
				if desc {
					return a.Key.value()
				}
				return d.Key.value()
			}(), d.Key.value(), a.Key.value(), d.Key.value(), a.Key.value(), d.ID, a.ID).Scan(&previousFirst)
			if err != nil {
				return err
			}
			if previousFirst {
				c.source = 1
			}
		}
		h := c.heads[c.source]
		pos := localQueuePosition{Set: true, Key: h.Key, ID: h.ID}
		if c.source == 0 {
			c.input.Cursor.Current = pos
		} else {
			c.input.Cursor.Previous = pos
		}
		c.stage = 3
		return nil
	}
	h := c.heads[c.source]
	c.stage = 0
	// Current keys shadowed by ANY retained row are absent, even retained
	// tombstones or rows whose maintained key moved to another position.
	if !personal && c.source == 0 {
		var shadow bool
		if err := dbwork.Snapshot(ctx).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_browse_rows_previous WHERE generation=(SELECT generation FROM catalog_generations WHERE singleton=1 AND retaining=1) AND entity_id=?)`, h.ID).Scan(&shadow); err != nil {
			return err
		}
		if shadow {
			return nil
		}
	}
	if !h.Present {
		return nil
	}
	if rest {
		column, zero, _ := personalSortColumn(c.input.Query.Sort[0].Field)
		var valued bool
		if err := read.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_personal_sort_rows e WHERE profile_id=? AND entity_id=? AND `+column+`>`+zero+`)`, c.input.Viewer.Profile, h.ID).Scan(&valued); err != nil {
			return err
		}
		if valued {
			return nil
		}
	}
	c.candidate = c.template.Candidate(h.ID)
	return nil
}

// probe issues two disjoint index seeks, never an OR postfilter. LIMIT 1
// precedes ownership, visibility and the query predicate. The length probe
// reserves the retained key and SQL/driver copies before decoding it.
func (c *localQueueComputation) probe(ctx context.Context, b *readprep.Budget, source int, pos localQueuePosition, personal, desc bool, expression, index string) (localQueueHead, error) {
	table := "catalog_browse_rows"
	scope := `e.library_id=? AND e.kind=?`
	args := []any{c.template.plan.libraryID, c.template.plan.kinds[0]}
	present := "1"
	if personal {
		table = "catalog_personal_sort_rows"
		column, zero, _ := personalSortColumn(c.input.Query.Sort[0].Field)
		scope = `e.profile_id=? AND ` + scope + ` AND e.` + column + `>` + zero
		args = append([]any{c.input.Viewer.Profile}, args...)
	}
	if source == 1 {
		table += "_previous"
		index += "_previous"
		scope = `e.generation=(SELECT generation FROM catalog_generations WHERE singleton=1 AND retaining=1) AND ` + scope
		present = "e.present"
		if index == "catalog_browse_title_previous" || index == "catalog_browse_added_previous" || index == "catalog_browse_year_previous" || index == "catalog_browse_duration_previous" || index == "catalog_browse_rating_previous" {
			scope += ` AND e.present=1`
		}
	}
	direction, op := "ASC", ">"
	if desc {
		direction, op = "DESC", "<"
	}
	base := ` FROM ` + table + ` e INDEXED BY ` + index + ` WHERE ` + scope
	seek := func(where, order string, values []any) (localQueueHead, error) {
		var h localQueueHead
		var bytes int
		err := dbwork.Snapshot(ctx).QueryRowContext(ctx, `SELECT e.entity_id,COALESCE(length(CAST(`+expression+` AS BLOB)),0),`+present+base+where+` ORDER BY `+order+` LIMIT 1`, append(append([]any{}, args...), values...)...).Scan(&h.ID, &bytes, &h.Present)
		if errors.Is(err, sql.ErrNoRows) {
			return h, nil
		}
		if err != nil {
			return h, err
		}
		if err = c.reserve(b, int64(bytes)*8+8192); err != nil {
			return h, err
		}
		var value any
		err = dbwork.Snapshot(ctx).QueryRowContext(ctx, `SELECT `+expression+` FROM `+table+` e WHERE `+func() string {
			if personal {
				return "e.profile_id=? AND "
			}
			if source == 1 {
				return "e.generation=(SELECT generation FROM catalog_generations WHERE singleton=1 AND retaining=1) AND "
			}
			return ""
		}()+`e.entity_id=?`, func() []any {
			if personal {
				return []any{c.input.Viewer.Profile, h.ID}
			}
			return []any{h.ID}
		}()...).Scan(&value)
		h.Key.set(value)
		return h, err
	}
	if !pos.Set {
		return seek("", expression+` `+direction+`,e.entity_id `+direction, nil)
	}
	h, err := seek(` AND `+expression+` IS ? AND e.entity_id`+op+`?`, `e.entity_id `+direction, []any{pos.Key.value(), pos.ID})
	if err != nil || h.ID != 0 {
		return h, err
	}
	if pos.Key.Type == 0 {
		if desc {
			return localQueueHead{}, nil
		}
		return seek(` AND `+expression+` IS NOT NULL`, expression+` `+direction+`,e.entity_id `+direction, nil)
	}
	h, err = seek(` AND `+expression+op+`?`, expression+` `+direction+`,e.entity_id `+direction, []any{pos.Key.value()})
	if err != nil || h.ID != 0 || !desc {
		return h, err
	}
	return seek(` AND `+expression+` IS NULL`, `e.entity_id `+direction, nil)
}

// Pause releases delivery buffers but preserves first-window demand for retry.
// Its private handle remains bounded/expired by the shared store, not a registry.
func (it *QueueLocalOrderedIterator) Pause() {
	if it == nil {
		return
	}
	it.delivery.Release()
	it.delivery = readprep.Result{}
	it.cursor = localQueueCursor{}
	it.handle = ""
}
func (it *QueueLocalOrderedIterator) ReserveDelivery(bytes int64) error {
	return it.delivery.ResizeDelivery(bytes + int64(8*len(it.delivery.Payload)+8*len(it.spec)) + 32768)
}
func (it *QueueLocalOrderedIterator) CheckBasis(ctx context.Context, tx *sql.Tx) error {
	var input localQueueSpec
	if err := json.Unmarshal(it.spec, &input); err != nil {
		return err
	}
	return it.service.selectionBasisCheck(input.Viewer, input.Query.LibraryID, input.Basis)(ctx, tx)
}
