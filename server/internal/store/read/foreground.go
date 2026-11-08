package read

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// ForegroundReader is harness-only. Public reads require a ConsumeForeground
// scope bound to this snapshot pool; no SQL handle escapes.
type ForegroundReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewForegroundReader(s *governor.Snapshots) (*ForegroundReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &ForegroundReader{snapshots: s, pool: s}, nil
}

var ErrNotReady = errors.New("foreground source facts are not ready")

// TitleInput selects a bounded child/credit window and an optional asset's
// ready file facts. Children and credits continue only at their exact fence.
type TitleInput struct {
	View        BrowseInput
	Item, Asset int64
	After       *TitleCursor
}
type TitleCursor struct {
	fence                   browseFence
	item, child, cast, crew int64
	source                  []any
}
type UserState struct {
	Revision, ResumePosition, ResumeUnit, PlayCount int64
	Favorite, Watchlisted, Watched, Completed       bool
}
type TitleItem struct {
	BrowseItem
	Kind     int64
	Overview string
}
type Image struct {
	Role, Digest, Provider, Origin string
	Revision                       int64
}
type Credit struct {
	Ord, Person            int64
	Name, Role, Department string
}
type Version struct {
	Asset, Part int64
	Edition     string
	Available   bool
}
type Stream struct {
	Index                        int64
	Type, Codec, Language, Title string
	Channels                     int64
	Default, Forced              bool
}
type Chapter struct {
	Index      int64
	Title      string
	Start, End float64
}
type PlaybackFile struct {
	Asset                                         int64
	Token, Path, Container                        string
	Size, ModifiedNS                              int64
	Duration, Start                               float64
	End                                           *float64
	AssetIncarnation, LinkIncarnation             [16]byte
	AssetRevision, LinkRevision, PreparedRevision int64
	Streams                                       []Stream
	Chapters                                      []Chapter
}
type TitleDetail struct {
	Item                                TitleItem
	State                               UserState
	Children                            []BrowseItem
	Images                              []Image
	Cast, Crew                          []Credit
	Versions                            []Version
	File                                *PlaybackFile
	Next                                *TitleCursor
	ChildrenExhausted, CreditsExhausted bool
	RawRows                             int
}

func validTitle(in TitleInput) bool {
	return in.Item > 0 && in.Asset >= 0 && in.View.After == nil && !in.View.Descending && !in.View.KeepArticles
}
func stateValue(row []any) UserState {
	if row[0] == nil {
		return UserState{}
	}
	return UserState{Revision: row[0].(int64), ResumePosition: row[1].(int64), ResumeUnit: row[2].(int64), PlayCount: row[3].(int64), Favorite: row[4].(int64) != 0, Watchlisted: row[5].(int64) != 0, Watched: row[6].(int64) != 0, Completed: row[7].(int64) != 0}
}

