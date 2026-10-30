package catalog

import "github.com/PorticoMediaServer/portico/server/internal/assets"
import "github.com/PorticoMediaServer/portico/server/internal/dbwork"
import "github.com/PorticoMediaServer/portico/server/internal/compactcatalog"

// Pre-batching file projection retained only as an equivalence oracle.
// titleFiles reads a title's files with their tracks: a handful of rows per file, the title's
// own. The path is the owner's to see.
func (s *Service) referenceTitleFiles(item string, owner bool) ([]TitleFile, error) {
	rows, err := s.read().Query(`SELECT a.token,a.path,a.size,a.container,a.duration,a.video_codec,a.width,a.height,link.available,link.part_index,link.edition
 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id
 WHERE link.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) ORDER BY `+compactcatalog.BestVersionOrder("link", "a"), item)
	if err != nil {
		return nil, err
	}
	out := []TitleFile{}
	for rows.Next() {
		f := TitleFile{Audio: []TitleFileTrack{}, Subtitles: []TitleFileTrack{}}
		var codec string
		var width, height int
		if err = rows.Scan(&f.ID, &f.Path, &f.Size, &f.Container, &f.Duration, &codec, &width, &height, &f.Available, &f.PartIndex, &f.Edition); err != nil {
			rows.Close()
			return nil, err
		}
		if !owner {
			f.Path = ""
		}
		if f.Duration > 0 && f.Size > 0 {
			f.BitRate = int64(float64(f.Size) * 8 / f.Duration)
		}
		if codec != "" || height > 0 {
			f.Video = &TitleFileVideo{Codec: codec, Width: width, Height: height}
		}
		out = append(out, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		f := &out[i]
		if f.Video != nil {
			if f.Disc, err = ReadDiscMediaFacts(s.Context(), s.read(), f.ID); err != nil {
				return nil, err
			}
		}
		streams, err := s.read().Query(`SELECT type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,detail_json FROM asset_streams WHERE asset_id=? AND type IN('video','audio') ORDER BY stream_index`, f.ID)
		if err != nil {
			return nil, err
		}
		video := false
		for streams.Next() {
			var kind, transfer, raw string
			var t TitleFileTrack
			if err = streams.Scan(&kind, &t.Codec, &t.Language, &t.Title, &t.Channels, &t.ChannelLayout, &t.Default, &t.Forced, &transfer, &raw); err != nil {
				streams.Close()
				return nil, err
			}
			var stream assets.Stream
			stream.DecodeDetail(raw)
			detail := stream.Detail
			if kind == "audio" {
				t.ObjectAudio, t.BitRate = detail.ObjectAudio, detail.BitRate
				f.Audio = append(f.Audio, t)
				continue
			}
			// The first video stream is the picture; later ones are attached covers.
			if video || f.Video == nil {
				continue
			}
			video = true
			f.Video.DynamicRange = detail.DynamicRange
			if f.Video.DynamicRange == "" {
				f.Video.DynamicRange = assets.DynamicRangeOf(transfer, detail.DolbyVisionProfile)
			}
			f.Video.HDR10Plus, f.Video.FrameRate, f.Video.BitDepth, f.Video.BitRate = detail.HDR10Plus, detail.FrameRate, detail.BitDepth, detail.BitRate
		}
		err = streams.Err()
		streams.Close()
		if err != nil {
			return nil, err
		}
		subs, err := s.read().Query(`SELECT format,language,title,is_default,is_forced,origin='sidecar' FROM asset_subtitles WHERE asset_id=? ORDER BY origin,stream_index,id`, f.ID)
		if err != nil {
			return nil, err
		}
		for subs.Next() {
			var t TitleFileTrack
			if err = subs.Scan(&t.Codec, &t.Language, &t.Title, &t.Default, &t.Forced, &t.External); err != nil {
				subs.Close()
				return nil, err
			}
			f.Subtitles = append(f.Subtitles, t)
		}
		err = subs.Err()
		subs.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) referenceDiscTitles(files []TitleFile) *FileInfoDisc {
	tx := dbwork.Snapshot(s.Context())
	if tx == nil {
		return nil
	}
	for _, file := range files {
		var object, revision string
		if err := tx.QueryRowContext(s.Context(), `SELECT o.id,o.revision FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE a.token=? AND o.retired=0 AND o.state='available' ORDER BY o.id LIMIT 1`, file.ID).Scan(&object, &revision); err != nil {
			continue
		}
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
