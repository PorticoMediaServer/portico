package read

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"time"
)

type RSReadingReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
	mobi      bool
}

func NewRSReadingReader(s *governor.Snapshots) (*RSReadingReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &RSReadingReader{snapshots: s, pool: s}, nil
}

// Mobi configuration is private trusted composition, never a request option.
// The phase4 adapter must derive it from the existing configured converter.
func newRSReadingReaderWithMobi(s *governor.Snapshots) (*RSReadingReader, error) {
	r, err := NewRSReadingReader(s)
	if err == nil {
		r.mobi = true
	}
	return r, err
}

type ReadingFactsInput struct {
	View BrowseInput
	Item int64
}
type ReadingAttribution struct{ Value, Key, Source *string }
type ReadingFacts struct {
	ID                                                                 int64
	PublicID                                                           [16]byte
	Title                                                              string
	Year                                                               int64
	AddedAt                                                            *string
	Available                                                          bool
	PublicationType, PublicationTypeSource, IssueDate, IssueDateSource *string
	Author, Series                                                     ReadingAttribution
	SeriesID                                                           *string
	NumberedOrder                                                      *int64
	NumberValue                                                        *float64
}

// Facts returns Reading metadata and inventory availability only. Primary source
// admission, conversion/opening, physical paths and Reading locations remain
// owned by the existing source/intent flow. Unavailable accepted titles remain
// eligible. Every accepted nullable attribution keeps its absence semantics.
func (r *RSReadingReader) Facts(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ReadingFactsInput) (ReadingFacts, BrowseReadResult, error) {
	if r == nil {
		return ReadingFacts{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (ReadingFacts, BrowseReadResult, error) {
		in.View.Now = at
		return r.facts(ctx, request, budget, output, in)
	})
}
func (r *RSReadingReader) facts(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ReadingFactsInput) (ReadingFacts, BrowseReadResult, error) {
	if r == nil || in.Item <= 0 || in.View.Kind != 20 && in.View.Kind != 21 || in.View.After != nil {
		return ReadingFacts{}, BrowseReadResult{}, ErrInput
	}
	in.View.policy = readPolicyBinding{recipe: rsReadingNativePolicy, subject: in.Item}
	if r.mobi {
		in.View.policy.recipe = rsReadingMobiPolicy
	}

	windows := &rsByteWindow{source: r.snapshots, preflightCalls: 768, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	var inventory *rsReadingInventory
	var pubs [][]any
	var library string
	businessDone := false
	root := func(q browseQuery) ([]any, error) {
		rows, err := q("rs.reading.source", in.Item, in.View.Library, in.View.Kind)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		if businessDone {
			businessDone = false
			if err := inventory.verify(q, in, library); err != nil {
				return nil, err
			}
		}
		return rows[0], nil
	}
	return publishRead(ctx, windows, request, budget, output, in.View, []readStep[ReadingFacts]{
		func(_ context.Context, q browseQuery, f browseFence, _ *ReadingFacts) error {
			library = f.library
			businessDone = false
			inventory = &rsReadingInventory{sources: make(map[string][]any), assets: make(map[int64][]any), states: make(map[int64][2]bool)}
			bytes, err := rsReadingOctets(q, "rs.reading.octets", in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return err
			}
			maxBytes, total := max(bytes, 1), bytes
			pubs, err = rsReadingPublications(q, in.Item, in.View.Library, library)
			if err != nil {
				return err
			}
			inventory.ids, err = q("rs.reading.source_ids", library, int64(rsReadingPublicationCap+1))
			if err != nil {
				return err
			}
			if len(inventory.ids) > rsReadingPublicationCap {
				return ErrUnqualified
			}
			for _, v := range pubs {
				bytes, err := rsReadingOctets(q, "rs.reading.asset_octets", v[0], in.View.Library)
				if err != nil {
					return err
				}
				total, err = rsReadingProspectiveBytes(total, bytes, output.Request.Need[governor.MemoryBytes]/8)
				if err != nil {
					return err
				}
				maxBytes = max(maxBytes, bytes)
			}
			for _, v := range inventory.ids {
				if len(v[0].(string)) > 256 {
					return ErrUnqualified
				}
				bytes, err := rsReadingOctets(q, "rs.reading.library_source_octets", v[0], library)
				if err != nil {
					return err
				}
				total, err = rsReadingProspectiveBytes(total, bytes, output.Request.Need[governor.MemoryBytes]/8)
				if err != nil {
					return err
				}
				maxBytes = max(maxBytes, bytes)
			}
			if int64(len(pubs)+len(inventory.ids))*256 > output.Request.Need[governor.MemoryBytes]/8 {
				return governor.ErrSnapshotBudget
			}
			return windows.arm(maxBytes, 512)
		},
		func(_ context.Context, q browseQuery, _ browseFence, p *ReadingFacts) error {
			gone, err := q("rs.reading.retired", in.Item)
			if err != nil {
				return err
			}
			inventory.retired = len(gone) > 0
			for _, idrow := range inventory.ids {
				id := idrow[0].(string)
				rows, err := q("rs.reading.library_source", id, library)
				if err != nil {
					return err
				}
				if len(rows) != 1 {
					return ErrStale
				}
				inventory.sources[id] = rows[0]
			}
			for _, v := range pubs {
				id := v[0].(int64)
				rows, err := q("rs.reading.asset", id, in.View.Library)
				if err != nil {
					return err
				}
				if len(rows) != 1 {
					return ErrStale
				}
				inventory.assets[id] = rows[0]
				available, err := inventory.available(q, id, library)
				if err != nil {
					return err
				}
				if rsReadingFormat(in.View.Kind, v[1].(string), v[2].(int64) != 0, r.mobi) && available {
					p.Available = true
				}
			}
			rows, err := q("rs.reading.facts", in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return err
			}
			if len(rows) != 1 {
				return ErrStale
			}
			v := rows[0]
			p.ID = v[0].(int64)
			id, ok := v[1].([]byte)
			if !ok || len(id) != 16 {
				return ErrUnqualified
			}
			copy(p.PublicID[:], id)
			p.Title = v[2].(string)
			p.Year = v[3].(int64)
			text := func(n int) *string {
				if v[n] == nil {
					return nil
				}
				s := v[n].(string)
				return &s
			}
			p.AddedAt = text(4)
			p.PublicationType = text(5)
			p.PublicationTypeSource = text(6)
			p.IssueDate = text(7)
			p.IssueDateSource = text(8)
			p.Author = ReadingAttribution{text(9), text(10), text(11)}
			p.Series = ReadingAttribution{text(12), text(13), text(15)}
			p.SeriesID = text(14)
			if v[16] != nil {
				x := v[16].(int64)
				p.NumberedOrder = &x
			}
			if v[17] != nil {
				var x float64
				switch n := v[17].(type) {
				case float64:
					x = n
				case int64:
					x = float64(n)
				default:
					return ErrUnqualified
				}
				p.NumberValue = &x
			}
			businessDone = true
			return nil
		},
	}, root)
}
