package read

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"time"
)

// IAReadingInput selects accepted source evidence, never a selected reading source.
// Book (20) and comic (21) are distinct from audiobook Book (8).
type IAReadingInput struct {
	View BrowseInput
	Item int64
}
type IAReadingPublication struct {
	Asset                                                                         int64
	AssetRevision                                                                 int64
	PublicationType, TypeFormat, TypeField, TypeObject, TypeRevision              *string
	TypeComplete                                                                  *bool
	ClassifierVersion                                                             *int64
	Format                                                                        string
	PathHead                                                                      bool
	PublisherSequence                                                             *int64
	Object, SourceRevision, Author, AuthorSource, IssueDate, Series, SeriesSource *string
	SeriesNumber                                                                  *float64
	AssetToken                                                                    string
	AssetAvailable, LinkAvailable                                                 bool
	LinkRevision                                                                  int64
	SourceID, RelativePath, ObjectRevision, ObjectState                           *string
	ObjectRetired                                                                 *int64
}
type IAReadingResult struct {
	Item         BrowseItem
	Kind         int64
	Publications []IAReadingPublication
	RawRows      int
	Exhausted    bool
}

func iaReadingString(v any) *string {
	if v == nil {
		return nil
	}
	s := v.(string)
	return &s
}
func (r *ForegroundReader) IAReadingMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAReadingInput) (IAReadingResult, BrowseReadResult, error) {
	if r == nil {
		return IAReadingResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IAReadingResult, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaReading(ctx, request, budget, output, in)
	})
}

type iaReadingScope struct {
	browseSnapshots
	current         *governor.ReadScope
	calls           int
	copied, ceiling int64
	counts          []iaReadingSnapshotCalls
	nextQueries     uint16
}

type iaReadingSnapshotCalls struct {
	at    time.Time
	calls int
}

func (s *iaReadingScope) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	before := s.calls
	queries := uint16(128)
	s.nextQueries = 0
	callback := func(scope *governor.ReadScope) error {
		s.current = scope
		defer func() { s.current = nil }()
		return fn(scope)
	}
	var result governor.SnapshotResult
	var e error
	if queries > 0 {
		pool, ok := s.browseSnapshots.(interface {
			ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
		})
		if !ok {
			return result, ErrUnqualified
		}
		result, e = pool.ReadForecasted(ctx, r, b, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: queries}, callback)
	} else {
		result, e = s.browseSnapshots.Read(ctx, r, b, callback)
	}
	s.counts = append(s.counts, iaReadingSnapshotCalls{result.Started, s.calls - before})
	return result, e
}
func (s *iaReadingScope) exact(name string, cols []int, args ...any) ([]any, error) {
	if s.current == nil {
		return nil, governor.ErrSnapshotClosed
	}
	s.calls += 2
	octets := readStatementID("foreground.ia.reading." + name + "_octets")
	if name == "link" {
		octets = iaLinkOctetsID
	}
	row, e := s.current.QueryExactBytes(octets, readStatementID("foreground.ia.reading."+name), cols, args...)
	if errors.Is(e, sql.ErrNoRows) {
		s.calls-- // The length point found no row; no payload statement ran.
		return nil, ErrDenied
	}
	if e != nil {
		return nil, e
	}
	s.copied += 64 + int64(len(row))*32
	for _, v := range row {
		switch v := v.(type) {
		case string:
			s.copied += int64(len(v)) * 2
		case []byte:
			s.copied += int64(len(v)) * 3
		}
	}
	if s.copied > s.ceiling {
		return nil, governor.ErrSnapshotBudget
	}
	return row, nil
}

