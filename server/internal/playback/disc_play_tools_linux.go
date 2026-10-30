//go:build linux

package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// discPlayToolWitness freezes the actual configured executables, never a later
// environment/default selection. Both calls around measurement reobserve the
// complete software loader closure and its file generations. Scripts or an
// incomplete closure cannot produce a reusable disc input/fact snapshot.
func discPlayToolWitness(helper, probe, converter string) (func(context.Context) (playbackdisc.ToolPins, error), error) {
	paths := [3]string{helper, probe, converter}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, disc.ErrRemoteUnqualified
		}
	}
	return func(ctx context.Context) (playbackdisc.ToolPins, error) {
		if ctx == nil || dbwork.Snapshot(ctx) != nil {
			return playbackdisc.ToolPins{}, disc.ErrProtocol
		}
		var digests, runtimes [3]string
		for i, path := range paths {
			before, complete, err := mediaexec.RuntimeIdentity(ctx, path)
			if err != nil {
				return playbackdisc.ToolPins{}, err
			}
			if !complete || before == "" {
				return playbackdisc.ToolPins{}, disc.ErrRemoteUnqualified
			}
			digest, err := discExecutableDigest(ctx, path)
			if err != nil {
				return playbackdisc.ToolPins{}, err
			}
			after, complete, err := mediaexec.RuntimeIdentity(ctx, path)
			if err != nil {
				return playbackdisc.ToolPins{}, err
			}
			if !complete || before != after {
				return playbackdisc.ToolPins{}, ErrStaleOffer
			}
			digests[i], runtimes[i] = digest, after
		}
		raw, err := json.Marshal(runtimes)
		if err != nil {
			return playbackdisc.ToolPins{}, err
		}
		identity := sha256.Sum256(append([]byte("disc-play-runtime-v1\x00"), raw...))
		if err = ctx.Err(); err != nil {
			return playbackdisc.ToolPins{}, err
		}
		return playbackdisc.ToolPins{DiscHelperDigest: digests[0], FFprobeDigest: digests[1], FFmpegDigest: digests[2], RuntimeIdentity: hex.EncodeToString(identity[:])}, nil
	}, nil
}

type discToolHashReader struct {
	ctx context.Context
	io.Reader
}

func (r discToolHashReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func discExecutableDigest(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 512<<20 {
		return "", disc.ErrRemoteUnqualified
	}
	hash := sha256.New()
	n, err := io.Copy(hash, discToolHashReader{ctx, io.LimitReader(file, before.Size()+1)})
	if err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	current, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if n != before.Size() || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || before.Size() != current.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(current.ModTime()) {
		return "", ErrStaleOffer
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
