package storage

import (
	"encoding/json"
	"errors"
)

func parseLyricProbe(data []byte) ([]map[string]string, error) {
	var payload struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			Tags map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if json.Unmarshal(data, &payload) != nil || len(payload.Streams) > 128 {
		return nil, errors.New("invalid lyric tags")
	}
	out := []map[string]string{payload.Format.Tags}
	for _, s := range payload.Streams {
		out = append(out, s.Tags)
	}
	return out, nil
}

type lyricProbeBuffer struct{ data []byte }

func (b *lyricProbeBuffer) Write(p []byte) (int, error) {
	if len(p) > 1<<20-len(b.data) {
		return 0, errors.New("embedded lyrics exceed limit")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
