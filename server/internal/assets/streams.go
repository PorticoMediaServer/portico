package assets

import (
	"database/sql"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Stream struct {
	Index         int    `json:"index"`
	Type          string `json:"type"`
	Codec         string `json:"codec"`
	Language      string `json:"language,omitempty"`
	Title         string `json:"title,omitempty"`
	Channels      int    `json:"channels,omitempty"`
	ChannelLayout string `json:"channelLayout,omitempty"`
	Default       bool   `json:"default"`
	Forced        bool   `json:"forced"`
	// Colour characteristics decide HDR. They are published because a client
	// that knows the source is PQ can explain a tone-mapped picture.
	ColorTransfer  string `json:"colorTransfer,omitempty"`
	ColorPrimaries string `json:"colorPrimaries,omitempty"`
	// Accessibility dispositions drive automatic track choice: an SDH track is
	// not the forced track, and a commentary is never the default programme audio.
	HearingImpaired bool `json:"-"`
	Commentary      bool `json:"-"`
	// Detail is the planning fact set; see StreamDetail. It is stored, and read by
	// delivery planning, but is not part of the published offer stream.
	Detail StreamDetail `json:"-"`
}

// storedDetail is the persisted form of everything Stream keeps off the wire.
type storedDetail struct {
	StreamDetail
	HearingImpaired bool `json:"hearingImpaired,omitempty"`
	Commentary      bool `json:"commentary,omitempty"`
}

// EncodeDetail is the stored representation of one stream's planning facts.
func (s Stream) EncodeDetail() string {
	detail := s.Detail
	detail.StreamBreadth = StreamBreadth{}
	raw, err := json.Marshal(storedDetail{detail, s.HearingImpaired, s.Commentary})
	if err != nil || len(raw) > 4096 {
		return ""
	}
	return string(raw)
}

// DecodeDetail restores planning facts. A missing or malformed value yields the
// zero detail, which planning reads as "not observed".
func (s *Stream) DecodeDetail(raw string) {
	var d storedDetail
	if raw == "" || len(raw) > 4096 || json.Unmarshal([]byte(raw), &d) != nil {
		return
	}
	s.Detail, s.HearingImpaired, s.Commentary = d.StreamDetail, d.HearingImpaired, d.Commentary
}

func streamText(value string, max int) string {
	if !utf8.ValidString(value) {
		return ""
	}
	r := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value))
	if len(r) > max {
		r = r[:max]
	}
	return string(r)
}
func PersistStreams(tx *sql.Tx, asset string, size, modified int64, streams []Stream) error {
	if streams == nil {
		return nil
	}
	fingerprint, e := streamInputFingerprint(streams)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES(?,1,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET detail_version=excluded.detail_version,revision=asset_stream_facts.revision+CASE WHEN asset_stream_facts.fingerprint!=excluded.fingerprint OR asset_stream_facts.size!=excluded.size OR asset_stream_facts.modified_ns!=excluded.modified_ns THEN 1 ELSE 0 END,size=excluded.size,modified_ns=excluded.modified_ns,fingerprint=excluded.fingerprint`, asset, size, modified, fingerprint, StreamDetailVersion)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`DELETE FROM asset_streams WHERE asset_id=?`, asset); e != nil {
		return e
	}
	for _, s := range streams {
		_, e = tx.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, asset, s.Index, s.Type, s.Codec, s.Language, s.Title, s.Channels, s.ChannelLayout, s.Default, s.Forced, streamText(s.ColorTransfer, 32), streamText(s.ColorPrimaries, 32), s.EncodeDetail())
		if e != nil {
			return e
		}
		if e = persistStreamBreadth(tx, asset, s); e != nil {
			return e
		}
	}
	return nil
}
