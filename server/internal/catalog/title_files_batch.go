package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// titleFiles reads only the already authorized child page, in its original
// order. Track queries use bounded token chunks, rather than two statements for
// every version. All reads still join the caller's original snapshot.
func (s *Service) titleFiles(items []string, owner bool) ([]TitleFile, error) {
	out := []TitleFile{}
	if len(items) == 0 {
		return out, nil
	}
	rows, err := s.read().Query(`SELECT requested.value,a.token,a.path,a.size,a.container,a.duration,a.video_codec,a.width,a.height,link.available,link.part_index,link.edition
 FROM json_each(?) requested CROSS JOIN catalog_entities e ON e.public_id=pid_blob(requested.value)
 JOIN catalog_asset_links link ON link.entity_id=e.id JOIN catalog_assets a ON a.id=link.asset_id
 ORDER BY CAST(requested.key AS INTEGER),`+compactcatalog.BestVersionOrder("link", "a"), idsJSON(items))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		f := TitleFile{Audio: []TitleFileTrack{}, Subtitles: []TitleFileTrack{}}
		var codec string
		var width, height int
		if err = rows.Scan(&f.ItemID, &f.ID, &f.Path, &f.Size, &f.Container, &f.Duration, &codec, &width, &height, &f.Available, &f.PartIndex, &f.Edition); err != nil {
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
	positions := map[string][]int{}
	tokens := []string{}
	for i, f := range out {
		if _, exists := positions[f.ID]; !exists {
			tokens = append(tokens, f.ID)
		}
		positions[f.ID] = append(positions[f.ID], i)
	}
	const chunk = 128
	for from := 0; from < len(tokens); from += chunk {
		batch := tokens[from:min(from+chunk, len(tokens))]
		if err := s.titleFileTracks(out, positions, batch); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) titleFileTracks(files []TitleFile, positions map[string][]int, tokens []string) error {
	raw := idsJSON(tokens)
	videoTokens := []string{}
	for _, token := range tokens {
		if files[positions[token][0]].Video != nil {
			videoTokens = append(videoTokens, token)
		}
	}
	discs, err := s.readDiscMediaFactsBatch(videoTokens)
	if err != nil {
		return err
	}
	for _, token := range tokens {
		for _, i := range positions[token] {
			if files[i].Video != nil {
				files[i].Disc = discs[token]
			}
		}
	}
	streams, err := s.read().Query(`SELECT asset_id,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,detail_json
 FROM asset_streams WHERE asset_id IN(SELECT value FROM json_each(?)) AND type IN('video','audio') ORDER BY asset_id,stream_index`, raw)
	if err != nil {
		return err
	}
	video := map[string]bool{}
	for streams.Next() {
		var token, kind, transfer, detailJSON string
		var track TitleFileTrack
		if err = streams.Scan(&token, &kind, &track.Codec, &track.Language, &track.Title, &track.Channels, &track.ChannelLayout, &track.Default, &track.Forced, &transfer, &detailJSON); err != nil {
			streams.Close()
			return err
		}
		var stream assets.Stream
		stream.DecodeDetail(detailJSON)
		detail := stream.Detail
		if kind == "audio" {
			track.ObjectAudio, track.BitRate = detail.ObjectAudio, detail.BitRate
			for _, i := range positions[token] {
				files[i].Audio = append(files[i].Audio, track)
			}
			continue
		}
		if video[token] {
			continue
		}
		video[token] = true
		for _, i := range positions[token] {
			picture := files[i].Video
			if picture == nil {
				continue
			}
			picture.DynamicRange = detail.DynamicRange
			if picture.DynamicRange == "" {
				picture.DynamicRange = assets.DynamicRangeOf(transfer, detail.DolbyVisionProfile)
			}
			picture.HDR10Plus, picture.FrameRate, picture.BitDepth, picture.BitRate = detail.HDR10Plus, detail.FrameRate, detail.BitDepth, detail.BitRate
		}
	}
	err = streams.Err()
	streams.Close()
	if err != nil {
		return err
	}
	subs, err := s.read().Query(`SELECT asset_id,format,language,title,is_default,is_forced,origin='sidecar'
 FROM asset_subtitles WHERE asset_id IN(SELECT value FROM json_each(?)) ORDER BY asset_id,origin,stream_index,id`, raw)
	if err != nil {
		return err
	}
	defer subs.Close()
	for subs.Next() {
		var token string
		var track TitleFileTrack
		if err = subs.Scan(&token, &track.Codec, &track.Language, &track.Title, &track.Default, &track.Forced, &track.External); err != nil {
			return err
		}
		for _, i := range positions[token] {
			files[i].Subtitles = append(files[i].Subtitles, track)
		}
	}
	return subs.Err()
}
