package assets

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

const (
	LocalArtworkUnknownRank      int64 = 1<<63 - 1
	LocalArtworkNumberedRankBase int64 = 1024
	MaxArtworkSourceNameBytes          = naming.MaxPathBytes
)

// LocalArtworkCandidate is an actual decoded, admitted sidecar image. Slot is
// the existing item/poster, show/backdrop or season/N/poster catalogue slot;
// Key is the local content-addressed image key. Neither is a path or a grant.
type LocalArtworkCandidate struct {
	Slot       string
	Key        string
	SourceName string
	SourceRank int64
}

// NormalizeLocalArtworkSource preserves legacy Slot/Key-only producers as an
// unknown order fact. A missing basename can never assert canonical rank zero.
// Names are source basenames, never paths, normalized labels or derived hashes.
func NormalizeLocalArtworkSource(name string, rank int64) (string, int64, error) {
	if name == "" {
		if rank != 0 && rank != LocalArtworkUnknownRank {
			return "", 0, errors.New("artwork rank without source basename")
		}
		return "", LocalArtworkUnknownRank, nil
	}
	if !utf8.ValidString(name) || len(name) > MaxArtworkSourceNameBytes || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || rank < 0 {
		return "", 0, errors.New("invalid local artwork order fact")
	}
	return name, rank, nil
}
