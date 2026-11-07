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

// rsPeopleRaw bounds byte-point work per physical operation; exhaustive people
// pages compose resumable governed preparation rather than a long point loop.
const rsPeopleRaw = 4

// Credits visits at most4 raw credit positions per library/kind and emits at
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
	var raw [][]any
	var exhausted bool
	windows := &rsByteWindow{source: r.snapshots, preflightCalls: rsPeopleRaw + 10}
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
	scalarVisible := func(row []any, f browseFence) bool {
		return row[3] != nil && row[7].(int64) == in.View.Library && row[8].(int64) == in.View.Kind && row[4].(int64) == 0 &&
			(row[6] != nil && (f.maximumAge < 0 || row[6].(int64) <= f.maximumAge) || row[6] == nil && f.allowUnrated != 0) &&
			row[9] != nil && row[9].(int64) == row[2].(int64) && row[10] != nil && row[10].(int64) == 1
	}
	return publishRead(ctx, windows, request, budget, output, in.View, []readStep[PersonCreditsPage]{
		func(ctx context.Context, q browseQuery, f browseFence, p *PersonCreditsPage) error {
			if in.After != nil && (in.After.person != in.Person || in.After.class != in.Class || in.After.fence != f) {
				return ErrStale
			}
			raw = nil
			exhausted = true
			type seek struct {
				name string
				args []any
			}
			seeks := []seek{{"first", []any{in.Person}}}
			if c := in.After; c != nil {
				seeks = []seek{{"image_tail", []any{in.Person, c.work, c.ordinal, c.image}}, {"ordinal_tail", []any{in.Person, c.work, c.ordinal}}, {"next", []any{in.Person, c.work}}}
			}
			for _, s := range seeks {
				limit := rsPeopleRaw - len(raw)
				rows, err := q("rs.people."+s.name, append(s.args, int64(limit))...)
				if err != nil {
					return err
				}
				if len(rows) > limit {
					return ErrUnqualified
				}
				raw = append(raw, rows...)
				if len(raw) == rsPeopleRaw {
					exhausted = false
					break
				}
			}
			p.RawRows = len(raw)
			maxBytes, totalBytes := int64(0), int64(0)
			// Reserve person data only prospectively; decode it AFTER a visible credit.
			sizes, err := q("rs.people.person_octets", in.Person)
			if err != nil {
				return err
			}
			if len(sizes) != 1 {
				return ErrDenied
			}
			personBytes, err := rsPointBytes(sizes[0])
			if err != nil {
				return err
			}
			maxBytes, totalBytes = personBytes, personBytes
			admitted := 0
			for n, row := range raw {
				if !scalarVisible(row, f) {
					continue
				}
				sizes, err = q("rs.people.credit_octets", row[2], row[1], in.Person, in.View.Library, in.View.Kind)
				if err != nil {
					return err
				}
				if len(sizes) != 1 {
					return ErrStale
				}
				bytes, err := rsPointBytes(sizes[0])
				if err != nil {
					return err
				}
				// A byte-limited raw prefix is pending work, never an exhaustive result.
				// One too-large current row refuses; it is never silently skipped.
				if bytes > output.Request.Need[governor.MemoryBytes]/8-totalBytes {
					if n == 0 {
						return governor.ErrSnapshotBudget
					}
					raw = raw[:n]
					exhausted = false
					break
				}
				totalBytes += bytes
				maxBytes = max(maxBytes, bytes)
				admitted++
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if admitted == 0 {
				return nil
			}
			return windows.arm(maxBytes, 2*rsPeopleRaw+2)
		},
		func(ctx context.Context, q browseQuery, f browseFence, p *PersonCreditsPage) error {
			for _, row := range raw {
				p.RawPositions++
				p.Next = &PersonCreditsCursor{fence: f, person: in.Person, class: in.Class, work: row[0].(int64), ordinal: row[1].(int64), image: row[2].(int64), source: source}
				if !scalarVisible(row, f) {
					continue
				}
				rows, err := q("rs.people.credit", row[2], row[1], in.Person, in.View.Library, in.View.Kind)
				if err != nil {
					return err
				}
				if len(rows) != 1 {
					return ErrStale
				}
				v := rows[0]
				if !admittedItem(v, f) {
					return ErrStale
				}
				if f.container != 0 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
					return ErrUnqualified
				}
				dept := v[9].(string)
				cast := strings.EqualFold(dept, "Acting") || strings.EqualFold(dept, "Cast")
				if in.Class == PersonCastCredits && !cast || in.Class == PersonCrewCredits && cast {
					continue
				}
				item, err := itemValue(v)
				if err != nil {
					return err
				}
				p.Credits = append(p.Credits, PersonWorkCredit{Item: TitleItem{BrowseItem: item, Kind: in.View.Kind}, Ordinal: row[1].(int64), Image: row[2].(int64), CreditedName: v[7].(string), Role: v[8].(string), Department: dept})
				if p.Person == nil {
					people, err := q("rs.people.person", in.Person)
					if err != nil {
						return err
					}
					if len(people) != 1 {
						return ErrDenied
					}
					v := people[0]
					p.Person = &PersonFacts{ID: v[0].(int64), Token: v[1].(string), Name: v[2].(string), SortName: v[3].(string), Biography: v[4].(string), BirthPlace: v[5].(string), BiographySource: v[6].(string), BirthDate: v[7].(string), DeathDate: v[8].(string), Revision: v[9].(int64)}
				}
				if len(p.Credits) == in.View.Limit {
					return ctx.Err()
				}
			}
			p.Exhausted = exhausted
			if exhausted {
				p.Next = nil
			}
			return ctx.Err()
		},
	}, root)
}
