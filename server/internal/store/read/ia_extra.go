package read

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// IAExtraMode selects one closed extra/edition/part preparation window.
// Extras are video membership evidence: a member belongs to one explicit owner
// (or is explicitly unresolved) and keeps every edition, part and cue link
// separate. Nothing here accepts, allocates identity or starts a scan.
type IAExtraMode uint8

const (
	// IAExtraOwnerMode lists the owner's members of one exact extra type.
	IAExtraOwnerMode IAExtraMode = iota + 1
	// IAExtraPathMode resolves one member at its exact parent path, type and title.
	IAExtraPathMode
	// IAExtraMemberMode hydrates one exact extra item and its own links.
	IAExtraMemberMode
)

// IAExtraInput is harness-only preparation input. Entity is the owner entity in
// owner/path mode and the extra item itself in member mode. View.Kind is the
// subject's kind; a foreign kind, library or retired subject is refused.
type IAExtraInput struct {
	View              BrowseInput
	Mode              IAExtraMode
	Entity            int64
	ExtraKind         string
	ParentPath, Title string
	After             *IAExtraCursor
}

// IAExtraCursor is a private raw continuation. source pins the subject entity
// from the same snapshot, so a torn page cannot resume across a changed owner.
type IAExtraCursor struct {
	fence       browseFence
	mode        IAExtraMode
	entity, id  int64
	kind        string
	path, title string
	source      []any
}

// IAExtraMember is one retained extra row with its complete bounded link cohort.
// Owner is nil only when the database has no owner: an unresolved owner is
// never replaced by the parent path, the extra item or a guessed identity.
// Boundary carries the exact episode asset boundary status for the member asset
// and is empty when the pair has no boundary row. AssetMembers counts the
// bounded reverse memberships of the member's asset (deduplicated files).
type IAExtraMember struct {
	Item, Kind, Revision                              int64
	Owner                                             *int64
	AssetMembers                                      int
	ParentPath, Type, Title, AssetToken, RelativePath string
	Boundary                                          string
	Links                                             []IAAudioLink
}

// IAExtraPage is one bounded preparation page. Exhausted proves that the whole
// remaining raw owner/path window was consumed; an empty non-exhausted page is
// pending work, never a zero-match answer. Entity is always the subject
// entity: the owner in owner/path mode and the extra item in member mode.
type IAExtraPage struct {
	Entity                BrowseItem
	Members               []IAExtraMember
	Next                  *IAExtraCursor
	Exhausted             bool
	RawRows, RawPositions int
}

const (
	maxIAExtraLinks      = 16
	maxIAExtraPathBytes  = 16384
	maxIAExtraTitleBytes = 4096
	maxIAExtraTokenBytes = 512
)

// The known extra types are the closed item_extras.kind domain. "other" is the
// explicit unknown type, never a silent fallback for a missing value.
func iaExtraKind(value string) bool {
	switch value {
	case "trailer", "featurette", "deleted_scene", "behind_the_scenes", "interview", "scene", "short", "other":
		return true
	default:
		return false
	}
}

// validateIAExtraText bounds one retained identifier. It never cleans, folds or
// trims: a different value must be refused, not rewritten into another one.
func validateIAExtraText(field, value string, limit int) error {
	if len(value) == 0 || len(value) > limit || !utf8.ValidString(value) {
		return ErrInput
	}
	if strings.IndexByte(value, 0) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ErrInput
	}
	return nil
}

// validateIAExtraPath keeps the exact native parent path bytes: no cleaning,
// escaping or relative rewriting, and no path that could name another library.
func validateIAExtraPath(value string) error {
	if err := validateIAExtraText("parent path", value, maxIAExtraPathBytes); err != nil {
		return err
	}
	if strings.HasPrefix(value, "/") || strings.IndexByte(value, '\\') >= 0 || value == "." {
		return ErrInput
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return ErrInput
		}
	}
	return nil
}

// iaExtraSnapshots exposes only the current scope to this closed recipe. It
// preserves GV's snapshot lifecycle and native reservation; exact point pairs
// preflight actual octets in that same snapshot before any variable payload.
type iaExtraCalls struct {
	n           int
	publication bool
}
type iaExtraSnapshots struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	extra             []iaExtraCalls
	rootCalls         int
	copied, copyLimit int64
}

