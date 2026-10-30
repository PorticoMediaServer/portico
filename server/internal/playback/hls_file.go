package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrHLSArtifactUnavailable = errors.New("stream artifact unavailable")

// OpenHLSFileContext coordinates production, then opens the authorized artifact
// beneath the pinned output directory. The caller owns the handle through HTTP.
func (s *Service) OpenHLSFileContext(ctx context.Context, grant, name string) (*os.File, error) {
	path, err := s.HLSFileContext(ctx, grant, name)
	if err != nil {
		return nil, err
	}
	return s.hls.openOutputPath(ctx, path, name)
}

// A path returned before a spill may name the retired root. Resolve its already
// authorized session against current placement while holding the move fence.
func (h *HLS) openOutputPath(ctx context.Context, path, name string) (*os.File, error) {
	id := filepath.Base(filepath.Dir(path))
	if !generatedDirectoryID.MatchString(id) || filepath.Base(path) != name {
		return nil, ErrHLSArtifactUnavailable
	}
	for {
		if err := h.waitOutputMove(ctx, id); err != nil {
			return nil, err
		}
		h.mu.Lock()
		if h.moving[id] != nil {
			h.mu.Unlock()
			continue
		}
		if path != filepath.Join(h.root, id, name) && path != filepath.Join(h.memoryRoot, id, name) {
			h.mu.Unlock()
			return nil, ErrHLSArtifactUnavailable
		}
		if h.cleaning[id] {
			h.mu.Unlock()
			return nil, ErrHLSArtifactUnavailable
		}
		root := h.outputRootLocked(id)
		if h.outputBusy == nil {
			h.outputBusy = map[string]int{}
		}
		h.outputBusy[id]++
		h.mu.Unlock()
		open := h.outputOpen
		if open == nil {
			open = openHLSOutput
		}
		file, err := open(root, id, name)
		h.releaseOutput(id)
		if err != nil {
			return nil, ErrHLSArtifactUnavailable
		}
		return file, nil
	}
}

func openHLSOutput(root *os.Root, session, name string) (*os.File, error) {
	if root == nil || session == "" || session == "." || session == ".." || strings.ContainsAny(session, `/\`) || !artifactName.MatchString(name) {
		return nil, os.ErrInvalid
	}
	file, err := root.OpenFile(filepath.Join(session, name), hlsReadFlags, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, os.ErrInvalid
	}
	return file, nil
}
