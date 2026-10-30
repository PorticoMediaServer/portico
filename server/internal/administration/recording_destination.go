package administration

import (
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// RecordingDestination uses the existing owner picker scope. It never admits a
// library URL, managed mount locator, symlink, system or server-data directory.
func (s *Service) RecordingDestination(path string) (string, error) { return s.recordingFolder(path, true) }

// RecordingRootIdentity rechecks policy and identity after startup binds its
// stores; it permits the driver's newly created internal directories.
func (s *Service) RecordingRootIdentity(path string) (string, error) { return s.recordingFolder(path, false) }

func (s *Service) recordingFolder(path string, empty bool) (string, error) {
	if !safeText(path, 4096) || !filepath.IsAbs(path) || filepath.Clean(path) != path { return "", ErrInput }
	if systemPath(path) || s.pickerPolicy().CheckBrowse(path) != nil { return "", ErrDenied }
	if !filesystemWritable(path) { return "", ErrDenied }
	if err := storagepaths.At(s.StateDirectory()).ValidateRecordings(path); err != nil { return "", ErrDenied }
	if empty { return storagepaths.EmptyRecordingRootIdentity(path) }
	return storagepaths.RecordingRootIdentity(path)
}
