package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"strconv"
	"strings"
	"time"
)

// LocalVersionPolicy is a trusted configured root contract, not inferred from
// file size/mtime or a filesystem type name. Stable object IDs and write-sensitive
// revisions plus no in-place changes while a version is leased must be established
// by the source owner (e.g. immutable snapshots or coordinated file replacement).
// Arbitrary network/FUSE/rclone roots must not opt in without equivalent evidence.
type LocalVersionPolicy struct {
	Scope                string
	StableObjectIDs      bool
	RevisionTracksWrites bool
	NoInPlaceMutation    bool
}

func (c *Client) localPolicy(path string) (LocalVersionPolicy, error) {
	if c.Guard != nil {
		if err := c.Guard(path); err != nil {
			return LocalVersionPolicy{}, err
		}
	}
	if c.VersionPolicy == nil {
		return LocalVersionPolicy{}, mediasource.ErrIdentityRequired
	}
	p, err := c.VersionPolicy(path)
	if err != nil || p.Scope == "" || !p.StableObjectIDs || !p.RevisionTracksWrites || !p.NoInPlaceMutation {
		return LocalVersionPolicy{}, mediasource.ErrIdentityRequired
	}
	return p, nil
}

func (c *Client) DiscoverPlaybackVersion(ctx context.Context, path string) (mediasource.Version, error) {
	p, err := c.localPolicy(path)
	if err != nil {
		return mediasource.Version{}, err
	}
	r := request{Operation: "playback-discover-version", Path: path, VersionScope: p.Scope}
	if c.MountedRoot != nil {
		r.MountedRoot = c.MountedRoot(path)
	}
	return c.runVersion(ctx, r, nil)
}

func (c *Client) checkPinnedPolicy(path string, version mediasource.Version) (int64, error) {
	p, err := c.localPolicy(path)
	if err != nil {
		return 0, err
	}
	e := version.Evidence()
	if version.ID() == "" || e.Kind != mediasource.LocalRevision || !e.NoInPlaceMutation {
		return 0, mediasource.ErrIdentityRequired
	}
	if p.Scope != e.Scope {
		return 0, mediasource.ErrSourceChanged
	}
	parts := strings.Split(e.Revision, ":")
	if len(parts) != 3 {
		return 0, mediasource.ErrEvidence
	}
	modified, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, mediasource.ErrEvidence
	}
	return modified, nil
}

func (c *Client) OpenVersionedPlayback(ctx context.Context, path string, version mediasource.Version) (*PlaybackReader, error) {
	modified, err := c.checkPinnedPolicy(path, version)
	if err != nil {
		return nil, err
	}
	e := version.Evidence()
	return c.openPlayback(ctx, path, e.Size, modified, &e)
}

