package streaming

import (
	"context"
	"errors"
	"fmt"

	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// strmProbe is the confined ffprobe document of a STRM target: one video and
// one audio stream.
func strmProbe(format, video, profile, pixFmt string, height int, audio string) []byte {
	return []byte(fmt.Sprintf(`{"format":{"format_name":%q,"duration":"600.000000","start_time":"0.000000","bit_rate":"6000000"},"streams":[
{"index":0,"codec_type":"video","codec_name":%q,"profile":%q,"pix_fmt":%q,"width":%d,"height":%d,"bit_rate":"5500000","color_transfer":"bt709","disposition":{"default":1}},
{"index":1,"codec_type":"audio","codec_name":%q,"channels":2,"channel_layout":"stereo","bit_rate":"192000","tags":{"language":"eng"},
	"disposition":{"default":1}}]}`, format, video, profile, pixFmt, height*16/9, height, audio))
}

type strmWindows struct{}

func (strmWindows) OpenPlaybackWindow(context.Context, string) (subtitles.WindowInput, error) {
	return nil, errors.New("no window in this test")
}
