package downloads

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

const selectionDownloadRows = 20
const selectionDownloadScalar = 64 << 10

// SelectionDownloadCapture walks raw links, not a filtered/sorted version set.
// The caller holds the catalogue witness between steps and charges Bytes().
// No snapshot, query result list or disk scratch survives a call.
type SelectionDownloadCapture struct {
	after               int64
	best                selectionDownloadSource
	have, several, done bool
	firstPart           int
}

func (c *SelectionDownloadCapture) Bytes() int64 {
	return 512 + int64(len(c.best.Path)+len(c.best.Asset)+len(c.best.Container)+len(c.best.VideoCodec)+len(c.best.AudioCodec)+len(c.best.Item)+len(c.best.Library)+len(c.best.Kind))
}

type selectionDownloadSource struct {
	source
	assetID              int64
	part, assetAvailable int
	start                float64
	end                  *float64
}

// CaptureSelectionDownload uses the exact compactcatalog.BestVersionOrder:
// part ascending, asset availability/height/size descending, token ascending.
// At most twenty raw links are decoded per call; a many-version title yields.
// A refusal is pinned as its existing download reason, never silently omitted.
func CaptureSelectionDownload(ctx context.Context, tx *sql.Tx, target selectionactions.Target, c *SelectionDownloadCapture) (selectionactions.Target, bool, error) {
	if c == nil || tx == nil || target.EntityID <= 0 {
		return target, false, ErrInput
	}
	if c.done {
		return finishSelectionDownload(target, c), true, nil
	}
	rows, e := tx.QueryContext(ctx, `SELECT l.asset_id,l.part_index,l.available,l.start_seconds,l.end_seconds,
 CASE WHEN length(CAST(a.token AS BLOB))<=160 THEN a.token ELSE '' END,
 CASE WHEN length(CAST(a.path AS BLOB))<=? THEN a.path ELSE '' END,
 length(CAST(a.path AS BLOB))+length(CAST(a.token AS BLOB))+length(CAST(a.container AS BLOB))+length(CAST(a.video_codec AS BLOB))+length(CAST(a.audio_codec AS BLOB)),
 a.size,a.modified_ns,a.available,a.height,
 CASE WHEN length(CAST(a.container AS BLOB))<=65536 THEN a.container ELSE '' END,
 CASE WHEN length(CAST(a.video_codec AS BLOB))<=65536 THEN a.video_codec ELSE '' END,
 CASE WHEN length(CAST(a.audio_codec AS BLOB))<=65536 THEN a.audio_codec ELSE '' END,a.duration
 FROM catalog_asset_links l CROSS JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.asset_id>? ORDER BY l.asset_id LIMIT ?`, selectionDownloadScalar, target.EntityID, c.after, selectionDownloadRows)
	if e != nil {
		return target, false, e
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var r selectionDownloadSource
		var available, pathBytes int
		if e = rows.Scan(&r.assetID, &r.part, &available, &r.start, &r.end, &r.Asset, &r.Path, &pathBytes, &r.Size, &r.ModifiedNS, &r.assetAvailable, &r.Height, &r.Container, &r.VideoCodec, &r.AudioCodec, &r.Duration); e != nil {
			return target, false, e
		}
		if pathBytes > selectionDownloadScalar || r.Asset == "" {
			return target, false, selectionactions.ErrCapacity
		}
		r.Available = available != 0
		r.Item, r.Library, r.Kind = target.ItemID, target.LibraryID, target.Kind
		if !c.have {
			c.firstPart = r.part
		} else if c.firstPart != r.part {
			c.several = true
		}
		if !c.have || betterSelectionDownload(r, c.best) {
			c.best = r
		}
		c.have = true
		c.after = r.assetID
		n++
	}
	if e = rows.Err(); e != nil {
		return target, false, e
	}
	if n < selectionDownloadRows {
		c.done = true
		return finishSelectionDownload(target, c), true, nil
	}
	return target, false, nil
}
func betterSelectionDownload(a, b selectionDownloadSource) bool {
	if a.part != b.part {
		return a.part < b.part
	}
	if a.assetAvailable != b.assetAvailable {
		return a.assetAvailable > b.assetAvailable
	}
	if a.Height != b.Height {
		return a.Height > b.Height
	}
	if a.Size != b.Size {
		return a.Size > b.Size
	}
	return a.Asset < b.Asset
}
func finishSelectionDownload(t selectionactions.Target, c *SelectionDownloadCapture) selectionactions.Target {
	t.VersionID = ""
	switch {
	case !c.have:
		t.DownloadIdentity = ReasonItemDeleted
	case c.several:
		t.DownloadIdentity = ReasonSeveralFiles
	case !c.best.Available || c.best.Size <= 0:
		t.DownloadIdentity = ReasonSourceMissing
	default:
		t.VersionID = c.best.Asset
		t.DownloadIdentity = selectionDownloadIdentity(c.best)
	}
	return t
}
func selectionDownloadIdentity(s selectionDownloadSource) string {
	raw, _ := json.Marshal([]any{s.Asset, s.Path, s.Size, s.ModifiedNS, s.part, s.start, s.end, s.Available, s.assetAvailable, s.Height, s.Container, s.VideoCodec, s.AudioCodec, s.Duration})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ListSelectionAction pages actual admitted preparations for one exact device,
// profile and command. It consumes raw claims before visibility filtering, so a
// sparse or now-hidden page still advances without an unbounded scan.
func (s *Service) ListSelectionAction(ctx context.Context, p identity.Principal, operationID, deviceID, cursor string, limit int) (Page, error) {
	out := Page{Items: []Preparation{}}
	if !validID.MatchString(operationID) || !validID.MatchString(deviceID) || limit < 1 || limit > 200 || len(cursor) > 1024 {
		return out, ErrInput
	}
	after := ""
	if cursor != "" {
		ms, id, e := decodeCursor(cursor)
		if e != nil || ms != 0 {
			return out, ErrInput
		}
		after = id
	}
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if _, _, e = s.requestAccess(ctx, tx, p, deviceID); e != nil {
		return out, e
	}
	closed, err := selectionActionTransferClosedTx(ctx, tx, p, operationID, deviceID)
	if err != nil {
		return out, err
	}
	out.Closed = &closed

	settings, e := readSettings(tx)
	if e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT preparation_id FROM download_action_claims WHERE profile_key=? AND device_id=? AND operation_id=? AND preparation_id>? ORDER BY preparation_id LIMIT ?`, operations.ViewerKey(p), deviceID, operationID, after, limit+1)
	if e != nil {
		return out, e
	}
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > limit {
		out.NextCursor = encodeCursor(0, ids[limit-1])
		ids = ids[:limit]
	}
	for _, id := range ids {
		r, err := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE id=? AND profile_key=?`, id, operations.ViewerKey(p)))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return out, err
		}
		if err = s.visiblePreparation(ctx, tx, p, r); errors.Is(err, identity.ErrContentRestricted) {
			continue
		} else if err != nil {
			return out, err
		}
		out.Items = append(out.Items, s.publish(r, settings.RetentionDays))
	}
	return out, gated.Commit()
}

