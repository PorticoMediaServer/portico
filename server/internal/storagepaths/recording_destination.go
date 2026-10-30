package storagepaths

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

var ErrRecordingFolderNotEmpty = errors.New("recording folder is not empty")

// EmptyRecordingRootIdentity pins an existing empty directory without creating,
// deleting or changing its contents. The identity is private activation metadata.
func EmptyRecordingRootIdentity(path string) (string, error) { return recordingRootIdentity(path, true) }

// RecordingRootIdentity reads an existing effective root without requiring it
// to be empty; retained content is normal for an unchanged startup binding.
func RecordingRootIdentity(path string) (string, error) { return recordingRootIdentity(path, false) }

func recordingRootIdentity(path string, requireEmpty bool) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path { return "", errors.New("invalid recording folder") }
	if err := canonicalParents(path); err != nil { return "", err }
	folder, err := os.Open(path)
	if err != nil { return "", err }
	defer folder.Close()
	info, err := folder.Stat()
	if err != nil || !info.IsDir() { return "", errors.New("recording folder unavailable") }
	identity, err := recordingDirectoryIdentity(folder, info)
	if err != nil { return "", err }
	if requireEmpty {
		names, err := folder.Readdirnames(1)
		if err != nil && err != io.EOF { return "", err }
		if len(names) != 0 { return "", ErrRecordingFolderNotEmpty }
	}
	if err = canonicalParents(path); err != nil { return "", err }
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) { return "", errors.New("recording folder changed") }
	return identity, nil
}
