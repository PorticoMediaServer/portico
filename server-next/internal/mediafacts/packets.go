package mediafacts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

// Packets is an audio file's packet clock in microseconds. Packets may cover more than
// the decoded audio (encoder delay and padding). Copied audiobook parts must be placed
// using this length, or the next part's packets overlap the previous part.
type Packets struct {
	// StartUS is where the first packet starts, against the start FFmpeg gives the file
	// (format start_time), which is where FFmpeg's concat demuxer puts the file: 0 or less.
	StartUS int64
	// LengthUS is how long the packets take together: from the start of the first to the
	// end of the last.
	LengthUS int64
}

// ErrNoPackets: the file's packets could not be measured exactly. The file is still played;
// it has only its gapless length.
var ErrNoPackets = errors.New("the file's packets could not be measured")

// packetsTime bounds one measurement: an MP3 with no header that counts its frames is read
// whole, which on remote storage takes long.
const packetsTime = 10 * time.Minute

// measurePackets reads the first and tail packets of indexed containers. MP3 uses
// its header's frame count when available; headerless MP3 and ADTS are walked
// frame by frame because their byte seeks cannot establish an exact packet clock.
func (p Probe) measurePackets(ctx context.Context, path string, facts *Facts) (Packets, error) {
	if p.ReadGuard != nil {
		if err := p.ReadGuard(ctx, path); err != nil {
			return Packets{}, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, packetsTime)
	defer cancel()
	protocols := "file,pipe"
	if strings.Contains(path, "://") {
		protocols = "http,tcp"
	}
	options := []string{"-protocol_whitelist", protocols, "-format_whitelist", mediaexec.LocalInputFormats}

	mp3 := facts.Container == "mp3" && facts.AudioCodec == "mp3"
	adts := facts.Container == "aac" || facts.Container == "adts"
	intervals := "%+#1,99999999%"
	if mp3 || adts {
		intervals = "%+#1"
	}
	found, err := p.packetRead(ctx, path, options, intervals)
	if err != nil {
		return Packets{}, err
	}
	if mp3 || adts {
		// Unindexed sound is counted without decoding. An MP3 header can supply
		// that count without reading the rest of the file.
		source, size, done, err := p.openReader(ctx, path)
		if err != nil {
			return Packets{}, err
		}
		defer done()
		read := readerAt(source, size)

		extent := func(_ context.Context, offset, length int64) ([]byte, error) { return read(offset, length) }
		var samples int64
		var rate int
		counted := false
		if mp3 {
			head, err := audiofacts.MP3Head(read)
			if err != nil {
				return Packets{}, err
			}
			samples, rate, counted = audiofacts.MP3HeaderSamples(ctx, head)
			if !counted {
				samples, rate, err = audiofacts.MP3Samples(ctx, extent, size)
			}
		} else {
			samples, rate, err = audiofacts.ADTSSamples(ctx, extent, size)
		}
		if err != nil {
			if ctx.Err() != nil {
				return Packets{}, ctx.Err()
			}
			return Packets{}, ErrNoPackets
		}
		if mp3 && rate != found.rate || rate <= 0 || samples <= 0 {
			return Packets{}, ErrNoPackets
		}
		found.end = new(big.Rat).Add(found.start, big.NewRat(samples, int64(rate)))
		if mp3 && !counted {
			// Without a frame-count header, ffprobe's bitrate estimate is not a duration.
			facts.Duration = float64(samples) / float64(rate)
			facts.BitRate = int64(float64(size) * 8 / facts.Duration)
		}
	}
	// Some demuxers (notably WAV) return no packets when seeking beyond EOF.
	// Seek inside their stated length instead, then read through the actual end.
	if !mp3 && !adts && found.end != nil && found.duration > 0 {
		end, _ := new(big.Rat).Sub(found.end, found.formatStart).Float64()
		if end < found.duration-0.000001 {
			found, err = p.packetRead(ctx, path, options, "%+#1,"+strconv.FormatFloat(max(0, found.duration-10), 'f', 6, 64)+"%")
			if err != nil {
				return Packets{}, err
			}
		}
	}
	if found.end == nil || found.end.Cmp(found.start) <= 0 {
		return Packets{}, ErrNoPackets
	}
	start := new(big.Rat).Sub(found.start, found.formatStart)
	length := new(big.Rat).Sub(found.end, found.start)
	return Packets{StartUS: microseconds(start), LengthUS: microseconds(length)}, nil
}

// packetFacts is what one packet read found, in seconds: the file's start, its first packet's
// start and the latest end of a packet read.
type packetFacts struct {
	rate               int
	duration           float64
	formatStart, start *big.Rat
	end                *big.Rat
}

func (p Probe) packetRead(ctx context.Context, path string, options []string, intervals string) (packetFacts, error) {
	args := append(append([]string{}, options...), "-v", "error", "-select_streams", "a:0", "-read_intervals", intervals,
		"-show_entries", "stream=sample_rate,time_base:format=start_time,duration:packet=pts,duration", "-of", "json", path)
	data, err := p.run(ctx, path, path, options, args, false)
	if err != nil {
		return packetFacts{}, err
	}
	var doc struct {
		Streams []struct {
			Rate     string `json:"sample_rate"`
			TimeBase string `json:"time_base"`
		} `json:"streams"`
		Format struct {
			Start    string `json:"start_time"`
			Duration string `json:"duration"`
		} `json:"format"`
		Packets []struct {
			PTS      *int64 `json:"pts"`
			Duration *int64 `json:"duration"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || len(doc.Streams) != 1 || len(doc.Packets) == 0 {
		return packetFacts{}, ErrNoPackets
	}
	base, ok := new(big.Rat).SetString(doc.Streams[0].TimeBase)
	if !ok || base.Sign() <= 0 {
		return packetFacts{}, ErrNoPackets
	}
	out := packetFacts{formatStart: new(big.Rat)}
	out.rate, _ = strconv.Atoi(doc.Streams[0].Rate)
	out.duration, _ = strconv.ParseFloat(doc.Format.Duration, 64)
	if doc.Format.Start != "" {
		if out.formatStart, ok = new(big.Rat).SetString(doc.Format.Start); !ok {
			return packetFacts{}, ErrNoPackets
		}
	}
	at := func(ticks int64) *big.Rat { return new(big.Rat).Mul(big.NewRat(ticks, 1), base) }
	for n, packet := range doc.Packets {
		if packet.PTS == nil || packet.Duration == nil || *packet.Duration < 0 {
			return packetFacts{}, ErrNoPackets
		}
		if n == 0 {
			out.start = at(*packet.PTS)
		}
		if end := at(*packet.PTS + *packet.Duration); out.end == nil || end.Cmp(out.end) > 0 {
			out.end = end
		}
	}
	return out, nil
}

// readerAt reads length bytes at offset, never past the end.
func readerAt(source io.ReaderAt, size int64) func(offset, length int64) ([]byte, error) {
	return func(offset, length int64) ([]byte, error) {
		buf := make([]byte, max(0, min(length, size-offset)))
		n, err := source.ReadAt(buf, offset)
		if errors.Is(err, io.EOF) && n > 0 {
			err = nil
		}
		return buf[:n], err
	}
}

func microseconds(r *big.Rat) int64 {
	f, _ := new(big.Rat).Mul(r, big.NewRat(1_000_000, 1)).Float64()
	return int64(math.Round(f))
}

// CompleteAudioPackets records the packet clock for a sound-only file during a scan.
// An unmeasurable file keeps its ordinary duration; zero prevents repeated attempts
// until the file changes. Transient read failures leave it for the next scan.
func (p Probe) CompleteAudioPackets(ctx context.Context, path string, facts *Facts) error {
	if facts.AudioCodec == "" || facts.VideoCodec != "" {
		return nil
	}
	packets, err := p.measurePackets(ctx, path, facts)
	if err != nil && !errors.Is(err, ErrNoPackets) {
		return err
	}
	facts.Packets = &packets
	return nil
}
