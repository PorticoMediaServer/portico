package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
)

// A download is more than its media file. The bundle is everything else a
// device needs to list the title, show it and play it properly with no server
// in reach: what it is and where it sits in its show, album or book, its
// description, its artwork, its chapters and skip markers, its subtitle tracks
// and its audio tracks. The facts are in the document; the files (artwork,
// subtitles) are named in it and fetched under the same transfer address as
// the media file, so a device that can fetch the one can fetch the others and
// needs no second kind of permission.

// BundleFile is one file that travels with a download. Path is relative to the
// transfer address the device holds for the download.
type BundleFile struct {
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
}

// BundleArtwork is one picture. Poster is the title's own face (for an episode
// the season's or show's poster, for a song its album cover); still is an
// episode's own frame; backdrop is the wide picture behind a title page.
type BundleArtwork struct {
	Role string `json:"role"`
	BundleFile
}

type BundleChapter struct {
	Index        int     `json:"index"`
	Title        string  `json:"title"`
	StartSeconds float64 `json:"startSeconds"`
	EndSeconds   float64 `json:"endSeconds"`
	// Image is the chapter's thumbnail, when the library makes them. A device
	// that is told it is missing simply shows the chapter without one.
	Image *BundleFile `json:"image,omitempty"`
}

// BundleTrickplay is the scrubbing preview set of the downloaded file: sprite
// sheets of frames every IntervalSeconds, TileWidth by TileHeight each, Columns
// by Rows to a sheet, and the WebVTT track that places each frame in time.
type BundleTrickplay struct {
	ID              string  `json:"id"`
	TileWidth       int     `json:"tileWidth"`
	TileHeight      int     `json:"tileHeight"`
	Columns         int     `json:"columns"`
	Rows            int     `json:"rows"`
	IntervalSeconds float64 `json:"intervalSeconds"`
	FrameCount      int     `json:"frameCount"`
	// DurationSeconds is the title's length the frames cover; SourceOffsetSeconds
	// is where the title begins inside the captured file (frames are indexed
	// from the file's start).
	DurationSeconds     float64      `json:"durationSeconds"`
	SourceOffsetSeconds float64      `json:"sourceOffsetSeconds"`
	Thumbnails          BundleFile   `json:"thumbnails"`
	Tiles               []BundleFile `json:"tiles"`
}

// MaxBundleTrickplayTiles bounds the sheets a download carries (a sheet holds
// up to a hundred frames, so this is hours of video at the usual interval).
const MaxBundleTrickplayTiles = 128

// MaxBundleChapterImages bounds the chapter thumbnails a download carries.
const MaxBundleChapterImages = 200

// TrickplayThumbnailsPath, TrickplayTilePath and ChapterImagePath are where a
// download's previews are fetched, relative to the transfer address.
func TrickplayThumbnailsPath(set string) string { return "trickplay/" + set + "/thumbnails.vtt" }
func TrickplayTilePath(set string, tile int) string {
	return "trickplay/" + set + "/tiles/" + strconv.Itoa(tile) + ".jpg"
}
func ChapterImagePath(source string, index int) string {
	return "chapters/" + source + "/" + strconv.Itoa(index) + "/image"
}

// BundleMarker is a stretch a viewer may skip: an intro, a recap, credits.
// Automatic says the server would skip it unprompted for a viewer who has
// asked for that; a marker without it is offered as a button only.
type BundleMarker struct {
	Kind         string  `json:"kind"`
	StartSeconds float64 `json:"startSeconds"`
	EndSeconds   float64 `json:"endSeconds"`
	Automatic    bool    `json:"automatic"`
}

