package subtitles

import (
	"encoding/json"
	"strconv"
)

type FontAttachment struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}
type Delivery struct {
	Format       string `json:"format"`
	URL          string `json:"url,omitempty"`
	CompanionURL string `json:"companionUrl,omitempty"`
	FontsURL     string `json:"fontsUrl,omitempty"`
	// LinesURL (image subtitles) answers how many lines are forced (Lines),
	// for a device that offers "Forced only" only when it means something.
	LinesURL string `json:"linesUrl,omitempty"`
}
type FontFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type FontFiles struct {
	SkippedCount int        `json:"skippedCount,omitempty"`
	Fonts        []FontFile `json:"fonts"`
}

// MediaBaseURL keeps subtitle payloads in a namespace distinct from linear
// playback, regardless of the grant or file format.
func MediaBaseURL(grant, resource string, revision int64) string {
	return "/v1/media/subtitles/" + grant + "/" + resource + "/" + strconv.FormatInt(revision, 10)
}

func deliveryFormats(format string) []Delivery {
	switch format {
	case "ass", "ssa":
		return []Delivery{{Format: "text"}, {Format: "ass"}}
	case "pgs", "sup":
		return []Delivery{{Format: "pgs"}}
	case "vobsub", "idx":
		return []Delivery{{Format: "vobsub"}}
	case "dvb":
		return []Delivery{{Format: "dvb"}}
	default:
		return []Delivery{{Format: "text"}}
	}
}

func originalURL(base, format string) string {
	suffix := format
	switch format {
	case "pgs":
		suffix = "sup"
	case "vobsub":
		suffix = "idx"
	case "dvb":
		suffix = "mks"
	}
	return base + "/original." + suffix
}
func deliveries(base, format string) []Delivery {
	out := deliveryFormats(format)
	for i := range out {
		if out[i].Format == "text" {
			out[i].URL = base + ".vtt"
		} else {
			out[i].URL = originalURL(base, format)
		}
		if out[i].Format == "ass" {
			out[i].FontsURL = base + "/fonts"
		}
		if out[i].Format == "vobsub" {
			out[i].CompanionURL = base + "/original.sub"
		}
		if out[i].Format == "pgs" || out[i].Format == "vobsub" {
			out[i].LinesURL = base + "/lines"
		}
	}
	return out
}

func RenderingFormat(format string) string {
	for _, d := range deliveryFormats(format) {
		if d.Format != "text" {
			return d.Format
		}
	}
	return "text"
}

func originalArchive(raw []byte) bool {
	var a struct {
		Version int `json:"version"`
	}
	return json.Unmarshal(raw, &a) == nil && a.Version == 2
}
