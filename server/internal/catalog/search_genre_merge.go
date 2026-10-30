package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type genreComputation struct {
	runtime                   *GenreRuntime
	descriptor                genreDescriptor
	bytes                     int64
	bases                     []FacetBaseSnapshot
	initialized               bool
	frameAfter                int64
	library                   int
	cursor, high              []int64
	rows                      []genreRow
	hasGenres, earlier, later bool
	phase                     int
	artIndex, artRole         int
	art                       *genreArtMerge
	usedBackdrops, usedCovers map[int64]bool
}

func (c *genreComputation) AccountedBytes() int64 { return c.bytes }
func (c *genreComputation) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	if err := b.Resize(c.bytes); err != nil {
		return readprep.StepResult{}, err
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	if !c.initialized {
		bases, ready, err := c.resolve(ctx)
		if err != nil {
			return readprep.StepResult{}, err
		}
		if !ready {
			timer := time.NewTimer(8 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return readprep.StepResult{}, ctx.Err()
			case <-timer.C:
			}
			return readprep.StepResult{}, nil
		}
		c.bases = bases
		c.cursor = make([]int64, len(bases))
		c.high = make([]int64, len(bases))
		c.rows = make([]genreRow, 0, c.descriptor.Spec.Limit+1)
		for i, s := range bases {
			if err := dbwork.QueryRow(ctx, c.runtime.db, `SELECT COALESCE(max(id),0) FROM catalog_facet_base_values INDEXED BY catalog_search_genre_value_walk WHERE library_id=? AND field='genre'`, s.Scope.LibraryID).Scan(&c.high[i]); err != nil {
				return readprep.StepResult{}, err
			}
		}
		c.initialized = true
	}
	for _, s := range c.bases {
		if _, err := facetBaseSnapshot(ctx, dbwork.ReadHandle(ctx, c.runtime.db), s.ID, true); err != nil {
			return readprep.StepResult{}, readprep.ErrStale
		}
	}
	if c.phase == 0 {
		done, err := c.scan(ctx)
		if err != nil {
			return readprep.StepResult{}, err
		}
		if !done {
			return readprep.StepResult{}, nil
		}
		if err = c.trim(ctx); err != nil {
			return readprep.StepResult{}, err
		}
		c.phase = 1
		c.usedBackdrops = map[int64]bool{}
		c.usedCovers = map[int64]bool{}
	}
	if c.phase == 1 {
		done, err := c.prepareArt(ctx)
		if err != nil {
			return readprep.StepResult{}, err
		}
		if !done {
			return readprep.StepResult{}, nil
		}
		c.phase = 2
	}
	for _, s := range c.bases {
		if _, err := facetBaseSnapshot(ctx, dbwork.ReadHandle(ctx, c.runtime.db), s.ID, true); err != nil {
			return readprep.StepResult{}, readprep.ErrStale
		}
	}
	reserve := c.bytes + int64(65536+4*len(c.descriptor.Scopes)*4096)
	if err := b.Resize(reserve); err != nil {
		return readprep.StepResult{}, err
	}
	payload, err := json.Marshal(genreSummary{Bases: c.bases, Rows: c.rows, HasGenres: c.hasGenres, Earlier: c.earlier, Later: c.later})
	if err != nil {
		return readprep.StepResult{}, err
	}
	c.bytes = reserve
	return readprep.StepResult{Done: true, Payload: payload}, nil
}
func (c *genreComputation) resolve(ctx context.Context) ([]FacetBaseSnapshot, bool, error) {
	d := c.descriptor
	if len(d.Scopes) == 0 {
		return []FacetBaseSnapshot{}, true, nil
	}
	if len(d.Bases) == len(d.Scopes) {
		for i := 1; i < len(d.Bases); i++ {
			if d.Bases[i].Witness != d.Bases[0].Witness {
				return nil, false, readprep.ErrStale
			}
		}
		return d.Bases, true, nil
	}
	// Demand is BASE scheduling only, not query persistence. Inspect at most one
	// retained witness per quantum, and indexed exact witnesses in other classes.
	for _, scope := range d.Scopes {
		_, err := c.runtime.bases.Demand(ctx, scope)
		if err != nil && !errors.Is(err, compactcatalog.ErrBuilding) {
			return nil, false, err
		}
	}
	q := dbwork.ReadHandle(ctx, c.runtime.db)
	var currentArt int64
	if err := q.QueryRowContext(ctx, `SELECT art_epoch FROM catalog_search_genre_state WHERE singleton=1`).Scan(&currentArt); err != nil {
		return nil, false, err
	}
	// A cold request has not captured B yet. Follow the producer's current
	// artwork epoch through its quiet restart gap; an initial epoch is not a
	// lease on a now-unreachable staging image. Once resolved, c.bases pins B.
	first := d.Scopes[0]
	query := `SELECT b.id FROM catalog_facet_base_classes cl JOIN catalog_facet_bases b ON b.class_id=cl.id WHERE cl.library_id=? AND cl.field='genre' AND cl.policy=? AND cl.source=? AND b.complete=1 AND b.authority=?`
	args := []any{first.LibraryID, first.Policy, first.Source, d.Authorities[0]}
	if c.frameAfter > 0 {
		query += ` AND b.id<?`
		args = append(args, c.frameAfter)
	}
	query += ` ORDER BY b.id DESC LIMIT 1`
	var id int64
	err := q.QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		c.frameAfter = 0
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	c.frameAfter = id
	s, err := facetBaseSnapshot(ctx, q, id, true)
	if err != nil {
		return nil, false, readprep.ErrStale
	}
	var w genreWitness
	if json.Unmarshal([]byte(s.Witness), &w) != nil || w.ArtEpoch != currentArt {
		return nil, false, nil
	}
	bases := []FacetBaseSnapshot{s}
	for i, scope := range d.Scopes[1:] {
		var other int64
		err = q.QueryRowContext(ctx, `SELECT b.id FROM catalog_facet_base_classes cl JOIN catalog_facet_bases b ON b.class_id=cl.id WHERE cl.library_id=? AND cl.field='genre' AND cl.policy=? AND cl.source=? AND b.complete=1 AND b.witness=? AND b.authority=? ORDER BY b.id DESC LIMIT 1`, scope.LibraryID, scope.Policy, scope.Source, s.Witness, d.Authorities[i+1]).Scan(&other)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		b, err := facetBaseSnapshot(ctx, q, other, true)
		if err != nil {
			return nil, false, readprep.ErrStale
		}
		bases = append(bases, b)
	}
	return bases, true, nil
}
func (c *genreComputation) scan(ctx context.Context) (bool, error) {
	if c.library >= len(c.bases) {
		return true, nil
	}
	i := c.library
	s := c.bases[i]
	rows, err := dbwork.Query(ctx, c.runtime.db, `SELECT id,length(CAST(value AS BLOB)) FROM catalog_facet_base_values INDEXED BY catalog_search_genre_value_walk WHERE library_id=? AND field='genre' AND id>? AND id<=? ORDER BY id LIMIT 4096`, s.Scope.LibraryID, c.cursor[i], c.high[i])
	if err != nil {
		return false, err
	}
	// Release each raw directory cursor before additional indexed probes. A
	// bounded ID-only batch needs no label cache, even for legal huge strings.
	type raw struct{ id, width int64 }
	batch := make([]raw, 0, 4096)
	bytes := int64(0)
	started := time.Now()
	for rows.Next() {
		var r raw
		if err = rows.Scan(&r.id, &r.width); err != nil {
			rows.Close()
			return false, err
		}
		batch = append(batch, r)
		bytes += r.width
		if bytes >= 4<<20 || time.Since(started) >= 4*time.Millisecond {
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(batch) == 0 {
		c.library++
		return c.library == len(c.bases), nil
	}
	// Processing stops by time too; unprocessed raw IDs are simply sought again.
	for _, raw := range batch {
		c.cursor[i] = raw.id
		row, exists, err := genreAggregate(ctx, c.runtime.db, c.bases, i, raw.id)
		if err != nil {
			return false, err
		}
		if exists {
			c.hasGenres = true
			spec := c.descriptor.Spec
			drawable := spec.Preview == "" || spec.Preview == "backdrop" && row.Backdrops > 0 || spec.Preview == "covers" && row.Posters >= 2
			if drawable {
				accept := true
				if spec.Direction > 0 {
					cmp, err := genreCompare(ctx, c.runtime.db, row, spec.Anchor)
					if err != nil {
						return false, err
					}
					switch spec.Direction {
					case 1:
						accept = cmp > 0
						if !accept {
							c.earlier = true
						}
					case 2:
						accept = cmp < 0
						if !accept {
							c.later = true
						}
					case 3:
						accept = cmp >= 0
						if !accept {
							c.earlier = true
						}
					}
				}
				if accept {
					if err = c.retain(ctx, row); err != nil {
						return false, err
					}
				}
			}
		}
		if time.Since(started) >= 8*time.Millisecond {
			break
		}
	}
	return false, nil
}
func genreAggregate(ctx context.Context, db *sql.DB, bases []FacetBaseSnapshot, owner int, value int64) (genreRow, bool, error) {
	row := genreRow{Base: bases[owner].ID, Value: value}
	q := dbwork.ReadHandle(ctx, db)
	for i, s := range bases {
		var id, count int64
		var back, poster int
		err := q.QueryRowContext(ctx, `SELECT v.id,-r.negative_count,COALESCE(a.backdrops,0),COALESCE(a.posters,0) FROM catalog_facet_base_values v JOIN catalog_facet_base_rows r ON r.base_id=? AND r.value_id=v.id LEFT JOIN catalog_search_genre_ready a ON a.base_id=r.base_id AND a.value_id=r.value_id WHERE v.library_id=? AND v.field='genre' AND v.value=(SELECT value FROM catalog_facet_base_values WHERE id=?)`, s.ID, s.Scope.LibraryID, value).Scan(&id, &count, &back, &poster)
		if errors.Is(err, sql.ErrNoRows) {
			if i == owner {
				return row, false, nil
			}
			continue
		}
		if err != nil {
			return row, false, err
		}
		if i < owner {
			return row, false, nil
		}
		if count < 1 || row.Count > math.MaxInt64-count {
			return row, false, ErrFacetBaseInput
		}
		row.Count += count
		row.Backdrops += back
		row.Posters += poster
	}
	return row, row.Count > 0, nil
}
func genreCompare(ctx context.Context, db *sql.DB, a, b genreRow) (int, error) {
	if a.Count > b.Count {
		return -1, nil
	}
	if a.Count < b.Count {
		return 1, nil
	}
	var less, greater bool
	err := dbwork.QueryRow(ctx, db, `SELECT (a.label_order,a.value_order)<(b.label_order,b.value_order),(a.label_order,a.value_order)>(b.label_order,b.value_order) FROM catalog_facet_base_rows a JOIN catalog_facet_base_rows b ON b.base_id=? AND b.value_id=? WHERE a.base_id=? AND a.value_id=?`, b.Base, b.Value, a.Base, a.Value).Scan(&less, &greater)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, readprep.ErrStale
	}
	if err != nil {
		return 0, err
	}
	if less {
		return -1, nil
	}
	if greater {
		return 1, nil
	}
	return 0, nil
}
func (c *genreComputation) retain(ctx context.Context, row genreRow) error {
	lo, hi := 0, len(c.rows)
	for lo < hi {
		mid := (lo + hi) / 2
		cmp, err := genreCompare(ctx, c.runtime.db, row, c.rows[mid])
		if err != nil {
			return err
		}
		if cmp < 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	n := c.descriptor.Spec.Limit + 1
	if len(c.rows) == n {
		if c.descriptor.Spec.Direction == 2 {
			if lo == 0 {
				c.earlier = true
				return nil
			}
			copy(c.rows, c.rows[1:])
			c.rows = c.rows[:n-1]
			lo--
			c.earlier = true
		} else {
			if lo == n {
				c.later = true
				return nil
			}
			c.rows = c.rows[:n-1]
			c.later = true
		}
	}
	c.rows = append(c.rows, genreRow{})
	copy(c.rows[lo+1:], c.rows[lo:])
	c.rows[lo] = row
	return nil
}
func (c *genreComputation) trim(ctx context.Context) error {
	spec := c.descriptor.Spec
	if len(c.rows) > spec.Limit {
		if spec.Direction == 2 {
			c.rows = c.rows[1:]
			c.earlier = true
		} else {
			c.rows = c.rows[:spec.Limit]
			c.later = true
		}
	}
	sizes := make([]int64, len(c.rows))
	for i, row := range c.rows {
		if err := dbwork.QueryRow(ctx, c.runtime.db, `SELECT 6*(length(CAST(v.value AS BLOB))+length(CAST(t.label AS BLOB)))+4096 FROM catalog_facet_base_rows r JOIN catalog_facet_base_values v ON v.id=r.value_id JOIN catalog_facet_base_texts t ON t.id=r.text_id WHERE r.base_id=? AND r.value_id=?`, row.Base, row.Value).Scan(&sizes[i]); err != nil {
			return err
		}
	}
	used := int64(0)
	if spec.Direction == 2 {
		lo := len(c.rows)
		for lo > 0 {
			if lo < len(c.rows) && used+sizes[lo-1] > 256<<10 {
				break
			}
			lo--
			used += sizes[lo]
		}
		if lo > 0 {
			c.earlier = true
		}
		c.rows = c.rows[lo:]
	} else {
		hi := 0
		for hi < len(c.rows) {
			if hi > 0 && used+sizes[hi] > 256<<10 {
				break
			}
			used += sizes[hi]
			hi++
		}
		if hi < len(c.rows) {
			c.later = true
		}
		c.rows = c.rows[:hi]
	}
	return nil
}