// BundleAudio describes one audio track inside the downloaded file, in the
// order the file holds them.
type BundleAudio struct {
	Index    int    `json:"index"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
	Default  bool   `json:"default"`
}

// BundleSubtitle is one subtitle track and the forms it can be fetched in.
type BundleSubtitle struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Default  bool   `json:"default"`
	Forced   bool   `json:"forced"`
	// Files is keyed by form. "vtt" is the track as plain text, present for
	// every text track and for a styled one. "ass", "pgs", "vobsub" and "dvb"
	// are the track as it was authored, for a device that draws it itself;
	// "sub" is the pictures beside a VobSub index; "font0" onward are a styled
	// script's attached fonts.
	Files map[string]BundleFile `json:"files"`
	// Lines is an image track's line count and how many are forced (PGS,
	// VobSub), so an offline player offers "Forced only" exactly where the
	// streaming one does.
	Lines *BundleLines `json:"lines,omitempty"`
}

// BundleLines counts an image subtitle track's lines.
type BundleLines struct {
	Forced int `json:"forced"`
	Total  int `json:"total"`
}

// BundleMedia is the downloaded file itself, as a player needs to know it
// before opening it.
type BundleMedia struct {
	Container  string `json:"container"`
	VideoCodec string `json:"videoCodec,omitempty"`
	AudioCodec string `json:"audioCodec,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

// Source and schema share the 2MiB UTF-8 byte ceiling. JSON Schema's
// maxLength is supplemented by this explicit byte constraint and source parser.
type PublicationJSON string

func (PublicationJSON) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "maxLength": 2 << 20, "x-maxUtf8Bytes": 2 << 20, "contentMediaType": "application/json"}
}

type Bundle struct {
	// Exact stored UTF-8 descriptor; its digest is bound by the signed receipt.
	Publication   PublicationJSON  `json:"publication,omitempty"`
	PreparationID string           `json:"preparationId"`
	ItemID        string           `json:"itemId"`
	Quality       string           `json:"quality"`
	QualityLabel  string           `json:"qualityLabel"`
	Title         Title            `json:"title"`
	Overview      string           `json:"overview"`
	Media         BundleMedia      `json:"media"`
	Artwork       []BundleArtwork  `json:"artwork"`
	Chapters      []BundleChapter  `json:"chapters"`
	Markers       []BundleMarker   `json:"markers"`
	Audio         []BundleAudio    `json:"audio"`
	Subtitles     []BundleSubtitle `json:"subtitles"`
	// Trickplay is the file's scrubbing previews, when the library makes them.
	Trickplay *BundleTrickplay `json:"trickplay,omitempty"`
	// SourceID names the library file the chapters, markers and subtitles
	// belong to. The routes layer uses it to list subtitle tracks.
	SourceID string `json:"-"`
}

// artworkRoles is what each kind of title is listed and shown with, and the
// width each is kept at on a device: enough for a tablet, not a television.
func artworkRoles(kind string) []struct {
	role  string
	width int
} {
	type entry = struct {
		role  string
		width int
	}
	switch kind {
	case "song", "audiobook_file":
		return []entry{{"cover", 800}}
	case "episode":
		return []entry{{"poster", 800}, {"still", 800}, {"backdrop", 1920}}
	}
	return []entry{{"poster", 800}, {"backdrop", 1920}}
}

// ArtworkWidth is the width a download keeps one role of artwork at, or 0 for
// a role downloads do not carry.
func ArtworkWidth(role string) int {
	switch role {
	case "poster", "still", "cover":
		return 800
	case "backdrop":
		return 1920
	}
	return 0
}

