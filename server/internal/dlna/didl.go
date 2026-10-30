package dlna

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// Originals are served as they are: byte seeks (OP=01), not converted (CI=0),
// streaming transfer with background and stall support, DLNA 1.5.
const (
	dlnaOperations  = "DLNA.ORG_OP=01;DLNA.ORG_CI=0"
	dlnaFlags       = "DLNA.ORG_FLAGS=01700000000000000000000000000000"
	contentFeatures = dlnaOperations + ";" + dlnaFlags
)

// mediaType is the type a file is served and described with. Players match
// protocolInfo against what they decode, so it must be the served type.
func mediaType(container, video string) string {
	return assets.OriginalContentType(container, video)
}

func protocolInfo(mime string) string { return "http-get:*:" + mime + ":" + contentFeatures }

// sourceProtocols is ConnectionManager's GetProtocolInfo source list: every
// type an original can be served as.
func sourceProtocols() string {
	types := []string{"video/mp4", "video/x-matroska", "video/webm", "video/x-msvideo", "video/mp2t", "video/mpeg",
		"audio/mpeg", "audio/aac", "audio/flac", "audio/ogg", "audio/wav", "audio/aiff", "audio/mp4", "audio/x-ape", "audio/x-wavpack", "audio/x-dsf", "audio/x-dff", "audio/x-matroska", "audio/x-ms-wma", "audio/x-tta", "audio/x-musepack", "audio/x-caf", "application/octet-stream"}
	out := make([]string, len(types))
	for i, v := range types {
		out[i] = protocolInfo(v)
	}
	return strings.Join(out, ",")
}

// duration is DIDL's H+:MM:SS.F+.
func duration(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	ms := int64(seconds*1000 + 0.5)
	return fmt.Sprintf("%d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// extension names a media URL for players that judge a file by its name.
func extension(container string) string {
	if container == "" || strings.Trim(container, "abcdefghijklmnopqrstuvwxyz0123456789") != "" || len(container) > 8 {
		return ""
	}
	return "." + container
}

func attribute(b *strings.Builder, name, value string) {
	b.WriteString(" " + name + `="` + xmlText(value) + `"`)
}

func element(b *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	b.WriteString("<" + name + ">" + xmlText(value) + "</" + name + ">")
}

// didl renders objects as a DIDL-Lite document with URLs on base.
func didl(base string, objects []object) string {
	var b strings.Builder
	b.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:dlna="urn:schemas-dlna-org:metadata-1-0/">`)
	for _, o := range objects {
		tag := "item"
		if o.container {
			tag = "container"
		}
		b.WriteString("<" + tag)
		attribute(&b, "id", o.id)
		attribute(&b, "parentID", o.parent)
		attribute(&b, "restricted", "1")
		if o.container {
			attribute(&b, "searchable", "0")
			attribute(&b, "childCount", strconv.Itoa(o.children))
		}
		b.WriteString(">")
		element(&b, "dc:title", o.title)
		element(&b, "upnp:class", o.class)
		if o.year > 0 {
			element(&b, "dc:date", fmt.Sprintf("%04d-01-01", o.year))
		}
		if o.artist != "" {
			element(&b, "upnp:artist", o.artist)
			element(&b, "dc:creator", o.artist)
		}
		element(&b, "upnp:album", o.album)
		if o.track > 0 && o.res != nil {
			element(&b, "upnp:originalTrackNumber", strconv.Itoa(o.track))
		}
		if o.art != "" {
			element(&b, "upnp:albumArtURI", base+artPath+o.art)
		}
		if r := o.res; r != nil {
			b.WriteString("<res")
			attribute(&b, "protocolInfo", protocolInfo(mediaType(r.container, r.video)))
			if r.sampleRate > 0 {
				attribute(&b, "sampleFrequency", strconv.Itoa(r.sampleRate))
			}
			if r.bitDepth > 0 {
				attribute(&b, "bitsPerSample", strconv.Itoa(r.bitDepth))
			}
			if r.channels > 0 {
				attribute(&b, "nrAudioChannels", strconv.Itoa(r.channels))
			}
			if r.size > 0 {
				attribute(&b, "size", strconv.FormatInt(r.size, 10))
			}
			if d := duration(r.duration); d != "" {
				attribute(&b, "duration", d)
			}
			if r.video != "" && r.width > 0 && r.height > 0 {
				attribute(&b, "resolution", strconv.Itoa(r.width)+"x"+strconv.Itoa(r.height))
			}
			b.WriteString(">" + xmlText(base+mediaPath+r.token+resourceExtension(r.container, r.video)) + "</res>")
		}
		b.WriteString("</" + tag + ">")
	}
	b.WriteString("</DIDL-Lite>")
	return b.String()
}
