package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strconv"
	"time"
)

// TitleFile is one file of a title as its Files list reads: where it is (owners only), how big
// it is and every track in it. Only the lazy File info read carries it.
type TitleFile struct {
	Disc      *DiscMediaFacts `json:"disc,omitempty"`
	ItemID    string          `json:"itemId,omitempty"`
	ID        string          `json:"id"`
	Path      string          `json:"path,omitempty"`
	Size      int64           `json:"size"`
	Container string          `json:"container"`
	Duration  float64         `json:"duration"`
	BitRate   int64           `json:"bitRate,omitempty"`
	Available bool            `json:"available"`
	// PartIndex is 0 for a whole title. Files with the same part index are
	// versions of one another; the first listed is the one played by default.
	PartIndex int `json:"partIndex"`
	// Edition is the version's edition label where its name or local metadata
	// gave one.
	Edition   string           `json:"edition,omitempty"`
	Video     *TitleFileVideo  `json:"video,omitempty"`
	Audio     []TitleFileTrack `json:"audio"`
	Subtitles []TitleFileTrack `json:"subtitles"`
}

type TitleFileVideo struct {
	Codec  string `json:"codec"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// DynamicRange is one of the assets.Range constants ("sdr", "hdr10", "hlg", "dolby_vision").
	DynamicRange string  `json:"dynamicRange,omitempty"`
	HDR10Plus    bool    `json:"hdr10Plus,omitempty"`
	FrameRate    float64 `json:"frameRate,omitempty"`
	BitDepth     int     `json:"bitDepth,omitempty"`
	BitRate      int64   `json:"bitRate,omitempty"`
}

// TitleFileTrack is an audio or subtitle track. External is a subtitle file beside the media.
type TitleFileTrack struct {
	Language      string `json:"language,omitempty"`
	Codec         string `json:"codec"`
	Title         string `json:"title,omitempty"`
	Channels      int    `json:"channels,omitempty"`
	ChannelLayout string `json:"channelLayout,omitempty"`
	ObjectAudio   string `json:"objectAudio,omitempty"`
	BitRate       int64  `json:"bitRate,omitempty"`
	Default       bool   `json:"default,omitempty"`
	Forced        bool   `json:"forced,omitempty"`
	External      bool   `json:"external,omitempty"`
}

// DetailLanguages is the union across versions, without file identity or facts.
type DetailLanguages struct {
	Audio     []string `json:"audio"`
	Subtitles []string `json:"subtitles"`
}

type FileInfoManage struct {
	Delete bool `json:"delete"`
	Split  bool `json:"split"`
	Merge  bool `json:"merge"`
}

type FileInfo struct {
	Scope    DetailScope     `json:"scope"`
	Revision ContentRevision `json:"revision"`
	Kind     string          `json:"kind"`
	Files    []TitleFile     `json:"files"`
	// Disc is the owner-only ordered title list of a disc image's current scan.
	// Viewer reads never carry it; the block is absent when no probe is stored.
	Disc       *FileInfoDisc   `json:"disc,omitempty"`
	Manage     *FileInfoManage `json:"manage,omitempty"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// FileInfoDisc names the disc image's own ordered titles for one exact file.
// FileID is the same TitleFile ID the caller received, so a multi-asset title
// binds the block to the file that was actually scanned. Positions are the stored
// stable Title N order; chapters and angles are the probe's own counts.
type FileInfoDisc struct {
	FileID string              `json:"fileId"`
	Kind   FileInfoDiscKind    `json:"kind"`
	Titles []FileInfoDiscTitle `json:"titles"`
}

// FileInfoDiscKind is the closed wire set the disc protocol publishes.
type FileInfoDiscKind string

func (FileInfoDiscKind) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []any{"bluray", "dvd"}}
}

type FileInfoDiscTitle struct {
	Number     int   `json:"number"`
	DurationMs int64 `json:"durationMs"`
	Chapters   int   `json:"chapters"`
	Angles     int   `json:"angles"`
}

func (s *Service) detailLanguages(item string) (DetailLanguages, error) {
	out := DetailLanguages{Audio: []string{}, Subtitles: []string{}}
	rows, err := s.read().Query(`SELECT DISTINCT type,language FROM (SELECT st.type,st.language FROM catalog_asset_links l JOIN asset_streams st ON st.asset_id=(SELECT token FROM catalog_assets WHERE id=l.asset_id) WHERE l.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?1)) AND st.type='audio' UNION SELECT 'subtitle',st.language FROM catalog_asset_links l JOIN asset_subtitles st ON st.asset_id=(SELECT token FROM catalog_assets WHERE id=l.asset_id) WHERE l.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?1))) WHERE language<>'' ORDER BY type,language`, item)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, language string
		if err = rows.Scan(&kind, &language); err != nil {
			return out, err
		}
		if kind == "audio" {
			out.Audio = append(out.Audio, language)
		} else {
			out.Subtitles = append(out.Subtitles, language)
		}
	}
	return out, rows.Err()
}

