//go:build !linux && !darwin

package recordingmedia

import (
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// Configured layouts fail closed where descriptor-relative, no-follow custody
// and physical retirement have not been supplied. The original layout remains.
type layoutDirectory struct{ file *os.File }

func openLayoutDirectory(string) (*layoutDirectory, error) { return nil, mediaartifact.ErrPrivate }
func (*layoutDirectory) Close() error                      { return nil }
func (*layoutDirectory) parent(string, bool) (*layoutDirectory, string, error) {
	return nil, "", mediaartifact.ErrPrivate
}
func (*layoutDirectory) open(string) (*os.File, error)               { return nil, mediaartifact.ErrPrivate }
func (*layoutDirectory) link(*layoutDirectory, string, string) error { return mediaartifact.ErrPrivate }
func (*layoutDirectory) remove(string) error                         { return mediaartifact.ErrPrivate }

func layoutPhysicalIdentity(os.FileInfo) string { return "" }
