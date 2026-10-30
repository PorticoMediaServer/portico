package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// remoteFacts is what the server measured of a STRM target's media when it
// accepted a session, stored as remote_playback.facts_json. The server opens
// the URL, never the player, so a STRM is planned like any other file from
// these facts: relayed as it is, remuxed or converted. Every plan for the
// session, the first and every re-plan, reads the same facts.
type remoteFacts struct {
	Container  string         `json:"container"`
	DurationUS int64          `json:"durationUs"`
	BitRate    int64          `json:"bitRate,omitempty"`
	Streams    []remoteStream `json:"streams"`
}

// remoteStream keeps everything a stream row and its breadth row hold for a
// catalogued file, including the facts Stream keeps off its published shape.
type remoteStream struct {
	assets.Stream
	StreamDetail    assets.StreamDetail `json:"detail"`
	HearingImpaired bool                `json:"hearingImpaired,omitempty"`
	Commentary      bool                `json:"commentary,omitempty"`
}

func (r remoteStream) stream() assets.Stream {
	st := r.Stream
	st.Detail, st.HearingImpaired, st.Commentary = r.StreamDetail, r.HearingImpaired, r.Commentary
	return st
}

const remoteFactsLimit = 64 << 10

var errRemoteFacts = errors.New("remote source facts are missing or malformed")

// measuredRemoteFacts keeps the planning facts of a probe: its container,
// duration, rate and its video and audio streams. A source with no picture is
// not a STRM the server plays.
func measuredRemoteFacts(f assets.Facts) (remoteFacts, error) {
	out := remoteFacts{Container: f.Container, BitRate: f.BitRate, Streams: []remoteStream{}}
	if !(f.Duration > 0) || f.Duration > 86400 || out.Container == "" {
		return remoteFacts{}, errRemoteFacts
	}
	out.DurationUS = int64(f.Duration * 1e6)
	video := false
	for _, st := range f.Streams {
		if st.Type != "video" && st.Type != "audio" {
			continue
		}
		video = video || st.Type == "video"
		out.Streams = append(out.Streams, remoteStream{Stream: st, StreamDetail: st.Detail, HearingImpaired: st.HearingImpaired, Commentary: st.Commentary})
	}
	if !video {
		return remoteFacts{}, errRemoteFacts
	}
	if raw, err := json.Marshal(out); err != nil || len(raw) > remoteFactsLimit {
		return remoteFacts{}, errRemoteFacts
	}
	return out, nil
}

func (r remoteFacts) encode() (string, error) {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > remoteFactsLimit {
		return "", errRemoteFacts
	}
	return string(raw), nil
}

func decodeRemoteFacts(raw string) (remoteFacts, error) {
	var r remoteFacts
	if len(raw) < 2 || len(raw) > remoteFactsLimit || json.Unmarshal([]byte(raw), &r) != nil || r.Container == "" || r.DurationUS <= 0 {
		return remoteFacts{}, errRemoteFacts
	}
	return r, nil
}

// source is the planner's view of the measured media. It keeps the STRM's
// asset id and is marked Remote, so its plan is served from the relay or from
// the remote window input, never from a catalogue path.
func (r remoteFacts) source(asset string) DeliverySource {
	f := assets.Facts{Container: r.Container, Duration: float64(r.DurationUS) / 1e6}
	for _, st := range r.Streams {
		f.Streams = append(f.Streams, st.stream())
	}
	out := discSourceFacts(asset, f)
	if out.BitRate == 0 && r.BitRate > 0 {
		// The container's stated rate; per-stream rates are often absent.
		out.BitRate = r.BitRate
	}
	out.Remote = true
	return out
}

// summary is the first-codec catalogue summary of the measured media.
func (r remoteFacts) summary() (video, audio string, width, height int, duration float64) {
	for _, st := range r.Streams {
		if st.Type == "video" && video == "" {
			video, width, height = st.Codec, st.StreamDetail.Width, st.StreamDetail.Height
		}
		if st.Type == "audio" && audio == "" {
			audio = st.Codec
		}
	}
	return video, audio, width, height, float64(r.DurationUS) / 1e6
}

// remoteSessionSourceTx is a STRM session's measured source. ok is false for
// any other session.
func remoteSessionSourceTx(ctx context.Context, tx sourceQuery, session, asset string) (DeliverySource, bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT rp.facts_json FROM remote_playback rp JOIN playback_sessions ps ON ps.id=rp.session_id WHERE rp.session_id=? AND ps.asset_id=?`, session, asset).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DeliverySource{}, false, nil
	}
	if err != nil {
		return DeliverySource{}, false, err
	}
	facts, err := decodeRemoteFacts(raw)
	if err != nil {
		return DeliverySource{}, false, err
	}
	return facts.source(asset), true, nil
}

// lastRemoteSourceTx is the most recently measured media of a STRM, for a
// side-effect-free plan preview. It is only a preview: a start measures again.
func lastRemoteSourceTx(ctx context.Context, tx sourceQuery, asset string) (DeliverySource, bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT rp.facts_json FROM remote_playback rp JOIN playback_sessions ps ON ps.id=rp.session_id WHERE ps.asset_id=? ORDER BY ps.rowid DESC LIMIT 1`, asset).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DeliverySource{}, false, nil
	}
	if err != nil {
		return DeliverySource{}, false, err
	}
	facts, err := decodeRemoteFacts(raw)
	if err != nil {
		return DeliverySource{}, false, err
	}
	return facts.source(asset), true, nil
}
