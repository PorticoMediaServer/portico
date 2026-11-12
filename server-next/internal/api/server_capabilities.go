package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/backup"
	"github.com/PorticoMediaServer/portico/server-next/internal/capabilityreport"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/hardwarefacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaengine"
)

// "What this server can do" (12-console rule 74), in Settings › Troubleshooting: what works
// and, for anything that does not, why: FFmpeg, the graphics devices, the folders the server
// must write to, and the protection around reading untrusted files. It is answered from what
// the server found out beside its start (and beside the owner's last change of program or
// folder); nothing is run to answer it. The folders alone are looked at, at most once a
// minute: one small file made and removed in each.

// ServerCapabilities is everything "What this server can do" lists, area by area.
type ServerCapabilities struct {
	Items []Capability `json:"items"`
}

// Capability is one thing the server can or cannot do.
type Capability struct {
	// Area is ffmpeg, graphics, folders or protection.
	Area string `json:"area"`
	// Name is what it is: ffmpeg; a graphics backend (vaapi, qsv, nvenc, amf, videotoolbox),
	// vulkan, or graphics for the whole; a folder (data, conversions, backups, recordings);
	// decoder_sandbox or decoder_network_isolation.
	Name string `json:"name"`
	// State is works, does_not_work, checking (not found out yet) or not_used (the owner
	// chose software encoding; the recordings folder is made with the first recording).
	State string `json:"state"`
	// Reason is why not, as a stable code; Detail says it in English, or for something that
	// works, how. Path is a folder's or a program's.
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
	Path   string `json:"path,omitempty"`
}

const (
	capabilityWorks    = "works"
	capabilityBroken   = "does_not_work"
	capabilityChecking = "checking"
	capabilityNotUsed  = "not_used"
)

// capabilityEngine is what the media engine answers of its tools (mediaengine.Engine).
type capabilityEngine interface {
	Capabilities(context.Context) mediaengine.Capabilities
}

func (s *Server) capabilityRoutes() {
	Handle(s, "GET /v1/admin/capabilities", Owner, func(r *http.Request, _ None) (ServerCapabilities, error) {
		return s.serverCapabilities(r.Context()), nil
	})
}

func (s *Server) serverCapabilities(ctx context.Context) ServerCapabilities {
	var tools mediaengine.Capabilities
	engine, ok := s.playback.Engine().(capabilityEngine)
	if ok {
		tools = engine.Capabilities(ctx)
	} else {
		tools.Asking = true
	}
	out := ServerCapabilities{}
	out.Items = append(out.Items, ffmpegCapability(tools))
	out.Items = append(out.Items, graphicsCapabilities(tools)...)
	out.Items = append(out.Items, s.folderCapabilities(tools.ConversionFolder)...)
	out.Items = append(out.Items, protectionCapabilities()...)
	return out
}

func ffmpegCapability(t mediaengine.Capabilities) Capability {
	c := Capability{Area: "ffmpeg", Name: "ffmpeg", Path: t.FFmpeg}
	switch {
	case t.Asking:
		c.State, c.Detail = capabilityChecking, "FFmpeg is being asked what it can do."
	case t.Converter:
		c.State, c.Detail = capabilityWorks, "FFmpeg "+t.Version
	case t.Problem == "":
		c.State, c.Reason, c.Detail = capabilityBroken, "ffmpeg_not_found", "No FFmpeg was found, so only original files play: nothing can be converted."
	default:
		c.State, c.Reason, c.Detail = capabilityBroken, "ffmpeg_not_working", "FFmpeg could not be used, so only original files play: "+t.Problem
	}
	return c
}

func graphicsCapabilities(t mediaengine.Capabilities) []Capability {
	whole := Capability{Area: "graphics", Name: "graphics"}
	switch {
	case t.Asking:
		whole.State = capabilityChecking
		return []Capability{whole}
	case !t.Converter:
		whole.State, whole.Reason, whole.Detail = capabilityBroken, "no_ffmpeg", "Graphics devices are used through FFmpeg, and there is none."
		return []Capability{whole}
	}
	var out []Capability
	working := false
	for _, p := range t.Graphics {
		c := Capability{Area: "graphics", Name: string(p.Backend), Path: p.Device}
		if p.Available {
			working = true
			c.State, c.Detail = capabilityWorks, "Converts with "+p.Encoder
		} else {
			c.State, c.Reason, c.Detail = capabilityBroken, p.Reason, graphicsReasons[p.Reason]
		}
		out = append(out, c)
	}
	switch {
	case t.SoftwareEncoding:
		whole.State, whole.Reason, whole.Detail = capabilityNotUsed, "software_encoding_chosen",
			"Software encoding is chosen in Settings › Streaming: conversions run on the processor and no graphics device is used."
	case working:
		whole.State = capabilityWorks
	case len(t.Graphics) == 0:
		whole.State, whole.Detail = capabilityChecking, "The graphics devices have not been tried yet."
	default:
		whole.State, whole.Reason = capabilityBroken, decoder.HardwareFactReasonNoDevice
		whole.Detail = "No graphics device works for conversions, so they run on the processor."
		if remedy := hardwarefacts.Detect().Remedy; remedy != "" {
			whole.Detail += " " + remedy
		}
	}
	vulkan := Capability{Area: "graphics", Name: "vulkan", State: capabilityWorks,
		Detail: "Dolby Vision films without a compatible base layer, and HDR tone mapping, are converted on the graphics device."}
	if !t.Vulkan {
		vulkan.State, vulkan.Reason = capabilityBroken, "vulkan_unavailable"
		vulkan.Detail = "FFmpeg found no working Vulkan graphics device"
		if t.VulkanProblem != "" {
			vulkan.Detail += " (" + t.VulkanProblem + ")"
		}
		vulkan.Detail += ". Dolby Vision films without a compatible base layer play only on a device that plays Dolby Vision itself."
	}
	return append(append([]Capability{whole}, out...), vulkan)
}

