package streaming

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// trackEnds is where each video and audio track of a file ends: its last packet's time plus
// that packet's length, by kind.
func (r *mediaRig) trackEnds(file string) map[string]float64 {
	r.t.Helper()
	raw, err := exec.Command(r.ffprobe, "-v", "error", "-show_entries", "stream=index,codec_type",
		"-show_entries", "packet=stream_index,pts_time,duration_time", "-of", "json", file).Output()
	if err != nil {
		r.t.Fatalf("ffprobe %s: %v", file, err)
	}
	var probed struct {
		Streams []struct {
			Index int    `json:"index"`
			Type  string `json:"codec_type"`
		} `json:"streams"`
		Packets []struct {
			Stream   int    `json:"stream_index"`
			PTS      string `json:"pts_time"`
			Duration string `json:"duration_time"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(raw, &probed); err != nil {
		r.t.Fatal(err)
	}
	kinds := map[int]string{}
	for _, s := range probed.Streams {
		if s.Type == "video" || s.Type == "audio" {
			kinds[s.Index] = s.Type
		}
	}
	ends := map[string]float64{}
	for _, p := range probed.Packets {
		kind, ok := kinds[p.Stream]
		if !ok {
			continue
		}
		at, err := strconv.ParseFloat(p.PTS, 64)
		if err != nil {
			continue
		}
		length, _ := strconv.ParseFloat(p.Duration, 64)
		ends[kind] = max(ends[kind], at+length)
	}
	return ends
}

// playedEnds joins every segment each of a stream's playlists lists, as a player would play
// them to the end, and says where each track ends.
func (r *mediaRig) playedEnds(grant string) map[string]float64 {
	r.t.Helper()
	ends := map[string]float64{}
	for _, p := range r.playlists(grant) {
		var joined []byte
		if p.init != "" {
			joined = append(joined, r.fetch(grant, p.init)...)
		}
		for _, segment := range p.segments {
			joined = append(joined, r.fetch(grant, segment)...)
		}
		ext := ".ts"
		if p.init != "" {
			ext = ".mp4"
		}
		file := filepath.Join(r.t.TempDir(), "played"+ext)
		if err := os.WriteFile(file, joined, 0600); err != nil {
			r.t.Fatal(err)
		}
		for kind, end := range r.trackEnds(file) {
			ends[kind] = max(ends[kind], end)
		}
	}
	return ends
}

// A converted title plays to its last sample, whatever its length: one whose last moments
// fall under a second past a six-second mark (that tail is carried in the slot before it)
// and one whose last slot is long. Measured from the segments the stream lists, against the
// source. Playback rule 13 keeps the original picture and sound where the device plays them;
// a title that stops short loses its end.
func TestAConvertedFilmPlaysToItsLastSample(t *testing.T) {
	for _, length := range []string{"12.4", "15.5"} {
		for _, fragmented := range []bool{true, false} {
			name := length + "s in MPEG-TS"
			if fragmented {
				name = length + "s in fragmented MP4"
			}
			t.Run(name, func(t *testing.T) {
				rig := newMediaRig(t)
				// MPEG-4 part 2 is converted to H.264, which goes out in MPEG-TS; a 10-bit
				// picture is converted to AV1 10-bit for the browser, which goes out only in
				// fragmented MP4.
				film := rig.generate("film-"+length+".avi", 16, "-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp3", "-t", length)
				if fragmented {
					film = rig.generate("film-"+length+".mkv", 16, "-c:v", "libx264", "-pix_fmt", "yuv420p10le", "-profile:v", "high10",
						"-c:a", "mp3", "-t", length)
				}
				stream, plan := rig.start("film", film, 0, nil)
				if plan.VideoAction != "convert" {
					t.Fatalf("the film for a browser was not converted: %s", plan.Strategy)
				}
				if want := map[bool]string{true: "fmp4_hls", false: "mpegts_hls"}[fragmented]; plan.OutputContainer != want {
					t.Fatalf("planned %s, wanted %s", plan.OutputContainer, want)
				}
				rig.ready("film")
				rig.requireEnds(rig.trackEnds(film), rig.playedEnds(stream.Grant))
				rig.streams.Stop("film")
			})
		}
	}
}

// The same for a song: a song that has to be converted plays to its last sample, so the
// last notes of every converted track on an album are heard.
func TestAConvertedSongPlaysToItsLastSample(t *testing.T) {
	for _, length := range []string{"12.6", "15.5"} {
		t.Run(length+"s", func(t *testing.T) {
			rig := newMediaRig(t)
			song := rig.generate("song-"+length+".mka", 16, "-map", "1:a", "-c:a", "ac3", "-t", length)
			stream, plan := rig.start("song", song, 0, nil)
			if plan.AudioAction != "convert" || plan.Mode != "hls" {
				t.Fatalf("AC-3 for a browser was not converted over HLS: %s audio %s", plan.Strategy, plan.AudioAction)
			}
			rig.ready("song")
			rig.requireEnds(rig.trackEnds(song), rig.playedEnds(stream.Grant))
			rig.streams.Stop("song")
		})
	}
}

// requireEnds fails a stream whose tracks end before the source's. A converted track's last
// packet is cut differently, so it may end up to one packet early (an MP3 or AC-3 frame is
// at most 36 ms, a frame of a 24 fps film 42 ms); anything more is the end of the title lost.
func (r *mediaRig) requireEnds(source, played map[string]float64) {
	r.t.Helper()
	const packet = 0.05
	for kind, end := range source {
		got, ok := played[kind]
		if !ok {
			r.t.Fatalf("the stream has no %s track; the source's ends at %.3fs", kind, end)
		}
		r.t.Logf("%s: the source ends at %.3fs, the stream at %.3fs", kind, end, got)
		if got < end-packet {
			r.t.Errorf("%s ends at %.3fs, %.3fs before the source's end at %.3fs", kind, got, end-got, end)
		}
	}
}
