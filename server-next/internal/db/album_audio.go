package db

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// AlbumAudioSummary is the common recorded format of an album's visible songs.
// Missing facts stay absent. Mixed formats and wholly unknown formats have no common facts.
type AlbumAudioSummary struct {
	State      string
	Codec      string
	BitDepth   int
	SampleRate int
	Channels   int
	Bitrate    int64
}

// albumAudio runs only for an album detail, not for lists. The left joins retain songs
// whose files have not been probed; their unknown facts must not be silently skipped.
func albumAudio(ctx context.Context, tx *sql.Tx, v Viewer, acc access, albumID int64) (*AlbumAudioSummary, error) {
	var total, known, codecs, depths, minDepth, maxDepth, rates, minRate, maxRate, channels, minChannels, maxChannels, bitrates int
	var minBitrate, maxBitrate int64
	out := &AlbumAudioSummary{State: "unknown"}
	args := append(v.args(), sql.Named("album", albumID), sql.Named("track", KindTrack), sql.Named("audio", StreamAudio))
	err := tx.QueryRowContext(ctx, `SELECT count(*), count(nullif(s.codec, '')), count(DISTINCT nullif(s.codec, '')), ifnull(min(s.codec), ''),
        count(nullif(s.bit_depth, 0)), ifnull(min(nullif(s.bit_depth, 0)), 0), ifnull(max(s.bit_depth), 0),
        count(nullif(s.sample_rate, 0)), ifnull(min(nullif(s.sample_rate, 0)), 0), ifnull(max(s.sample_rate), 0),
        count(nullif(s.channels, 0)), ifnull(min(nullif(s.channels, 0)), 0), ifnull(max(s.channels), 0),
        count(nullif(s.bitrate, 0)), ifnull(min(nullif(s.bitrate, 0)), 0), ifnull(max(s.bitrate), 0)
        FROM items t LEFT JOIN media m ON m.item_id = t.id LEFT JOIN media_parts p ON p.media_id = m.id
        LEFT JOIN media_streams s ON s.file_id = p.file_id AND s.kind = @audio
        WHERE t.parent_id = @album AND t.kind = @track AND `+acc.visible("t"), args...).Scan(
		&total, &known, &codecs, &out.Codec, &depths, &minDepth, &maxDepth, &rates, &minRate, &maxRate,
		&channels, &minChannels, &maxChannels, &bitrates, &minBitrate, &maxBitrate)
	if err != nil {
		return nil, err
	}
	lossless := mediafacts.LosslessAudio(out.Codec)
	if codecs > 1 || minRate != maxRate || minChannels != maxChannels || lossless && minDepth != maxDepth {
		return &AlbumAudioSummary{State: "mixed"}, nil
	}
	if total == 0 || known != total {
		return &AlbumAudioSummary{State: "unknown"}, nil
	}
	out.State = "common"
	if lossless && depths == total {
		out.BitDepth = minDepth
	}
	if rates == total {
		out.SampleRate = minRate
	}
	if channels == total {
		out.Channels = minChannels
	}
	// Lossless file bitrate varies with its contents. A lossy bitrate is useful only when
	// all the album's streams have that same known rate; never show a decoder's bit depth.
	if !lossless && bitrates == total && minBitrate == maxBitrate {
		out.Bitrate = minBitrate
	}
	return out, nil
}
