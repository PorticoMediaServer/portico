package decoder

import (
	"fmt"
	"strings"
)

type BurnIn struct {
	File, Format         string
	FontsDirectory       string
	DynamicRange         string
	PositionUS, OffsetUS int64
	InputStartUS         int64
	Width, Height        int
}

// burnFilter runs on system frames after HDR-to-SDR and before scaling/upload.
// Both subtitle formats retain the title clock across restarted HLS windows.
func burnFilter(b BurnIn, facts *ToolchainFacts) (string, []string, error) {
	if err := validateBurnIn(b); err != nil {
		return "", nil, err
	}
	switch b.Format {
	case "ass", "ssa":
		if facts != nil && !facts.Filters["ass"] {
			return "", nil, fmt.Errorf("subtitle_burn_filter_unavailable")
		}
		file := escapeFilterPath(b.File)
		ass := "ass=filename=" + file + ":alpha=1"
		if b.FontsDirectory != "" {
			ass += ":fontsdir=" + escapeFilterPath(b.FontsDirectory)
		}
		if b.Width > 0 && b.Height > 0 {
			ass += fmt.Sprintf(":original_size=%dx%d", b.Width, b.Height)
		}
		shift := seconds(b.PositionUS - b.OffsetUS)
		draw := "setpts=PTS+(" + shift + ")/TB," + ass + ",setpts=PTS-(" + shift + ")/TB"
		if b.DynamicRange == "hdr10" || b.DynamicRange == "hlg" {
			return "split[burn_picture][burn_text];[burn_text]format=rgba,colorchannelmixer=aa=0," + draw + "," + hdrSubtitleFilter(b.DynamicRange) + "[burn_cues];[burn_picture][burn_cues]overlay=format=yuv420p10:eof_action=pass:shortest=0", []string{}, nil
		}
		return draw, nil, nil
	case "pgs", "vobsub", "mks", "dvb":
		if facts != nil && !facts.Filters["overlay"] {
			return "", nil, fmt.Errorf("subtitle_burn_filter_unavailable")
		}
		inputs, err := bitmapSubtitleInput(b)
		if err != nil {
			return "", nil, err
		}
		fit := subtitleFit(b)
		cue := fmt.Sprintf("[1:s:0]settb=AVTB,setpts=PTS+(%s)/TB,format=rgba,%s", seconds(b.OffsetUS-b.PositionUS), fit)
		overlay := "overlay=eof_action=pass:shortest=0"
		if b.DynamicRange == "hdr10" || b.DynamicRange == "hlg" {
			cue += "," + hdrSubtitleFilter(b.DynamicRange)
			overlay = "overlay=format=yuv420p10:eof_action=pass:shortest=0"
		}
		filter := "setpts=PTS-STARTPTS[burn_picture];" + cue + "[burn_cues];[burn_picture][burn_cues]" + overlay
		return filter, inputs, nil
	}
	return "", nil, ErrInvalidConfiguration
}

// bitmapSubtitleInput opens one sealed bitmap subtitle file with only the
// demuxer its format needs. Undo only this input's demuxer-origin subtraction:
// global copyts would also retain the sought movie's audio clock and offset it
// twice. The processor burn and the device composite share it, so both read one
// file the same way.
func bitmapSubtitleInput(b BurnIn) ([]string, error) {
	if b.Width < 1 || b.Height < 1 || b.Width > 8192 || b.Height > 4320 {
		return nil, ErrInvalidConfiguration
	}
	mux := map[string]string{"pgs": "sup", "vobsub": "vobsub", "mks": "matroska", "dvb": "matroska"}[b.Format]
	if mux == "" {
		return nil, ErrInvalidConfiguration
	}
	formats := mux
	if b.Format == "vobsub" {
		formats += " ,mpeg"
		formats = strings.ReplaceAll(formats, " ", "")
	}
	return []string{"-itsoffset", seconds(b.InputStartUS), "-protocol_whitelist", "file", "-format_whitelist", formats, "-f", mux, "-i", b.File}, nil
}

// subtitleFit fits a bitmap's own canvas without distorting its glyphs, centering
// transparent padding when a cropped video has a different shape.
func subtitleFit(b BurnIn) string {
	return fmt.Sprintf("scale=w='max(2,trunc(iw*min(%d/iw,%d/ih)/2)*2)':h='max(2,trunc(ih*min(%d/iw,%d/ih)/2)*2)',pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black@0", b.Width, b.Height, b.Width, b.Height, b.Width, b.Height)
}

// FFmpeg parses AVOptions and then the filter graph, so paths need both
// escaping layers, including the Windows drive colon and backslashes.
func escapeFilterPath(path string) string {
	path = strings.NewReplacer("\\", "\\\\", "'", "\\'", ":", "\\:").Replace(path)
	return strings.NewReplacer("\\", "\\\\", "'", "\\'", ",", "\\,", "[", "\\[", "]", "\\]", ";", "\\;").Replace(path)
}

// Subtitle white is 100-nit BT.709, independently converted to the HDR output
// transfer. The source picture never passes through an SDR working surface.
func hdrSubtitleFilter(dynamicRange string) string {
	transfer := "smpte2084"
	if dynamicRange == "hlg" {
		transfer = "arib-std-b67"
	}
	return "format=gbrap16le,zscale=pin=bt709:tin=bt709:min=gbr:p=bt2020:t=" + transfer + ":m=bt2020nc:r=tv:npl=100,format=yuva420p10le"
}
