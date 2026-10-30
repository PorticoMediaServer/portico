package mediaanalysis

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// Trickplay delivery reuses the analysis artifact store. A sheet and its
// descriptor are ordinary immutable artifacts of the trickplay stage, so
// retention, custody leases and source fencing are the existing ones.
const (
	trickplaySheetKind      = "trickplay_tiles"
	trickplayDescriptorKind = "trickplay_set"
	maxTrickplaySheetBytes  = 4 << 20
	maxChapterImageBytes    = 2 << 20
	// A sheet stays inside decoder-friendly dimensions and one screenful of
	// frames, so a scrub preview never downloads an unbounded image.
	maxSheetPixels = 4096
	maxSheetFrames = 100
)

// TrickplayDescriptor is the durable geometry of one produced set. It is
// written once as an artifact and repeated in the stage summary so listing a
// set never has to open stored bytes.
type TrickplayDescriptor struct {
	Version    int    `json:"version"`
	TileWidth  int    `json:"tileWidth"`
	TileHeight int    `json:"tileHeight"`
	Columns    int    `json:"columns"`
	Rows       int    `json:"rows"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	IntervalUS string `json:"intervalUS"`
	FrameCount int    `json:"frameCount"`
	TileCount  int    `json:"tileCount"`
	DurationUS string `json:"durationUS"`
}
type trickplaySummary struct {
	TrickplayDescriptor
	Sampled bool `json:"sampled"`
}

func (d TrickplayDescriptor) interval() int64 { n, _ := parseUS(d.IntervalUS); return n }
func (d TrickplayDescriptor) duration() int64 { n, _ := parseUS(d.DurationUS); return n }
func (d TrickplayDescriptor) perSheet() int   { return d.Columns * d.Rows }
func (d TrickplayDescriptor) valid() bool {
	if d.Version != 1 || d.TileWidth < 16 || d.TileHeight < 16 || d.Columns < 1 || d.Rows < 1 {
		return false
	}
	if d.Width != d.Columns*d.TileWidth || d.Height != d.Rows*d.TileHeight || d.Width > maxSheetPixels || d.Height > maxSheetPixels {
		return false
	}
	if d.FrameCount < 1 || d.FrameCount > 65536 || d.perSheet() > maxSheetFrames {
		return false
	}
	if d.TileCount != (d.FrameCount+d.perSheet()-1)/d.perSheet() {
		return false
	}
	return d.interval() > 0 && d.duration() > 0
}

type trickplayLayout struct {
	TileWidth, TileHeight   int
	Columns, Rows, PerSheet int
	IntervalUS              int64
	MaxFrames               int
}

// trickplayGeometry folds the owner's library policy into the operator's frame
// budget. The smaller bound always wins; neither is inferred from the media.
func trickplayGeometry(policy catalog.TrickplaySettings, budget int) trickplayLayout {
	p := policy.Normalized()
	width := p.TileWidth
	height := width * 9 / 16
	if height%2 != 0 {
		height++
	}
	columns := max(1, min(10, maxSheetPixels/width))
	rows := max(1, min(10, maxSheetPixels/height))
	for columns*rows > maxSheetFrames {
		if rows > 1 {
			rows--
			continue
		}
		columns--
	}
	frames := p.MaxTiles
	if budget > 0 {
		frames = min(frames, budget)
	}
	return trickplayLayout{TileWidth: width, TileHeight: height, Columns: columns, Rows: rows, PerSheet: columns * rows, IntervalUS: int64(p.IntervalSeconds) * 1000000, MaxFrames: max(1, frames)}
}
func (g trickplayLayout) descriptor(frames int, interval, duration int64) TrickplayDescriptor {
	return TrickplayDescriptor{Version: 1, TileWidth: g.TileWidth, TileHeight: g.TileHeight, Columns: g.Columns, Rows: g.Rows,
		Width: g.Columns * g.TileWidth, Height: g.Rows * g.TileHeight, IntervalUS: fmt.Sprint(interval),
		FrameCount: frames, TileCount: (frames + g.PerSheet - 1) / g.PerSheet, DurationUS: fmt.Sprint(duration)}
}

type trickplaySheet struct {
	layout trickplayLayout
	canvas *image.RGBA
	frames int
}

func newTrickplaySheet(g trickplayLayout) *trickplaySheet {
	return &trickplaySheet{layout: g, canvas: image.NewRGBA(image.Rect(0, 0, g.Columns*g.TileWidth, g.Rows*g.TileHeight))}
}
func (s *trickplaySheet) width() int  { return s.layout.Columns * s.layout.TileWidth }
func (s *trickplaySheet) height() int { return s.layout.Rows * s.layout.TileHeight }
func (s *trickplaySheet) place(frame image.Image) {
	column, row := s.frames%s.layout.Columns, s.frames/s.layout.Columns
	at := image.Rect(column*s.layout.TileWidth, row*s.layout.TileHeight, (column+1)*s.layout.TileWidth, (row+1)*s.layout.TileHeight)
	draw.Draw(s.canvas, at, frame, image.Point{}, draw.Src)
	s.frames++
}
func (s *trickplaySheet) reset() {
	draw.Draw(s.canvas, s.canvas.Bounds(), image.Black, image.Point{}, draw.Src)
	s.frames = 0
}
func (s *trickplaySheet) encode() ([]byte, error) {
	var data bytes.Buffer
	if e := jpeg.Encode(&data, s.canvas, &jpeg.Options{Quality: 75}); e != nil {
		return nil, e
	}
	if data.Len() > maxTrickplaySheetBytes {
		return nil, ErrBudget
	}
	return data.Bytes(), nil
}

// TrickplaySet is the viewer-facing descriptor of one produced set.
type TrickplaySet struct {
	ID              string  `json:"id"`
	SourceID        string  `json:"sourceId"`
	SourceRevision  string  `json:"sourceRevision"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	TileWidth       int     `json:"tileWidth"`
	TileHeight      int     `json:"tileHeight"`
	Columns         int     `json:"columns"`
	Rows            int     `json:"rows"`
	IntervalSeconds float64 `json:"intervalSeconds"`
	DurationSeconds float64 `json:"durationSeconds"`
	// SourceOffsetSeconds is where this item begins inside the captured source.
	// Frames are captured over the whole source, so a trimmed association must
	// add this offset before choosing a frame. The WebVTT track already has it
	// applied; this field exists for clients that index frames themselves.
	SourceOffsetSeconds float64 `json:"sourceOffsetSeconds"`
	TileCount           int     `json:"tileCount"`
	FrameCount          int     `json:"frameCount"`
	Stale               bool    `json:"stale"`
	Revision            string  `json:"revision"`
	TilesURL            string  `json:"tilesUrl"`
	ThumbnailsURL       string  `json:"thumbnailsUrl"`
}
type TrickplayView struct {
	Scope Scope          `json:"scope"`
	Sets  []TrickplaySet `json:"sets"`
}

