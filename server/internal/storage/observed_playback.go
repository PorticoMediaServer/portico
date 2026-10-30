package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sync"
	"time"
)

const ObservedReadLimit = 256 << 10
const observedFrameLimit = 512 << 10

type ObservedFileObservation struct {
	RootBindingID, ObjectBindingID string
	SizeKnown                      bool
	Size                           int64
	ModifiedKnown                  bool
	ModifiedNS                     int64
	ChangeToken                    string
}
type observedCommand struct {
	Operation string
	Offset    int64
	Length    int
}
type observedResponse struct {
	Failure     string
	Observation *ObservedFileObservation
	After       *ObservedFileObservation
	Data        []byte
}
type observedCall struct {
	command observedCommand
	done    chan observedCallResult
}
type observedCallResult struct {
	response observedResponse
	err      error
}

// ObservedPlaybackReader retains exactly one helper acquisition. Protocol loss,
// timeout and cancellation close it; no path reopen or helper restart occurs.
// The supervisor owns RootLease.Release through physical helper exit.
type ObservedPlaybackReader struct {
	owner     context.Context
	life      context.Context
	cancel    context.CancelFunc
	input     *io.PipeWriter
	calls     chan observedCall
	timeout   time.Duration
	closeOnce sync.Once
}

// OpenObservedPlayback takes ownership of lease even on error. Root registration
// is mandatory; source observations are never converted to a strong Version.
func (c *Client) OpenObservedPlayback(setup context.Context, lease *RootLease, bindings ...*PlaybackBinding) (reader *ObservedPlaybackReader, err error) {
	transferred := false
	defer func() {
		if !transferred {
			releaseRootLease(lease)
		}
	}()
	if !validRootLease(lease) {
		return nil, ErrRootLease
	}
	if !observedPlaybackSupported {
		return nil, ErrObservedSourceUnsupported
	}
	if c == nil || c.Supervisor == nil || c.Binary == "" {
		return nil, ErrPlaybackSource
	}
	if err := setup.Err(); err != nil {
		return nil, err
	}
	if err := lease.Lifetime.Err(); err != nil {
		return nil, ErrObservedSourceLost
	}
	// Guard only consults the managed owner; the source parent performs no stat/open.
	path := filepath.Join(lease.RootPath, lease.RelativePath)
	if c.Guard != nil {
		if err := c.Guard(path); err != nil {
			return nil, err
		}
	}
	managed, err := c.managedRecording(setup, path)
	if err != nil {
		return nil, err
	}
	if len(bindings) > 0 {
		if _, err := c.bindPlaybackRequest(request{Path: path}, bindings); err != nil {
			return nil, err
		}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	life, cancel := context.WithCancel(context.Background())
	stopLease := context.AfterFunc(lease.Lifetime, cancel)
	input, writer := io.Pipe()
	f := &ObservedPlaybackReader{owner: lease.Lifetime, life: life, cancel: cancel, input: writer, calls: make(chan observedCall), timeout: timeout}
	req := request{Operation: "playback-observed", Path: lease.RootPath, ManagedArtifact: managed, ObservedRegistrationID: lease.RegistrationID, ObservedRelativePath: lease.RelativePath}
	if len(bindings) == 1 {
		req.PinnedPlayback, req.PlaybackBinding = true, bindings[0]
	}
	if lease.MountPath != "" {
		req.MountedRoot, req.ExpectedMountIdentity = lease.MountPath, lease.MountIdentity
	} else if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(path)
	}
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = input
	closeRoot, err := configureObservedRoot(cmd, lease.Directory, &req)
	if err != nil {
		stopLease()
		input.Close()
		writer.Close()
		cancel()
		return nil, err
	}
	operationDone := func() {}
	stopScope := func() bool { return false }
	if lease.Operations != nil {
		operationDone, err = lease.Operations.Begin()
		if err != nil {
			stopLease()
			closeRoot()
			input.Close()
			writer.Close()
			cancel()
			return nil, err
		}
		f.owner = &scopeOwnerLifetime{Context: lease.Lifetime, scope: lease.Operations.Context()}
		stopScope = context.AfterFunc(lease.Operations.Context(), cancel)
	}
	ready := make(chan error, 1)
	transferred = true
	supervise.Go("storage.observed-playback", func() {
		defer input.Close()
		defer writer.Close()
		defer cancel()
		runErr := c.Supervisor.RunOwnedCompletion(life, "playback:observed:"+fmtReaderID(), cmd, func(out io.Reader) error {
			defer writer.Close()
			encoder := json.NewEncoder(writer)
			if err := encoder.Encode(req); err != nil {
				return err
			}
			initial, err := readObservedResponse(out)
			if err != nil {
				return err
			}
			if initial.Observation == nil || initial.After != nil || len(initial.Data) != 0 {
				return ErrPlaybackSource
			}
			ready <- nil
			for {
				select {
				case <-life.Done():
					return life.Err()
				case call := <-f.calls:
					if f.owner.Err() != nil {
						f.Close()
						call.done <- observedCallResult{err: ErrObservedSourceLost}
						return ErrObservedSourceLost
					}
					err := encoder.Encode(call.command)
					var response observedResponse
					if err == nil {
						response, err = readObservedResponse(out)
					}
					call.done <- observedCallResult{response, err}
					if err != nil {
						return err
					}
				}
			}
		}, func() { stopLease(); stopScope(); closeRoot(); releaseRootLease(lease) }, operationDone)
		if runErr == nil {
			runErr = ErrObservedSourceLost
		}
		select {
		case ready <- runErr:
		default:
		}
	})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-ready:
		if err != nil {
			f.Close()
			return nil, err
		}
		if setup.Err() != nil {
			f.Close()
			return nil, setup.Err()
		}
		if f.owner.Err() != nil {
			f.Close()
			return nil, ErrObservedSourceLost
		}
		return f, nil
	case <-setup.Done():
		f.Close()
		return nil, setup.Err()
	case <-life.Done():
		f.Close()
		select {
		case reason := <-ready:
			if reason != nil {
				return nil, reason
			}
		default:
		}
		return nil, ErrObservedSourceLost
	case <-timer.C:
		f.Close()
		return nil, ErrPlaybackTimeout
	}
}
func (f *ObservedPlaybackReader) Close() error {
	f.closeOnce.Do(func() { f.cancel(); f.input.Close() })
	return nil
}