// ApplySelectionDownload admits exactly one pinned target in one background
// writer transaction. The actual preparation claim and the journal checkpoint
// commit together; neither a crash nor an ambiguous response can split them.
// The journal supplies OperationID after sealing; it is not part of capture.
func (s *Service) ApplySelectionDownload(ctx context.Context, p identity.Principal, action string, options selectionactions.Options, target selectionactions.Target, file *selectionactions.File, authorize selectionactions.Authorize, checkpoint func(*sql.Tx, selectionactions.Effect) error) error {
	if action != "download" || file != nil || authorize == nil || checkpoint == nil || !validID.MatchString(target.OperationID) || !validID.MatchString(options.DeviceID) || options.Quality == "" || preparationQuality(options.Quality) != nil {
		return ErrInput
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if e = authorize(ctx, tx, p, action, target.LibraryID, &target); e != nil {
		return e
	}
	if e = selectionDownloadTargetTx(ctx, tx, target); e != nil {
		return e
	}
	visible, args, e := s.requestAccess(ctx, tx, p, options.DeviceID)
	if e != nil {
		return e
	}
	var present int
	bound := []any{target.EntityID, target.ItemID}
	bound = append(bound, args...)
	e = tx.QueryRowContext(ctx, `SELECT 1 FROM catalog_entities i WHERE i.id=? AND i.public_id=pid_blob(?) AND (`+visible+`)`, bound...).Scan(&present)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	if e = s.visibleItem(ctx, tx, p, target.ItemID); e != nil {
		return e
	}
	effect := selectionactions.Effect{}
	if !validDigest.MatchString(target.DownloadIdentity) {
		switch target.DownloadIdentity {
		case ReasonItemDeleted, ReasonSeveralFiles, ReasonSourceMissing:
			effect.Code = target.DownloadIdentity
		default:
			return ErrInput
		}
	} else {
		found, err := selectionPinnedSource(ctx, tx, target)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, selectionactions.ErrCapacity) {
			effect.Code = ReasonSourceChanged
		} else if err != nil {
			return err
		} else if selectionDownloadIdentity(found) != target.DownloadIdentity {
			effect.Code = ReasonSourceChanged
		} else {
			effect.Code, e = s.admitSelectionDownload(ctx, tx, p, options, target, found.source)
			if e != nil {
				return e
			}
		}
	}
	if e = checkpoint(tx, effect); e != nil {
		return e
	}
	if e = gated.Commit(); e != nil {
		return e
	}
	s.signal()
	return nil
}

