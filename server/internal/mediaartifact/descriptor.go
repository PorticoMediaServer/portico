package mediaartifact

import (
	"context"
	"os"
)

// ReadDescriptor owns one fresh, verified, read-only artifact descriptor and
// its physical deletion lease. It conveys no source, publication or viewer
// authority. The producer must retain those original checks separately.
type ReadDescriptor struct{ reader *Reader }

// OpenDescriptor resolves only a closed Object through this owned store. Like
// Open, it verifies the actual bytes and retains their physical read lease.
// Each call opens an independent cursor; it never duplicates a caller's active
// reader, reopens an arbitrary path, or substitutes a latest object.
func (s *Store) OpenDescriptor(ctx context.Context, object Object) (*ReadDescriptor, error) {
	reader, err := s.Open(ctx, object)
	if err != nil {
		return nil, err
	}
	return &ReadDescriptor{reader: reader}, nil
}

// File is for a trusted descriptor-confined helper/converter handoff. Keep the
// ReadDescriptor alive until that original process exits; closing only File is
// not a release of the artifact lease. It is not a locator or a media grant.
func (d *ReadDescriptor) File() *os.File {
	if d == nil || d.reader == nil {
		return nil
	}
	return d.reader.file
}

func (d *ReadDescriptor) Close() error {
	if d == nil || d.reader == nil {
		return nil
	}
	return d.reader.Close()
}
