package writer

// Project only an accepted native effect from the same fresh operation. This
// does not issue authority, reserve quota, append an event, commit, or produce
// an Applied/public wire result. Replay must classify before source effects.
func uoSavedOriginalResult(root string, purpose CommandID, q uoSavedInput, effect uoSavedEffect, appliedMS int64) (uoRootSavedResult, error) {
	var zero uoRootSavedResult
	var expected CommandID
	switch q.action {
	case uoSavedCreate:
		expected = CreateSavedResource
	case uoSavedEdit:
		expected = PatchSavedResource
	case uoSavedDelete:
		expected = DeleteSavedResource
	case uoSavedEntries, uoSavedUndo:
		expected = EditSavedResourceMembers
	case uoSavedReorder:
		expected = ReorderSavedResource
	case uoSavedShare, uoSavedUnshare:
		expected = SetSavedResourceSharing
	default:
		return zero, ErrInvalid
	}
	if purpose != expected || q.id != effect.id || effect.revision != q.revision+1 || effect.deleted != (q.action == uoSavedDelete) || effect.generation <= 0 || effect.selectionRevision <= 0 || len(effect.outcomes) > 200 {
		return zero, ErrInvalid
	}
	// Existing saved-resource acceptance advances its public revision even when
	// all entry outcomes are unchanged. It remains an accepted source/head delta;
	// classifying it as an effect-free replay would lose the original result.
	v := uoRootSavedResult{root: root, resource: effect.id, kind: purpose, revision: effect.revision, generation: effect.generation, selectionRevision: effect.selectionRevision, reclaimedRows: effect.reclaimedRows, reclaimedBytes: effect.reclaimedBytes,
		deleted: effect.deleted, changed: true, entriesPresent: purpose == EditSavedResourceMembers, undoToken: effect.undoToken, undoUntilMS: effect.undoUntil, appliedMS: appliedMS}
	for _, outcome := range effect.outcomes {
		var status uint8
		switch outcome.status {
		case "added":
			status = 1
		case "removed":
			status = 2
		case "unchanged":
			status = 3
		case "not_found":
			status = 4
		default:
			return zero, ErrInvalid
		}
		v.entries = append(v.entries, uoRootSavedEntry{status: status, reference: outcome.input, entry: outcome.id})
	}
	if err := preflightUORootSaved(v); err != nil {
		return zero, err
	}
	return v, nil
}
