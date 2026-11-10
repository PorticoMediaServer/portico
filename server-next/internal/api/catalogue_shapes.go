package api

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The shapes of the browse and personal-state routes. One Item shape serves every kind and
// both the list and the detail; the detail fills in more of it.

// Library is a library the caller may see. Views, Sorts and Filters are present on
// GET /v1/libraries/{id} and say what the library can be browsed by, so an app never
// hard-codes them.
type Library struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Views   []View   `json:"views,omitempty"`
	Sorts   []Sort   `json:"sorts,omitempty"`
	Filters []Filter `json:"filters,omitempty"`
}

// Libraries is the answer of GET /v1/libraries.
type Libraries struct {
	Items []Library `json:"items"`
}

// View is one way into a library: its films, its shows, its albums. Kind is what to pass to
// GET /v1/items as kind.
type View struct {
	Kind string `json:"kind"`
}

// Sort is a sort GET /v1/items accepts for this library, with the direction it starts in.
// Titles with no value for a sort come last in either direction. "random" is one order for
// as long as its cursor is followed, and a new one each time it is asked for without a cursor.
type Sort struct {
	ID    string `json:"id"`
	Order string `json:"order"`
}

// Filter is a filter GET /v1/items accepts for this library. Type says what a value looks
// like: "boolean", "tag" (a name or an id; GET /v1/facets lists the choices), "year"
// ("1999" or "1990..1999") or "choice" (one of Values).
type Filter struct {
	ID     string   `json:"id"`
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
}

// ItemPage is one page of GET /v1/items. NextCursor is absent at the end. Total is present
// when the request asked for it with total=true. A request with at=<position> in place of a
// cursor gets the page that starts at that position, counting from 0, in the sort asked for.
type ItemPage struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
	Total      *int   `json:"total,omitempty"`
}

// Item is anything a person can browse.
type Item struct {
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Title         string            `json:"title"`
	Year          int               `json:"year,omitempty"`
	ReleaseDate   string            `json:"releaseDate,omitempty"`
	ParentID      string            `json:"parentId,omitempty"`
	RootID        string            `json:"rootId,omitempty"`
	Index         *int              `json:"index,omitempty"`
	ParentIndex   *int              `json:"parentIndex,omitempty"`
	DurationMs    int64             `json:"durationMs,omitempty"`
	ContentRating string            `json:"contentRating,omitempty"`
	Ratings       *Ratings          `json:"ratings,omitempty"`
	AddedAt       string            `json:"addedAt"`
	Images        map[string]string `json:"images"`
	Counts        *ItemCounts       `json:"counts,omitempty"`
	State         ItemState         `json:"state"`
	// LibraryID is the library the item is in, on every card and every page.
	LibraryID string `json:"libraryId"`

	// On GET /v1/items/{id} only.
	OriginalTitle string            `json:"originalTitle,omitempty"`
	Summary       string            `json:"summary,omitempty"`
	Tagline       string            `json:"tagline,omitempty"`
	Edition       string            `json:"edition,omitempty"`
	Available     *bool             `json:"available,omitempty"`
	Parent        *ItemRef          `json:"parent,omitempty"`
	Root          *ItemRef          `json:"root,omitempty"`
	Tags          []Tag             `json:"tags,omitempty"`
	ExternalIDs   map[string]string `json:"externalIds,omitempty"`
	Credits       []Credit          `json:"credits,omitempty"`
	CreditTotal   *int              `json:"creditTotal,omitempty"`
	Versions      []Media           `json:"versions,omitempty"`
}

// ItemRef names the season or show an item belongs to, enough to draw a breadcrumb.
type ItemRef struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Index *int   `json:"index,omitempty"`
}

// Ratings are other people's opinions of a title, each out of ten.
type Ratings struct {
	Community float64 `json:"community,omitempty"`
	Critic    float64 `json:"critic,omitempty"`
}

// ItemCounts is how much a show, season, artist or album holds. Leaves are the playable
// items under it that the caller may see.
type ItemCounts struct {
	Children        int `json:"children"`
	Leaves          int `json:"leaves"`
	UnwatchedLeaves int `json:"unwatchedLeaves"`
}