// graphicsReasons say each probe outcome in English.
var graphicsReasons = map[string]string{
	decoder.ProbeUnsupportedPlatform: "Not available on this system.",
	decoder.ProbeConfinementMissing:  "FFmpeg cannot reach the graphics device from inside its protection.",
	decoder.ProbeEncodeFailed:        "A test conversion on it failed: its driver, or the device being passed to the server, is missing.",
	decoder.ProbeTimedOut:            "A test conversion on it did not finish in time.",
	decoder.ProbeCircuitOpen:         "It was working and stopped: conversions run on the processor until it works again.",
}

func protectionCapabilities() []Capability {
	reported := map[string]capabilityreport.Status{}
	for _, st := range capabilityreport.All() {
		reported[st.Capability] = st
	}
	var out []Capability
	for _, name := range []string{capabilityreport.DecoderSandbox, capabilityreport.DecoderNetworkIsolation} {
		c := Capability{Area: "protection", Name: name, State: capabilityChecking}
		if st, ok := reported[name]; ok {
			c.State, c.Detail = capabilityWorks, st.Detail
			if !st.Available {
				c.State, c.Reason = capabilityBroken, st.Code
			}
		}
		out = append(out, c)
	}
	return out
}

// folderCheckEvery is how long one look at the folders is kept.
const folderCheckEvery = time.Minute

// folderCheckLimit bounds one folder's look: a drive that does not answer is a folder that
// does not work, never a request that hangs.
const folderCheckLimit = 5 * time.Second

type folderLook struct {
	mu    sync.Mutex
	at    time.Time
	key   string
	items []Capability
	// A timed-out drive still has one check until that check answers.
	pending map[string]chan error
}

func (s *Server) folderCapabilities(conversions string) []Capability {
	recordings := ""
	if s.dvrNow.Load() != nil {
		recordings = s.dvrOptions().Folder
	}
	backups := s.backups.Folder()
	defaultBackups := backup.Document.Get(s.settings).Folder == ""
	key := conversions + "\x00" + backups + "\x00" + recordings
	look := &s.folderLook
	look.mu.Lock()
	defer look.mu.Unlock()
	now := s.now()
	if look.key == key && now.Sub(look.at) < folderCheckEvery {
		return look.items
	}
	look.at, look.key = now, key
	items := []Capability{folderCapability("data", s.dataDir, look.check(s.dataDir, func() error { return writable(s.dataDir) }))}
	if conversions != "" {
		items = append(items, folderCapability("conversions", conversions, look.check(conversions, func() error { return writable(conversions) })))
	}
	items = append(items, folderCapability("backups", backups, look.check(backups, func() error {
		// Only the default folder inside Portico's data folder may be made here.
		// FolderProblem has its own timeout, which would lose track of a hung probe.
		if defaultBackups {
			if err := os.MkdirAll(backups, 0o700); err != nil {
				return err
			}
		}
		return writable(backups)
	})))
	if recordings != "" {
		problem := look.check(recordings, func() error {
			if _, err := os.Stat(recordings); errors.Is(err, os.ErrNotExist) {
				// The parent may be a media folder. Only inspect it; never write there.
				info, err := os.Stat(filepath.Dir(recordings))
				if err != nil {
					return err
				}
				if !info.IsDir() {
					return errors.New("the parent is not a folder")
				}
				return errRecordingFolderNotMade
			}
			return writable(recordings)
		})
		items = append(items, folderCapability("recordings", recordings, problem))
	}
	look.items = items
	return items
}

var errRecordingFolderNotMade = errors.New("made with the first recording")

func folderCapability(name, path string, problem error) Capability {
	c := Capability{Area: "folders", Name: name, Path: path, State: capabilityWorks}
	if errors.Is(problem, errRecordingFolderNotMade) {
		c.State, c.Detail = capabilityNotUsed, "Made with the first recording. Write access is checked once it exists."
	} else if problem != nil {
		c.State, c.Reason, c.Detail = capabilityBroken, "not_writable", "Portico cannot write to this folder: "+problem.Error()
	}
	return c
}

// check is called with l.mu held. A timeout leaves the same buffered result in
// pending, so later requests wait for it instead of starting another check.
func (l *folderLook) check(folder string, check func() error) error {
	if l.pending == nil {
		l.pending = make(map[string]chan error)
	}
	folder = filepath.Clean(folder)
	done := l.pending[folder]
	if done == nil {
		done = make(chan error, 1)
		l.pending[folder] = done
		go func() { done <- check() }()
	}
	select {
	case err := <-done:
		delete(l.pending, folder)
		return err
	case <-time.After(folderCheckLimit):
		return errors.New("it did not answer in time")
	}
}

// writable makes and removes one small file in Portico's configured folder.
func writable(folder string) error {
	f, err := os.CreateTemp(folder, ".portico-check-*")
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	return os.Remove(f.Name())
}