func selectionPinnedSource(ctx context.Context, tx *sql.Tx, t selectionactions.Target) (selectionDownloadSource, error) {
	var s selectionDownloadSource
	var available, pathBytes int
	var kind int
	e := tx.QueryRowContext(ctx, `SELECT l.asset_id,l.part_index,l.available,l.start_seconds,l.end_seconds,
 CASE WHEN length(CAST(a.token AS BLOB))<=160 THEN a.token ELSE '' END,
 CASE WHEN length(CAST(a.path AS BLOB))<=? THEN a.path ELSE '' END,
 length(CAST(a.path AS BLOB))+length(CAST(a.token AS BLOB))+length(CAST(a.container AS BLOB))+length(CAST(a.video_codec AS BLOB))+length(CAST(a.audio_codec AS BLOB)),
 a.size,a.modified_ns,a.available,a.height,
 CASE WHEN length(CAST(a.container AS BLOB))<=65536 THEN a.container ELSE '' END,
 CASE WHEN length(CAST(a.video_codec AS BLOB))<=65536 THEN a.video_codec ELSE '' END,
 CASE WHEN length(CAST(a.audio_codec AS BLOB))<=65536 THEN a.audio_codec ELSE '' END,a.duration,cl.library_id,i.kind
 FROM catalog_assets a CROSS JOIN catalog_asset_links l ON l.asset_id=a.id AND l.entity_id=? CROSS JOIN catalog_entities i ON i.id=l.entity_id CROSS JOIN catalog_libraries cl ON cl.id=i.library_id
 WHERE a.token=? AND i.public_id=pid_blob(?)`, selectionDownloadScalar, t.EntityID, t.VersionID, t.ItemID).Scan(&s.assetID, &s.part, &available, &s.start, &s.end, &s.Asset, &s.Path, &pathBytes, &s.Size, &s.ModifiedNS, &s.assetAvailable, &s.Height, &s.Container, &s.VideoCodec, &s.AudioCodec, &s.Duration, &s.Library, &kind)
	if e != nil {
		return s, e
	}
	if pathBytes > selectionDownloadScalar || s.Asset == "" {
		return s, selectionactions.ErrCapacity
	}
	s.Item = t.ItemID
	s.Kind, _ = compactcatalog.Kind(kind).Name()
	s.Available = available != 0
	s.Parts = 1
	if s.Library != t.LibraryID || s.Kind != t.Kind {
		return s, selectionDownloadChanged()
	}
	return s, nil
}