// Image is a single stored preview delivered to a viewer as bytes.
type Image struct {
	MIME   string
	Digest string
	Data   []byte
}

type storedSet struct {
	resultID, assetID    string
	sourceRevision       string
	currentRevision      string
	incarnation          string
	configuration        int64
	resultIncarnation    string
	resultConfiguration  int64
	start, end, duration int64
	descriptor           TrickplayDescriptor
}

func (v storedSet) stale() bool {
	return v.sourceRevision != v.currentRevision || v.resultIncarnation != v.incarnation || v.resultConfiguration != v.configuration
}
func (v storedSet) revision() string {
	return token(v.resultID, v.sourceRevision, v.currentRevision, v.incarnation, strconv.FormatInt(v.configuration, 10))
}

const trickplaySetQuery = `SELECT r.id,a.token,r.source_revision,o.revision,src.incarnation,src.generation,r.root_incarnation,r.configuration_generation,r.summary_json,ia.start_seconds,COALESCE(ia.end_seconds,a.duration),a.duration
 FROM catalog_entities item CROSS JOIN catalog_asset_links ia ON ia.entity_id=item.id CROSS JOIN catalog_assets a ON a.id=ia.asset_id
 CROSS JOIN inventory_objects o INDEXED BY inventory_objects_asset ON o.asset_id=a.token
 JOIN library_sources src ON src.id=o.source_id
 JOIN analysis_results r ON r.object_id=o.id AND r.stage='trickplay' AND r.retired_ms=0
 JOIN analysis_heads h ON h.result_id=r.id AND h.object_id=r.object_id AND h.source_revision=r.source_revision AND h.stage='trickplay'
 WHERE item.public_id=pid_blob(?) AND src.library_id=? AND a.available=1 AND o.state='available' AND o.retired=0 AND o.root_incarnation=src.incarnation AND src.enabled=1`

