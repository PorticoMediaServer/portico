package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// TitleFacts is the accepted scalar portion of a detail response. It complements
// RD2's artwork/credit/file windows; it grants no authority over referenced IDs.
// Reading publications and disc technical facts have separate source lifetimes.
type TitleFacts struct {
	Item                                                                                  TitleItem
	Year                                                                                  int64
	OriginalTitle, Tagline, Edition, ReleaseDate, ContentRating, Studio, Network, Country string
	AddedAt, LastContentAddedAt                                                           *string
	Show                                                                                  *ShowFacts
	Season                                                                                *SeasonFacts
	Episode                                                                               *EpisodeFacts
	Album                                                                                 *AlbumFacts
	Song                                                                                  *SongFacts
	Book                                                                                  *BookFacts
	AudioFile                                                                             *AudioFileFacts
	Collection                                                                            *CollectionFacts
}
type ShowFacts struct {
	HideSeasons, AbsoluteNumbering, NewestFirst bool
	SettingsRevision                            int64
	Ranges                                      string
}
type SeasonFacts struct{ Show, Number int64 }
type EpisodeFacts struct {
	Show                          int64
	Season                        *int64
	Number                        int64
	Numbering                     string
	AirDate                       *int64
	IdentityStatus, OrderingBasis *string
}
type AlbumFacts struct {
	Artist                                  int64
	RecordLabel, ReleaseCountry, EditionKey string
}
type SongFacts struct {
	Album       int64
	Disc, Track *int64
}
type BookFacts struct{ Author, Narrator, Series, SeriesPosition string }
type AudioFileFacts struct {
	Book       int64
	Disc, Part *int64
}
type CollectionFacts struct {
	NameKey  *string
	Revision int64
}

type TitleFactsInput struct {
	View BrowseInput
	Item int64
}

// Facts reads one current entity without external probes, child expansion or
// duplicated provider evidence. Public use requires ConsumeForeground custody.
func (r *ForegroundReader) Facts(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in TitleFactsInput) (TitleFacts, BrowseReadResult, error) {
	if r == nil {
		return TitleFacts{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (TitleFacts, BrowseReadResult, error) {
		in.View.Now = at
		return r.facts(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) facts(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in TitleFactsInput) (TitleFacts, BrowseReadResult, error) {
	title := TitleInput{View: in.View, Item: in.Item}
	if r == nil || !validTitle(title) {
		return TitleFacts{}, BrowseReadResult{}, ErrInput
	}
	var source []any
	// Match publication's cumulative copy limit while preserving the original
	// request ceiling for the governor's additional fixed decode overhead.
	windows := &rsByteWindow{source: r.snapshots, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	root := func(q browseQuery) ([]any, error) {
		rows, err := q("rs.detail.source", in.Item, in.View.Library, in.View.Kind)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		source = rows[0]
		return rows[0], nil
	}
	return publishRead(ctx, windows, request, budget, output, in.View, []readStep[TitleFacts]{
		func(ctx context.Context, q browseQuery, f browseFence, d *TitleFacts) error {
			if source[2].(int64) != 0 || (source[3] == nil && f.allowUnrated == 0) || (source[3] != nil && f.maximumAge >= 0 && source[3].(int64) > f.maximumAge) {
				return ErrDenied
			}
			sizes, err := q("rs.detail.facts_octets", in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return err
			}
			if len(sizes) != 1 {
				return ErrDenied
			}
			bytes, err := rsPointBytes(sizes[0])
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			return windows.arm(bytes, 2)
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *TitleFacts) error {
			rows, err := q("rs.detail.facts", in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return err
			}
			if len(rows) != 1 || !admittedItem(rows[0], f) {
				return ErrDenied
			}
			v := rows[0]
			item, err := itemValue(v)
			if err != nil {
				return err
			}
			*d = TitleFacts{Item: TitleItem{BrowseItem: item, Kind: in.View.Kind, Overview: v[7].(string)}, Year: v[8].(int64), OriginalTitle: v[9].(string), Tagline: v[10].(string), Edition: v[11].(string), ReleaseDate: v[12].(string), ContentRating: v[13].(string), Studio: v[14].(string), Network: v[15].(string), Country: v[16].(string), AddedAt: rsText(v[17]), LastContentAddedAt: rsText(v[18])}
			required := func(n int) (int64, error) {
				if v[n] == nil {
					return 0, ErrUnqualified
				}
				return v[n].(int64), nil
			}
			switch in.View.Kind {
			case 1, 5: // Movies and artists use the common accepted descriptive fields.
			case 2:
				d.Show = &ShowFacts{HideSeasons: v[19].(int64) != 0, AbsoluteNumbering: v[20].(int64) != 0, NewestFirst: v[21].(int64) != 0, SettingsRevision: v[22].(int64), Ranges: v[23].(string)}
			case 3:
				show, err := required(24)
				if err != nil {
					return err
				}
				number, err := required(26)
				if err != nil {
					return err
				}
				d.Season = &SeasonFacts{Show: show, Number: number}
			case 4:
				show, err := required(24)
				if err != nil {
					return err
				}
				number, err := required(27)
				if err != nil {
					return err
				}
				if v[28] == nil {
					return ErrUnqualified
				}
				d.Episode = &EpisodeFacts{Show: show, Season: rsInteger(v[25]), Number: number, Numbering: v[28].(string), AirDate: rsInteger(v[29]), IdentityStatus: rsText(v[30]), OrderingBasis: rsText(v[31])}
			case 6:
				artist, err := required(32)
				if err != nil {
					return err
				}
				d.Album = &AlbumFacts{Artist: artist, RecordLabel: v[33].(string), ReleaseCountry: v[34].(string), EditionKey: v[35].(string)}
			case 7:
				album, err := required(36)
				if err != nil {
					return err
				}
				d.Song = &SongFacts{Album: album, Disc: rsInteger(v[37]), Track: rsInteger(v[38])}
			case 8:
				d.Book = &BookFacts{Author: v[39].(string), Narrator: v[40].(string), Series: v[41].(string), SeriesPosition: v[42].(string)}
			case 9:
				book, err := required(43)
				if err != nil {
					return err
				}
				d.AudioFile = &AudioFileFacts{Book: book, Disc: rsInteger(v[37]), Part: rsInteger(v[44])}
			case 10:
				d.Collection = &CollectionFacts{NameKey: rsText(v[45]), Revision: v[46].(int64)}
			default:
				return ErrUnqualified
			}
			return ctx.Err()
		},
	}, root)
}
func rsInteger(v any) *int64 {
	if v == nil {
		return nil
	}
	n := v.(int64)
	return &n
}
func rsText(v any) *string {
	if v == nil {
		return nil
	}
	s := v.(string)
	return &s
}
