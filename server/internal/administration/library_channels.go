package administration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"sort"
	"strings"
	"time"

	// WebP is accepted on the way in exactly as the metadata editor's upload
	// path accepts it. Nothing is re-encoded: the stored object is the bytes the
	// owner supplied, after they have been proved decodable.
	_ "golang.org/x/image/webp"
	"github.com/PorticoMediaServer/portico/server/internal/atomicfile"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// CriterionField is one attribute a channel block can select on. The fields are
// whatever the catalog's attribute projection actually holds, so the page never
// offers a filter the library cannot answer.
type CriterionField struct {
	Field       string `json:"field"`
	Name        string `json:"name"`
	ValueCount  int64  `json:"valueCount"`
	Description string `json:"description"`
}

// ChannelLogoPaths reports only stored logo objects. A guide never advertises
// an image URL before the background import has published its bytes.
func (s *Service) ChannelLogoPaths(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if s == nil || len(ids) == 0 {
		return out, nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id FROM admin_channel_logos WHERE channel_id IN(SELECT value FROM json_each(?))`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = "/v1/library-channels/" + id + "/logo"
	}
	return out, rows.Err()
}

// criterionNames gives the projection's field keys a readable name.
var criterionNames = map[string]string{
	"genre": "Genre", "tag": "Tag", "label": "Label", "studio": "Studio", "network": "Network",
	"contentRating": "Content rating", "decade": "Decade", "year": "Year", "language": "Language",
	"country": "Country", "collection": "Collection", "mood": "Mood",
}

// CriterionValue is one selectable value with how many items carry it.
type CriterionValue struct {
	Value string `json:"value"`
	Key   string `json:"valueKey"`
	Count int64  `json:"itemCount"`
}

// CriteriaPage is one page of a field's values, or the field list itself.
type CriteriaPage struct {
	Fields     []CriterionField `json:"fields"`
	Field      string           `json:"field"`
	Values     []CriterionValue `json:"values"`
	NextCursor string           `json:"nextCursor"`
	LibraryID  string           `json:"libraryId"`
}

// Criteria lists the tag and label vocabulary a library channel can select on.
// With no field it returns the fields; with a field it pages that field's
// values, most used first.
func (s *Service) Criteria(ctx context.Context, auth Authorize, library, field, token string, limit int) (CriteriaPage, error) {
	out := CriteriaPage{Fields: []CriterionField{}, Values: []CriterionValue{}, Field: field, LibraryID: library}
	size, err := pageSize(limit)
	if err != nil {
		return out, err
	}
	if !safeText(field, 64) || !safeText(library, 128) {
		return out, ErrInput
	}
	c, err := decodeCursor("criteria", token)
	if err != nil {
		return out, err
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		if !tableExists(ctx, tx, "catalog_item_attribute_edges") {
			return nil
		}
		if library != "" {
			if _, _, err := libraryExists(ctx, tx, library); err != nil {
				return err
			}
		}
		if field == "" {
			// Playable kinds are exactly the old items table's membership
			// (movie, episode, song, audiobook file, extra).
			rows, err := tx.QueryContext(ctx, `SELECT f.field,COUNT(DISTINCT t.value_key) FROM catalog_attribute_fields f JOIN catalog_attribute_terms t ON t.field_id=f.id JOIN catalog_item_attribute_edges ed ON ed.term_id=t.id JOIN catalog_entities e ON e.id=ed.item_id JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.retired=0 AND cl.retired=0 AND (?='' OR cl.library_id=?) GROUP BY f.field ORDER BY f.field`, library, library)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var f CriterionField
				if err = rows.Scan(&f.Field, &f.ValueCount); err != nil {
					return err
				}
				if name, ok := criterionNames[f.Field]; ok {
					f.Name = name
				} else {
					f.Name = f.Field
				}
				f.Description = "Items carrying this " + strings.ToLower(f.Name)
				out.Fields = append(out.Fields, f)
			}
			return rows.Err()
		}
		// One row per value key; the display value is the smallest observed
		// spelling (the old table could return one row per spelling).
		rows, err := tx.QueryContext(ctx, `SELECT MIN(ed.source_value),t.value_key,COUNT(*) AS n FROM catalog_item_attribute_edges ed JOIN catalog_attribute_terms t ON t.id=ed.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id JOIN catalog_entities e ON e.id=ed.item_id JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 JOIN catalog_libraries cl ON cl.id=e.library_id WHERE f.field=? AND e.retired=0 AND cl.retired=0 AND (?='' OR cl.library_id=?) AND (?='' OR t.value_key>?) GROUP BY t.value_key ORDER BY t.value_key LIMIT ?`, field, library, library, c.ID, c.ID, size+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v CriterionValue
			if err = rows.Scan(&v.Value, &v.Key, &v.Count); err != nil {
				return err
			}
			out.Values = append(out.Values, v)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(out.Values) > size {
			out.Values = out.Values[:size]
			out.NextCursor = encodeCursor("criteria", 0, out.Values[size-1].Key)
		}
		return nil
	})
	return out, err
}

// BlockCriterion is one clause of a preset's selection.
type BlockCriterion struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

// BlockPresetBody is a named, reusable block definition: what it selects, how it
// orders, and how long it runs.
type BlockPresetBody struct {
	LibraryID       string           `json:"libraryId"`
	Criteria        []BlockCriterion `json:"criteria"`
	Order           string           `json:"order"`
	DurationMinutes int              `json:"durationMinutes"`
	// StartMinute places the block within a day; -1 means "anywhere".
	StartMinute int      `json:"startMinute"`
	Days        []string `json:"days"`
	Description string   `json:"description"`
}

// BlockOrders and Days are the published choice lists.
var (
	BlockOrders = []string{"shuffle", "sequential", "recent", "least-played"}
	Days        = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
)

// BlockOperators is the clause vocabulary.
var BlockOperators = []string{"includes", "excludes"}

// BlockPreset is a stored preset.
type BlockPreset struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Revision  int64           `json:"revision"`
	Body      BlockPresetBody `json:"body"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}

// BlockPresetPage is one page of the preset listing.
type BlockPresetPage struct {
	Items        []BlockPreset       `json:"items"`
	NextCursor   string              `json:"nextCursor"`
	Enumerations map[string][]string `json:"enumerations"`
}

// BlockPresetChange creates or updates a preset.
type BlockPresetChange struct {
	ExpectedRevision int64           `json:"expectedRevision"`
	OperationID      string          `json:"operationId"`
	Name             string          `json:"name"`
	Body             BlockPresetBody `json:"body"`
}

func validatePreset(c *BlockPresetChange) error {
	if c.Body.Criteria == nil {
		c.Body.Criteria = []BlockCriterion{}
	}
	fields := []string{}
	if c.Name == "" || !safeText(c.Name, 120) {
		fields = append(fields, "name")
	}
	if !oneOf(c.Body.Order, BlockOrders...) {
		fields = append(fields, "body.order")
	}
	if !safeText(c.Body.Description, 400) || !safeText(c.Body.LibraryID, 128) {
		fields = append(fields, "body.description")
	}
	if len(c.Body.Criteria) > 16 {
		fields = append(fields, "body.criteria")
	}
	for i := range c.Body.Criteria {
		clause := &c.Body.Criteria[i]
		if clause.Values == nil {
			clause.Values = []string{}
		}
		if clause.Field == "" || !safeText(clause.Field, 64) || !oneOf(clause.Operator, BlockOperators...) || len(clause.Values) == 0 || len(clause.Values) > 200 {
			fields = append(fields, "body.criteria")
			break
		}
		for _, value := range clause.Values {
			if value == "" || !safeText(value, 200) {
				fields = append(fields, "body.criteria")
				break
			}
		}
	}
	if c.Body.Days == nil {
		c.Body.Days = []string{}
	}
	seen := map[string]bool{}
	for _, day := range c.Body.Days {
		if !oneOf(day, Days...) || seen[day] {
			fields = append(fields, "body.days")
			break
		}
		seen[day] = true
	}
	if c.Body.StartMinute < -1 || c.Body.StartMinute > 1439 {
		fields = append(fields, "body.startMinute")
	}
	if len(fields) > 0 {
		return invalid(fields...)
	}
	clampInt(&c.Body.DurationMinutes, 5, 1440)
	sort.Strings(c.Body.Days)
	return nil
}

func presetKey(name string) string { return strings.ToLower(strings.Join(strings.Fields(name), " ")) }

// BlockPresets pages the presets by name.
func (s *Service) BlockPresets(ctx context.Context, auth Authorize, token string, limit int) (BlockPresetPage, error) {
	out := BlockPresetPage{Items: []BlockPreset{}, Enumerations: map[string][]string{"order": BlockOrders, "operator": BlockOperators, "days": Days, "applicationOrder": BlockOrders}}
	size, err := pageSize(limit)
	if err != nil {
		return out, err
	}
	c, err := decodeCursor("block-presets", token)
	if err != nil {
		return out, err
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,name,name_key,revision,body,created_ms,updated_ms FROM admin_channel_presets WHERE (?='' OR name_key>?) ORDER BY name_key,id LIMIT ?`, c.ID, c.ID, size+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		keys := []string{}
		for rows.Next() {
			var p BlockPreset
			var body, key string
			var created, updated int64
			if err = rows.Scan(&p.ID, &p.Name, &key, &p.Revision, &body, &created, &updated); err != nil {
				return err
			}
			_ = json.Unmarshal([]byte(body), &p.Body)
			if p.Body.Criteria == nil {
				p.Body.Criteria = []BlockCriterion{}
			}
			if p.Body.Days == nil {
				p.Body.Days = []string{}
			}
			p.CreatedAt, p.UpdatedAt = timeFromMilliseconds(created), timeFromMilliseconds(updated)
			out.Items = append(out.Items, p)
			keys = append(keys, key)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(out.Items) > size {
			out.Items = out.Items[:size]
			out.NextCursor = encodeCursor("block-presets", 0, keys[size-1])
		}
		return nil
	})
	return out, err
}

// SaveBlockPreset creates a preset when id is empty and updates it otherwise.
func (s *Service) SaveBlockPreset(ctx context.Context, auth Authorize, id string, change BlockPresetChange) (BlockPreset, error) {
	var out BlockPreset
	if !validOperationID(change.OperationID) {
		return out, ErrInput
	}
	if err := validatePreset(&change); err != nil {
		return out, err
	}
	scope := "block-preset"
	digest := digestOf(struct {
		ID     string            `json:"id"`
		Change BlockPresetChange `json:"change"`
	}{id, change})
	body, _ := json.Marshal(change.Body)
	err := s.transaction(ctx, auth, func(tx *sql.Tx) error {
		stored, replayed, err := receipt[BlockPreset](ctx, tx, scope, change.OperationID, digest)
		if err != nil {
			return err
		}
		if replayed {
			out = stored
			return nil
		}
		if change.Body.LibraryID != "" {
			if _, _, err = libraryExists(ctx, tx, change.Body.LibraryID); err != nil {
				return invalid("body.libraryId")
			}
		}
		at := s.milliseconds()
		key := presetKey(change.Name)
		var clash string
		err = tx.QueryRowContext(ctx, `SELECT id FROM admin_channel_presets WHERE name_key=?`, key).Scan(&clash)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if clash != "" && clash != id {
			return invalid("name")
		}
		if id == "" {
			if change.ExpectedRevision != 0 {
				return ErrConflict
			}
			if id, err = identifier(); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO admin_channel_presets VALUES(?,?,?,1,?,?,?)`, id, change.Name, key, string(body), at, at); err != nil {
				return err
			}
		} else {
			var revision int64
			if err = tx.QueryRowContext(ctx, `SELECT revision FROM admin_channel_presets WHERE id=?`, id).Scan(&revision); errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			} else if err != nil {
				return err
			}
			if revision != change.ExpectedRevision {
				return ErrConflict
			}
			if _, err = tx.ExecContext(ctx, `UPDATE admin_channel_presets SET name=?,name_key=?,revision=revision+1,body=?,updated_ms=? WHERE id=?`, change.Name, key, string(body), at, id); err != nil {
				return err
			}
		}
		var created, updated int64
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT id,name,revision,body,created_ms,updated_ms FROM admin_channel_presets WHERE id=?`, id).Scan(&out.ID, &out.Name, &out.Revision, &raw, &created, &updated); err != nil {
			return err
		}
		_ = json.Unmarshal([]byte(raw), &out.Body)
		out.CreatedAt, out.UpdatedAt = timeFromMilliseconds(created), timeFromMilliseconds(updated)
		return saveReceipt(ctx, tx, scope, change.OperationID, digest, out, at)
	})
	return out, err
}

// DeleteBlockPreset removes a preset. Channels already built from it keep their
// schedule; only the reusable definition goes.
func (s *Service) DeleteBlockPreset(ctx context.Context, auth Authorize, id string, expectedRevision int64, operationID string) error {
	if !validOperationID(operationID) || id == "" {
		return ErrInput
	}
	return s.transaction(ctx, auth, func(tx *sql.Tx) error {
		var revision int64
		err := tx.QueryRowContext(ctx, `SELECT revision FROM admin_channel_presets WHERE id=?`, id).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if revision != expectedRevision {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM admin_channel_presets WHERE id=?`, id)
		return err
	})
}

