package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"strconv"
	"strings"
)

const maxVersionedDescriptorBytes int64 = 64 << 10

var ErrDescriptorBounds = errors.New("source descriptor exceeds structural bounds")

// VersionedDescriptor binds immutable string bytes to the actual local source
// version observed through the supervised opened-handle reader. Its version is
// an access dependency, distinct from the remote media version a STRM identifies.
type VersionedDescriptor struct {
	Version    mediasource.Version
	Data       string
	Digest     string
	ModifiedNS int64
}

// ReadVersionedDescriptor uses the same trusted root qualification as versioned
// media access. Discovery and read are separate supervised stages, bounded by the
// caller deadline and each helper timeout; no parent source stat/read occurs.
// This structural descriptor limit is not a limit on remote media length.
func (c *Client) ReadVersionedDescriptor(ctx context.Context, path string) (VersionedDescriptor, error) {
	version, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil {
		return VersionedDescriptor{}, err
	}
	evidence := version.Evidence()
	if evidence.Size > maxVersionedDescriptorBytes {
		return VersionedDescriptor{}, ErrDescriptorBounds
	}
	reader, err := c.OpenVersionedPlayback(ctx, path, version)
	if err != nil {
		return VersionedDescriptor{}, err
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, maxVersionedDescriptorBytes+1))
	if err != nil {
		return VersionedDescriptor{}, err
	}
	if int64(len(raw)) != evidence.Size {
		return VersionedDescriptor{}, mediasource.ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return VersionedDescriptor{}, err
	}
	// The adapter owns this revision format (mtime:0:0; ctime fields retired).
	parts := strings.Split(evidence.Revision, ":")
	if len(parts) != 3 {
		return VersionedDescriptor{}, mediasource.ErrEvidence
	}
	modified, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return VersionedDescriptor{}, mediasource.ErrEvidence
	}
	digest := sha256.Sum256(raw)
	return VersionedDescriptor{Version: version, Data: string(raw), Digest: hex.EncodeToString(digest[:]), ModifiedNS: modified}, nil
}
