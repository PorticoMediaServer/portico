package mounts

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// ServeSource is private process-start material, not a public source document.
// Configuration must be cleared by its consumer after the transient copy. The
// retained source configuration is never edited, renamed or deleted here.
type ServeSource struct {
	Program                rcloneprogram.Program
	MountID                string
	Remote, Generation     string
	ApprovedProgramDigest  string
	Configuration          []byte
	CacheBytes, CacheFloor int64
}

// CaptureServeSource reads one exact current configured generation for the
// shared rclone serve path. It does not launch a native adapter, reapprove bytes,
// or treat the selected program/configuration as source-operation permission.
func (s *Service) CaptureServeSource(ctx context.Context, id, generation string) (ServeSource, error) {
	var out ServeSource
	if generation == "" {
		return out, storage.ErrRemoteConfig
	}
	b, err := s.Native(ctx, id)
	if err != nil {
		return out, err
	}
	if b.generation != generation {
		return out, storage.ErrRemoteChanged
	}
	info, err := os.Lstat(b.config)
	if err != nil || !info.Mode().IsRegular() {
		return out, storage.ErrRemoteConfig
	}
	root, err := os.OpenRoot(s.private)
	if err != nil {
		return out, storage.ErrRemoteConfig
	}
	defer root.Close()
	f, err := root.Open(filepath.Base(b.config))
	if err != nil {
		return out, storage.ErrRemoteConfig
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return out, storage.ErrRemoteChanged
	}
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || validateConfig(string(raw), b.remote) != nil {
		clear(raw)
		return out, storage.ErrRemoteConfig
	}
	current, _, cache, floor, err := s.BackendInfo(ctx, id)
	if err != nil || strconv.FormatInt(current, 10) != generation || b.Validate(ctx) != nil {
		clear(raw)
		return out, storage.ErrRemoteChanged
	}
	return ServeSource{Program: rcloneprogram.Program{Path: b.executable, Included: b.included}, MountID: b.id, ApprovedProgramDigest: b.digest, Remote: b.remote, Generation: generation, Configuration: raw, CacheBytes: cache, CacheFloor: floor}, nil
}