// FileInfo reads technical facts only when asked. Album/book reads page their
// child titles; every child's visibility is checked before its files are read.
func (s *Service) FileInfo(viewer Viewer, server, item, cursor string, limit int, owner bool) (FileInfo, error) {
	out := FileInfo{Files: []TitleFile{}}
	var library, kind string
	if err := s.read().QueryRow(`SELECT l.library_id,k.name FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?) AND e.retired=0 AND l.retired=0`, item).Scan(&library, &kind); err != nil {
		return out, err
	}
	namespace := "item"
	if kind == "album" || kind == "book" {
		namespace = kind
	}
	if err := s.VisibleEntity(s.Context(), viewer, namespace, item); err != nil {
		return out, err
	}
	revision, err := s.ContentRevision(library, viewer.Profile)
	if err != nil {
		return out, err
	}
	out.Scope = DetailScope{server, library, item, viewer.Fence}
	out.Revision = revision
	out.Kind = kind
	if owner {
		out.Manage = &FileInfoManage{Delete: true, Split: kind == "movie" || kind == "song" || kind == "audiobook_file", Merge: true}
	}
	ids := []string{item}
	if namespace != "item" {
		if limit == 0 {
			limit = 100
		}
		if limit < 1 || limit > 200 {
			return out, ErrCursor
		}
		scope := cursorScope{Library: library, Profile: viewer.Profile, Viewer: viewer.Fence, View: "file_info", Entity: item, Limit: limit}
		var after int64
		if cursor != "" {
			c, e := s.decodeDetailCursor(cursor, scope, revision)
			if e != nil {
				return out, e
			}
			after, e = strconv.ParseInt(c.Value, 10, 64)
			if e != nil || after < 0 {
				return out, ErrCursor
			}
		}
		visible, args := viewer.itemVisibilitySQL("e.id")
		values := append([]any{item, after}, args...)
		values = append(values, limit+1)
		rows, e := s.read().Query(`SELECT pid(e.public_id),e.id FROM catalog_entities e WHERE e.parent_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND e.id>? AND `+visible+` ORDER BY e.id LIMIT ?`, values...)
		if e != nil {
			return out, e
		}
		ids = []string{}
		keys := []int64{}
		for rows.Next() {
			var id string
			var key int64
			if e = rows.Scan(&id, &key); e != nil {
				rows.Close()
				return out, e
			}
			ids = append(ids, id)
			keys = append(keys, key)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if len(ids) > limit {
			ids = ids[:limit]
			out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: strconv.FormatInt(keys[limit-1], 10), Expires: time.Now().Add(30 * time.Minute).Unix()}, revision)
			if err != nil {
				return out, err
			}
		}
	} else if cursor != "" {
		return out, ErrCursor
	}
	out.Files, err = s.titleFiles(ids, owner)
	if err != nil {
		return out, err
	}
	if owner {
		out.Disc = s.discTitles(out.Files)
	}
	after, err := s.ContentRevision(library, viewer.Profile)
	if err != nil {
		return out, err
	}
	if after != revision {
		return out, ErrStaleContinuation
	}
	return out, nil
}

// discTitles reads the stored probe that belongs to one exact current asset in this
// same authorized snapshot. A disc with no stored probe, or a stale source, has no
// block: the caller already authorized the item, and this grants no further access.
func (s *Service) discTitles(files []TitleFile) *FileInfoDisc {
	tx := dbwork.Snapshot(s.Context())
	if tx == nil {
		return nil
	}
	// Resolve the original first available observation for each file together:
	// the lowest current object ID is chosen before its probe is tested, so a
	// later facts-bearing observation never stands in for it.
	// Non-disc files require no per-file probe lookup. A disc probe is still
	// decoded and fingerprint-checked by its existing exact-observation reader.
	type observation struct{ object, revision string }
	observations := map[string]observation{}
	const chunk = 128
	for from := 0; from < len(files); from += chunk {
		tokens := make([]string, 0, chunk)
		for _, file := range files[from:min(from+chunk, len(files))] {
			tokens = append(tokens, file.ID)
		}
		rows, err := tx.QueryContext(s.Context(), `SELECT requested.value,o.id,o.revision
 FROM json_each(?) requested
 JOIN inventory_objects o ON o.id=(SELECT first.id FROM inventory_objects first JOIN catalog_assets a ON a.token=first.asset_id WHERE a.token=requested.value AND first.retired=0 AND first.state='available' ORDER BY first.id LIMIT 1)
 JOIN inventory_disc_facts d ON d.object_id=o.id`, idsJSON(tokens))
		if err != nil {
			return nil
		}
		for rows.Next() {
			var token string
			var value observation
			if err = rows.Scan(&token, &value.object, &value.revision); err != nil {
				rows.Close()
				return nil
			}
			observations[token] = value
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil
		}
	}
	for _, file := range files {
		original, ok := observations[file.ID]
		if !ok {
			continue
		}
		object, revision := original.object, original.revision
		probe, err := ReadInventoryDiscProbe(s.Context(), tx, object, revision)
		if err != nil || len(probe.Titles) == 0 || (probe.Kind != "bluray" && probe.Kind != "dvd") {
			continue
		}
		out := &FileInfoDisc{FileID: file.ID, Kind: FileInfoDiscKind(probe.Kind), Titles: make([]FileInfoDiscTitle, 0, len(probe.Titles))}
		for position, title := range probe.OrderedTitles() {
			out.Titles = append(out.Titles, FileInfoDiscTitle{Number: position + 1, DurationMs: title.DurationMs, Chapters: len(title.Chapters), Angles: title.AngleCount})
		}
		return out
	}
	return nil
}

// Detail rows never silently reuse an old ordering after a publication.
func (s *Service) decodeDetailCursor(raw string, scope cursorScope, revision ContentRevision) (cursorValue, error) {
	value, err := s.decodeRevisionCursor(raw, scope, revision)
	if err != nil {
		return value, err
	}
	if value.CatalogRevision != revision.Catalog || value.ViewerRevision != revision.Viewer {
		return cursorValue{}, ErrStaleContinuation
	}
	return value, nil
}
