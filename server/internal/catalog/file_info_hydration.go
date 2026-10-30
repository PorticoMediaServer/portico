package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const fileInfoSafeInteger int64 = 9007199254740991

// One delivery reservation covers row buffers, decoded detail, escaping,
// encoder and overlapping transport copies. Large legal text is never cached.
type fileInfoHydrationBudget struct {
	result        readprep.Result
	maximum, used int64
	exceptional   bool
}

func (b *fileInfoHydrationBudget) row(bytes int64) (bool, error) {
	if bytes < 0 || bytes > (math.MaxInt64-32768)/16 {
		return false, readprep.ErrInvalid
	}
	need := 32768 + 16*bytes
	if need > b.maximum {
		if b.used > 32768 {
			return false, nil
		}
		b.exceptional = true
		return true, nil
	}
	if b.exceptional {
		return false, nil
	}
	if b.used > 32768 && b.used+need > 1<<20 {
		return false, nil
	}
	if need > math.MaxInt64-b.used {
		return false, readprep.ErrInvalid
	}
	if e := b.result.ResizeDelivery(b.used + need); e != nil {
		return false, e
	}
	b.used += need
	return true, nil
}
func fileInfoInt(n int64) (*int64, string) {
	if n > fileInfoSafeInteger || n < -fileInfoSafeInteger {
		return nil, strconv.FormatInt(n, 10)
	}
	return &n, ""
}
func fileInfoDuration(n float64) (*float64, string) {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, "unrepresentable"
	}
	return &n, ""
}

func (r *FileInfoRuntime) fileRowBytes(ctx context.Context, coord fileInfoOccurrence, owner bool, video int64) (int64, error) {
	var n int64
	e := dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, `SELECT length(CAST(a.token AS BLOB))+CASE WHEN ? THEN length(CAST(a.path AS BLOB)) ELSE 0 END+length(CAST(a.container AS BLOB))+length(CAST(a.video_codec AS BLOB))+length(CAST(a.audio_codec AS BLOB))+length(CAST(l.edition AS BLOB))+COALESCE((SELECT length(CAST(edition AS BLOB)) FROM catalog_item_details WHERE entity_id=l.entity_id),0)
 +COALESCE((SELECT sum(length(CAST(o.old_text AS BLOB))) FROM catalog_info_changes c JOIN catalog_info_scalar_old o ON o.change_id=c.id WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.phase<3),0)
 +CASE WHEN ? THEN COALESCE((SELECT sum(length(CAST(o.old_path AS BLOB))) FROM catalog_info_changes c JOIN catalog_info_path_old o ON o.change_id=c.id WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.phase<3),0) ELSE 0 END
 +COALESCE((SELECT length(CAST(detail_json AS BLOB))+length(CAST(color_transfer AS BLOB)) FROM asset_streams WHERE rowid=? AND asset_id=a.token),0)
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? AND l.asset_id=?`, owner, owner, video, coord.Item, coord.Asset).Scan(&n)
	if errors.Is(e, sql.ErrNoRows) {
		e = readprep.ErrStale
	}
	return n, e
}

func (r *FileInfoRuntime) hydrateFile(ctx context.Context, caller readprep.Caller, opening string, proof fileInfoOpeningProof, ad FileInfoAdmission, coord fileInfoOccurrence, videoRow int64) (FileInfoFile, error) {
	var out FileInfoFile
	q := dbwork.ReadHandle(ctx, r.db)
	var token string
	e := q.QueryRowContext(ctx, `SELECT a.token,pid(e.public_id),l.available,l.part_index,l.edition FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN catalog_entities e ON e.id=l.entity_id WHERE l.entity_id=? AND l.asset_id=? AND e.retired=0`, coord.Item, coord.Asset).Scan(&token, &out.ItemID, &out.Available, &out.PartIndex, &out.Edition)
	if errors.Is(e, sql.ErrNoRows) {
		return out, readprep.ErrStale
	}
	if e != nil {
		return out, e
	}
	frame, e := compactcatalog.ReadInfoAsset(ctx, q, coord.Asset, token, ad.Owner)
	if e != nil {
		return out, e
	}
	out.ID, out.Path, out.Container = token, frame.Path, frame.Container
	out.Size, out.SizeExact = fileInfoInt(frame.Size)
	out.Duration, out.DurationState = fileInfoDuration(frame.Duration)
	if frame.Duration > 0 && !math.IsInf(frame.Duration, 0) && frame.Size > 0 {
		v := float64(frame.Size) * 8 / frame.Duration
		if v <= float64(fileInfoSafeInteger) {
			out.BitRate = int64(v)
		}
	}
	out.Occurrence, e = r.issueOccurrence(ctx, caller, opening, proof, ad, coord, nil)
	if e != nil {
		return out, e
	}
	if frame.VideoCodec != "" || frame.Height > 0 {
		out.Video = &FileInfoVideo{TitleFileVideo: TitleFileVideo{Codec: frame.VideoCodec, Width: int(frame.Width), Height: int(frame.Height)}}
		if videoRow > 0 {
			var detail, transfer string
			e = q.QueryRowContext(ctx, `SELECT detail_json,color_transfer FROM asset_streams WHERE rowid=? AND asset_id=?`, videoRow, token).Scan(&detail, &transfer)
			if errors.Is(e, sql.ErrNoRows) {
				return out, readprep.ErrStale
			}
			if e != nil {
				return out, e
			}
			var stream assets.Stream
			stream.DecodeDetail(detail)
			d := stream.Detail
			out.Video.Profile = d.Profile
			out.Video.DynamicRange = d.DynamicRange
			if out.Video.DynamicRange == "" {
				out.Video.DynamicRange = assets.DynamicRangeOf(transfer, d.DolbyVisionProfile)
			}
			out.Video.HDR10Plus, out.Video.FrameRate, out.Video.BitDepth, out.Video.BitRate = d.HDR10Plus, d.FrameRate, d.BitDepth, d.BitRate
		}
		out.Disc, e = r.fileDiscFacts(ctx, coord.Asset, frame.Size, frame.ModifiedNS)
		if e != nil {
			return out, e
		}
	}
	return out, nil
}

