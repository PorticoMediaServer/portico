package storage

import (
	"context"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/artifactlease"
	"regexp"
	"strings"
)

var recordingObjectID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (c *Client) managedRecording(ctx context.Context, path string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false, nil
	}
	if c.RecordingRoot != "" {
		relative, err := filepath.Rel(c.RecordingRoot, path)
		if err != nil {
			return false, err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if (len(parts) == 3 && recordingObjectID.MatchString(parts[0]) && parts[1] == "objects" && recordingObjectID.MatchString(parts[2])) ||
			(len(parts) == 5 && recordingObjectID.MatchString(parts[0]) && parts[1] == "captures" && recordingObjectID.MatchString(parts[2]) && parts[3] == "objects" && recordingObjectID.MatchString(parts[4])) {
			return true, nil
		}
	}
	if c.RecordingLayout != nil {
		return c.RecordingLayout(ctx, path)
	}
	return false, nil
}

func lockRecordingFile(req request, f *os.File) error {
	if !req.ManagedArtifact {
		return nil
	}
	if e := artifactlease.Shared(f); e != nil {
		return ErrPlaybackSource
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 {
		return ErrPlaybackSource
	}
	path := req.Path
	if req.ObservedRelativePath != "" {
		path = filepath.Join(path, req.ObservedRelativePath)
	}
	current, e := os.Lstat(path)
	if e != nil || !os.SameFile(info, current) {
		return ErrPlaybackSource
	}
	return nil
}
