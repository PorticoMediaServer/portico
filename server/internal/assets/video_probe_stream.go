package assets

import (
	"encoding/json"
	"errors"
)

// VideoStreamDetailFromProbeJSON normalizes one actual video stream from an
// already bounded probe response. It opens no source and grants no eligibility.
func VideoStreamDetailFromProbeJSON(raw []byte) (StreamDetail, error) {
	var stream struct {
		Kind   string `json:"codec_type"`
		Codec  string `json:"codec_name"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		probeStreamDetail
	}
	if err := json.Unmarshal(raw, &stream); err != nil {
		return StreamDetail{}, err
	}
	if stream.Kind != "video" {
		return StreamDetail{}, errors.New("probe stream is not video")
	}
	return streamDetail("video", stream.Codec, stream.Width, stream.Height, stream.probeStreamDetail), nil
}