// hydrateSelectionSource is the existing resolveVersionSource technical-fact
// projection after a pinned indexed lookup, without its whole-item part scan.
func hydrateSelectionSource(ctx context.Context, tx *sql.Tx, s source, start float64, end *float64) (source, error) {
	if s.Kind == "song" {
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM music_virtual_tracks v JOIN catalog_entities e ON e.id=v.entity_id WHERE v.asset_id=? AND e.public_id=pid_blob(?) AND v.start_seconds=? AND v.end_seconds=?)`, s.Asset, s.Item, start, end).Scan(&s.Cue); e != nil {
			return s, e
		}
		if s.Cue && end != nil {
			s.Duration = *end - start
		}
	}
	if s.Duration > 0 {
		s.VideoBitrateBPS = int64(float64(s.Size) * 8 / s.Duration)
	}
	if facts, e := playback.DeliverySourceTx(ctx, tx, s.Asset); e == nil {
		if len(facts.Audio) > 0 {
			a := facts.Audio[0]
			s.AudioRate, s.AudioDepth, s.AudioChannels, s.AudioCodec, s.AudioBitrateBPS = a.SampleRate, a.BitDepth, a.Channels, a.Codec, int(a.BitRate)
		}
		if facts.Video != nil {
			if facts.Video.Height > 0 {
				s.Height = facts.Video.Height
			}
			if facts.Video.BitRate > 0 {
				s.VideoBitrateBPS = facts.Video.BitRate
			}
		}
	}
	if s.Kind == "song" {
		s.AudioLossless = assets.KnownLosslessAudio(s.AudioCodec)
		var known *bool
		e := tx.QueryRowContext(ctx, `SELECT lossless FROM music_technical_facts WHERE asset_id=? AND size=? AND modified_ns=?`, s.Asset, s.Size, s.ModifiedNS).Scan(&known)
		if e == nil {
			s.AudioLossless = known
		} else if !errors.Is(e, sql.ErrNoRows) {
			return s, e
		}
		if f, ok, e := audiofacts.Read(ctx, tx, s.Asset); e != nil {
			return s, e
		} else if ok {
			s.AudioRate, s.AudioDepth, s.AudioChannels = f.SampleRate, f.BitDepth, f.Channels
			if f.BitRate > 0 {
				s.AudioBitrateBPS = int(f.BitRate)
			}
			if s.AudioBitrateBPS == 0 && f.DurationFrames > 0 {
				s.AudioBitrateBPS = int(float64(s.Size) * 8 * float64(f.SampleRate) / float64(f.DurationFrames))
			}
		}
	}
	return s, nil
}

func (s *Service) admitSelectionDownload(ctx context.Context, tx *sql.Tx, p identity.Principal, o selectionactions.Options, t selectionactions.Target, found source) (string, error) {
	// Re-read just this pinned link for cue boundaries; never select a new best.
	var start float64
	var end *float64
	if e := tx.QueryRowContext(ctx, `SELECT l.start_seconds,l.end_seconds FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE a.token=? AND l.entity_id=?`, t.VersionID, t.EntityID).Scan(&start, &end); e != nil {
		return "", e
	}
	var e error
	found, e = hydrateSelectionSource(ctx, tx, found, start, end)
	if e != nil {
		return "", e
	}
	requested, e := requestedDownloadQuality(ctx, tx, p, o.Quality, found)
	if e != nil {
		return "", e
	}
	device := playback.CapabilityDocumentTx(ctx, tx, p)
	quality := s.resolveQuality(ctx, tx, device, requested, found)
	recipe := chooseRecipe(ctx, tx, device, quality, found, s.deliveryConfig())
	if reason := s.preparationReason(ctx, tx, quality, recipe, found); reason != "" {
		return reason, nil
	}
	key, now := operations.ViewerKey(p), s.millis()
	room := newStorageAllowance(tx)
	existing, e := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE profile_key=? AND item_id=? AND quality=? AND source_asset_id=? AND state IN('queued','running','ready','paused','failed') ORDER BY CASE state WHEN 'ready' THEN 0 WHEN 'running' THEN 1 WHEN 'queued' THEN 2 WHEN 'paused' THEN 3 ELSE 4 END,id LIMIT 1`, key, t.EntityID, quality, found.Asset))
	id := ""
	if e == nil {
		if existing.ItemID != t.ItemID {
			return ReasonItemDeleted, nil
		}
		id = existing.ID
		if existing.State == StateFailed {
			if e = s.restart(ctx, tx, p, room, existing, now); errors.Is(e, ErrStorageFull) {
				return ReasonStorageFull, nil
			} else if e != nil {
				return "", e
			}
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	} else {
		estimate, estimated := estimateBytes(quality, recipe, found)
		if e = room.admit(storedQualityBudget(quality, recipe), estimate); errors.Is(e, ErrStorageFull) {
			return ReasonStorageFull, nil
		} else if e != nil {
			return "", e
		}
		// A stable base-command id survives transport ambiguity without a separate
		// per-target receipt. It is not used to discover operation preparations.
		raw, _ := json.Marshal([]any{key, o.DeviceID, t.OperationID, t.ItemID, t.VersionID, quality, t.DownloadIdentity})
		sum := sha256.Sum256(raw)
		id = hex.EncodeToString(sum[:])
		title, err := selectionDownloadTitle(ctx, tx, t.EntityID, found.Duration)
		if err != nil {
			return "", err
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,item_public,library_id,title_json,source_asset_id,quality,recipe,origin,batch_id,state,bytes_total,estimated,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'items',?,'queued',?,?,?,?)`, id, key, p.Authority, p.AccountID, p.ProfileID, t.EntityID, t.ItemID, found.Library, titleSnapshot(title), found.Asset, quality, recipe, t.OperationID, estimate, estimated, now, now)
		if e != nil {
			return "", e
		}
		principal, e := json.Marshal(p)
		if e != nil {
			return "", e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO download_preparation_authority(preparation_id,principal,device_id) VALUES(?,?,?)`, id, string(principal), o.DeviceID); e != nil {
			return "", e
		}
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO download_action_claims(profile_key,device_id,operation_id,preparation_id) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, key, o.DeviceID, t.OperationID, id)
	return "", e
}

