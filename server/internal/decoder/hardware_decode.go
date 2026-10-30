package decoder

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func hardwareDecodeKey(codec string, depth int) string {
	switch codec {
	case "avc":
		codec = "h264"
	case "h265":
		codec = "hevc"
	case "mpeg2video":
		codec = "mpeg2"
	}
	if codec == "" || depth != 8 && depth != 10 && depth != 12 {
		return ""
	}
	return codec + ":" + strconv.Itoa(depth)
}

func hardwareDecodeArgs(backend HardwareBackend) []string {
	name := map[HardwareBackend]string{BackendNVENC: "cuda", BackendVAAPI: "vaapi", BackendQSV: "qsv", BackendVideoToolbox: "videotoolbox"}[backend]
	if name == "" {
		return nil
	}
	surface := name
	if backend == BackendVideoToolbox {
		surface = "videotoolbox_vld"
	}
	args := []string{"-hwaccel", name, "-hwaccel_output_format", surface}
	if backend != BackendVideoToolbox {
		args = append(args, "-hwaccel_device", "hw")
	}
	return args
}

func hardwareDownloadFormat(depth int) string {
	switch depth {
	case 10:
		return "p010le"
	case 12:
		return "yuv420p12le"
	default:
		return "nv12"
	}
}

// probeDecodeCodecs proves decode independently of encode: software creates a
// tiny owned fixture, then the selected device must deliver hardware frames.
// hwdownload fails if FFmpeg silently falls back to software decoding. Absent
// cells are unmeasured (including VC-1 until a licensed fixture is supplied).
func (d *HardwareDetector) probeDecodeCodecs(ctx context.Context, executable string, backend HardwareBackend, device string, timeout time.Duration) map[string]bool {
	out := map[string]bool{}
	if len(hardwareDecodeArgs(backend)) == 0 {
		return out
	}
	dir, err := os.MkdirTemp("", "portico-hardware-decode-")
	if err != nil {
		return out
	}
	defer os.RemoveAll(dir)
	for _, cell := range []struct {
		codec, encoder, mux string
		depth               int
	}{{"h264", "libx264", "h264", 8}, {"h264", "libx264", "h264", 10}, {"hevc", "libx265", "hevc", 8}, {"hevc", "libx265", "hevc", 10}, {"hevc", "libx265", "hevc", 12}, {"av1", "libsvtav1", "ivf", 8}, {"av1", "libsvtav1", "ivf", 10}, {"vp9", "libvpx-vp9", "ivf", 8}, {"vp9", "libvpx-vp9", "ivf", 10}, {"mpeg2", "mpeg2video", "mpeg2video", 8}} {
		if ctx.Err() != nil {
			break
		}
		key := hardwareDecodeKey(cell.codec, cell.depth)
		file := filepath.Join(dir, strings.ReplaceAll(key, ":", "-")+".bin")
		pixel := "yuv420p"
		if cell.depth > 8 {
			pixel = "yuv420p" + strconv.Itoa(cell.depth) + "le"
		}
		args := []string{"-v", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "testsrc2=size=160x128:rate=25", "-frames:v", "2", "-threads", "1", "-c:v", cell.encoder, "-pix_fmt", pixel}
		switch cell.encoder {
		case "libx264":
			args = append(args, "-preset", "ultrafast")
		case "libx265":
			args = append(args, "-preset", "ultrafast", "-x265-params", "pools=none:frame-threads=1:log-level=error")
		case "libsvtav1":
			args = append(args, "-svtav1-params", "lp=1", "-preset", "12")
		case "libvpx-vp9":
			args = append(args, "-deadline", "realtime", "-cpu-used", "8")
		}
		args = append(args, "-f", cell.mux, file)
		bounded, cancel := context.WithTimeout(ctx, timeout)
		cmd, err := mediaexec.CommandContext(bounded, mediaexec.Job{Executable: executable, Args: args, WriteDirs: []string{dir}, MaxFileBytes: 1 << 20, Background: true})
		if err == nil {
			cmd.WaitDelay = time.Second
			if d.Run != nil {
				err = d.Run(bounded, cmd)
			} else {
				cmd.Stderr = &toolOutput{}
				err = cmd.Run()
			}
		}
		cancel()
		info, statErr := os.Stat(file)
		if err != nil || statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 1<<20 {
			continue
		}
		decode := []string{"-v", "error", "-nostdin"}
		if spec := backendSpecs[backend]; spec.device != nil {
			decode = append(decode, spec.device(device)...)
		}
		decode = append(decode, hardwareDecodeArgs(backend)...)
		demux := cell.mux
		if cell.codec == "mpeg2" {
			demux = "mpegvideo" // Raw MPEG-2 has a different demuxer/muxer name.
		}
		decode = append(decode, "-f", demux, "-i", file, "-frames:v", "2", "-threads", "1", "-vf", "hwdownload,format="+hardwareDownloadFormat(cell.depth), "-an", "-sn", "-f", "null", "-")
		bounded, cancel = context.WithTimeout(ctx, timeout)
		out[key] = d.runInput(bounded, executable, decode, []string{file}) == nil
		cancel()
	}
	return out
}
