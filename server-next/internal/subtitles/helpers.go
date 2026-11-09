package subtitles

import (
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server-next/internal/subtitleoffer"
)

func requestDigest(v any) string { b, _ := json.Marshal(v); return digestBytes(b) }

func textFormat(s string) bool { return subtitleoffer.TextFormat(s) }