// The accepted observation pin is chosen before testing its probe. These are
// point joins and a chapter endpoint over producer-assigned contiguous order.
// They do not enumerate a disc or substitute a later facts-bearing observation.
func (r *FileInfoRuntime) fileDiscFacts(ctx context.Context, asset, size, modified int64) (*DiscMediaFacts, error) {
	var out DiscMediaFacts
	e := dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, `SELECT d.kind,d.packaging,t.title_number,
 COALESCE((SELECT c.chapter_order+1 FROM inventory_disc_chapters c WHERE c.object_id=t.object_id AND c.title_key=t.title_key ORDER BY c.chapter_order DESC LIMIT 1),0)
 FROM catalog_assets a NOT INDEXED CROSS JOIN inventory_disc_facts d ON d.object_id=a.info_owner_object AND d.source_revision=a.info_owner_revision AND d.asset_id=a.token
 JOIN inventory_disc_titles t ON t.object_id=d.object_id AND t.title_key=d.media_title_key
 JOIN asset_stream_facts st ON st.asset_id=a.token AND st.revision=d.media_stream_revision AND st.size=? AND st.modified_ns=?
 JOIN asset_chapter_facts ch ON ch.asset_id=a.token AND ch.revision=d.media_chapter_revision AND ch.size=? AND ch.modified_ns=? AND ch.status='known'
 WHERE a.id=? AND d.encryption='none'`, size, modified, size, modified, asset).Scan(&out.Kind, &out.Packaging, &out.TitleNumber, &out.ChapterCount)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &out, nil
}
func (r *FileInfoRuntime) discCandidate(ctx context.Context, coord fileInfoOccurrence) (bool, error) {
	var yes bool
	e := dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_asset_links l CROSS JOIN catalog_assets a NOT INDEXED ON a.id=l.asset_id CROSS JOIN inventory_disc_facts d ON d.object_id=a.info_owner_object AND d.source_revision=a.info_owner_revision AND d.asset_id=a.token WHERE l.entity_id=? AND l.asset_id=? AND d.encryption='none' AND EXISTS(SELECT 1 FROM inventory_disc_titles t WHERE t.object_id=d.object_id))`, coord.Item, coord.Asset).Scan(&yes)
	return yes, e
}

func (r *FileInfoRuntime) trackRowBytes(ctx context.Context, asset, row int64, lane byte) (int64, error) {
	query := `SELECT length(CAST(codec AS BLOB))+length(CAST(language AS BLOB))+length(CAST(title AS BLOB))+length(CAST(channel_layout AS BLOB))+length(CAST(detail_json AS BLOB)) FROM asset_streams WHERE asset_id=(SELECT token FROM catalog_assets WHERE id=?) AND rowid=?`
	if lane == fileInfoLaneSubtitles {
		query = `SELECT length(CAST(format AS BLOB))+length(CAST(language AS BLOB))+length(CAST(title AS BLOB)) FROM asset_subtitles WHERE asset_id=(SELECT token FROM catalog_assets WHERE id=?) AND rowid=?`
	}
	var n int64
	e := dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, query, asset, row).Scan(&n)
	if errors.Is(e, sql.ErrNoRows) {
		e = readprep.ErrStale
	}
	return n, e
}
func (r *FileInfoRuntime) hydrateTrack(ctx context.Context, asset, row, ordinal int64, lane byte) (FileInfoRawTrack, error) {
	out := FileInfoRawTrack{Ordinal: ordinal}
	q := dbwork.ReadHandle(ctx, r.db)
	var e error
	if lane == fileInfoLaneAudio {
		var detail string
		e = q.QueryRowContext(ctx, `SELECT codec,language,title,channels,channel_layout,is_default,is_forced,detail_json FROM asset_streams WHERE asset_id=(SELECT token FROM catalog_assets WHERE id=?) AND rowid=? AND type='audio'`, asset, row).Scan(&out.Codec, &out.Language, &out.Title, &out.Channels, &out.ChannelLayout, &out.Default, &out.Forced, &detail)
		if e == nil {
			var st assets.Stream
			st.DecodeDetail(detail)
			out.ObjectAudio = st.Detail.ObjectAudio
			out.BitRate = st.Detail.BitRate
			out.Profile = st.Detail.Profile
			out.SampleRate = st.Detail.SampleRate
			out.BitDepth = st.Detail.BitsPerSample
			out.Commentary = st.Commentary
			out.HearingImpaired = st.HearingImpaired
		}
	} else {
		e = q.QueryRowContext(ctx, `SELECT format,language,title,is_default,is_forced,origin='sidecar',hearing_impaired FROM asset_subtitles WHERE asset_id=(SELECT token FROM catalog_assets WHERE id=?) AND rowid=?`, asset, row).Scan(&out.Codec, &out.Language, &out.Title, &out.Default, &out.Forced, &out.External, &out.HearingImpaired)
	}
	if errors.Is(e, sql.ErrNoRows) {
		e = readprep.ErrStale
	}
	return out, e
}