// Preserve the ordinary preparation Title projection with pinned-source duration;
// readTitle would aggregate every source version during this writer quantum.
func selectionDownloadTitle(ctx context.Context, tx *sql.Tx, entity int64, duration float64) (Title, error) {
	var out Title
	var kind int
	e := tx.QueryRowContext(ctx, `SELECT e.kind,e.title,e.year,? FROM catalog_entities e WHERE e.id=?`, duration, entity).Scan(&kind, &out.Name, &out.Year, &out.DurationSeconds)
	if e != nil {
		return out, e
	}
	out.Kind, _ = compactcatalog.Kind(kind).Name()
	out.OnServer = true
	var group, number sql.NullInt64
	switch compactcatalog.Kind(kind) {
	case compactcatalog.Episode:
		var numbering string
		e = tx.QueryRowContext(ctx, `SELECT pid(show.public_id),show.title,COALESCE(pid(season.public_id),''),COALESCE(s.title,''),s.number,ep.number,ep.numbering
 FROM catalog_episodes ep JOIN catalog_entities show ON show.id=ep.show_id
 LEFT JOIN catalog_entities season ON season.id=ep.season_id LEFT JOIN catalog_seasons s ON s.entity_id=ep.season_id
 WHERE ep.entity_id=?`, entity).Scan(&out.ParentID, &out.ParentTitle, &out.GroupID, &out.GroupTitle, &group, &number, &numbering)
		// An episode filed by air date has no number a person would recognise.
		if numbering == "date" {
			number = sql.NullInt64{}
		}
	case compactcatalog.Track:
		var provider string
		e = tx.QueryRowContext(ctx, `SELECT pid(album.public_id),album.title,cs.disc_number,cs.track_number,COALESCE(artist.title,''),cs.provider_artist
 FROM catalog_songs cs JOIN catalog_entities album ON album.id=cs.album_id
 LEFT JOIN catalog_albums ca ON ca.entity_id=cs.album_id LEFT JOIN catalog_entities artist ON artist.id=ca.artist_id
 WHERE cs.entity_id=?`, entity).Scan(&out.ParentID, &out.ParentTitle, &group, &number, &out.Artist, &provider)
		if provider != "" {
			out.Artist = provider
		}
	case compactcatalog.Part:
		e = tx.QueryRowContext(ctx, `SELECT pid(book.public_id),book.title,bf.disc_number,bf.part_number,COALESCE(cb.author,'')
 FROM catalog_book_files bf JOIN catalog_entities book ON book.id=bf.book_id LEFT JOIN catalog_books cb ON cb.entity_id=bf.book_id
 WHERE bf.entity_id=?`, entity).Scan(&out.ParentID, &out.ParentTitle, &group, &number, &out.Artist)
	}
	if errors.Is(e, sql.ErrNoRows) {
		// The side row is still being written by a scan: the title's own facts
		// are enough to list it, and the claim refreshes them when it is ready.
		e = nil
	}
	out.GroupNumber, out.Number = numberOf(group), numberOf(number)
	return out, e
}

