package read

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// IPHistoryInput selects one owner's current descriptive facts and one bounded
// page of typed history. Kind is the exact owner namespace; Entity is the
// catalogue subject id, or the catalog_people id for Kind "person". Catalog
// subjects must be visible and live in the caller's admitted library.
type IPHistoryInput struct {
	View   BrowseInput
	Kind   string
	Entity int64
	After  *IPHistoryCursor
}

// IPHistoryCursor is private values, never a wire token. It pins the shared
// fence, the owner/source witness and the last raw history id.
type IPHistoryCursor struct {
	fence  browseFence
	kind   string
	entity int64
	source []any
	id     int64
}

// IPOwnerFact is the current owner row, if one exists. Present=false means no
// descriptive owner has been written; history may still exist.
type IPOwnerFact struct {
	Present      bool
	ID, Revision int64
	Retired      bool
}

// IPOwnerField is one exact current owner field. ValuePresent/AutomaticPresent
// distinguish explicit empty from absent; Locked is the owner lock.
type IPOwnerField struct {
	Field, Value, Automatic, Actor, ObservedAt string
	ValuePresent, AutomaticPresent, Locked     bool
}

// IPHistoryField is one typed before/after field captured by the owner edit.
type IPHistoryField struct {
	Field, BeforeValue, AfterValue                string
	BeforeAutomatic, AfterAutomatic               string
	BeforePresent, AfterPresent                   bool
	BeforeAutomaticPresent, AfterAutomaticPresent bool
	BeforeLocked, AfterLocked                     bool
}

// IPHistoryEntry is one bounded history header plus its typed fields.
type IPHistoryEntry struct {
	ID                     int64
	BaseRevision, Revision string
	Actor, ObservedAt      string
	Fields                 []IPHistoryField
}

// IPHistoryPage couples the current owner facts with one raw keyset page.
// Exhausted proves the remaining keyset was consumed; a non-exhausted page has
// a continuation and is never a zero-match answer.
type IPHistoryPage struct {
	Kind      string
	Entity    int64
	Owner     IPOwnerFact
	Fields    []IPOwnerField
	Entries   []IPHistoryEntry
	Next      *IPHistoryCursor
	Exhausted bool
	RawRows   int
}

// ipHistoryOwnerKinds is the closed owner namespace. It matches IP-24's typed
// owner key, including its distinct person owner.
func ipHistoryOwnerKinds(kind string) bool {
	switch kind {
	case "item", "show", "season", "album", "artist", "book", "collection", "person":
		return true
	}
	return false
}

