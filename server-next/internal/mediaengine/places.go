package mediaengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// The owner can change the folder conversions are written to, and the ffmpeg that makes
// them, while the server runs. A stream keeps the folder and the program it started with
// until it ends, so nothing playing is interrupted; every stream started after the change
// uses the new ones. Each pair of folder and program therefore has its own set of streams
// (a place). There is one current place; an older one stays only while a stream in it is
// still playing, and is then stopped and its folder cleared.

// place is one conversion folder and one ffmpeg, with the streams started under them.
type place struct {
	streams *streaming.Streams
	// folder is the conversion folder; number names this place's own folder inside it.
	folder string
	ffmpeg string
	number int
	// ctx ends this place's housekeeping; stop ends ctx.
	ctx  context.Context
	stop context.CancelFunc
}

// ownedFolder is the folder the server makes inside a folder the owner chose. The streams
// empty their folder when they start and when they stop, so it is never the owner's folder
// itself: pointing the setting at a folder with other things in it loses nothing.
const ownedFolder = "portico-conversions"

// open makes a place in a conversion folder. Each place writes to a numbered folder of its
// own inside it, because a set of streams empties its folder when it starts and when it
// stops, and two places can share one conversion folder (when only the program changed).
// A folder that cannot be made or written to is refused. Caller holds mu, or is New.
func (e *Engine) open(base, ffmpeg string) (*place, error) {
	e.opened++
	ctx, stop := context.WithCancel(e.ctx)
	streams, err := streaming.New(ctx, streaming.Options{Folder: filepath.Join(base, strconv.Itoa(e.opened)), FFmpeg: ffmpeg,
		Libraries: e.folders, Processes: e.processes, Settings: e})
	if err != nil {
		stop()
		return nil, err
	}
	return &place{streams: streams, folder: base, ffmpeg: ffmpeg, number: e.opened, ctx: ctx, stop: stop}, nil
}