// ItemState is the caller's own state for an item.
type ItemState struct {
	Watched      bool   `json:"watched"`
	PositionMs   int64  `json:"positionMs,omitempty"`
	Position     string `json:"position,omitempty"` // a place in a book
	PlayCount    int    `json:"playCount"`
	LastPlayedAt string `json:"lastPlayedAt,omitempty"`
	Favorite     bool   `json:"favorite"`
	InList       bool   `json:"inList"`
	Hidden       bool   `json:"hidden"`
	Rating       *int   `json:"rating,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
}

// Tag is a genre, studio, label or similar.
type Tag struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Credit is one person's part in an item.
type Credit struct {
	PersonID  string `json:"personId"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Character string `json:"character,omitempty"`
	Image     string `json:"image,omitempty"`
}

// CreditPage is one page of GET /v1/items/{id}/credits, in billing order. With part=cast it
// holds only the people seen or heard (actors, guests, narrators, artists) and with part=crew
// everyone else; Total counts the part asked for.
type CreditPage struct {
	Items      []Credit `json:"items"`
	NextCursor string   `json:"nextCursor,omitempty"`
	Total      int      `json:"total"`
}

// Media is one playable version of an item.
type Media struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	Edition       string   `json:"edition,omitempty"`
	Container     string   `json:"container,omitempty"`
	DurationMs    int64    `json:"durationMs,omitempty"`
	Bitrate       int64    `json:"bitrate,omitempty"`
	Size          int64    `json:"size,omitempty"`
	Width         int      `json:"width,omitempty"`
	Height        int      `json:"height,omitempty"`
	VideoCodec    string   `json:"videoCodec,omitempty"`
	AudioCodec    string   `json:"audioCodec,omitempty"`
	AudioChannels int      `json:"audioChannels,omitempty"`
	HDR           string   `json:"hdr,omitempty"`
	Parts         []Part   `json:"parts"`
	Markers       []Marker `json:"markers,omitempty"`
}

// Part is one file of a version, or the range of a file between StartMs and EndMs.
type Part struct {
	Size    int64    `json:"size"`
	StartMs *int64   `json:"startMs,omitempty"`
	EndMs   *int64   `json:"endMs,omitempty"`
	Streams []Stream `json:"streams"`
}

// Stream is one video, audio or subtitle track.
type Stream struct {
	Index           int     `json:"index"`
	Kind            string  `json:"kind"`
	Codec           string  `json:"codec,omitempty"`
	Profile         string  `json:"profile,omitempty"`
	Language        string  `json:"language,omitempty"`
	Title           string  `json:"title,omitempty"`
	Default         bool    `json:"default,omitempty"`
	Forced          bool    `json:"forced,omitempty"`
	HearingImpaired bool    `json:"hearingImpaired,omitempty"`
	Bitrate         int64   `json:"bitrate,omitempty"`
	Width           int     `json:"width,omitempty"`
	Height          int     `json:"height,omitempty"`
	FrameRate       float64 `json:"frameRate,omitempty"`
	BitDepth        int     `json:"bitDepth,omitempty"`
	HDR             string  `json:"hdr,omitempty"`
	Channels        int     `json:"channels,omitempty"`
	ChannelLayout   string  `json:"channelLayout,omitempty"`
	SampleRate      int     `json:"sampleRate,omitempty"`
}

