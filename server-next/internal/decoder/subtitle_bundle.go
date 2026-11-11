package decoder

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

// MaxBundledSubtitles is the most subtitle tracks one bundle takes from a file.
const MaxBundledSubtitles = 256

// subtitleInput is the start of every subtitle command: one input on the bridge.
func subtitleInput(input string) []string {
	return []string{"-hide_banner", "-nostdin", "-v", "error", "-threads", "1", "-copyts", "-start_at_zero",
		"-protocol_whitelist", "http,tcp", "-format_whitelist", "mov,matroska,avi,mpegts,mpeg", "-i", input}
}

// RunSubtitleBundle copies subtitle tracks of one file, untouched, into one small Matroska
// file, reading the file once, at background priority: what a prepared copy keeps the
// subtitles inside its source from, however many there are. places are the tracks' places
// among the file's subtitle tracks (ffmpeg's 0:s:N), so nothing has to be probed first. max
// bounds the whole output.
func RunSubtitleBundle(ctx context.Context, sup *processpool.Supervisor, key, executable, input string, res *EndpointReservation,
	places []int, max int64) ([]byte, error) {
	endpoint, err := bridgeEndpoint(input)
	if err != nil || !res.matches(endpoint) || !filepath.IsAbs(executable) || key == "" || len(places) == 0 || len(places) > MaxBundledSubtitles {
		res.Close()
		return nil, ErrInvalidConfiguration
	}
	args := subtitleInput(input)
	for _, place := range places {
		if place < 0 || place > 65535 {
			res.Close()
			return nil, ErrInvalidConfiguration
		}
		args = append(args, "-map", "0:s:"+strconv.Itoa(place))
	}
	args = append(args, "-vn", "-an", "-dn", "-c:s", "copy", "-map_metadata", "-1", "-f", "matroska", "pipe:1")
	cmd, err := decoderJob{executable: executable, args: args, endpoint: endpoint, background: true}.command()
	if err != nil {
		res.Close()
		return nil, err
	}
	return runSubtitleIn(ctx, sup, "optimization:subtitle:"+key, cmd, input, res, max)
}

// RunSubtitleToVTT writes one text subtitle track of a file (a bundle) as WebVTT, at
// background priority, at most 4 MiB.
func RunSubtitleToVTT(ctx context.Context, sup *processpool.Supervisor, key, executable, input string, res *EndpointReservation, index int) ([]byte, error) {
	endpoint, err := bridgeEndpoint(input)
	if err != nil || !res.matches(endpoint) || !filepath.IsAbs(executable) || key == "" || index < 0 || index > 65535 {
		res.Close()
		return nil, ErrInvalidConfiguration
	}
	args := append(subtitleInput(input), "-map", "0:"+strconv.Itoa(index), "-vn", "-an", "-dn", "-c:s", "webvtt", "-map_metadata", "-1",
		"-f", "webvtt", "pipe:1")
	cmd, err := decoderJob{executable: executable, args: args, endpoint: endpoint, background: true}.command()
	if err != nil {
		res.Close()
		return nil, err
	}
	return runSubtitleIn(ctx, sup, "optimization:subtitle:"+key, cmd, input, res, 4<<20)
}