func (r *ForegroundReader) iaReading(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAReadingInput) (IAReadingResult, BrowseReadResult, error) {
	if r == nil || in.Item <= 0 || in.View.After != nil || in.View.Descending || in.View.KeepArticles || (in.View.Kind != 20 && in.View.Kind != 21) {
		return IAReadingResult{}, BrowseReadResult{}, ErrInput
	}
	s := &iaReadingScope{browseSnapshots: r.snapshots, ceiling: output.Request.Need[governor.MemoryBytes] / 4}
	var keys [][]any
	source := func(q browseQuery) ([]any, error) {
		row, e := s.exact("entity", []int{1, 2}, in.Item, in.View.Library)
		if e != nil {
			return nil, e
		}

		keys, e := q("foreground.ia.reading.members", in.Item)
		if e != nil {
			return nil, e
		}
		if len(keys) > 8 {
			return nil, ErrUnqualified
		}
		for _, key := range keys {
			asset := key[0].(int64)
			p, e := s.exact("publication", []int{0, 3, 4, 5, 6, 7, 8, 9}, in.Item, asset)
			if e != nil {
				return nil, e
			}
			a, e := s.exact("asset", []int{0}, asset, in.View.Library)
			if e != nil {
				return nil, e
			}
			l, e := s.exact("link", []int{6, 7}, in.Item, asset, in.View.Library)
			if e != nil {
				return nil, e
			}
			row = append(row, asset)
			row = append(row, p...)
			row = append(row, a...)
			row = append(row, l...)
			typ, e := s.exact("type", []int{0, 1, 2, 4, 5}, in.Item, asset)
			if errors.Is(e, ErrDenied) {
				row = append(row, false)
			} else if e != nil {
				return nil, e
			} else {
				row = append(row, true)
				row = append(row, typ...)
			}
			if p[1].(int64) != 0 {
				if p[3] == nil {
					return nil, ErrUnqualified
				}
				o, e := s.exact("object", []int{0, 1, 2, 3, 4}, p[3])
				if e != nil {
					return nil, e
				}
				row = append(row, o...)
			}
		}
		return row, nil
	}
	steps := []readStep[IAReadingResult]{func(ctx context.Context, q browseQuery, f browseFence, d *IAReadingResult) error {
		row, e := s.exact("entity", []int{1, 2}, in.Item, in.View.Library)
		if e != nil {
			return e
		}
		if row[3].(int64) != in.View.Kind || row[4].(int64) != 0 || row[6] == nil && f.allowUnrated == 0 || row[6] != nil && f.maximumAge >= 0 && row[6].(int64) > f.maximumAge {
			return ErrDenied
		}
		if len(row[1].([]byte)) != 16 {
			return ErrUnqualified
		}
		d.Item = BrowseItem{ID: in.Item, Title: row[2].(string), Revision: row[5].(int64)}
		copy(d.Item.PublicID[:], row[1].([]byte))
		d.Kind = in.View.Kind
		keys, e = q("foreground.ia.reading.members", in.Item)
		if e != nil {
			return e
		}
		d.RawRows = len(keys)
		if len(keys) > 8 {
			return ErrUnqualified
		}
		d.Exhausted = true
		return ctx.Err()
	}}
	for n := 0; n < 8; n++ {
		n := n
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *IAReadingResult) error {
			if n >= len(keys) {
				return nil
			}
			asset := keys[n][0].(int64)
			row, e := s.exact("publication", []int{0, 3, 4, 5, 6, 7, 8, 9}, in.Item, asset)
			if e != nil {
				return e
			}
			p := IAReadingPublication{Asset: asset, Format: row[0].(string), PathHead: row[1].(int64) != 0, PublisherSequence: iaReadingNumber(row[2]), Object: iaReadingString(row[3]), SourceRevision: iaReadingString(row[4]), Author: iaReadingString(row[5]), AuthorSource: iaReadingString(row[6]), IssueDate: iaReadingString(row[7]), Series: iaReadingString(row[8]), SeriesSource: iaReadingString(row[9])}
			if row[10] != nil {
				v := row[10].(float64)
				p.SeriesNumber = &v
			}
			a, e := s.exact("asset", []int{0}, asset, in.View.Library)
			if e != nil {
				return e
			}
			p.AssetToken = a[0].(string)
			p.AssetRevision = a[2].(int64)
			p.AssetAvailable = a[1].(int64) != 0
			l, e := s.exact("link", []int{6, 7}, in.Item, asset, in.View.Library)
			if e != nil {
				return e
			}
			p.LinkAvailable = l[3].(int64) != 0
			p.LinkRevision = l[8].(int64)
			typ, e := s.exact("type", []int{0, 1, 2, 4, 5}, in.Item, asset)
			if e != nil && !errors.Is(e, ErrDenied) {
				return e
			}
			if e == nil {
				p.PublicationType = iaReadingString(typ[0])
				p.TypeFormat = iaReadingString(typ[1])
				p.TypeField = iaReadingString(typ[2])
				v := typ[3].(int64) != 0
				p.TypeComplete = &v
				p.TypeObject = iaReadingString(typ[4])
				p.TypeRevision = iaReadingString(typ[5])
				p.ClassifierVersion = iaReadingNumber(typ[6])
			}
			d.Publications = append(d.Publications, p)
			return ctx.Err()
		})
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *IAReadingResult) error {
			if n >= len(d.Publications) {
				return nil
			}
			p := &d.Publications[n]
			if !p.PathHead {
				return nil
			}
			if p.Object == nil || p.SourceRevision == nil {
				return ErrUnqualified
			}
			o, e := s.exact("object", []int{0, 1, 2, 3, 4}, *p.Object)
			if e != nil {
				return e
			}
			if o[4].(string) != p.AssetToken {
				return ErrUnqualified
			}
			p.SourceID = iaReadingString(o[0])
			p.RelativePath = iaReadingString(o[1])
			p.ObjectRevision = iaReadingString(o[2])
			p.ObjectState = iaReadingString(o[3])
			p.ObjectRetired = iaReadingNumber(o[5])
			return ctx.Err()
		})
	}
	page, result, e := iaReadingPublish(ctx, s, request, budget, output, in.View, steps, source)
	for _, c := range s.counts {
		publication := false
		for _, w := range result.PublicationWindows {
			if w.Started == c.at {
				publication = true
				break
			}
		}
		if publication {
			result.PublicationStatements += c.calls
		} else {
			result.LookupStatements += c.calls
		}
	}
	return page, result, e
}

func iaReadingNumber(v any) *int64 {
	if v == nil {
		return nil
	}
	n := v.(int64)
	return &n
}