// Marker is a chapter or a span that can be skipped.
type Marker struct {
	Kind    string `json:"kind"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Title   string `json:"title,omitempty"`
}

// Related is the answer of GET /v1/items/{id}/related: the items tied to this one, by how.
type Related struct {
	Groups []RelatedGroup `json:"groups"`
}

// RelatedGroup is one kind of tie, such as "extra" or "collection_member".
type RelatedGroup struct {
	Relation string `json:"relation"`
	Items    []Item `json:"items"`
}

// Person is someone with credits. Their titles are GET /v1/items?person=<id>.
type Person struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Biography  string            `json:"biography,omitempty"`
	BirthDate  string            `json:"birthDate,omitempty"`
	DeathDate  string            `json:"deathDate,omitempty"`
	Birthplace string            `json:"birthplace,omitempty"`
	Images     map[string]string `json:"images"`
	TitleCount int               `json:"titleCount"`
}

// Facets is the answer of GET /v1/facets: the choices of one filter over a list, each with
// how many of the list's items it would leave. With facet=letter the values are the first
// letters of the list's titles in title order ("#" for digits and symbols), and a letter's
// position in the list sorted by title is the sum of the counts before it.
type Facets struct {
	Facet  string       `json:"facet"`
	Values []FacetValue `json:"values"`
}

// FacetValue is one choice. Value is what to send as the filter.
type FacetValue struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}

// Home is the Home screen, composed by the server. An app draws the rows it is given.
type Home struct {
	Rows []HomeRow `json:"rows"`
}

// HomeRow is one row. Shape says how to draw its cards: "landscape" or "poster".
type HomeRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Shape string `json:"shape"`
	Items []Item `json:"items"`
}

// StateRequest is the body of PUT /v1/items/{id}/state. Only the fields sent change.
type StateRequest struct {
	Watched    *bool   `json:"watched"`
	PositionMs *int64  `json:"positionMs"`
	Position   *string `json:"position"`
	Favorite   *bool   `json:"favorite"`
	InList     *bool   `json:"inList"`
	Hidden     *bool   `json:"hidden"`
	// Rating is half a star to five stars, sent as 1 to 10, or null to clear it.
	Rating NullableInt `json:"rating"`
	// MadeAgoMs is how long ago the person made this edit, by the device's own measure, for
	// an edit that waited while the device was out of reach. The server never reads a
	// device's clock; of two edits to one field, the one made later wins.
	MadeAgoMs int64 `json:"madeAgoMs"`
}

// NullableInt tells a number, an explicit null and an absent field apart.
type NullableInt struct {
	Set   bool
	Value *int
}

func (n *NullableInt) UnmarshalJSON(raw []byte) error {
	n.Set = true
	if bytes.Equal(raw, []byte("null")) {
		return nil
	}
	return json.Unmarshal(raw, &n.Value)
}

// StateReply is the state as it now stands, whether or not the edit won. Undo is present
// after a change to a show, season, album or other holder.
type StateReply struct {
	ItemState
	Undo *UndoOffer `json:"undo,omitempty"`
}

// UndoOffer takes back a change to everything under a holder, until it expires.
type UndoOffer struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// UndoRequest is the body of POST /v1/items/state:undo.
type UndoRequest struct {
	Token string `json:"token"`
}

// Event is the data of one "change" event on GET /v1/events: what to read again. ID is absent
// for something deleted; LibraryID then says which library's lists changed.
type Event struct {
	Topic     string `json:"topic"`
	Op        string `json:"op"`
	ID        string `json:"id,omitempty"`
	LibraryID string `json:"libraryId,omitempty"`
}

func timeString(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func imageMap(images []db.Image) map[string]string {
	out := map[string]string{}
	for _, image := range images {
		if image.FileHash != "" {
			out[image.Role.String()] = "/v1/images/" + image.FileHash
		}
	}
	return out
}

func stateShape(s db.State) ItemState {
	return ItemState{Watched: s.Watched, PositionMs: s.PositionMS, Position: s.PositionText, PlayCount: s.PlayCount,
		LastPlayedAt: timeString(s.LastPlayedAt), Favorite: s.Favorite, InList: s.InList, Hidden: s.Hidden, Rating: s.Rating,
		UpdatedAt: timeString(s.UpdatedAt)}
}

func itemShape(it db.Item) Item {
	out := Item{ID: it.PublicID, Kind: it.Kind.String(), Title: it.Title, Year: it.Year, ReleaseDate: it.ReleaseDate,
		ParentID: it.ParentPublicID, RootID: it.RootPublicID, Index: it.IndexNumber, ParentIndex: it.ParentIndex,
		DurationMs: it.DurationMS, ContentRating: it.ContentRating, AddedAt: timeString(it.AddedAt), Images: imageMap(it.Images),
		State: stateShape(it.State), LibraryID: it.LibraryID}
	if it.CommunityRating != 0 {
		out.Ratings = &Ratings{Community: it.CommunityRating}
	}
	if !it.Kind.Playable() {
		out.Counts = &ItemCounts{Children: it.ChildCount, Leaves: it.Leaves, UnwatchedLeaves: it.Leaves - it.WatchedLeaves}
	}
	return out
}

func itemShapes(items []db.Item) []Item {
	out := make([]Item, len(items))
	for n, it := range items {
		out[n] = itemShape(it)
	}
	return out
}

func detailShape(d db.ItemDetail) Item {
	out := itemShape(d.Item)
	out.OriginalTitle, out.Summary, out.Tagline, out.Edition = d.OriginalTitle, d.Summary, d.Tagline, d.Edition
	out.Available, out.CreditTotal = &d.Available, &d.CreditTotal
	if d.CriticRating != 0 {
		if out.Ratings == nil {
			out.Ratings = &Ratings{}
		}
		out.Ratings.Critic = d.CriticRating
	}
	for _, ref := range []struct {
		from *db.ItemRef
		to   **ItemRef
	}{{d.Parent, &out.Parent}, {d.Root, &out.Root}} {
		if ref.from != nil {
			*ref.to = &ItemRef{ID: ref.from.PublicID, Kind: ref.from.Kind.String(), Title: ref.from.Title, Index: ref.from.Index}
		}
	}
	for _, tag := range d.Tags {
		out.Tags = append(out.Tags, Tag{ID: tag.PublicID, Kind: tag.Kind, Name: tag.Name})
	}
	if len(d.ExternalIDs) > 0 {
		out.ExternalIDs = map[string]string{}
		for _, ext := range d.ExternalIDs {
			out.ExternalIDs[ext.Provider] = ext.ID
		}
	}
	out.Credits = creditShapes(d.Credits)
	for _, v := range d.Versions {
		m := Media{ID: v.PublicID, Name: v.Name, Edition: v.Edition, Container: v.Container, DurationMs: v.DurationMS, Bitrate: v.Bitrate,
			Size: v.Size, Width: v.Width, Height: v.Height, VideoCodec: v.VideoCodec, AudioCodec: v.AudioCodec,
			AudioChannels: v.AudioChannels, HDR: v.HDR, Parts: []Part{}}
		for _, p := range v.Parts {
			m.Parts = append(m.Parts, Part{Size: p.Size, StartMs: p.StartMS, EndMs: p.EndMS, Streams: streamShapes(p.Streams)})
		}
		for _, k := range v.Markers {
			m.Markers = append(m.Markers, Marker{Kind: k.Kind.String(), StartMs: k.StartMS, EndMs: k.EndMS, Title: k.Title})
		}
		out.Versions = append(out.Versions, m)
	}
	return out
}

func streamShapes(streams []db.Stream) []Stream {
	out := make([]Stream, len(streams))
	for index, s := range streams {
		out[index] = Stream{Index: index, Kind: s.Kind.String(), Codec: s.Codec, Profile: s.Profile,
			Language: s.Language, Title: s.Title, Default: s.Default, Forced: s.Forced, HearingImpaired: s.HearingImpaired,
			Bitrate: s.Bitrate, Width: s.Width, Height: s.Height, FrameRate: s.FrameRate, BitDepth: s.BitDepth, HDR: s.HDR,
			Channels: s.Channels, ChannelLayout: s.ChannelLayout, SampleRate: s.SampleRate}
	}
	return out
}

func creditShapes(credits []db.Credit) []Credit {
	out := make([]Credit, len(credits))
	for n, c := range credits {
		out[n] = Credit{PersonID: c.PersonID, Name: c.Name, Role: c.Role.String(), Character: c.Character}
		if c.Image != nil && c.Image.FileHash != "" {
			out[n].Image = "/v1/images/" + c.Image.FileHash
		}
	}
	return out
}