func scanStoredSet(row interface{ Scan(...any) error }) (storedSet, error) {
	var v storedSet
	var summary string
	var start, end, duration float64
	if e := row.Scan(&v.resultID, &v.assetID, &v.sourceRevision, &v.currentRevision, &v.incarnation, &v.configuration, &v.resultIncarnation, &v.resultConfiguration, &summary, &start, &end, &duration); e != nil {
		return v, e
	}
	var parsed trickplaySummary
	if e := json.Unmarshal([]byte(summary), &parsed); e != nil {
		return v, ErrUnsupported
	}
	v.descriptor = parsed.TrickplayDescriptor
	if !v.descriptor.valid() {
		return v, ErrUnsupported
	}
	var e error
	if v.start, e = exactUS(start); e != nil {
		return v, e
	}
	if v.end, e = exactUS(end); e != nil {
		return v, e
	}
	if v.duration, e = exactUS(duration); e != nil {
		return v, e
	}
	if v.end <= v.start || v.end > v.duration {
		return v, ErrConflict
	}
	return v, nil
}
func (v storedSet) projection(itemID string) TrickplaySet {
	d := v.descriptor
	return TrickplaySet{ID: v.resultID, SourceID: v.assetID, SourceRevision: v.sourceRevision,
		Width: d.Width, Height: d.Height, TileWidth: d.TileWidth, TileHeight: d.TileHeight,
		Columns: d.Columns, Rows: d.Rows, IntervalSeconds: float64(d.interval()) / 1e6,
		DurationSeconds: float64(v.end-v.start) / 1e6, SourceOffsetSeconds: float64(v.start) / 1e6,
		TileCount: d.TileCount, FrameCount: d.FrameCount,
		Stale: v.stale(), Revision: v.revision(),
		TilesURL:      TrickplayTilesPath(itemID, v.resultID),
		ThumbnailsURL: TrickplayThumbnailsPath(itemID, v.resultID)}
}

// TrickplayTilesPath and TrickplayThumbnailsPath are the published delivery
// URLs. They are item-scoped and carry the same authorization as item art.
func TrickplayTilesPath(item, set string) string {
	return "/v1/items/" + item + "/trickplay/" + set + "/tiles"
}
func TrickplayThumbnailsPath(item, set string) string {
	return "/v1/items/" + item + "/trickplay/" + set + "/thumbnails.vtt"
}

// ChapterImagePath is the published chapter thumbnail URL for one chapter of
// one source. An absent image means no URL, never a broken link.
func ChapterImagePath(item, chapter string) string {
	return "/v1/items/" + item + "/chapters/" + chapter + "/image"
}