// ChannelLogo is a stored channel logo.
type ChannelLogo struct {
	ChannelID string `json:"channelId"`
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Bytes     int64  `json:"bytes"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Revision  int64  `json:"revision"`
	UpdatedAt string `json:"updatedAt"`
	URL       string `json:"url"`
}

// LogoUploadBytes bounds one logo upload, matching the artwork upload path.
const LogoUploadBytes = 10 << 20

// logoLimits are the dimensions a channel logo must fall within. A one-pixel
// image is not a logo and a poster-sized one is not either.
const (
	minLogoPixels = 16
	maxLogoPixels = 4096
)

// sniffLogo identifies the container from the bytes. A declared content type is
// never trusted, exactly as in the metadata editor's upload path.
func sniffLogo(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case len(raw) >= 12 && bytes.Equal(raw[0:4], []byte("RIFF")) && bytes.Equal(raw[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

func logoExtension(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	default:
		return ".webp"
	}
}

func (s *Service) logoRoot() (string, error) {
	if s.StateDirectory() == "" {
		return "", ErrUnavailable
	}
	root := storagepaths.Path(s.StateDirectory(), "channel-logos")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", ErrUnavailable
	}
	return root, nil
}

// UploadChannelLogo validates and stores one channel's logo. The image is proved
// decodable before anything is written, and the stored object is named by its
// own digest so an identical upload costs nothing.
func (s *Service) UploadChannelLogo(ctx context.Context, auth Authorize, channel string, raw []byte, expectedRevision *int64, operationID string) (ChannelLogo, error) {
	out := ChannelLogo{ChannelID: channel}
	if channel == "" || !safeText(channel, 200) || !validOperationID(operationID) || (expectedRevision != nil && *expectedRevision < 0) {
		return out, ErrInput
	}
	if len(raw) == 0 || len(raw) > LogoUploadBytes {
		return out, ErrImage
	}
	mime := sniffLogo(raw)
	if mime == "" {
		return out, ErrImage
	}
	// Decoding happens before any transaction opens, so a hostile image never
	// holds a write lock.
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < minLogoPixels || config.Height < minLogoPixels || config.Width > maxLogoPixels || config.Height > maxLogoPixels {
		return out, ErrImage
	}
	// The upload is never kept: the logo is one rendition, 256 px on its long
	// edge and capped like card artwork, lossless PNG when it has transparency.
	logo, err := metadata.EncodeChannelLogo(ctx, raw, map[string]bool{"jpeg": true, "png": true, "webp": true})
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if errors.Is(err, metadata.ErrArtworkCapUnreachableEncoding) {
			if e := metadata.RecordArtworkRefusal(ctx, s.db, err); e != nil {
				return out, e
			}
		}
		return out, ErrImage
	}
	root, err := s.logoRoot()
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(logo.Data)
	digest := hex.EncodeToString(sum[:])
	mime = logo.Mime
	path := filepath.Join(root, digest+logoExtension(mime))
	s.files.Lock()
	if _, err = os.Stat(path); err != nil {
		// The logo is addressed by the digest of its own bytes, so a reader that
		// finds this name expects exactly these bytes. A partial write would be a
		// file whose name is a promise it does not keep.
		if err = atomicfile.Write(path, logo.Data, 0600); err != nil {
			s.files.Unlock()
			return out, ErrUnavailable
		}
	}
	s.files.Unlock()
	scope := "channel-logo"
	receiptDigest := digestOf([]string{channel, digest, operationID})
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		stored, replayed, err := receipt[ChannelLogo](ctx, tx, scope, operationID, receiptDigest)
		if err != nil {
			return err
		}
		if replayed {
			out = stored
			return nil
		}
		var revision, previousBytes int64
		var previousDigest, previousMime string
		var previousWidth, previousHeight int
		var previousUpdated int64
		err = tx.QueryRowContext(ctx, `SELECT revision,digest,media_type,bytes,width,height,updated_ms FROM admin_channel_logos WHERE channel_id=?`, channel).Scan(&revision, &previousDigest, &previousMime, &previousBytes, &previousWidth, &previousHeight, &previousUpdated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if expectedRevision != nil && revision != *expectedRevision {
			return ErrConflict
		}
		if previousDigest == digest {
			out = ChannelLogo{ChannelID: channel, Digest: previousDigest, MediaType: previousMime, Bytes: previousBytes, Width: previousWidth, Height: previousHeight, Revision: revision, UpdatedAt: timeFromMilliseconds(previousUpdated), URL: "/v1/library-channels/" + channel + "/logo"}
			return saveReceipt(ctx, tx, scope, operationID, receiptDigest, out, s.milliseconds())
		}
		at := s.milliseconds()
		if _, err = tx.ExecContext(ctx, `INSERT INTO admin_channel_logos VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(channel_id) DO UPDATE SET digest=excluded.digest,media_type=excluded.media_type,bytes=excluded.bytes,width=excluded.width,height=excluded.height,revision=admin_channel_logos.revision+1,updated_ms=excluded.updated_ms`, channel, digest, mime, len(logo.Data), logo.Width, logo.Height, revision+1, at); err != nil {
			return err
		}
		if err = metadata.RecordArtworkOutcome(ctx, tx, "logo", logo); err != nil {
			return err
		}
		out = ChannelLogo{ChannelID: channel, Digest: digest, MediaType: mime, Bytes: int64(len(logo.Data)), Width: logo.Width, Height: logo.Height, Revision: revision + 1, UpdatedAt: timeFromMilliseconds(at), URL: "/v1/library-channels/" + channel + "/logo"}
		return saveReceipt(ctx, tx, scope, operationID, receiptDigest, out, at)
	})
	return out, err
}

// ChannelLogoBytes reads a stored logo for delivery. This is the one viewer
// visible read on these pages: a logo is part of the channel a viewer sees.
func (s *Service) ChannelLogoBytes(ctx context.Context, channel string) ([]byte, string, string, error) {
	if s == nil || s.db == nil {
		return nil, "", "", ErrUnavailable
	}
	var digest, mime string
	err := s.db.QueryRowContext(ctx, `SELECT digest,media_type FROM admin_channel_logos WHERE channel_id=?`, channel).Scan(&digest, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", "", ErrNotFound
	}
	if err != nil {
		return nil, "", "", err
	}
	root, err := s.logoRoot()
	if err != nil {
		return nil, "", "", err
	}
	raw, err := os.ReadFile(filepath.Join(root, digest+logoExtension(mime)))
	if err != nil {
		return nil, "", "", ErrNotFound
	}
	return raw, mime, digest, nil
}

// ChannelHealth reports one generated channel's condition: whether it has a
// published generation, how far its schedule reaches, and what stopped it.
type ChannelHealth struct {
	ChannelID        string `json:"channelId"`
	Name             string `json:"name"`
	Enabled          bool   `json:"enabled"`
	State            string `json:"state"`
	HealthCode       string `json:"healthCode"`
	Healthy          bool   `json:"healthy"`
	ActiveGeneration string `json:"activeGeneration"`
	GeneratedThrough string `json:"generatedThrough"`
	ScheduledEntries int64  `json:"scheduledEntries"`
	Candidates       int64  `json:"candidates"`
	Unresolved       int64  `json:"unresolved"`
	PendingJob       bool   `json:"pendingGeneration"`
	Message          string `json:"message"`
	ObservedAt       string `json:"observedAt"`
}

// ChannelHealthFor reads one library channel's health.
func (s *Service) ChannelHealthFor(ctx context.Context, auth Authorize, channel string) (ChannelHealth, error) {
	out := ChannelHealth{ChannelID: channel, ObservedAt: timeFromMilliseconds(s.milliseconds())}
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		if !tableExists(ctx, tx, "lc_channels") {
			return ErrUnavailable
		}
		var enabled, removed int
		var through int64
		err := tx.QueryRowContext(ctx, `SELECT name,enabled,removed,state,health_code,active_generation,generated_through_ms FROM lc_channels WHERE id=?`, channel).Scan(&out.Name, &enabled, &removed, &out.State, &out.HealthCode, &out.ActiveGeneration, &through)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out.Enabled = enabled != 0 && removed == 0
		// A channel that has never published has no generated-through instant;
		// epoch zero is not a fact about it. The list producer in
		// livechannels/library applies the same guard.
		if through > 0 {
			out.GeneratedThrough = timeFromMilliseconds(through)
		}
		if out.ActiveGeneration != "" {
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM lc_entries WHERE generation_id=?`, out.ActiveGeneration).Scan(&out.ScheduledEntries); err != nil {
				return err
			}
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE(candidate_count,0),COALESCE(unresolved_count,0) FROM lc_generations WHERE id=?`, out.ActiveGeneration).Scan(&out.Candidates, &out.Unresolved); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var pending int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM lc_generations WHERE channel_id=? AND status='pending'`, channel).Scan(&pending); err != nil {
			return err
		}
		out.PendingJob = pending > 0
		out.Healthy = out.HealthCode == "" && out.ActiveGeneration != "" && out.ScheduledEntries > 0
		switch {
		case !out.Enabled:
			out.Message = "The channel is switched off."
		case out.ActiveGeneration == "":
			out.Message = "No schedule has been published yet."
		case out.HealthCode != "":
			out.Message = "The last generation reported " + out.HealthCode + "."
		case out.ScheduledEntries == 0:
			out.Message = "The published schedule is empty; the block criteria matched nothing."
		default:
			out.Message = "The channel is publishing a schedule."
		}
		return nil
	})
	return out, err
}