// empty removes what an earlier run of the server left in a conversion folder. It is only
// ever called on a folder the server made for itself.
func empty(base string) error {
	// A link is never followed: emptying through one would empty wherever it points.
	if info, err := os.Lstat(base); err == nil && !info.IsDir() {
		return fmt.Errorf("%s is a link or a file; the server only uses a real folder there, so that it never empties anything else", base)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(base)
	for _, entry := range entries {
		if err == nil {
			err = os.RemoveAll(filepath.Join(base, entry.Name()))
		}
	}
	return err
}

// SetConversion changes where conversions are written and which ffmpeg makes them, with no
// restart. folder is a folder of the owner's choosing, in which the server keeps a folder
// of its own; ffmpeg is a path to the program. Empty means the one the server started with.
// Streams that are playing are not touched.
//
// Nothing changes, and the reason is returned, when the folder cannot be used or the
// program is not a working ffmpeg. A new program is asked what it can do before the
// switch, so the call takes a few seconds for a program the server has not seen (and the
// owner's save shows that it is being checked); once it returns, no play waits. Run it
// beside a request, not in one that must be quick.
func (e *Engine) SetConversion(folder, ffmpeg string) error {
	// One switch at a time, start to finish: two close together cannot cross over which
	// program's abilities are in force. mu is held only for the swap itself, so emptying a
	// big folder or asking a program never holds up a play or a media request.
	e.switching.Lock()
	defer e.switching.Unlock()
	e.mu.Lock()
	current, target, program := e.current, e.folder, e.program
	inUse := map[string]bool{current.folder: true}
	for _, p := range e.retired {
		inUse[p.folder] = true
	}
	e.mu.Unlock()
	if folder != "" {
		if !filepath.IsAbs(folder) {
			return errors.New("the conversion folder must be a full path")
		}
		target = filepath.Join(folder, ownedFolder)
	}
	if ffmpeg != "" {
		path, err := exec.LookPath(ffmpeg)
		if err != nil || !filepath.IsAbs(path) {
			return fmt.Errorf("FFmpeg was not found at %s", ffmpeg)
		}
		program = path
	}
	if target == current.folder && program == current.ffmpeg {
		return nil
	}
	var tools *toolset
	var facts decoder.ToolchainFacts
	pair, probe := e.startFFmpeg, e.startFFprobe
	if program != current.ffmpeg {
		// The program is tried before anything is switched: it must say it is ffmpeg, and
		// then answer what it can do, in its sandbox, as at start.
		var err error
		if ffmpeg != "" {
			if err = isFFmpeg(e.ctx, program); err != nil {
				return fmt.Errorf("%s is not a working FFmpeg, so the one in use stays: %w", ffmpeg, err)
			}
			pair = program
			if sibling, err := exec.LookPath(filepath.Join(filepath.Dir(program), mediatools.Name("ffprobe"))); err == nil {
				probe = sibling
			}
		}
		if tools, facts, err = e.answer(e.ctx, program, probe); err != nil && ffmpeg != "" {
			return fmt.Errorf("%s is not a working FFmpeg, so the one in use stays: %w", ffmpeg, err)
		}
	}
	// A folder that streams still play from (the owner switched away and back) is used as it
	// is. Any other has what an earlier run left in it removed.
	if !inUse[target] {
		if err := empty(target); err != nil {
			return fmt.Errorf("the conversion folder cannot be used: %w", err)
		}
	}
	if warning := e.ConversionFolderWarning(folder); warning != "" {
		e.log.Warn(warning)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	next, err := e.open(target, program)
	if err != nil {
		return fmt.Errorf("the conversion folder cannot be used: %w", err)
	}
	if tools != nil {
		// Everything that finds the tools by itself (the scanner's reader, previews) follows.
		if err := mediatools.ConfigurePair(pair, probe); err != nil && ffmpeg != "" {
			next.stop()
			return fmt.Errorf("%s cannot be used, so the one in use stays: %w", ffmpeg, err)
		}
		if tools.converter {
			decoder.ConfigureToolchain(facts)
		}
		e.asking = tools
	}
	e.current = next
	if e.running {
		go next.streams.Run(next.ctx)
	}
	e.retired = append(e.retired, current)
	e.closeIdleLocked()
	return nil
}

// ConversionFolderWarning is what the owner should be told about a folder they chose for
// conversions, or "": inside a library, scans see the files being converted come and go.
// It is a warning. The choice is theirs.
func (e *Engine) ConversionFolderWarning(folder string) string {
	if folder == "" {
		return ""
	}
	for _, library := range e.folders() {
		if relative, err := filepath.Rel(library, folder); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return "The conversion folder " + folder + " is inside the library folder " + library +
				". Scans will see files being converted there. A folder outside your libraries is better."
		}
	}
	return ""
}

// isFFmpeg asks a program its version, which takes a moment, and refuses one that does
// not answer as ffmpeg (ffprobe picked by mistake, some other program).
func isFFmpeg(ctx context.Context, program string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// In the same sandbox as every other run of the program: it is not trusted yet.
	ask, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: program, Args: []string{"-version"}})
	if err != nil {
		return fmt.Errorf("it cannot be run: %w", err)
	}
	out, err := ask.Output()
	if err != nil {
		return fmt.Errorf("it did not answer -version: %w", err)
	}
	if !strings.HasPrefix(string(out), "ffmpeg version ") {
		return errors.New("it does not say it is ffmpeg")
	}
	return nil
}

// place is where a session's stream is, or the current place for a session with none.
func (e *Engine) place(sessionID string) *place {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p := e.by[sessionID]; p != nil {
		return p
	}
	return e.current
}

// enter puts a session's next stream in the current place. A stream it had in an older
// place is stopped first: a session has one stream. had says the session already had a
// stream in the current place.
func (e *Engine) enter(sessionID string) (in *place, had bool) {
	e.mu.Lock()
	old, current := e.by[sessionID], e.current
	e.by[sessionID] = current
	e.mu.Unlock()
	if old != nil && old != current {
		old.streams.Stop(sessionID)
		e.mu.Lock()
		e.closeIdleLocked()
		e.mu.Unlock()
	}
	return current, old == current
}

// leave records that a session has no stream in a place any more.
func (e *Engine) leave(sessionID string, in *place) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.by[sessionID] == in {
		delete(e.by, sessionID)
	}
	e.closeIdleLocked()
}

