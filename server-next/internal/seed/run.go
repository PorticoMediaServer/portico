package seed

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Totals is what an import run did, and how fast.
type Totals struct {
	Batches      int           `json:"batches"`
	Files        int           `json:"files"`
	FilesNew     int           `json:"filesNew"`
	FilesChanged int           `json:"filesChanged"`
	Items        int           `json:"items"`
	ItemsNew     int           `json:"itemsNew"`
	RowsWritten  int64         `json:"rowsWritten"`
	Elapsed      time.Duration `json:"elapsedNs"`
	FilesPerSec  float64       `json:"filesPerSecond"`
}

func (t *Totals) add(s db.ImportStats, elapsed time.Duration) {
	t.Batches++
	t.Files += s.Files
	t.FilesNew += s.FilesNew
	t.FilesChanged += s.FilesChanged
	t.Items += s.Items
	t.ItemsNew += s.ItemsNew
	t.RowsWritten += s.RowsWritten
	t.Elapsed = elapsed
	t.FilesPerSec = float64(t.Files) / elapsed.Seconds()
}

// Import creates the libraries if they are not there and imports the whole synthetic library.
// Run again on the same database it is an unchanged rescan: every file is looked at and
// nothing is written. progress, if not nil, is called after every batch.
func Import(ctx context.Context, d *db.DB, cfg Config, progress func(Totals)) (Totals, error) {
	var totals Totals
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		return totals, err
	}
	if len(libraries) == 0 {
		for _, spec := range Libraries {
			lib, err := d.CreateLibrary(ctx, spec.Name, spec.Kind, "en", []string{spec.Path})
			if err != nil {
				return totals, err
			}
			libraries = append(libraries, lib)
		}
	}
	start := time.Now()
	for _, lib := range libraries {
		err := Batches(cfg, lib, func(batch db.ImportBatch) error {
			stats, err := d.Import(ctx, batch)
			totals.add(stats, time.Since(start))
			if progress != nil {
				progress(totals)
			}
			return err
		})
		if err != nil {
			return totals, fmt.Errorf("library %s: %w", lib.Name, err)
		}
	}
	return totals, nil
}

// Profiles creates n members with one profile each. Most see everything. Three are restricted
// in the three ways a profile can be: by rating, by library and by a blocked label.
func Profiles(ctx context.Context, d *db.DB, n int) ([]db.Viewer, error) {
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		return nil, err
	}
	labels, err := d.Tags(ctx, db.TagLabel)
	if err != nil {
		return nil, err
	}
	var viewers []db.Viewer
	for i := range n {
		p := db.SeedProfile{Name: fmt.Sprint("viewer-", i)}
		switch {
		case i == n-1 && n > 1:
			age := 13
			p.MaxRatingAge = &age
		case i == n-2 && n > 2:
			for _, lib := range libraries {
				if lib.Kind == db.LibraryMovies || lib.Kind == db.LibraryMusic {
					p.Libraries = append(p.Libraries, lib.ID)
				}
			}
		case i == n-3 && n > 3 && len(labels) > 0:
			p.BlockedTags = []string{labels[0].PublicID}
		}
		v, err := d.CreateSeedProfile(ctx, p)
		if err != nil {
			return nil, err
		}
		viewers = append(viewers, v)
	}
	return viewers, nil
}

// LeafKind is the playable kind a library of this kind holds.
func LeafKind(libraryKind string) db.ItemKind {
	switch libraryKind {
	case db.LibraryShows:
		return db.KindEpisode
	case db.LibraryMusic:
		return db.KindTrack
	case db.LibraryBooks:
		return db.KindBook
	}
	return db.KindMovie
}

// State gives each profile about target state rows spread over the library: mostly watched,
// some part-way through, some favourites and some on My List. It reads the lists once, the
// way an app does, and writes through the same state path as background work. A restricted
// profile ends with fewer rows, because an edit to a title it may not see is refused.
func State(ctx context.Context, d *db.DB, viewers []db.Viewer, target int, seed uint64) (int, error) {
	if len(viewers) == 0 {
		return 0, nil
	}
	all := viewers[0]
	libraries, err := d.Libraries(ctx, all)
	if err != nil {
		return 0, err
	}
	leaves := 0
	for _, lib := range libraries {
		page, err := d.Items(ctx, all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{LeafKind(lib.Kind)}, Limit: 1, WithTotal: true})
		if err != nil {
			return 0, err
		}
		leaves += page.Total
	}
	if leaves == 0 {
		return 0, nil
	}
	chance := float64(target) / float64(leaves)
	random := make([]*rand.Rand, len(viewers))
	pending := make([][]db.ItemEdit, len(viewers))
	for n, v := range viewers {
		random[n] = rand.New(rand.NewPCG(seed, uint64(v.ProfileID)))
	}
	written := 0
	flush := func(n int) error {
		_, refusals, err := d.SetStates(ctx, db.Background, viewers[n], pending[n])
		for _, refusal := range refusals {
			if refusal == nil {
				written++
			} else if !errors.Is(refusal, db.ErrNotFound) && err == nil {
				err = refusal
			}
		}
		pending[n] = pending[n][:0]
		return err
	}
	yes := true
	for _, lib := range libraries {
		query := db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{LeafKind(lib.Kind)}, Limit: 200}
		for {
			page, err := d.Items(ctx, all, query)
			if err != nil {
				return written, err
			}
			for _, item := range page.Items {
				for n, r := range random {
					if r.Float64() >= chance {
						continue
					}
					edit := db.ItemEdit{ItemID: item.PublicID}
					switch pick := r.IntN(10); {
					case pick < 6:
						edit.Watched = &yes
					case pick < 8 && item.DurationMS > 0:
						position := item.DurationMS / 10 * int64(1+r.IntN(8))
						edit.PositionMS = &position
					case pick < 9:
						edit.Favorite = &yes
					default:
						edit.InList = &yes
					}
					if pending[n] = append(pending[n], edit); len(pending[n]) >= 400 {
						if err := flush(n); err != nil {
							return written, err
						}
					}
				}
			}
			if page.NextCursor == "" {
				break
			}
			query.Cursor = page.NextCursor
		}
	}
	for n := range viewers {
		if err := flush(n); err != nil {
			return written, err
		}
	}
	return written, nil
}