// selectionDownloadVersions avoids materializing all versions for a single
// source/profile decision while sharing preparedmedia's exact provenance check.
func selectionDownloadVersions(ctx context.Context, tx *sql.Tx, found source, profile string) ([]preparedmedia.Version, error) {
	v, ok, e := preparedmedia.DownloadVersionTx(ctx, tx, found.Item, found.Asset, profile)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, nil
	}
	return []preparedmedia.Version{v}, nil
}

// resolvePinnedDownloadSource is the worker's explicit-version path. Its part
// check uses two index endpoint seeks, then all source lookups are by identity.
func resolvePinnedDownloadSource(ctx context.Context, tx *sql.Tx, item, version string) (source, error) {
	var t selectionactions.Target
	var kind int
	t.ItemID, t.VersionID = item, version
	e := tx.QueryRowContext(ctx, `SELECT e.id,e.kind,cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&t.EntityID, &kind, &t.LibraryID)
	if e != nil {
		return source{}, e
	}
	t.Kind, _ = compactcatalog.Kind(kind).Name()
	pinned, e := selectionPinnedSource(ctx, tx, t)
	if e != nil {
		return source{}, e
	}
	single, e := preparedmedia.DownloadSinglePartTx(ctx, tx, item)
	if e != nil {
		return source{}, e
	}
	if !single {
		pinned.Parts = 2
	}
	return hydrateSelectionSource(ctx, tx, pinned.source, pinned.start, pinned.end)
}

func refreshPinnedDownloadTitle(ctx context.Context, tx *sql.Tx, id string) error {
	var entity int64
	var duration float64
	e := tx.QueryRowContext(ctx, `SELECT d.item_id,a.duration FROM download_preparations d JOIN catalog_entities e ON e.id=d.item_id AND e.public_id=pid_blob(d.item_public) JOIN catalog_assets a ON a.token=d.source_asset_id WHERE d.id=?`, id).Scan(&entity, &duration)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	title, e := selectionDownloadTitle(ctx, tx, entity, duration)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE download_preparations SET title_json=? WHERE id=?`, titleSnapshot(title), id)
	return e
}

// Authorize receives the accepted library ID, not a device ID. Device/session
// binding is independently checked by requestAccess in the same transaction.
func selectionDownloadTargetTx(ctx context.Context, tx *sql.Tx, t selectionactions.Target) error {
	if t.EntityID <= 0 || t.Identity == "" {
		return ErrInput
	}
	switch t.Kind {
	case "movie", "episode", "song", "audiobook_file":
	default:
		return ErrInput
	}
	var public, library, kind string
	var parent, added sql.NullInt64
	e := tx.QueryRowContext(ctx, `SELECT pid(e.public_id),cl.library_id,k.name,e.parent_id,e.added_ms FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind WHERE e.id=? AND e.public_id=pid_blob(?) AND e.retired=0 AND cl.retired=0`, t.EntityID, t.ItemID).Scan(&public, &library, &kind, &parent, &added)
	if errors.Is(e, sql.ErrNoRows) {
		return selectionDownloadChanged()
	}
	if e != nil {
		return e
	}
	var parentValue any
	if parent.Valid {
		parentValue = parent.Int64
	}
	lifetime := ""
	if added.Valid {
		lifetime = strconv.FormatInt(added.Int64, 10)
	}
	if library != t.LibraryID || kind != t.Kind || lifetime != t.Lifetime || selectionactions.TargetIdentity(public, library, kind, parentValue) != t.Identity {
		return selectionDownloadChanged()
	}
	return nil
}

// A changed lifetime or source identity is a proved target refusal. The
// journal must finish it honestly instead of retrying it as storage uncertainty.
func selectionDownloadChanged() error {
	return &selectionactions.TargetFailure{Code: ReasonSourceChanged, Cause: ErrConflict}
}