// Bundle reads what travels with one ready download. It answers for the file
// the claim points at, so the chapters and markers are that file's.
func (s *Service) Bundle(ctx context.Context, p identity.Principal, id string) (Bundle, error) {
	out := Bundle{PreparationID: id, Artwork: []BundleArtwork{}, Chapters: []BundleChapter{}, Markers: []BundleMarker{}, Audio: []BundleAudio{}, Subtitles: []BundleSubtitle{}}
	if !validID.MatchString(id) {
		return out, ErrInput
	}
	snap, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer snap.Rollback()
	tx := snap.Tx()
	current, e := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE id=? AND profile_key=?`, id, operations.ViewerKey(p)))
	if errors.Is(e, sql.ErrNoRows) || e == nil && !current.Title.OnServer {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if e = s.visibleItem(ctx, tx, p, current.ItemID); e != nil {
		if errors.Is(e, identity.ErrContentRestricted) {
			return out, ErrNotFound
		}
		return out, e
	}
	if current.State != StateReady {
		return out, ErrNotReady
	}
	var entity int64
	if e = tx.QueryRowContext(ctx, `SELECT item_id FROM download_preparations WHERE id=?`, id).Scan(&entity); e != nil {
		return out, e
	}
	out.ItemID, out.Quality, out.QualityLabel = current.ItemID, current.Quality, qualityLabel(current.Quality)
	if doc, err := publicationTx(ctx, tx, id); err != nil {
		return out, err
	} else if doc != nil {
		out.Publication = PublicationJSON(doc.raw)
	}
	if out.Title, e = readTitle(ctx, tx, entity); e != nil {
		return out, e
	}
	// A title's description is its own; a song has none and borrows nothing.
	var overview sql.NullString
	if e = tx.QueryRowContext(ctx, `SELECT overview FROM catalog_item_details WHERE entity_id=?`, entity).Scan(&overview); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	out.Overview = overview.String
	for _, art := range artworkRoles(out.Title.Kind) {
		out.Artwork = append(out.Artwork, BundleArtwork{Role: art.role, BundleFile: BundleFile{Path: "artwork/" + art.role, ContentType: "image/*"}})
	}
	// The library file behind the download: the file itself for an original,
	// the file a converted copy was made from otherwise. The conversion keeps
	// the timeline, so that file's chapters and markers describe the copy too.
	asset := current.Artifact.Ref
	if current.Artifact.Kind == "prepared" {
		if e = tx.QueryRowContext(ctx, `SELECT asset_id FROM prepared_media_versions WHERE id=?`, current.Artifact.Ref).Scan(&asset); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return out, ErrUnavailable
			}
			return out, e
		}
	}
	out.SourceID = asset
	if e = tx.QueryRowContext(ctx, `SELECT container,video_codec,audio_codec,width,height FROM catalog_assets WHERE token=?`, asset).Scan(&out.Media.Container, &out.Media.VideoCodec, &out.Media.AudioCodec, &out.Media.Width, &out.Media.Height); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if current.Artifact.Kind == "prepared" {
		// A converted copy is what its recipe made, whatever the source was.
		var facts string
		if e = tx.QueryRowContext(ctx, `SELECT facts_json FROM prepared_media_versions WHERE id=?`, current.Artifact.Ref).Scan(&facts); e != nil {
			return out, e
		}
		out.Media = preparedFacts(facts, current.Artifact.Container)
	}
	if out.Chapters, e = readChapters(ctx, tx, entity, asset, out.Title.Kind); e != nil {
		return out, e
	}
	// A film's or episode's chapter thumbnails travel with it (a book's chapters have none).
	if out.Title.Kind != "audiobook_file" && out.Title.Kind != "song" && len(out.Chapters) > 0 {
		images, err := playback.ChapterThumbnails(ctx, tx, asset)
		if err != nil {
			return out, err
		}
		carried := 0
		for i := range out.Chapters {
			if c := out.Chapters[i]; images[c.Index] && carried < MaxBundleChapterImages {
				out.Chapters[i].Image = &BundleFile{Path: ChapterImagePath(asset, c.Index), ContentType: "image/jpeg"}
				carried++
			}
		}
	}
	if out.Markers, e = readMarkers(ctx, tx, asset); e != nil {
		return out, e
	}
	if out.Audio, e = readAudio(ctx, tx, asset, current.Artifact.Kind == "prepared"); e != nil {
		return out, e
	}
	return out, snap.Commit()
}

// SubtitlePath is where one form of one subtitle track is fetched, relative to
// the transfer address.
func SubtitlePath(id string, revision int64, form string) string {
	return "subtitles/" + id + "/" + strconv.FormatInt(revision, 10) + "." + form
}

func readChapters(ctx context.Context, tx *sql.Tx, entity int64, asset, kind string) ([]BundleChapter, error) {
	out := []BundleChapter{}
	query, arg := `SELECT chapter_index,title,start_seconds,end_seconds FROM asset_chapters WHERE asset_id=? ORDER BY chapter_index LIMIT 2000`, any(asset)
	if kind == "audiobook_file" {
		// A book's chapters are the book's, filed against the part.
		var booked bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_book_chapters WHERE file_id=?)`, entity).Scan(&booked); e != nil {
			return out, e
		}
		if booked {
			query, arg = `SELECT chapter_index,title,start_seconds,end_seconds FROM catalog_book_chapters WHERE file_id=? ORDER BY chapter_index LIMIT 2000`, any(entity)
		}
	}
	rows, e := tx.QueryContext(ctx, query, arg)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var c BundleChapter
		if e = rows.Scan(&c.Index, &c.Title, &c.StartSeconds, &c.EndSeconds); e != nil {
			return out, e
		}
		if c.EndSeconds > c.StartSeconds && c.StartSeconds >= 0 {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// readMarkers applies the viewer projection's rules to what travels offline:
// weak or ambiguous guesses stay with the owner, overlapping markers of a kind
// become the one a viewer would be shown, and a library whose detection is off
// carries only markers an owner made or approved.
func readMarkers(ctx context.Context, tx *sql.Tx, asset string) ([]BundleMarker, error) {
	out := []BundleMarker{}
	policy, e := segmentmarkers.ReadConfidencePolicy(ctx, tx)
	if e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT m.id,m.kind,m.start_us,m.end_us,m.approved,m.confidence,m.provenance,src.library_id FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id JOIN analysis_markers m ON m.object_id=o.id AND m.source_revision=o.revision
 WHERE o.asset_id=? AND o.retired=0 AND o.state='available' AND m.deleted=0 ORDER BY m.start_us,m.id LIMIT 256`, asset)
	if e != nil {
		return out, e
	}
	type stored struct {
		segmentmarkers.Candidate
		library  string
		detected bool // a detector's, not an owner's
	}
	var found []stored
	for rows.Next() {
		var m stored
		var start, end int64
		var provenance string
		if e = rows.Scan(&m.ID, &m.Kind, &start, &end, &m.Approved, &m.Confidence, &provenance, &m.library); e != nil {
			rows.Close()
			return out, e
		}
		if !segmentmarkers.ViewerKind(m.Kind) || end <= start || start < 0 || !policy.ViewerVisible(m.Approved, m.Confidence, provenance) {
			continue
		}
		m.detected = !m.Approved && !strings.HasPrefix(provenance, "owner:")
		m.StartSeconds, m.EndSeconds = float64(start)/1e6, float64(end)/1e6
		m.AutomaticSafe = policy.AutomaticSafe(m.Approved, m.Confidence, provenance)
		found = append(found, m)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	off := map[string]bool{}
	var candidates []segmentmarkers.Candidate
	for _, m := range found {
		if _, known := off[m.library]; !known {
			mode, e := mediaanalysis.SegmentLibraryModeTx(ctx, tx, m.library)
			if e != nil {
				return out, e
			}
			off[m.library] = mode == mediaanalysis.SegmentModeOff
		}
		if m.detected && off[m.library] {
			continue
		}
		candidates = append(candidates, m.Candidate)
	}
	for _, m := range segmentmarkers.Strongest(candidates) {
		out = append(out, BundleMarker{Kind: m.Kind, StartSeconds: m.StartSeconds, EndSeconds: m.EndSeconds, Automatic: m.AutomaticSafe})
	}
	return out, nil
}

func readAudio(ctx context.Context, tx *sql.Tx, asset string, converted bool) ([]BundleAudio, error) {
	out := []BundleAudio{}
	rows, e := tx.QueryContext(ctx, `SELECT language,title,codec,channels,is_default FROM asset_streams WHERE asset_id=? AND type='audio' ORDER BY stream_index LIMIT 64`, asset)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		a := BundleAudio{Index: len(out)}
		if e = rows.Scan(&a.Language, &a.Title, &a.Codec, &a.Channels, &a.Default); e != nil {
			return out, e
		}
		if converted {
			// The conversion keeps every track and makes each stereo AAC.
			a.Codec, a.Channels = "aac", 2
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func preparedFacts(raw, container string) BundleMedia {
	var facts preparedmedia.Facts
	if json.Unmarshal([]byte(raw), &facts) != nil {
		return BundleMedia{Container: container}
	}
	if facts.Container == "" {
		facts.Container = container
	}
	return BundleMedia{Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height}
}
