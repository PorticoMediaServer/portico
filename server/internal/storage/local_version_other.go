//go:build !darwin && !linux

package storage

import (
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

// Windows and other OS object/revision implementations remain unqualified.
func localVersionEvidence(os.FileInfo, string) (mediasource.Evidence, error) {
	return mediasource.Evidence{}, mediasource.ErrIdentityRequired
}