// Detail returns at most40 children,100 cast,50 crew,16 versions and the
// selected file's <=255 streams/class and512 chapters. A pending raw child or
// credit window is explicit; it is never a successful exhaustive empty list.
func (r *ForegroundReader) Detail(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in TitleInput) (TitleDetail, BrowseReadResult, error) {
	if r == nil {
		return TitleDetail{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (TitleDetail, BrowseReadResult, error) {
		in.View.Now = at
		return r.detail(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) detail(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in TitleInput) (TitleDetail, BrowseReadResult, error) {
	if r == nil || !validTitle(in) {
		return TitleDetail{}, BrowseReadResult{}, ErrInput
	}
	child, cast, crew := int64(0), int64(-1), int64(-1)
	image := int64(0)
	if in.After != nil {
		child, cast, crew = in.After.child, in.After.cast, in.After.crew
	}
	var token string
	var source []any
	steps := []readStep[TitleDetail]{
		func(ctx context.Context, q browseQuery, f browseFence, d *TitleDetail) error {
			child, cast, crew = 0, -1, -1
			if in.After != nil {
				child, cast, crew = in.After.child, in.After.cast, in.After.crew
			}
			image = 0
			if in.After != nil && (in.After.fence != f || in.After.item != in.Item) {
				return ErrStale
			}
			if !admittedItem(source[16:23], f) {
				return ErrDenied
			}
			item, e := itemValue(source[16:23])
			if e != nil {
				return e
			}
			d.Item = TitleItem{BrowseItem: item, Kind: in.View.Kind}

			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *TitleDetail) error {
			rows, e := q("foreground.item", f.profile, in.Item, in.View.Library, in.View.Kind)
			if e != nil {
				return e
			}
			if len(rows) != 1 || !admittedItem(rows[0], f) {
				return ErrDenied
			}
			v, e := itemValue(rows[0])
			if e != nil {
				return e
			}
			d.Item = TitleItem{BrowseItem: v, Kind: in.View.Kind, Overview: rows[0][7].(string)}
			d.State = stateValue(rows[0][9:])
			d.RawRows++
			if rows[0][8] != nil {
				image = rows[0][8].(int64)
			}
			rows, e = q("foreground.children", in.Item, child, int64(MaxRaw))
			if e != nil {
				return e
			}
			d.RawRows += len(rows)
			n := 0
			for _, v := range rows {
				child = v[0].(int64)
				n++
				// A restricted container needs its own complete visible-member proof.
				if v[8].(int64) != 0 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
					return ErrUnqualified
				}
				if !admittedItem(v, f) {
					continue
				}
				item, e := itemValue(v)
				if e != nil {
					return e
				}
				d.Children = append(d.Children, item)
				if len(d.Children) == in.View.Limit {
					break
				}
			}
			d.ChildrenExhausted = n == len(rows) && len(rows) < MaxRaw
			rows, e = q("foreground.images", in.Item, int64(129))
			if e != nil {
				return e
			}
			d.RawRows += len(rows)
			if len(rows) > 128 {
				return ErrUnqualified
			}
			lastRole := ""
			for _, v := range rows { // PK order puts locked before matched for each role.
				role := v[0].(string)
				if role == lastRole {
					// The first row for a role is its precedence choice. A later
					// matched row never revives a role that was already consumed.
					continue
				}
				lastRole = role
				if v[2] == nil {
					// A locked selection with a NULL digest is a present empty
					// choice. It consumes the role and omits the image; it is
					// never asserted as a string or replaced by matched art.
					continue
				}
				d.Images = append(d.Images, Image{Role: role, Digest: v[2].(string), Provider: v[3].(string), Origin: v[4].(string), Revision: v[5].(int64)})
			}

			rows, e = q("foreground.credits.class", "Acting")
			if e != nil {
				return e
			}
			d.RawRows += len(rows)
			var department any
			if len(rows) == 1 {
				department = rows[0][0]
			}
			d.CreditsExhausted = true
			for _, class := range []struct {
				name   string
				after  *int64
				limit  int
				values *[]Credit
			}{
				{"cast", &cast, 100, &d.Cast}, {"crew", &crew, 50, &d.Crew},
			} {

				args := []any{in.Item, image, *class.after}
				name := "foreground.credits." + class.name
				if department == nil {
					if class.name == "cast" {
						continue
					}
					name += ".all"
				} else {
					args = append(args, department)
				}
				rows, e = q(name, args...)
				if e != nil {
					return e
				}
				d.RawRows += len(rows)
				if len(rows) == class.limit {
					d.CreditsExhausted = false
				}
				for _, v := range rows {
					c := Credit{Ord: v[0].(int64), Name: v[2].(string), Role: v[3].(string), Department: v[4].(string)}
					if v[1] != nil {
						c.Person = v[1].(int64)
					}
					*class.values = append(*class.values, c)
					*class.after = c.Ord
				}
			}

			rows, e = q("foreground.versions", in.Item, int64(17))
			if e != nil {
				return e
			}
			d.RawRows += len(rows)
			if len(rows) > 16 {
				return ErrUnqualified
			}
			for _, v := range rows {
				d.Versions = append(d.Versions, Version{Asset: v[0].(int64), Part: v[1].(int64), Edition: v[2].(string), Available: v[3].(int64) != 0})
			}
			if !d.ChildrenExhausted || !d.CreditsExhausted {
				d.Next = &TitleCursor{fence: f, item: in.Item, child: child, cast: cast, crew: crew, source: source}
			}
			return ctx.Err()
		},
	}
	if in.Asset != 0 {
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *TitleDetail) error {
			file, n, e := readPlaybackFile(q, in, token)
			d.RawRows += n
			d.File = file
			return errors.Join(e, ctx.Err())
		})
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, steps, titleSource(in, &token, &source))
}

// Playback resolves a selected current link and ready file/stream facts. It
// mints no playback authority and performs no IO or durable session start.
func (r *ForegroundReader) Playback(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in TitleInput) (PlaybackFile, BrowseReadResult, error) {
	if r == nil {
		return PlaybackFile{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (PlaybackFile, BrowseReadResult, error) {
		in.View.Now = at
		return r.playback(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) playback(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in TitleInput) (PlaybackFile, BrowseReadResult, error) {
	if r == nil || !validTitle(in) || in.Asset == 0 || in.After != nil {
		return PlaybackFile{}, BrowseReadResult{}, ErrInput
	}
	var token string
	var source []any
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[PlaybackFile]{
		func(ctx context.Context, q browseQuery, f browseFence, d *PlaybackFile) error {
			if !admittedItem(source[16:23], f) {
				return ErrDenied
			}

			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *PlaybackFile) error {
			file, _, e := readPlaybackFile(q, in, token)
			if e == nil {
				*d = *file
			}
			return errors.Join(e, ctx.Err())
		},
	}, titleSource(in, &token, &source))
}
func readPlaybackFile(q browseQuery, in TitleInput, token string) (*PlaybackFile, int, error) {
	rows, e := q("foreground.file", token, in.Item, in.View.Library)
	if e != nil {
		return nil, 0, e
	}
	if len(rows) != 1 {
		return nil, len(rows), ErrNotReady
	}
	v := rows[0]
	d := &PlaybackFile{Asset: in.Asset, Token: v[0].(string), Path: v[1].(string), Size: v[2].(int64), ModifiedNS: v[3].(int64), Container: v[4].(string), Duration: v[5].(float64), Start: v[6].(float64), AssetRevision: v[9].(int64), LinkRevision: v[11].(int64), PreparedRevision: v[12].(int64)}
	if len(v[8].([]byte)) != 16 || len(v[10].([]byte)) != 16 {
		return nil, 1, ErrUnqualified
	}
	copy(d.AssetIncarnation[:], v[8].([]byte))
	copy(d.LinkIncarnation[:], v[10].([]byte))
	if v[7] != nil {
		end := v[7].(float64)
		d.End = &end
	}
	rows, e = q("foreground.streams", d.Token, int64(766))
	if e != nil {
		return nil, 1, e
	}
	n := 1 + len(rows)
	counts := map[string]int{}
	if len(rows) > 765 {
		return nil, n, ErrUnqualified
	}
	for _, v := range rows {
		kind := v[1].(string)
		if kind != "audio" && kind != "video" && kind != "subtitle" {
			return nil, n, ErrUnqualified
		}
		counts[kind]++
		if counts[kind] > 255 {
			return nil, n, ErrUnqualified
		}
		d.Streams = append(d.Streams, Stream{Index: v[0].(int64), Type: kind, Codec: v[2].(string), Language: v[3].(string), Title: v[4].(string), Channels: v[5].(int64), Default: v[6].(int64) != 0, Forced: v[7].(int64) != 0})
	}
	rows, e = q("foreground.chapters", d.Token, int64(513))
	if e != nil {
		return nil, n, e
	}
	n += len(rows)
	if len(rows) > 512 {
		return nil, n, ErrUnqualified
	}
	for _, v := range rows {
		d.Chapters = append(d.Chapters, Chapter{Index: v[0].(int64), Title: v[1].(string), Start: v[2].(float64), End: v[3].(float64)})
	}
	return d, n, nil
}

func titleSource(in TitleInput, token *string, captured *[]any) rootRead {
	return func(q browseQuery) ([]any, error) {
		rows, e := q("foreground.source", in.Item, in.View.Library, in.View.Kind)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		if rows[0][1] != nil && (rows[0][2] == nil || rows[0][2].(int64) != 1) {
			return nil, ErrNotReady
		}
		source := rows[0]
		*token = ""
		if in.Asset != 0 {
			assets, e := q("foreground.asset_source", in.Asset, in.View.Library)
			if e != nil {
				return nil, e
			}
			if len(assets) != 1 {
				return nil, ErrNotReady
			}
			*token = assets[0][0].(string)
			source = append(source, assets[0]...)
			links, e := q("foreground.link_source", *token, *token, in.Asset, in.Item)
			if e != nil {
				return nil, e
			}
			if len(links) != 1 {
				return nil, ErrNotReady
			}
			source = append(source, links[0]...)
		}
		if in.After != nil && !equalSource(in.After.source, source) {
			return nil, ErrStale
		}
		*captured = source
		return source, nil
	}
}
