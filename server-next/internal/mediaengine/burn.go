package mediaengine

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitleoffer"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// burnTrack is a subtitle that can be drawn into the picture, and how its bytes are had.
type burnTrack struct {
	track db.Track
	// index is its number in the video file, for a track inside it.
	index int
	// kind is what the subtitle is, as a device names what it draws: pgs, vobsub, dvb, ass
	// or text. picture says it is made of pictures. format is the form the converter is
	// handed: mks (a track copied out as it is), ass, pgs or vobsub.
	kind, format string
	picture      bool
	// nudgeMs is the viewer's timing nudge, which is burned in with the subtitle.
	nudgeMs int64
}

// burning is the chosen subtitle as something that can be burned, or nil when there is no
// chosen subtitle or it cannot be burned. Every kind inside the video can be; of files
// beside it, text, PGS (.sup) and VobSub (.idx with its .sub). When the app asked for
// burning by name and it cannot be done, that is refused with the reason.
func burning(r playback.PlanRequest, facts mediafacts.Facts) (*burnTrack, error) {
	asked := r.Choice.SubtitleDelivery == "burn"
	if r.Choice.SubtitleTrackID == "" || r.Choice.SubtitlesOff {
		return nil, nil
	}
	for _, track := range r.Version.Tracks {
		if track.Kind != db.StreamSubtitle || playback.TrackID(track) != r.Choice.SubtitleTrackID {
			continue
		}
		b := &burnTrack{track: track, nudgeMs: r.Choice.SubtitleOffsetMs}
		switch format := subtitleFormat(track); {
		case format == "pgs" || format == "sup":
			b.kind, b.format, b.picture = "pgs", "pgs", true
		case format == "vobsub" || format == "idx":
			b.kind, b.format, b.picture = "vobsub", "vobsub", true
		case format == "dvb" && !track.Sidecar:
			b.kind, b.picture = "dvb", true
		case format == "ass" || format == "ssa":
			b.kind, b.format = "ass", format
		case subtitleoffer.TextFormat(format):
			b.kind, b.format = "text", "ass"
		default:
			b = nil
		}
		if b != nil && !track.Sidecar && track.Downloaded == "" {
			found := false
			if b.index, found = fileTrack(r.Version, facts, db.StreamSubtitle, "subtitle", r.Choice.SubtitleTrackID); !found {
				b = nil
			} else if b.picture {
				b.format = "mks"
			} else {
				b.format = "ass"
			}
		}
		if b == nil && asked {
			return nil, &playback.Invalid{Field: "subtitles.delivery", Message: "This subtitle is in a form that cannot be burned into the picture."}
		}
		return b, nil
	}
	return nil, nil
}

// burnLoader reads a plan's burned subtitle when the converter first needs it.
func (e *Engine) burnLoader(p *planned, source streaming.Source) func(context.Context) (*streaming.BurnSubtitle, error) {
	b := p.burn
	if p.disc != nil {
		// A disc's own subtitle is read out of the title.
		source = streaming.DiscSource(p.disc.open)
	}
	if source == nil {
		source = e.place("").streams.LibrarySource(p.place.Path, p.place.Size, time.Unix(0, p.place.ModifiedNS))
	}
	return func(ctx context.Context) (*streaming.BurnSubtitle, error) {
		// Taking a track out of a large film reads all of it once. Ten minutes covers a
		// slow disk; the stream stopping ends it sooner.
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		out := &streaming.BurnSubtitle{Format: b.format, OffsetUS: b.nudgeMs * 1000}
		var err error
		if !b.track.Sidecar && b.track.Downloaded == "" {
			out.Data, err = e.place("").streams.SubtitleTrack(ctx, source, b.index, !b.picture)
			return out, err
		}
		if b.track.Downloaded != "" {
			source = downloadedSubtitle{dir: e.subtitleDir, hash: b.track.DownloadedHash}
		} else {
			_, source, err = e.subtitleFile(ctx, b.track.FileID)
			if err != nil {
				return nil, err
			}
		}
		if out.Data, err = e.place("").streams.SubtitleFile(ctx, source); err != nil {
			return nil, err
		}
		switch {
		case b.kind == "vobsub":
			id, findErr := e.db.SubtitleCompanion(ctx, b.track.FileID)
			if findErr != nil {
				return nil, findErr
			}
			_, source, err = e.subtitleFile(ctx, id)
			if err == nil {
				out.Companion, err = e.place("").streams.SubtitleFile(ctx, source)
			}
		case b.kind == "text":
			out.Data, err = subtitles.TextAsASS(out.Data, subtitleFormat(b.track), 0)
		}
		return out, err
	}
}

// subtitleFile uses the recorded local file or configured remote source for both
// original delivery and burn-in. Neither path comes from the player's request.
func (e *Engine) subtitleFile(ctx context.Context, id int64) (db.FileOnDisk, streaming.Source, error) {
	file, err := e.db.FileOnDisk(ctx, id)
	if errors.Is(err, db.ErrNotFound) && e.remote != nil {
		var sourceID int64
		sourceID, file, err = e.db.RemoteFileOf(ctx, id)
		if err == nil {
			storage, _, err := e.remote(ctx, sourceID)
			if err != nil {
				return file, nil, err
			}
			return file, streaming.RemoteFile(storage, file.Path, file.Size, time.Unix(0, file.ModifiedNS)), nil
		}
	}
	if err != nil {
		return file, nil, err
	}
	return file, e.place("").streams.LibrarySource(file.Path, file.Size, time.Unix(0, file.ModifiedNS)), nil
}
