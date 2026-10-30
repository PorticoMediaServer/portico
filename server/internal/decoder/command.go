// Package decoder constructs confined decoder commands. It does not run them;
// the caller must retain its input borrow until supervised physical retirement.
package decoder

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

// ErrConfinementUnavailable is a decoder sandbox that a caller asked for
// explicitly (the fd-bound prepared-media sandbox) and this host lacks. Ordinary
// decoders never fail with it: they run with the baseline (mediaexec).
var ErrConfinementUnavailable = errors.New("decoder confinement unavailable on this platform")
var ErrInvalidConfiguration = errors.New("invalid confined decoder configuration")

// Linux dependency resolution and its helper protocol already admit this many
// exact files. Probe admission must not reject a successfully resolved capture.
const maxLibraryFiles = 512

// tokenPath is a private gateway route: a raw stream, or an HLS playlist whose
// ".m3u8" lets FFmpeg's HLS demuxer (7.1+) accept it by extension.
var tokenPath = regexp.MustCompile(`^/input/[a-f0-9]{64}(\.m3u8)?$`)

const ordinaryProbeFormats = "mov,matroska,mp3,flac,ogg,wav,aac,mpegts,avi"

// ProbeCommand builds a bridge-fed ffprobe through the one media executor:
// sandboxed where the platform can, the baseline elsewhere (mediaexec). Input is
// always a private bridge.
func ProbeCommand(executable, input string, reservation *EndpointReservation, libraries ...string) (*exec.Cmd, error) {
	return probeFormatsCommand(executable, input, reservation, ordinaryProbeFormats, libraries...)
}

// ProbeQueryCommand builds a confined bridge probe with a closed timeline query.
// Query values cannot introduce options, another input, or an output file.
func ProbeQueryCommand(executable, input string, reservation *EndpointReservation, query []string, libraries ...string) (*exec.Cmd, error) {
	if len(query) == 0 || len(query)%2 != 0 {
		return nil, ErrInvalidConfiguration
	}
	seen := map[string]bool{}
	entries := ""
	for i := 0; i < len(query); i += 2 {
		option, value := query[i], query[i+1]
		if seen[option] {
			return nil, ErrInvalidConfiguration
		}
		seen[option] = true
		valid := false
		switch option {
		case "-select_streams":
			valid = value == "v:0"
		case "-show_entries":
			entries = value
			valid = value == "format=start_time" || value == "packet=pts_time,flags"
		case "-of":
			valid = value == "csv=p=0"
		case "-read_intervals":
			intervals := strings.Split(value, ",")
			valid = len(intervals) <= 1000
			for _, interval := range intervals {
				stamp, suffix, ok := strings.Cut(interval, "%")
				if !ok || suffix != "+#1" || !probeIntervalStamp.MatchString(stamp) {
					valid = false
					break
				}
			}
		}
		if !valid {
			return nil, ErrInvalidConfiguration
		}
	}
	if !seen["-show_entries"] || !seen["-of"] {
		return nil, ErrInvalidConfiguration
	}
	if entries == "format=start_time" && len(seen) != 2 || entries == "packet=pts_time,flags" && (len(seen) != 4 || !seen["-read_intervals"] || !seen["-select_streams"]) {
		return nil, ErrInvalidConfiguration
	}
	args := []string{"-v", "error", "-threads", "1", "-protocol_whitelist", "http,tcp", "-format_whitelist", ordinaryProbeFormats, "-probesize", "8388608", "-analyzeduration", "10000000"}
	args = append(args, query...)
	args = append(args, input)
	return confinedProbeCommand(executable, input, reservation, args, libraries...)
}

var probeIntervalStamp = regexp.MustCompile(`^-?[0-9]{1,12}(\.[0-9]{1,6})?$`)

// probeFormatsCommand accepts only package-owned format lists; public callers retain
// the ordinary probe list. Prepared music uses its separately closed suite.
func probeFormatsCommand(executable, input string, reservation *EndpointReservation, formats string, libraries ...string) (*exec.Cmd, error) {
	args := []string{"-v", "error", "-threads", "1", "-protocol_whitelist", "http,tcp", "-format_whitelist", formats, "-probesize", "8388608", "-analyzeduration", "10000000", "-show_format", "-show_streams", "-of", "json", input}
	return confinedProbeCommand(executable, input, reservation, args, libraries...)
}

