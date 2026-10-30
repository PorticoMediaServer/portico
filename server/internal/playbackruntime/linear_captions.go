package playbackruntime

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/linearinput"
	"strings"
)

// captionMaster publishes only source-declared services and its actual
// declared bandwidth. The child stays under the same family-bound capability
// and rechecks every retained programme through ServeMedia's existing fence.
func captionMaster(c linearinput.CaptionDeclarations, query string) ([]byte, bool) {
	if !c.Valid() || query != "" && !playlistStart.MatchString(query) {
		return nil, false
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n")
	for _, r := range c.Renditions {
		// These values passed the gateway's strict attribute validation. Keep a
		// final emission fence as this helper also accepts internal test inputs.
		if !r.Valid() {
			return nil, false
		}
		def, auto := "NO", "NO"
		if r.Default {
			def = "YES"
		}
		if r.Autoselect {
			auto = "YES"
		}
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=CLOSED-CAPTIONS,GROUP-ID=\"%s\",NAME=\"%s\",INSTREAM-ID=\"%s\",DEFAULT=%s,AUTOSELECT=%s", c.GroupID, r.Name, r.InstreamID, def, auto)
		if r.Language != "" {
			fmt.Fprintf(&b, ",LANGUAGE=\"%s\"", r.Language)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,CLOSED-CAPTIONS=\"%s\"\nstream.m3u8", c.Bandwidth, c.GroupID)
	if query != "" {
		b.WriteByte('?')
		b.WriteString(query)
	}
	b.WriteByte('\n')
	return []byte(b.String()), true
}