// closeIdleLocked stops every older place no session plays from any more, and clears its
// folder. Caller holds mu.
func (e *Engine) closeIdleLocked() {
	kept := e.retired[:0]
	for _, p := range e.retired {
		busy := false
		for _, in := range e.by {
			busy = busy || in == p
		}
		if busy {
			kept = append(kept, p)
			continue
		}
		go func() {
			wait, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := p.streams.Shutdown(wait); err != nil {
				e.log.Error("clearing the conversion folder that is no longer used failed", "folder", p.folder, "error", err)
			}
			p.stop()
			_ = os.Remove(filepath.Join(p.folder, strconv.Itoa(p.number)))
		}()
	}
	e.retired = kept
}

// serving is the place whose streams answer an address: the one the grant belongs to.
func (e *Engine) serving(grant, file string) *streaming.Streams {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.retired {
		if p.streams.Serves(grant, file) {
			return p.streams
		}
	}
	return e.current.streams
}

// toolset is what one ffmpeg can do.
type toolset struct {
	// asked is closed once ffmpeg has said what it can do. Until then the rest is not to be
	// read: wait waits for it.
	asked chan struct{}
	// converter is false on a server without a working ffmpeg: only original files play.
	// fragmented says its ffmpeg can write fragmented MP4 streams.
	converter, fragmented bool
	// ffmpeg is the converter's path, and hardware what has been found out about this
	// machine's hardware encoders.
	ffmpeg   string
	hardware *decoder.HardwareDetector
}

// tools is the toolset in force.
func (e *Engine) tools() *toolset {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.asking
}

// wait waits until ffmpeg has said what it can do, and reports whether this server can
// convert and whether it can write fragmented MP4 streams.
func (t *toolset) wait(ctx context.Context) (converter, fragmented bool, err error) {
	select {
	case <-t.asked:
		return t.converter, t.fragmented, nil
	case <-ctx.Done():
		return false, false, ctx.Err()
	}
}

// answer asks an ffmpeg what it can do and waits for the answer. Nothing is put in force.
// A server with no ffmpeg at all gets a toolset that converts nothing, and no error.
func (e *Engine) answer(ctx context.Context, ffmpeg, ffprobe string) (*toolset, decoder.ToolchainFacts, error) {
	t := &toolset{asked: make(chan struct{})}
	close(t.asked)
	if ffmpeg == "" {
		ffmpeg = mediatools.Resolve("ffmpeg")
	}
	path, err := exec.LookPath(ffmpeg)
	if err != nil {
		return t, decoder.ToolchainFacts{}, nil
	}
	facts, err := decoder.ProbeToolchainKept(ctx, ffmpeg, e.kept)
	if err != nil {
		return t, facts, err
	}
	t.converter, t.fragmented = true, facts.Muxers["mp4"] && facts.Muxers["hls"]
	t.ffmpeg, t.hardware = path, &decoder.HardwareDetector{}
	if libraries, err := decoder.ResolveLibraries(path, ffprobe, nil); err == nil {
		t.hardware.Libraries = libraries
	}
	return t, facts, nil
}

// ask asks the ffmpeg the server starts with what it can do. That takes several seconds,
// so it happens beside the start: the server serves at once, and only a play that arrives
// in those seconds waits.
func (e *Engine) ask(ffmpeg string) *toolset {
	t := &toolset{asked: make(chan struct{})}
	go func() {
		defer close(t.asked)
		answered, facts, err := e.answer(e.ctx, ffmpeg, mediatools.Resolve("ffprobe"))
		if err != nil {
			if e.ctx.Err() == nil {
				e.log.Error("ffmpeg could not be asked what it can do, so only original files will play", "error", err)
			}
			return
		}
		// A switch made meanwhile has put another program in force: this answer is not
		// made the server's.
		e.switching.Lock()
		defer e.switching.Unlock()
		t.converter, t.fragmented, t.ffmpeg, t.hardware = answered.converter, answered.fragmented, answered.ffmpeg, answered.hardware
		if e.tools() != t || !answered.converter {
			return
		}
		decoder.ConfigureToolchain(facts)
		// The hardware encoders are tried out now, beside the start, so no play waits for
		// that: until it is done a conversion is planned in software.
		t.hardware.SelectCached(t.ffmpeg, "auto", "")
	}()
	return t
}