func (s *iaExtraSnapshots) NativeRowReservation() int64 { return s.base.NativeRowReservation() }
func (s *iaExtraSnapshots) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.extra = append(s.extra, iaExtraCalls{})
	callback := func(scope *governor.ReadScope) error {
		s.scope = scope
		defer func() { s.scope = nil }()
		return fn(scope)
	}
	// Instrumented test snapshots keep their boundary hooks; production harness
	// composition always supplies the actual pool and this bounded forecast.
	if pool, ok := s.base.(interface {
		ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
	}); ok {
		return pool.ReadForecasted(ctx, r, b, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: uint16(min(uint32(4096), r.Budget.RawRows))}, callback)
	}
	return s.base.Read(ctx, r, b, callback)
}
func (s *iaExtraSnapshots) point(name string, columns []int, args ...any) ([]any, error) {
	payload := readStatementID("foreground.ia.extra." + name)
	octets := readStatementID("foreground.ia.extra." + name + "_octets")
	if name == "link" {
		// The link payload is one shared shape; its octet preflight is registered
		// once by the merged IA audio family and reused, never re-registered.
		octets = iaLinkOctetsID
	}
	row, e := s.scope.QueryExactBytes(octets, payload, columns, args...)
	s.extra[len(s.extra)-1].n += 2
	if e != nil {
		return nil, e
	}
	s.copied += 64 + int64(len(row))*32
	for _, v := range row {
		switch v := v.(type) {
		case string:
			s.copied += 2 * int64(len(v))
		case []byte:
			s.copied += 3 * int64(len(v))
		}
	}
	if s.copied > s.copyLimit {
		return nil, governor.ErrSnapshotBudget
	}
	return row, nil
}
func (s *iaExtraSnapshots) links(q browseQuery, entity, library int64) ([]IAAudioLink, error) {
	rows, e := q("foreground.ia.extra.links", entity)
	if e != nil {
		return nil, e
	}
	if len(rows) > maxIAExtraLinks {
		return nil, ErrUnqualified
	}
	var links []IAAudioLink
	for _, key := range rows {
		row, e := s.point("link", []int{2, 7}, entity, key[0], library)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		l := IAAudioLink{Asset: row[0].(int64), Part: row[1].(int64), Edition: row[2].(string), Available: row[3].(int64) != 0, CueOrdinal: iaAudioInt(row[4]), Start: row[5].(float64), Revision: row[8].(int64), Pending: row[9] != nil}
		if row[6] != nil {
			n := row[6].(float64)
			l.End = &n
		}
		if len(row[7].([]byte)) != 16 {
			return nil, ErrUnqualified
		}
		copy(l.Incarnation[:], row[7].([]byte))
		links = append(links, l)
	}
	return links, nil
}

// IAExtraMembership is harness-only and requires ConsumeForeground custody.
// It discloses no SQL, connection, writer or provider capability.
func (r *ForegroundReader) IAExtraMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAExtraInput) (IAExtraPage, BrowseReadResult, error) {
	if r == nil {
		return IAExtraPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IAExtraPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaExtraMembership(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) iaExtraMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAExtraInput) (IAExtraPage, BrowseReadResult, error) {
	if r == nil || in.Entity <= 0 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IAExtraPage{}, BrowseReadResult{}, ErrInput
	}
	switch in.Mode {
	case IAExtraOwnerMode:
		if !iaExtraKind(in.ExtraKind) || in.ParentPath != "" || in.Title != "" {
			return IAExtraPage{}, BrowseReadResult{}, ErrInput
		}
	case IAExtraPathMode:
		if !iaExtraKind(in.ExtraKind) || validateIAExtraPath(in.ParentPath) != nil || validateIAExtraText("extra title", in.Title, maxIAExtraTitleBytes) != nil {
			return IAExtraPage{}, BrowseReadResult{}, ErrInput
		}
	case IAExtraMemberMode:
		if in.ExtraKind != "" || in.ParentPath != "" || in.Title != "" || in.After != nil {
			return IAExtraPage{}, BrowseReadResult{}, ErrInput
		}
	default:
		return IAExtraPage{}, BrowseReadResult{}, ErrInput
	}
	s := &iaExtraSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		row, e := s.point("entity", []int{1, 2, 3, 8}, in.Entity, in.View.Library)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		if row[7] != in.View.Kind || row[4] != int64(0) {
			return nil, ErrDenied
		}
		source = row
		return row, nil
	}
	steps := []readStep[IAExtraPage]{func(ctx context.Context, q browseQuery, f browseFence, d *IAExtraPage) error {
		if !admittedItem(source, f) {
			return ErrDenied
		}
		// Extra ownership never invents an inherited age policy. A restricted
		// profile is refused instead of publishing unrated child evidence.
		if f.maximumAge >= 0 || f.allowUnrated == 0 {
			return ErrUnqualified
		}
		if in.After != nil && (in.After.fence != f || in.After.mode != in.Mode || in.After.entity != in.Entity || in.After.kind != in.ExtraKind || in.After.path != in.ParentPath || in.After.title != in.Title || !equalSource(in.After.source, source)) {
			return ErrStale
		}
		var e error
		d.Entity, e = itemValue(source)
		return errors.Join(e, ctx.Err())
	}, func(ctx context.Context, q browseQuery, f browseFence, d *IAExtraPage) error {
		if in.Mode == IAExtraMemberMode {
			m, e := s.member(q, f, in.Entity)
			if e != nil {
				return e
			}
			if m.Item != 0 {
				d.Members = []IAExtraMember{m}
			}
			d.RawRows, d.RawPositions, d.Exhausted = 1, 1, true
			return ctx.Err()
		}
		after := int64(0)
		if in.After != nil {
			after = in.After.id
		}
		var keys [][]any
		var e error
		if in.Mode == IAExtraOwnerMode {
			keys, e = q("foreground.ia.extra.owner", in.Entity, in.ExtraKind, after, int64(MaxRaw))
		} else {
			keys, e = q("foreground.ia.extra.path", f.library, in.ParentPath, in.ExtraKind, in.Title, after, int64(MaxRaw))
		}
		if e != nil {
			return e
		}
		d.RawRows = len(keys)
		for _, key := range keys {
			id := key[0].(int64)
			// Advance on every RAW position, including retired and foreign rows.
			after = id
			d.RawPositions++
			m, e := s.member(q, f, id)
			if e != nil {
				return e
			}
			if m.Item == 0 {
				continue
			}
			d.Members = append(d.Members, m)
			if len(d.Members) == in.View.Limit {
				break
			}
		}
		d.Exhausted = d.RawPositions == len(keys) && len(keys) < MaxRaw
		if !d.Exhausted {
			d.Next = &IAExtraCursor{fence: f, mode: in.Mode, entity: in.Entity, id: after, kind: in.ExtraKind, path: in.ParentPath, title: in.Title, source: source}
		}
		return ctx.Err()
	}}
	page, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, root)
	// Exact point pairs bypass browseQuery, but remain native registered queries.
	// Each attempt has two business windows followed by fresh publication.
	for _, calls := range s.extra {
		if calls.publication {
			result.PublicationStatements += calls.n
		} else {
			result.LookupStatements += calls.n
		}
	}
	return page, result, e
}