// ImportLogos pulls the logos a live source advertises into the logo store.
// Nothing is fetched unless the composition root supplied a fetcher and the
// owner left logo import enabled.
func (s *Service) ImportLogos(ctx context.Context, auth Authorize, source, operationID string) (LogoImportResult, error) {
	out, _, _, err := s.importLogosBatch(ctx, auth, source, operationID, "", 0)
	return out, err
}

// importLogosBatch snapshots all candidate URLs in one read transaction. The
// background worker passes a cursor and a small limit; the public owner action
// may request the entire source. Fetches and writes happen after the snapshot
// closes, so a slow provider never holds a SQLite connection or the write gate.
func (s *Service) importLogosBatch(ctx context.Context, auth Authorize, source, operationID, after string, limit int) (LogoImportResult, string, bool, error) {
	out := LogoImportResult{SourceID: source}
	if !validOperationID(operationID) || source == "" || limit < 0 {
		return out, after, false, ErrInput
	}
	fetch := s.LogoFetch
	if fetch == nil {
		fetch = s.Fetch
	}
	if fetch == nil {
		out.Message = "This server is not configured to fetch remote logos."
		return out, after, true, nil
	}
	candidates := map[string]string{}
	skipped := map[string]bool{}
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		document, err := s.liveSourceDocument(ctx, tx, source)
		if err != nil {
			return err
		}
		if !document.Effective.LogoImport {
			out.Message = "Logo import is switched off for this source."
			return nil
		}
		var generation string
		if err = tx.QueryRowContext(ctx, `SELECT active_generation FROM live_sources WHERE id=?`, source).Scan(&generation); err != nil {
			return err
		}
		if generation == "" {
			out.Message = "This source has not published a channel list yet."
			return nil
		}
		candidates, err = s.logoCandidates(ctx, tx, source, generation)
		if err != nil {
			return err
		}
		if !document.EffectiveLogoImport.OverwriteExisting {
			rows, err := tx.QueryContext(ctx, `SELECT l.channel_id FROM live_channel_logos l JOIN admin_channel_logos a ON a.channel_id='live:'||?||':'||l.channel_id WHERE l.generation_id=?`, source, generation)
			if err != nil {
				return err
			}
			for rows.Next() {
				var channel string
				if err = rows.Scan(&channel); err != nil {
					break
				}
				skipped[channel] = true
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return err
			}
		}
		if limit > 0 {
			rows, err := tx.QueryContext(ctx, `SELECT l.channel_id FROM live_channel_logos l JOIN live_logo_failures f ON f.url=l.url WHERE l.generation_id=? AND f.retry_after_ms>?`, generation, s.milliseconds())
			if err != nil {
				return err
			}
			for rows.Next() {
				var channel string
				if err = rows.Scan(&channel); err != nil {
					break
				}
				skipped[channel] = true
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return out, after, false, err
	}
	if len(candidates) == 0 {
		if out.Message == "" {
			out.Message = "The source published no channel logos."
		}
		return out, after, true, nil
	}
	channels := make([]string, 0, len(candidates))
	for channel := range candidates {
		channels = append(channels, channel)
	}
	sort.Strings(channels)
	processed := 0
	for _, channel := range channels {
		if channel <= after {
			continue
		}
		if limit > 0 && processed >= limit {
			return out, after, false, nil
		}
		if err = ctx.Err(); err != nil {
			return out, after, false, err
		}
		processed++
		// Every candidate this sweep looks at ends Imported or Skipped, so the
		// owner's receipt ("5 imported, 3 skipped of 8") adds up. A logo that is
		// left alone by the overwrite guard is still one the sweep considered.
		out.Requested++
		if skipped[channel] {
			after = channel
			out.Skipped++
			continue
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		raw, fetchErr := fetch(fetchCtx, candidates[channel])
		cancel()
		if fetchErr != nil {
			if ctx.Err() != nil {
				return out, after, false, ctx.Err()
			}
			if err = s.rememberLogoFailure(ctx, candidates[channel]); err != nil {
				return out, after, false, err
			}
			after = channel
			out.Skipped++
			continue
		}
		sum := sha256.Sum256([]byte(channel))
		operation := operationID + "." + hex.EncodeToString(sum[:8])
		if len(operation) > 128 {
			key := sha256.Sum256([]byte(operation))
			operation = hex.EncodeToString(key[:])
		}
		var current int64
		err = s.db.QueryRowContext(ctx, `SELECT revision FROM admin_channel_logos WHERE channel_id=?`, "live:"+source+":"+channel).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return out, after, false, err
		}
		if _, err = s.UploadChannelLogo(ctx, auth, "live:"+source+":"+channel, raw, &current, operation); err != nil {
			if ctx.Err() != nil {
				return out, after, false, ctx.Err()
			}
			if rememberErr := s.rememberLogoFailure(ctx, candidates[channel]); rememberErr != nil {
				return out, after, false, rememberErr
			}
			after = channel
			out.Skipped++
			continue
		}
		if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM live_logo_failures WHERE url=?`, candidates[channel]); err != nil {
			return out, after, false, err
		}
		after = channel
		out.Imported++
	}
	return out, after, true, nil
}

func (s *Service) rememberLogoFailure(ctx context.Context, locator string) error {
	now := s.milliseconds()
	_, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `INSERT INTO live_logo_failures(url,attempts,retry_after_ms) VALUES(?,1,?+300000)
 ON CONFLICT(url) DO UPDATE SET attempts=live_logo_failures.attempts+1,
 retry_after_ms=?+min(86400000,300000*(1 << min(live_logo_failures.attempts,8)))`, locator, now, now)
	return err
}
