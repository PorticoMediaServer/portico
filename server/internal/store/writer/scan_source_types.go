package writer

import "bytes"

// Captured native values only. This shape supplies no principal, observation,
// birth or lease authority. W's current source reader remains a separate slice.
type scanSourceFacts struct {
	libraryID         int64
	libraryKey        string
	source            inventorySource
	policyRevision    int64
	nativeRecordBytes uint64 // complete payload measured in this capture snapshot, never authority
	identityRoot      []byte // catalog_libraries.root, never the physical source root
}

func (f scanSourceFacts) matches(o scanRunOriginalScope) bool {
	return f.libraryID == o.LibraryID && f.libraryKey == o.LibraryKey && f.policyRevision == o.PolicyRevision &&
		f.source.id == o.SourceID && f.source.generation == o.SourceGeneration && f.source.incarnation == o.SourceIncarnation &&
		f.source.rootIdentity == o.RootIdentity && f.source.followSymlinks == o.FollowSymlinks && bytes.Equal(f.source.resolvedRoot, o.ResolvedRoot)
}
