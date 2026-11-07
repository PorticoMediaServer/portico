package read

import (
	"context"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

type PersonReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewPersonReader(s *governor.Snapshots) (*PersonReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &PersonReader{snapshots: s, pool: s}, nil
}

type PersonCreditClass uint8

const (
	PersonAllCredits PersonCreditClass = iota
	PersonCastCredits
	PersonCrewCredits
)

type PersonCreditsInput struct {
	View   BrowseInput
	Person int64
	Class  PersonCreditClass
	After  *PersonCreditsCursor
}
type PersonCreditsCursor struct {
	fence                browseFence
	person               int64
	class                PersonCreditClass
	work, ordinal, image int64
	source               []any
}
type PersonFacts struct {
	ID                                                                                  int64
	Token, Name, SortName, Biography, BirthPlace, BiographySource, BirthDate, DeathDate string
	Revision                                                                            int64
}
type PersonWorkCredit struct {
	Item                           TitleItem
	Ordinal, Image                 int64
	CreditedName, Role, Department string
}

// PersonCreditsPage is a bounded credit-row component, not a complete PersonPage
// or its exact totals/known-for/grouping. Multiple roles on one work stay distinct.
type PersonCreditsPage struct {
	Person                *PersonFacts
	Credits               []PersonWorkCredit
	Next                  *PersonCreditsCursor
	Exhausted             bool
	RawRows, RawPositions int
}

// Credits visits at most64 raw credit positions per library/kind and emits at
// most40 rows. Pending/obsolete images and hidden works consume the same bound.
// Person facts require a currently permitted credit in this exact window.
func (r *PersonReader) Credits(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in PersonCreditsInput) (PersonCreditsPage, BrowseReadResult, error) {
	if r == nil {
		return PersonCreditsPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (PersonCreditsPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.credits(ctx, request, budget, output, in)
	})
}
func (r *PersonReader) credits(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in PersonCreditsInput) (PersonCreditsPage, BrowseReadResult, error) {
	if r == nil || in.Person <= 0 || in.Class > PersonCrewCredits || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return PersonCreditsPage{}, BrowseReadResult{}, ErrInput
	}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		rows, err := q("rs.people.source", in.Person)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		if in.After != nil && !equalSource(in.After.source, rows[0]) {
			return nil, ErrStale
		}
		source = rows[0]
		return rows[0], nil
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[PersonCreditsPage]{
		func(ctx context.Context, q browseQuery, f browseFence, p *PersonCreditsPage) error {
			if in.After != nil && (in.After.person != in.Person || in.After.class != in.Class || in.After.fence != f) {
				return ErrStale
			}
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, p *PersonCreditsPage) error {
			type seek struct {
				name string
				args []any
			}
			seeks := []seek{{"first", []any{in.Person}}}
			if c := in.After; c != nil {
				seeks = []seek{{"image_tail", []any{in.Person, c.work, c.ordinal, c.image}}, {"ordinal_tail", []any{in.Person, c.work, c.ordinal}}, {"next", []any{in.Person, c.work}}}
			}
			for _, s := range seeks {
				limit := MaxRaw - p.RawRows
				rows, err := q("rs.people."+s.name, append(s.args, int64(limit))...)
				if err != nil {
					return err
				}
				if len(rows) > limit {
					return ErrUnqualified
				}
				p.RawRows += len(rows)
				for _, row := range rows {
					p.RawPositions++
					p.Next = &PersonCreditsCursor{fence: f, person: in.Person, class: in.Class, work: row[0].(int64), ordinal: row[1].(int64), image: row[2].(int64), source: source}
					// Left joins preserve the raw position even after its live entity retires.
					if row[3] == nil || row[10].(int64) != in.View.Library || row[11].(int64) != in.View.Kind || !admittedItem(row[3:10], f) {
						continue
					}
					if row[12] == nil || row[12].(int64) != row[2].(int64) || row[13] == nil || row[13].(int64) != 1 {
						continue
					}
					if f.container != 0 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
						return ErrUnqualified
					}
					dept := row[16].(string)
					cast := strings.EqualFold(dept, "Acting") || strings.EqualFold(dept, "Cast")
					if in.Class == PersonCastCredits && !cast || in.Class == PersonCrewCredits && cast {
						continue
					}
					item, err := itemValue(row[3:10])
					if err != nil {
						return err
					}
					p.Credits = append(p.Credits, PersonWorkCredit{Item: TitleItem{BrowseItem: item, Kind: in.View.Kind}, Ordinal: row[1].(int64), Image: row[2].(int64), CreditedName: row[14].(string), Role: row[15].(string), Department: dept})
					if p.Person == nil {
						p.Person = &PersonFacts{ID: source[0].(int64), Token: source[1].(string), Name: source[2].(string), SortName: source[3].(string), Biography: source[4].(string), BirthPlace: source[5].(string), BiographySource: source[6].(string), BirthDate: source[7].(string), DeathDate: source[8].(string), Revision: source[9].(int64)}
					}
					if len(p.Credits) == in.View.Limit {
						return ctx.Err()
					}
				}
				if p.RawRows == MaxRaw {
					return ctx.Err()
				}
			}
			p.Exhausted = true
			p.Next = nil
			return ctx.Err()
		},
	}, root)
}