// member hydrates one exact extra item. A zero member with a nil error means the
// raw row was retained but is not currently published (retired item); the caller
// still advanced its raw cursor.
func (s *iaExtraSnapshots) member(q browseQuery, f browseFence, id int64) (IAExtraMember, error) {
	row, e := s.point("extra", []int{0, 1, 3, 4, 5, 6}, id)
	if errors.Is(e, sql.ErrNoRows) {
		return IAExtraMember{}, ErrDenied
	}
	if e != nil {
		return IAExtraMember{}, e
	}
	if row[0].(string) != f.library {
		return IAExtraMember{}, ErrDenied
	}
	if len(row[3].(string)) == 0 || !iaExtraKind(row[3].(string)) {
		return IAExtraMember{}, ErrDenied
	}
	ent, e := s.point("entity", []int{1, 2, 3, 8}, id, f.libraryID)
	if errors.Is(e, sql.ErrNoRows) {
		return IAExtraMember{}, ErrDenied
	}
	if e != nil {
		return IAExtraMember{}, e
	}
	// The extra item's own age evidence is not an inherited owner rating.
	if !admittedItem(ent, f) {
		return IAExtraMember{}, nil
	}
	var owner *int64
	if row[2] != nil {
		owner = iaAudioInt(row[2])
		ownerRow, e := s.point("entity", []int{1, 2, 3, 8}, *owner, f.libraryID)
		if errors.Is(e, sql.ErrNoRows) {
			return IAExtraMember{}, ErrDenied
		}
		if e != nil {
			return IAExtraMember{}, e
		}
		if !admittedItem(ownerRow, f) {
			return IAExtraMember{}, ErrDenied
		}
	}
	token := row[5].(string)
	if e = validateIAExtraText("asset token", token, maxIAExtraTokenBytes); e != nil {
		return IAExtraMember{}, ErrUnqualified
	}
	asset, e := q("foreground.ia.extra.asset", token, f.libraryID)
	if e != nil {
		return IAExtraMember{}, e
	}
	if len(asset) != 1 {
		return IAExtraMember{}, ErrDenied
	}
	links, e := s.links(q, id, f.libraryID)
	if e != nil {
		return IAExtraMember{}, e
	}
	found := false
	for _, l := range links {
		if l.Asset == asset[0][0].(int64) {
			found = true
		}
	}
	if !found {
		return IAExtraMember{}, ErrUnqualified
	}
	// The reverse window is bounded and must contain this exact entity in this
	// library; a shared asset never becomes a cross-library or invented member.
	members, e := q("foreground.ia.extra.membership", asset[0][0], int64(MaxRaw+1))
	if e != nil {
		return IAExtraMember{}, e
	}
	if len(members) > MaxRaw {
		return IAExtraMember{}, ErrUnqualified
	}
	mine := false
	for _, m := range members {
		if m[2] != f.libraryID {
			return IAExtraMember{}, ErrDenied
		}
		if m[0] == id {
			mine = true
		}
	}
	if !mine {
		return IAExtraMember{}, ErrUnqualified
	}
	boundary := ""
	status, e := s.point("boundary", []int{1, 2}, id, token)
	if e == nil {
		boundary = status[2].(string)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return IAExtraMember{}, e
	}
	return IAExtraMember{
		Item: id, Kind: ent[7].(int64), Revision: ent[5].(int64), Owner: owner, AssetMembers: len(members),
		ParentPath: row[1].(string), Type: row[3].(string), Title: row[4].(string),
		AssetToken: token, RelativePath: row[6].(string), Boundary: boundary, Links: links,
	}, nil
}
