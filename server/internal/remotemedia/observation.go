package remotemedia

import (
	"errors"
	"sort"
	"strings"
)

var ErrObservationChanged = errors.New("remote file observation changed; stop and rescan")
var ErrObservation = errors.New("remote file observation is incomplete")

// FileObservation records the scan facts, scoped independently of validators.
// Provider validators must come from fresh provider metadata, never an rclone
// served ETag. Empty provider fields explicitly mean the provider supplied none.
// Equality detects observable changes; it does not establish immutable bytes.
type FileObservation struct {
	SourceID, Incarnation, Root, Object, ObjectID string
	Size, ModifiedNS                              int64
	ModifiedKnown                                 bool
	ProviderHashes                                []ProviderHash
	ProviderETag, ServeETag                       string
}

type ProviderHash struct {
	Kind, Value string
}

// Canonical copies hashes into a deterministic set without hashing media bytes
// or interpreting an opaque provider value as a whole-file MD5. Scan storage
// retains unavailable metadata explicitly; range admission is stricter.
func (o FileObservation) Canonical() (FileObservation, error) {
	if o.SourceID == "" || o.Incarnation == "" || o.Object == "" || !cleanDAVRelative(o.Root) || !cleanDAVRelative(o.Object) || strings.Contains(o.Root+o.Object, ":") || o.Size < 0 || !o.ModifiedKnown && o.ModifiedNS != 0 || len(o.ProviderHashes) > 16 {
		return FileObservation{}, ErrObservation
	}
	for _, v := range []string{o.SourceID, o.Incarnation, o.ObjectID, o.ProviderETag, o.ServeETag} {
		if len(v) > 4096 || strings.ContainsAny(v, "\x00\r\n") {
			return FileObservation{}, ErrObservation
		}
	}
	o.ProviderHashes = append([]ProviderHash(nil), o.ProviderHashes...)
	sort.Slice(o.ProviderHashes, func(i, j int) bool { return o.ProviderHashes[i].Kind < o.ProviderHashes[j].Kind })
	for i, h := range o.ProviderHashes {
		if h.Kind == "" || len(h.Kind) > 64 || h.Value == "" || len(h.Value) > 4096 || strings.ContainsAny(h.Kind+h.Value, "\x00\r\n") || i > 0 && o.ProviderHashes[i-1].Kind == h.Kind {
			return FileObservation{}, ErrObservation
		}
	}
	return o, nil
}

func (o FileObservation) Validate() error {
	_, err := o.Canonical()
	return err
}

func (o FileObservation) ValidateForRange() error {
	if err := o.Validate(); err != nil {
		return err
	}
	// Section15 permits origins with weak or unavailable identity signals.
	// Missing time remains missing evidence, not a permission or byte guarantee.
	// The served ETag is still required for the HTTP precondition itself.
	if !serveETag(o.ServeETag) {
		return ErrObservation
	}
	return nil
}

func (o FileObservation) Equal(next FileObservation) bool {
	a, err := o.Canonical()
	if err != nil {
		return false
	}
	b, err := next.Canonical()
	if err != nil {
		return false
	}
	if a.SourceID != b.SourceID || a.Incarnation != b.Incarnation || a.Root != b.Root || a.Object != b.Object || a.ObjectID != b.ObjectID || a.Size != b.Size || a.ModifiedNS != b.ModifiedNS || a.ModifiedKnown != b.ModifiedKnown || a.ProviderETag != b.ProviderETag || a.ServeETag != b.ServeETag || len(a.ProviderHashes) != len(b.ProviderHashes) {
		return false
	}
	for i := range a.ProviderHashes {
		if a.ProviderHashes[i] != b.ProviderHashes[i] {
			return false
		}
	}
	return true
}

func (o FileObservation) Check(next FileObservation) error {
	if err := o.ValidateForRange(); err != nil {
		return err
	}
	if next.ValidateForRange() != nil || !o.Equal(next) {
		return ErrObservationChanged
	}
	return nil
}
