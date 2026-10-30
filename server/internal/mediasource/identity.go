// Package mediasource defines private source identity and timing primitives.
// It does not establish authorization, acquire sources, or expose a wire schema.
package mediasource

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	ErrEvidence         = errors.New("invalid source version evidence")
	ErrIdentityRequired = errors.New("stable source identity requires preparation or qualified sequential access")
	ErrSourceChanged    = errors.New("source version changed")
)

type EvidenceKind string

const (
	LocalRevision            EvidenceKind = "local_revision"
	StrongETag               EvidenceKind = "strong_etag"
	ImmutableProviderVersion EvidenceKind = "immutable_provider_version"
	ContentSHA256            EvidenceKind = "content_sha256"
)

// Evidence is supplied by a trusted source adapter, never directly by a client.
// Scope and Object identify the provider/root and object independently of its
// current locator. Revision is a filesystem revision, strong ETag or immutable
// provider version. LocalRevision additionally requires the adapter's explicit
// no-in-place-mutation guarantee: inode/size/mtime alone cannot establish it.
// Source adapters still enforce conditional IO and validate every reopened object.
// Digest evidence assumes the adapter serves the bytes that were hashed.
type Evidence struct {
	Kind                    EvidenceKind
	Scope, Object, Revision string
	Size                    int64
	NoInPlaceMutation       bool
}

// Version cannot be constructed or changed outside this package except through
// validated evidence. Its ID contains no locator, credential or filesystem path.
type Version struct {
	evidence Evidence
	id       string
}

func NewVersion(e Evidence) (Version, error) {
	if e.Kind == "" {
		return Version{}, ErrIdentityRequired
	}
	if e.Size < 0 || !validIdentityText(e.Scope) || !validIdentityText(e.Object) || !validIdentityText(e.Revision) {
		return Version{}, ErrEvidence
	}
	switch e.Kind {
	case LocalRevision:
		if !e.NoInPlaceMutation {
			return Version{}, ErrIdentityRequired
		}
	case StrongETag:
		if e.NoInPlaceMutation || !isStrongETag(e.Revision) {
			return Version{}, ErrEvidence
		}
	case ImmutableProviderVersion:
		if e.NoInPlaceMutation {
			return Version{}, ErrEvidence
		}
	case ContentSHA256:
		if e.NoInPlaceMutation || len(e.Revision) != 64 || strings.ToLower(e.Revision) != e.Revision {
			return Version{}, ErrEvidence
		}
		if _, err := hex.DecodeString(e.Revision); err != nil {
			return Version{}, ErrEvidence
		}
	default:
		return Version{}, ErrEvidence
	}
	// Fixed struct encoding provides field framing; delimiter collisions in object
	// IDs cannot create equivalence. The domain prefix versions this private key.
	b, err := json.Marshal(e)
	if err != nil {
		return Version{}, fmt.Errorf("%w: encoding", ErrEvidence)
	}
	sum := sha256.Sum256(append([]byte("portico.source-version.v1\x00"), b...))
	return Version{evidence: e, id: hex.EncodeToString(sum[:])}, nil
}

func validIdentityText(s string) bool {
	if len(s) == 0 || len(s) > 4096 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func isStrongETag(s string) bool {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return false
	}
	for i := 1; i < len(s)-1; i++ {
		b := s[i]
		if b != 0x21 && (b < 0x23 || b > 0x7e) && b < 0x80 {
			return false
		}
	}
	return true
}

func (v Version) ID() string         { return v.id }
func (v Version) Evidence() Evidence { return v.evidence }

// Match must be called on fresh adapter evidence before bytes are admitted under
// this version. URL renewal is deliberately absent: the adapter revalidates origin
// policy independently and supplies the same scoped object/version if unchanged.
func (v Version) Match(current Evidence) error {
	if v.id == "" {
		return ErrEvidence
	}
	next, err := NewVersion(current)
	if err != nil {
		return err
	}
	if next.id != v.id {
		return ErrSourceChanged
	}
	return nil
}

// Dependency associates an independently versioned sidecar/input with its role.
// The adapter supplies the complete dependency set; omitted inputs cannot be
// inferred here. Callers must not reuse a key after widening that set or policy.
type Dependency struct {
	Role    string
	Version Version
}
type Representation struct {
	Source                                Version
	Dependencies                          []Dependency
	Tracks                                []string // ordered output mapping; reordering changes representation
	Timeline, Transform, Output, Producer string   // immutable configuration IDs
}

func RepresentationID(r Representation) (string, error) {
	if r.Source.id == "" || len(r.Dependencies) > 128 || len(r.Tracks) > 128 || !validIdentityText(r.Timeline) || !validIdentityText(r.Transform) || !validIdentityText(r.Output) || !validIdentityText(r.Producer) {
		return "", ErrEvidence
	}
	type dependency struct{ Role, Version string }
	deps := make([]dependency, 0, len(r.Dependencies))
	seen := map[string]bool{}
	for _, d := range r.Dependencies {
		if !validIdentityText(d.Role) || d.Version.id == "" || seen[d.Role] {
			return "", ErrEvidence
		}
		seen[d.Role] = true
		deps = append(deps, dependency{d.Role, d.Version.id})
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Role < deps[j].Role })
	tracks := make([]string, 0, len(r.Tracks))
	seen = map[string]bool{}
	for _, id := range r.Tracks {
		if !validIdentityText(id) || seen[id] {
			return "", ErrEvidence
		}
		seen[id] = true
		tracks = append(tracks, id)
	}
	if len(tracks) == 0 {
		return "", ErrEvidence
	}
	body := struct {
		Source                                string
		Dependencies                          []dependency
		Tracks                                []string
		Timeline, Transform, Output, Producer string
	}{r.Source.id, deps, tracks, r.Timeline, r.Transform, r.Output, r.Producer}
	b, err := json.Marshal(body)
	if err != nil {
		return "", ErrEvidence
	}
	sum := sha256.Sum256(append([]byte("portico.representation.v1\x00"), b...))
	return hex.EncodeToString(sum[:]), nil
}
