package read

// Library identities come only from previously issued opaque evidence. The
// caller cannot supply a new library selector for a retained hit.
func nativeStampLibrary(s *FullSearchReadStamp, id int64) (browseFence, bool) {
	if s == nil || id <= 0 {
		return browseFence{}, false
	}
	if s.fence.permission.libraryID == id {
		return s.fence.permission, true
	}
	for _, f := range s.libraries {
		if f.libraryID == id {
			return f, true
		}
	}
	return browseFence{}, false
}
func nativeLibraryFence(fences []browseFence, id int64) (browseFence, bool) {
	for _, f := range fences {
		if f.libraryID == id {
			return f, true
		}
	}
	return browseFence{}, false
}
func nativeCommonFence(f nativeSearchFence) nativeSearchFence {
	p := f.permission
	p.library, p.root, p.bridgeRoot = "", "", ""
	p.libraryID, p.dataRevision, p.orderRevision, p.metadataRevision, p.ageRevision, p.viewerRevision = 0, 0, 0, 0, 0, 0
	p.originIncarnation, p.originRevision = [16]byte{}, 0
	f.permission = p
	return f
}

// Recheck each retained library in this physical scope, before using its item
// policy proof. Every statement/copy is charged by the enclosing query driver.
func nativeRecheckLibraries(q browseQuery, in BrowseInput, current nativeSearchFence, stamps []*FullSearchReadStamp) ([]browseFence, error) {
	fences := []browseFence{current.permission}
	for _, stamp := range stamps {
		if stamp == nil {
			return nil, ErrInput
		}
		expectedFences := []browseFence{stamp.fence.permission}
		for _, proof := range stamp.policies {
			f, ok := nativeStampLibrary(stamp, proof.library)
			if !ok {
				return nil, ErrStale
			}
			if _, found := nativeLibraryFence(expectedFences, proof.library); !found {
				expectedFences = append(expectedFences, f)
			}
		}
		for _, expected := range expectedFences {
			if prior, found := nativeLibraryFence(fences, expected.libraryID); found {
				if prior != expected {
					return nil, ErrStale
				}
				continue
			}
			view := in
			view.Library = expected.libraryID
			fresh, _, err := nativeReadFence(q, view)
			if err != nil {
				return nil, err
			}
			want := stamp.fence
			want.permission = expected
			if fresh != want || nativeCommonFence(fresh) != nativeCommonFence(current) {
				return nil, ErrStale
			}
			fences = append(fences, fresh.permission)
			if len(fences) > MaxNativeRetained+1 {
				return nil, ErrInput
			}
		}
	}
	return fences, nil
}
func nativeValidateRetainedPolicy(q browseQuery, in BrowseInput, fences []browseFence, proof nativePolicyWitness) error {
	f, ok := nativeLibraryFence(fences, proof.library)
	if !ok {
		return ErrStale
	}
	in.Library = f.libraryID
	return validateNativePolicy(q, in, f, proof)
}