func (s *Service) Trickplay(ctx context.Context, a Access) (TrickplayView, error) {
	out := TrickplayView{Scope: a.scope(Target{}), Sets: []TrickplaySet{}}
	gated, e := s.begin(ctx, a)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	rows, e := tx.QueryContext(ctx, trickplaySetQuery+` ORDER BY ia.part_index,a.token,r.created_ms DESC LIMIT 33`, a.ItemID, a.LibraryID)
	if e != nil {
		return out, e
	}
	sets := []storedSet{}
	for rows.Next() {
		v, err := scanStoredSet(rows)
		if err != nil {
			// One unreadable descriptor must not remove every other set.
			if errors.Is(err, ErrUnsupported) || errors.Is(err, ErrConflict) || errors.Is(err, ErrInput) {
				continue
			}
			rows.Close()
			return out, err
		}
		sets = append(sets, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(sets) > 32 {
		return out, ErrBudget
	}
	for _, v := range sets {
		out.Sets = append(out.Sets, v.projection(a.ItemID))
	}
	return out, gated.Commit()
}
func (s *Service) storedSetTx(ctx context.Context, tx *sql.Tx, a Access, set string) (storedSet, error) {
	if len(set) != 64 {
		return storedSet{}, ErrInput
	}
	return scanStoredSet(tx.QueryRowContext(ctx, trickplaySetQuery+` AND r.id=? LIMIT 1`, a.ItemID, a.LibraryID, set))
}

// Thumbnails renders the WebVTT image track for one set. Cue times are item
// relative, so a trimmed association never points a scrubber at source time.
func (s *Service) Thumbnails(ctx context.Context, a Access, set string) (string, error) {
	gated2, e := s.begin(ctx, a)
	if e != nil {
		return "", e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	v, e := s.storedSetTx(ctx, tx, a, set)
	if e != nil {
		return "", e
	}
	if e = gated2.Commit(); e != nil {
		return "", e
	}
	return trickplayVTT(v.descriptor, v.start, v.end), nil
}
func trickplayCue(us int64) string {
	ms := us / 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}
func trickplayVTT(d TrickplayDescriptor, start, end int64) string {
	var out strings.Builder
	out.WriteString("WEBVTT\n")
	interval, duration := d.interval(), d.duration()
	for i := 0; i < d.FrameCount; i++ {
		from, to := int64(i)*interval, min(duration, int64(i+1)*interval)
		if to <= start || from >= end || to <= from {
			continue
		}
		tile, within := i/d.perSheet(), i%d.perSheet()
		x, y := (within%d.Columns)*d.TileWidth, (within/d.Columns)*d.TileHeight
		out.WriteString("\n")
		out.WriteString(trickplayCue(max(from, start) - start))
		out.WriteString(" --> ")
		out.WriteString(trickplayCue(min(to, end) - start))
		out.WriteString("\n")
		fmt.Fprintf(&out, "tiles/%d.jpg#xywh=%d,%d,%d,%d\n", tile, x, y, d.TileWidth, d.TileHeight)
	}
	return out.String()
}

// Tile delivers one sprite sheet. The set is re-resolved inside the read so a
// retired result or replaced inventory cannot be served from a stale handle.
func (s *Service) Tile(ctx context.Context, a Access, set string, index int) (Image, error) {
	if index < 0 || index > 65535 {
		return Image{}, ErrInput
	}
	return s.artifactImage(ctx, a, func(ctx context.Context, tx *sql.Tx) (string, string, int, int64, error) {
		v, e := s.storedSetTx(ctx, tx, a, set)
		if e != nil {
			return "", "", 0, 0, e
		}
		if index >= v.descriptor.TileCount {
			return "", "", 0, 0, sql.ErrNoRows
		}
		return v.resultID, trickplaySheetKind, index, maxTrickplaySheetBytes, nil
	})
}

// ChapterImage delivers the preview captured at one chapter boundary. The
// chapter identity is the playing projection's own `{sourceId}:{index}`.
func (s *Service) ChapterImage(ctx context.Context, a Access, chapter string) (Image, error) {
	source, raw, ok := strings.Cut(chapter, ":")
	if !ok || source == "" || len(chapter) > 512 {
		return Image{}, ErrInput
	}
	index, e := strconv.Atoi(raw)
	if e != nil || index < 1 || index > 4096 || raw != strconv.Itoa(index) {
		return Image{}, ErrInput
	}
	return s.artifactImage(ctx, a, func(ctx context.Context, tx *sql.Tx) (string, string, int, int64, error) {
		v, e := s.resolve(ctx, tx, a, Target{SourceID: source})
		if e != nil {
			return "", "", 0, 0, e
		}
		if v.NeedsProbe {
			return "", "", 0, 0, ErrConflict
		}
		var result string
		e = tx.QueryRowContext(ctx, `SELECT r.id FROM analysis_heads h JOIN analysis_results r ON r.id=h.result_id WHERE h.object_id=? AND h.source_revision=? AND h.stage='chapter_images' AND r.root_incarnation=? AND r.configuration_generation=? AND r.retired_ms=0 AND (?='' OR r.evidence=?)`, v.ObjectID, v.Revision, v.Incarnation, v.Configuration, v.Evidence, v.Evidence).Scan(&result)
		if e != nil {
			return "", "", 0, 0, e
		}
		// Produced ordinals follow chapter_index order from one, zero based.
		return result, "chapter_images", index - 1, maxChapterImageBytes, nil
	})
}

// artifactImage holds the publication gate across the row read and the reader
// lease, so retention can never reclaim the object between the two.
func (s *Service) artifactImage(ctx context.Context, a Access, locate func(context.Context, *sql.Tx) (string, string, int, int64, error)) (Image, error) {
	s.publication.Lock()
	unlock := func() { s.publication.Unlock() }
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	gated3, e := s.begin(ctx, a)
	if e != nil {
		return Image{}, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	result, kind, ordinal, limit, e := locate(ctx, tx)
	if e != nil {
		return Image{}, e
	}
	var obj mediaartifact.Object
	var mime string
	e = tx.QueryRowContext(ctx, `SELECT digest,size,mime FROM analysis_artifacts WHERE result_id=? AND kind=? AND ordinal=?`, result, kind, ordinal).Scan(&obj.Digest, &obj.Size, &mime)
	if e != nil {
		return Image{}, e
	}
	if mime != "image/jpeg" {
		return Image{}, ErrUnsupported
	}
	if obj.Size <= 0 || obj.Size > limit {
		return Image{}, ErrBudget
	}
	release, e := s.custody.read(obj.Digest)
	if e != nil {
		if errors.Is(e, livechannels.ErrPhysicalBusy) {
			return Image{}, ErrConflict
		}
		return Image{}, e
	}
	defer release()
	reader, e := s.artifacts.Open(ctx, obj)
	if e == nil {
		e = gated3.Commit()
	}
	unlock()
	unlock = nil
	if reader != nil {
		defer reader.Close()
	}
	if e != nil {
		return Image{}, e
	}
	data, e := io.ReadAll(io.LimitReader(reader, obj.Size+1))
	if e != nil {
		return Image{}, e
	}
	if int64(len(data)) != obj.Size {
		return Image{}, mediaartifact.ErrIdentity
	}
	return Image{MIME: mime, Digest: obj.Digest, Data: data}, nil
}