func confinedProbeCommand(executable, input string, reservation *EndpointReservation, args []string, libraries ...string) (*exec.Cmd, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return nil, ErrInvalidConfiguration
	}
	if len(libraries) > maxLibraryFiles {
		return nil, ErrInvalidConfiguration
	}
	for _, file := range libraries {
		if !filepath.IsAbs(file) || filepath.Clean(file) != file {
			return nil, ErrInvalidConfiguration
		}
	}
	endpoint, err := bridgeEndpoint(input)
	if err != nil {
		return nil, err
	}
	if !reservation.matches(endpoint) {
		return nil, ErrInvalidConfiguration
	}
	return confinedCommand(executable, args, endpoint, libraries...)
}

func bridgeEndpoint(input string) (string, error) {
	u, err := url.Parse(input)
	if err != nil || u.Scheme != "http" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !tokenPath.MatchString(u.Path) {
		return "", ErrInvalidConfiguration
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host != "127.0.0.1" {
		return "", ErrInvalidConfiguration
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return "", ErrInvalidConfiguration
	}
	return net.JoinHostPort(host, port), nil
}

// ResolveLibraries lists an executable's exact dependency files for the
// sandbox (mediaexec owns the resolution).
func ResolveLibraries(ffmpeg, ffprobe string, extra []string) ([]string, error) {
	return mediaexec.ResolveLibraries(ffmpeg, ffprobe, extra)
}

// decoderJob is one decoder fed through a private loopback bridge. command()
// builds it per platform (confinement_*.go), always through mediaexec.
type decoderJob struct {
	executable string
	args       []string
	endpoint   string
	libraries  []string
	// outputPath is the one gateway path a live HLS job may PUT to (the Linux
	// sandbox proxy forwards PUT for it only).
	outputPath string
	// outputDir is a private output folder (an HLS window).
	outputDir  string
	background bool
	hardware   bool
}

func (d decoderJob) mediaJob() mediaexec.Job {
	job := mediaexec.Job{Executable: d.executable, Args: d.args, Loopback: d.endpoint, Libraries: d.libraries, Background: d.background, Hardware: d.hardware}
	if d.outputDir != "" {
		job.WriteDirs = []string{d.outputDir}
	}
	// Jobs that produce pictures may burn in text subtitles.
	job.Fonts = d.outputDir != "" || d.outputPath != ""
	return job
}

func confinedCommand(executable string, args []string, endpoint string, libraries ...string) (*exec.Cmd, error) {
	return decoderJob{executable: executable, args: args, endpoint: endpoint, libraries: libraries}.command()
}

// confinedOutputCommand is confinedCommand for a live HLS job that PUTs its
// output to the gateway.
func confinedOutputCommand(executable string, args []string, endpoint, output string, hardware bool, libraries ...string) (*exec.Cmd, error) {
	return decoderJob{executable: executable, args: args, endpoint: endpoint, libraries: libraries, outputPath: output, hardware: hardware}.command()
}

// confinedHLSCommand is confinedCommand for a job writing into its private
// output folder.
func confinedHLSCommand(executable string, args []string, endpoint, output string, hardware bool, libraries ...string) (*exec.Cmd, error) {
	return decoderJob{executable: executable, args: args, endpoint: endpoint, libraries: libraries, outputDir: output, hardware: hardware}.command()
}

// RequiredConfinement is why decoders may not run on this host at all: only
// when the owner requires the sandbox (PORTICO_DECODER_SANDBOX=required) and it
// isn't available for this tool. Otherwise Live TV, recording, prepared
// versions and playback run, sandboxed or with the baseline, and the owner's
// diagnostics say which (D-MEDIA-6, mediaexec).
func RequiredConfinement(executable string) error {
	if mediaexec.SandboxSetting() != mediaexec.SettingRequired {
		return nil
	}
	posture := mediaexec.Decide(executable)
	if posture.Sandboxed {
		return nil
	}
	return fmt.Errorf("%w: the owner requires the decoder sandbox and %s", ErrConfinementUnavailable, posture.Reason)
}

// mediaCommand builds a decoder through mediaexec. The owner's sandbox
// requirement surfaces as ErrConfinementUnavailable, the code callers already
// map to decoder_confinement_unavailable.
func mediaCommand(job mediaexec.Job) (*exec.Cmd, error) {
	cmd, err := mediaexec.Command(job)
	if errors.Is(err, mediaexec.ErrSandboxRequired) {
		return nil, fmt.Errorf("%w: %v", ErrConfinementUnavailable, err)
	}
	return cmd, err
}

// Fonts is the font pack every picture-producing decoder gets (mediaexec).
func Fonts() (mediaexec.FontPack, error) { return mediaexec.Fonts() }