// IPHistory is harness-only and requires ConsumeForeground custody. It discloses
// no SQL, connection, writer or provider capability, and never projects the
// legacy unbounded history JSON bodies.
func (r *ForegroundReader) IPHistory(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IPHistoryInput) (IPHistoryPage, BrowseReadResult, error) {
	if r == nil {
		return IPHistoryPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IPHistoryPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.ipHistory(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) ipHistory(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IPHistoryInput) (IPHistoryPage, BrowseReadResult, error) {
	if r == nil || in.Entity <= 0 || in.View.Library <= 0 || in.View.Kind < 1 || in.View.Kind > 10 || in.View.After != nil || in.View.Descending || in.View.KeepArticles || !ipHistoryOwnerKinds(in.Kind) {
		return IPHistoryPage{}, BrowseReadResult{}, ErrInput
	}
	s := &ipScope{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var source, witness []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		var rows [][]any
		var e error
		if in.Kind == "person" {
			rows, e = q("read.ip.owner_person", in.Entity, in.View.Library, in.View.Kind)
		} else {
			rows, e = q("read.ip.owner", in.Entity, in.View.Library, in.View.Kind)
		}
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		source = rows[0]
		return rows[0], nil
	}
	steps := []readStep[IPHistoryPage]{
		func(ctx context.Context, q browseQuery, f browseFence, d *IPHistoryPage) error {
			if e := ipOwnerScope(source, in, f); e != nil {
				return e
			}
			var rows [][]any
			var e error
			if in.Kind == "person" {
				rows, e = q("read.ip.owner_fact_person", in.Entity, "person")
			} else {
				rows, e = q("read.ip.owner_fact", in.Entity, in.Kind)
			}
			if e != nil {
				return e
			}
			ownerWitness := []any{nil, nil, nil}
			if len(rows) > 0 {
				if len(rows) != 1 || len(rows[0]) != 3 {
					return ErrUnqualified
				}
				ownerRow, e := ipOwnerColumns(rows[0][0], rows[0][1], rows[0][2])
				if e != nil {
					return e
				}
				d.Owner = ownerRow.fact
				ownerWitness = ownerRow.witness
			}
			d.Kind, d.Entity = in.Kind, in.Entity
			witness = append(append([]any(nil), source...), ownerWitness...)
			if in.After != nil && (in.After.fence != f || in.After.kind != in.Kind || in.After.entity != in.Entity || !equalSource(in.After.source, witness)) {
				return ErrStale
			}
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *IPHistoryPage) error {
			var sizes, rows [][]any
			var e error
			if in.Kind == "person" {
				sizes, e = q("read.ip.owner_fields_person_bytes", in.Entity, int64(ipOwnerMaxFields+1))
				if e != nil {
					return e
				}
				rows, e = q("read.ip.owner_fields_person", in.Entity, int64(ipOwnerMaxFields+1))
			} else {
				sizes, e = q("read.ip.owner_fields_bytes", in.Entity, in.Kind, int64(ipOwnerMaxFields+1))
				if e != nil {
					return e
				}
				rows, e = q("read.ip.owner_fields", in.Entity, in.Kind, int64(ipOwnerMaxFields+1))
			}
			if e != nil {
				return e
			}
			if e = ipVerifyRanges(sizes, rows, ipOwnerFieldsVariable); e != nil {
				return e
			}
			if len(rows) > ipOwnerMaxFields {
				return ErrUnqualified
			}
			for _, row := range rows {
				field, e := ipOwnerField(row)
				if e != nil {
					return e
				}
				d.Fields = append(d.Fields, field)
			}
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *IPHistoryPage) error {
			after := int64(1) << 62
			if in.After != nil {
				after = in.After.id
			}
			sizes, e := q("read.ip.history_bytes", in.Kind, in.Entity, after, int64(ipHistoryPageSize+1))
			if e != nil {
				return e
			}
			rows, e := q("read.ip.history", in.Kind, in.Entity, after, int64(ipHistoryPageSize+1))
			if e != nil {
				return e
			}
			if e = ipVerifyRanges(sizes, rows, ipHistoryVariable); e != nil {
				return e
			}
			d.RawRows = len(rows)
			if len(rows) > ipHistoryPageSize+1 {
				return ErrUnqualified
			}
			more := len(rows) > ipHistoryPageSize
			if more {
				rows = rows[:ipHistoryPageSize]
			}
			for _, row := range rows {
				entry, e := ipHistoryEntry(row)
				if e != nil {
					return e
				}
				d.Entries = append(d.Entries, entry)
			}
			d.Exhausted = !more
			if more {
				last := d.Entries[len(d.Entries)-1].ID
				d.Next = &IPHistoryCursor{fence: f, kind: in.Kind, entity: in.Entity, source: witness, id: last}
			}
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *IPHistoryPage) error {
			if len(d.Entries) == 0 {
				return ctx.Err()
			}
			low, high := d.Entries[len(d.Entries)-1].ID, d.Entries[0].ID
			sizes, e := q("read.ip.history_fields_bytes", in.Kind, in.Entity, low, high, int64(ipHistoryMaxFields+1))
			if e != nil {
				return e
			}
			rows, e := q("read.ip.history_fields", in.Kind, in.Entity, low, high, int64(ipHistoryMaxFields+1))
			if e != nil {
				return e
			}
			if e = ipVerifyRanges(sizes, rows, ipHistoryFieldsVariable); e != nil {
				return e
			}
			if len(rows) > ipHistoryMaxFields {
				return ErrUnqualified
			}
			byID := make(map[int64]int, len(d.Entries))
			for n, entry := range d.Entries {
				byID[entry.ID] = n
			}
			for _, row := range rows {
				id, ok := row[0].(int64)
				if !ok {
					return ErrUnqualified
				}
				position, ok := byID[id]
				if !ok || len(d.Entries[position].Fields) >= ipOwnerMaxFields {
					return ErrUnqualified
				}
				field, e := ipHistoryField(row)
				if e != nil {
					return e
				}
				d.Entries[position].Fields = append(d.Entries[position].Fields, field)
			}
			return ctx.Err()
		},
	}
	page, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, root)
	s.sincePublication(&result)
	return page, result, e
}

// ipOwnerScope proves the selected subject is live in the admitted library (or a
// person with a visible live credit) from fixed primitives only, and applies the
// shared admission age/unrated gate to the entity that admits the read.
func ipOwnerScope(row []any, in IPHistoryInput, f browseFence) error {
	if in.Kind == "person" {
		if len(row) != 3 {
			return ErrUnqualified
		}
		id, e1 := ipPositive(row[0])
		_, e2 := ipPositive(row[1])
		if e1 != nil || e2 != nil || id != in.Entity {
			return ErrUnqualified
		}
		if !ipAdmittedAge(row[2], f) {
			return ErrDenied
		}
		return nil
	}
	if len(row) != 6 {
		return ErrUnqualified
	}
	id, e1 := ipPositive(row[0])
	library, e2 := ipPositive(row[1])
	kind, e3 := ipPositive(row[2])
	_, e4 := ipBlob16(row[3])
	metadataRevision, e5 := ipPositive(row[4])
	for _, e := range []error{e1, e2, e3, e4, e5} {
		if e != nil {
			return ErrUnqualified
		}
	}
	if id != in.Entity || library != in.View.Library || kind != in.View.Kind || metadataRevision <= 0 {
		return ErrDenied
	}
	if !ipAdmittedAge(row[5], f) {
		return ErrDenied
	}
	return nil
}

type ipOwnerRow struct {
	fact    IPOwnerFact
	witness []any
}

func ipOwnerColumns(id, revision, retired any) (ipOwnerRow, error) {
	ownerID, e1 := ipPositive(id)
	ownerRevision, e2 := ipPositive(revision)
	if e1 != nil || e2 != nil {
		return ipOwnerRow{}, ErrUnqualified
	}
	var retiredMS int64
	if retired != nil {
		n, e3 := ipNonNegative(retired)
		if e3 != nil {
			return ipOwnerRow{}, ErrUnqualified
		}
		retiredMS = n
	}
	return ipOwnerRow{fact: IPOwnerFact{Present: true, ID: ownerID, Revision: ownerRevision, Retired: retiredMS > 0}, witness: []any{ownerID, ownerRevision, retiredMS}}, nil
}

func ipOwnerField(row []any) (IPOwnerField, error) {
	if len(row) != 8 {
		return IPOwnerField{}, ErrUnqualified
	}
	field, e1 := ipString(row[0])
	value, e2 := ipString(row[1])
	automatic, e3 := ipString(row[2])
	present, e4 := ipNonNegative(row[3])
	automaticPresent, e5 := ipNonNegative(row[4])
	locked, e6 := ipNonNegative(row[5])
	actor, e7 := ipString(row[6])
	observed, e8 := ipString(row[7])
	for _, e := range []error{e1, e2, e3, e4, e5, e6, e7, e8} {
		if e != nil {
			return IPOwnerField{}, ErrUnqualified
		}
	}
	if field == "" || len(field) > 64 || present > 1 || automaticPresent > 1 || locked > 1 || locked == 1 && present == 0 || actor == "" || observed == "" {
		return IPOwnerField{}, ErrUnqualified
	}
	return IPOwnerField{Field: field, Value: value, Automatic: automatic, ValuePresent: present == 1, AutomaticPresent: automaticPresent == 1, Locked: locked == 1, Actor: actor, ObservedAt: observed}, nil
}

func ipHistoryEntry(row []any) (IPHistoryEntry, error) {
	if len(row) != 5 {
		return IPHistoryEntry{}, ErrUnqualified
	}
	id, e1 := ipPositive(row[0])
	base, e2 := ipString(row[1])
	revision, e3 := ipString(row[2])
	actor, e4 := ipString(row[3])
	observed, e5 := ipString(row[4])
	for _, e := range []error{e1, e2, e3, e4, e5} {
		if e != nil {
			return IPHistoryEntry{}, ErrUnqualified
		}
	}
	if base == "" || revision == "" || actor == "" || observed == "" {
		return IPHistoryEntry{}, ErrUnqualified
	}
	return IPHistoryEntry{ID: id, BaseRevision: base, Revision: revision, Actor: actor, ObservedAt: observed}, nil
}

func ipHistoryField(row []any) (IPHistoryField, error) {
	if len(row) != 12 {
		return IPHistoryField{}, ErrUnqualified
	}
	field, e1 := ipString(row[1])
	before, e2 := ipString(row[2])
	beforePresent, e3 := ipNonNegative(row[3])
	beforeAutomatic, e4 := ipString(row[4])
	beforeAutomaticPresent, e5 := ipNonNegative(row[5])
	beforeLocked, e6 := ipNonNegative(row[6])
	after, e7 := ipString(row[7])
	afterPresent, e8 := ipNonNegative(row[8])
	afterAutomatic, e9 := ipString(row[9])
	afterAutomaticPresent, e10 := ipNonNegative(row[10])
	afterLocked, e11 := ipNonNegative(row[11])
	for _, e := range []error{e1, e2, e3, e4, e5, e6, e7, e8, e9, e10, e11} {
		if e != nil {
			return IPHistoryField{}, ErrUnqualified
		}
	}
	if field == "" || len(field) > 64 || beforePresent > 1 || beforeAutomaticPresent > 1 || beforeLocked > 1 || afterPresent > 1 || afterAutomaticPresent > 1 || afterLocked > 1 ||
		beforeLocked == 1 && beforePresent == 0 || afterLocked == 1 && afterPresent == 0 {
		return IPHistoryField{}, ErrUnqualified
	}
	return IPHistoryField{
		Field: field, BeforeValue: before, AfterValue: after, BeforeAutomatic: beforeAutomatic, AfterAutomatic: afterAutomatic,
		BeforePresent: beforePresent == 1, AfterPresent: afterPresent == 1,
		BeforeAutomaticPresent: beforeAutomaticPresent == 1, AfterAutomaticPresent: afterAutomaticPresent == 1,
		BeforeLocked: beforeLocked == 1, AfterLocked: afterLocked == 1,
	}, nil
}