// OpenVersionedPlaybackDescriptor returns a descriptor only after isolated
// validation against the selected version. The parent performs no source stat.
// Before/after encoder validation remains the producer's separate obligation.
func (c *Client) OpenVersionedPlaybackDescriptor(ctx context.Context, path string, version mediasource.Version) (*os.File, error) {
	modified, err := c.checkPinnedPolicy(path, version)
	if err != nil {
		return nil, err
	}
	f, err := c.OpenPlaybackDescriptor(ctx, path, version.Evidence().Size, modified)
	if err != nil {
		// The legacy transfer gate can reject changed size/mtime before it can
		// provide a descriptor. Classify only a proven change using another
		// bounded isolated acquisition; unavailable sources remain unavailable.
		if errors.Is(err, ErrPlaybackSource) {
			current, inspectErr := c.DiscoverPlaybackVersion(ctx, path)
			if inspectErr == nil && errors.Is(version.Match(current.Evidence()), mediasource.ErrSourceChanged) {
				return nil, mediasource.ErrSourceChanged
			}
		}
		return nil, err
	}
	if err := c.ValidatePlaybackVersion(ctx, f, version); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (c *Client) ValidatePlaybackVersion(ctx context.Context, file *os.File, version mediasource.Version) error {
	if file == nil || version.ID() == "" || version.Evidence().Kind != mediasource.LocalRevision {
		return mediasource.ErrIdentityRequired
	}
	e := version.Evidence()
	_, err := c.runVersion(ctx, request{Operation: "playback-validate-version", Version: &e}, file)
	return err
}

type versionResult struct {
	Evidence *mediasource.Evidence `json:"evidence,omitempty"`
	Failure  string                `json:"failure,omitempty"`
}

func (c *Client) runVersion(ctx context.Context, r request, file *os.File) (mediasource.Version, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b, err := json.Marshal(r)
	if err != nil {
		return mediasource.Version{}, mediasource.ErrEvidence
	}
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(b)
	if file != nil {
		cmd.ExtraFiles = []*os.File{file}
	}
	var result versionResult
	err = c.Supervisor.Run(ctx, "playback:version:"+fmtReaderID(), cmd, func(reader io.Reader) error {
		decoder := json.NewDecoder(io.LimitReader(reader, 8193))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return ErrPlaybackSource
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return ErrPlaybackSource
		}
		return nil
	})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return mediasource.Version{}, ErrPlaybackTimeout
		}
		return mediasource.Version{}, playbackOpenError(err)
	}
	if result.Failure != "" {
		switch result.Failure {
		case "source_changed":
			return mediasource.Version{}, mediasource.ErrSourceChanged
		case "identity_required":
			return mediasource.Version{}, mediasource.ErrIdentityRequired
		default:
			return mediasource.Version{}, ErrPlaybackSource
		}
	}
	if result.Evidence == nil {
		return mediasource.Version{}, ErrPlaybackSource
	}
	v, err := mediasource.NewVersion(*result.Evidence)
	if err != nil {
		return mediasource.Version{}, err
	}
	if r.Version != nil {
		expected, err := mediasource.NewVersion(*r.Version)
		if err != nil {
			return mediasource.Version{}, err
		}
		if err := expected.Match(v.Evidence()); err != nil {
			return mediasource.Version{}, err
		}
	} else if v.Evidence().Scope != r.VersionScope {
		return mediasource.Version{}, mediasource.ErrSourceChanged
	}
	return v, nil
}

func playbackVersionHelper(r request, out io.Writer) error {
	result := versionResult{}
	var file *os.File
	var err error
	if r.Operation == "playback-discover-version" {
		file, err = os.Open(r.Path)
	} else {
		file = os.NewFile(3, "versioned-playback-validation")
	}
	if err != nil || file == nil {
		result.Failure = "source_unavailable"
		return json.NewEncoder(out).Encode(result)
	}
	defer file.Close()
	scope := r.VersionScope
	if r.Version != nil {
		scope = r.Version.Scope
	}
	info, err := file.Stat()
	if err == nil {
		var evidence mediasource.Evidence
		evidence, err = localVersionEvidence(info, scope)
		if err == nil && r.Operation == "playback-discover-version" {
			current, e := os.Stat(r.Path)
			if e != nil || !os.SameFile(info, current) {
				err = mediasource.ErrSourceChanged
			}
		}
		if err == nil && r.Version != nil {
			var expected mediasource.Version
			expected, err = mediasource.NewVersion(*r.Version)
			if err == nil {
				err = expected.Match(evidence)
			}
		}
		if err == nil {
			result.Evidence = &evidence
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, mediasource.ErrSourceChanged):
			result.Failure = "source_changed"
		case errors.Is(err, mediasource.ErrIdentityRequired), errors.Is(err, mediasource.ErrEvidence):
			result.Failure = "identity_required"
		default:
			result.Failure = "source_unavailable"
		}
	}
	return json.NewEncoder(out).Encode(result)
}

func matchLocalVersion(file *os.File, expected mediasource.Evidence) error {
	v, err := mediasource.NewVersion(expected)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return ErrPlaybackSource
	}
	actual, err := localVersionEvidence(info, expected.Scope)
	if err != nil {
		return err
	}
	return v.Match(actual)
}