// ownerError checks the actual registration boundary; context.AfterFunc is only
// a wakeup mechanism and may not have propagated owner cancellation yet.
func (f *ObservedPlaybackReader) ownerError() error {
	if f.owner.Err() != nil {
		f.Close()
		return ErrObservedSourceLost
	}
	return nil
}
func (f *ObservedPlaybackReader) call(parent context.Context, command observedCommand) (observedResponse, error) {
	if err := f.ownerError(); err != nil {
		return observedResponse{}, err
	}
	if err := parent.Err(); err != nil {
		return observedResponse{}, err
	}
	if f.life.Err() != nil {
		return observedResponse{}, ErrObservedSourceLost
	}
	call := observedCall{command, make(chan observedCallResult, 1)}
	ctx, cancel := context.WithTimeout(parent, f.timeout)
	defer cancel()
	select {
	case f.calls <- call:
	case <-f.life.Done():
		return observedResponse{}, ErrObservedSourceLost
	case <-ctx.Done():
		return observedResponse{}, ctx.Err()
	}
	select {
	case result := <-call.done:
		if err := f.ownerError(); err != nil {
			return observedResponse{}, err
		}
		if result.err != nil {
			f.Close()
			return observedResponse{}, result.err
		}
		if ctx.Err() != nil {
			f.Close()
			return observedResponse{}, ctx.Err()
		}
		if f.life.Err() != nil {
			return observedResponse{}, ErrObservedSourceLost
		}
		if err := f.ownerError(); err != nil {
			return observedResponse{}, err
		}
		return result.response, nil
	case <-f.life.Done():
		return observedResponse{}, ErrObservedSourceLost
	case <-ctx.Done():
		f.Close()
		return observedResponse{}, ctx.Err()
	}
}
func (f *ObservedPlaybackReader) Observe(ctx context.Context) (ObservedFileObservation, error) {
	r, err := f.call(ctx, observedCommand{Operation: "observe"})
	if err != nil {
		return ObservedFileObservation{}, err
	}
	if r.Observation == nil || r.After != nil || len(r.Data) != 0 {
		f.Close()
		return ObservedFileObservation{}, ErrObservedSourceLost
	}
	if err := f.ownerError(); err != nil {
		return ObservedFileObservation{}, err
	}
	return *r.Observation, nil
}
func (f *ObservedPlaybackReader) ReadExtent(ctx context.Context, offset int64, length int) ([]byte, error) {
	if offset < 0 || length < 1 || length > ObservedReadLimit {
		return nil, ErrPlaybackSource
	}
	r, err := f.call(ctx, observedCommand{Operation: "read", Offset: offset, Length: length})
	if err != nil {
		return nil, err
	}
	if r.Observation == nil || r.After == nil || len(r.Data) != length || observedFileConflict(*r.Observation, *r.After) {
		f.Close()
		return nil, ErrObservedSourceLost
	}
	if err := f.ownerError(); err != nil {
		return nil, err
	}
	return r.Data, nil
}
func validObservedFile(o *ObservedFileObservation) bool {
	return o != nil && o.RootBindingID != "" && o.ObjectBindingID != "" && len(o.RootBindingID) <= 8192 && len(o.ObjectBindingID) <= 4096 && len(o.ChangeToken) <= 4096 && (!o.SizeKnown || o.Size >= 0) && (o.SizeKnown || o.Size == 0) && (o.ModifiedKnown || o.ModifiedNS == 0)
}
func observedFileConflict(a, b ObservedFileObservation) bool {
	return a.RootBindingID != b.RootBindingID || a.ObjectBindingID != b.ObjectBindingID || (a.SizeKnown && b.SizeKnown && a.Size != b.Size) || (a.ModifiedKnown && b.ModifiedKnown && a.ModifiedNS != b.ModifiedNS) || (a.ChangeToken != "" && b.ChangeToken != "" && a.ChangeToken != b.ChangeToken)
}
func writeObservedResponse(out io.Writer, r observedResponse) error {
	b, err := json.Marshal(r)
	if err != nil || len(b) > observedFrameLimit {
		return ErrPlaybackSource
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if _, err = out.Write(h[:]); err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}
func readObservedResponse(in io.Reader) (observedResponse, error) {
	var h [4]byte
	if _, err := io.ReadFull(in, h[:]); err != nil {
		return observedResponse{}, ErrObservedSourceLost
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > observedFrameLimit {
		return observedResponse{}, ErrObservedSourceLost
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(in, b); err != nil {
		return observedResponse{}, ErrObservedSourceLost
	}
	var r observedResponse
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return observedResponse{}, ErrObservedSourceLost
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return observedResponse{}, ErrObservedSourceLost
	}
	if r.Failure != "" {
		if r.Observation != nil || r.After != nil || len(r.Data) != 0 {
			return observedResponse{}, ErrObservedSourceLost
		}
		switch r.Failure {
		case "root_registration":
			return observedResponse{}, ErrRootLease
		case "unsupported":
			return observedResponse{}, ErrObservedSourceUnsupported
		default:
			return observedResponse{}, ErrObservedSourceLost
		}
	}
	if !validObservedFile(r.Observation) || (r.After != nil && !validObservedFile(r.After)) || len(r.Data) > ObservedReadLimit {
		return observedResponse{}, ErrObservedSourceLost
	}
	return r, nil
}
func observedPlaybackHelper(req request, in io.Reader, out io.Writer) (result error) {
	defer func() {
		if result != nil {
			failure := "continuity_lost"
			if errors.Is(result, ErrRootLease) {
				failure = "root_registration"
			}
			if errors.Is(result, ErrObservedSourceUnsupported) {
				failure = "unsupported"
			}
			_ = writeObservedResponse(out, observedResponse{Failure: failure})
		}
	}()
	if !observedPlaybackSupported {
		return ErrObservedSourceUnsupported
	}
	if req.ObservedRegistrationID == "" || len(req.ObservedRegistrationID) > 4096 || !filepath.IsAbs(req.Path) || !filepath.IsLocal(req.ObservedRelativePath) || req.ObservedRelativePath == "." {
		return ErrRootLease
	}
	registered := inheritedObservedRoot(req)
	if registered == nil {
		return ErrRootLease
	}
	defer registered.Close()
	// OpenRoot owns the acquired namespace. No path-based validation precedes an
	// unrelated open: compare the root used for the actual relative file open.
	root, err := os.OpenRoot(req.Path)
	if err != nil {
		return err
	}
	defer root.Close()
	registeredInfo, err := registered.Stat()
	if err != nil || !registeredInfo.IsDir() {
		return ErrRootLease
	}
	rootInfo, err := root.Stat(".")
	if err != nil || !sameObservedRoot(registered, root, registeredInfo, rootInfo) {
		return ErrRootLease
	}
	rootObject, _, err := observedFileBinding(registered)
	if err != nil {
		return err
	}
	var file *os.File
	if req.PinnedPlayback {
		acquisition, e := bindPlaybackRequest(request{Path: filepath.Join(req.Path, req.ObservedRelativePath), MountedRoot: req.MountedRoot}, []*PlaybackBinding{req.PlaybackBinding})
		if e != nil {
			return e
		}
		if acquisition.Inventory != nil {
			file, err = openPlaybackRequestFile(acquisition)
		} else {
			// Keep the registered-root confinement for source-less and mounted
			// observations as well as the playback path's native no-follow check.
			// Both acquisitions must name the same actual object.
			file, err = root.Open(req.ObservedRelativePath)
			if err == nil {
				checked, checkErr := openPlaybackRequestFile(acquisition)
				if checkErr != nil {
					file.Close()
					return checkErr
				}
				actual, actualErr := file.Stat()
				checkedInfo, checkedErr := checked.Stat()
				checked.Close()
				if actualErr != nil || checkedErr != nil || !os.SameFile(actual, checkedInfo) {
					file.Close()
					return ErrPlaybackSource
				}
			}
		}
	} else {
		file, err = root.Open(req.ObservedRelativePath)
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if err = lockRecordingFile(req, file); err != nil {
		return err
	}
	observe := func() (ObservedFileObservation, error) {
		if req.MountedRoot != "" {
			if e := sameMountFilesystem(file, req.MountedRoot); e != nil {
				return ObservedFileObservation{}, e
			}
			actual, e := physicalMountIdentity(req.MountedRoot)
			if e != nil || (req.ExpectedMountIdentity != "" && actual != req.ExpectedMountIdentity) {
				return ObservedFileObservation{}, ErrObservedSourceLost
			}
		}
		// Check the retained root, not a mutable pathname that might now name a
		// replacement directory. Registration/mount lifetime remains owner-fenced.
		currentRoot, e := root.Stat(".")
		if e != nil || !sameObservedRoot(registered, root, registeredInfo, currentRoot) {
			return ObservedFileObservation{}, ErrObservedSourceLost
		}
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() {
			return ObservedFileObservation{}, ErrObservedSourceLost
		}
		object, token, e := observedFileBinding(file)
		if e != nil {
			return ObservedFileObservation{}, e
		}
		return ObservedFileObservation{RootBindingID: req.ObservedRegistrationID + ":" + rootObject, ObjectBindingID: object, SizeKnown: true, Size: info.Size(), ModifiedKnown: true, ModifiedNS: info.ModTime().UnixNano(), ChangeToken: token}, nil
	}
	initial, err := observe()
	if err != nil {
		return err
	}
	if err = writeObservedResponse(out, observedResponse{Observation: &initial}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1024), 1024)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		var command observedCommand
		if err = decoder.Decode(&command); err != nil {
			return err
		}
		var extra any
		if err = decoder.Decode(&extra); err != io.EOF {
			return ErrObservedSourceLost
		}
		before, e := observe()
		if e != nil {
			return e
		}
		if observedFileConflict(initial, before) {
			return ErrObservedSourceLost
		}
		response := observedResponse{Observation: &before}
		switch command.Operation {
		case "observe":
			if command.Offset != 0 || command.Length != 0 {
				return ErrObservedSourceLost
			}
		case "read":
			if command.Offset < 0 || command.Length < 1 || command.Length > ObservedReadLimit || command.Offset > before.Size || int64(command.Length) > before.Size-command.Offset {
				return ErrObservedSourceLost
			}
			data := make([]byte, command.Length)
			n, readErr := file.ReadAt(data, command.Offset)
			after, e := observe()
			if e != nil {
				return e
			}
			if observedFileConflict(before, after) || readErr != nil || n != len(data) {
				return ErrObservedSourceLost
			}
			response.After = &after
			response.Data = data
		default:
			return ErrObservedSourceLost
		}
		if err = writeObservedResponse(out, response); err != nil {
			return err
		}
	}
	return scanner.Err()
}
