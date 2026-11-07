package writer

import "unicode/utf8"

// prepareBasicCopyFamily derives only this command's bounded row chunk. The
// expected count is still the COMPLETE sealed native manifest, rechecked by
// every copy fence. Unselected slots carry no payload and cannot be copied.
// This avoids cloning/hashing the entire child set once for every copied row.
// Stage and Seal constructors continue to require their complete owned family.
// Original preparation funding and native manifest/source custody are external.
func prepareBasicCopyFamily(source basicStreamFactStage, family, expectedRows, start, rawChapters int64, chunk []basicMediaRow) (*basicMediaFamily, error) {
	limit := basicMediaFamilyLimit(family)
	if limit == 0 || expectedRows < 0 || start < 0 || start > expectedRows || rawChapters < 0 {
		return nil, ErrInvalid
	}
	if expectedRows > limit || len(chunk) > 8 || rawChapters > MaxRawChapters {
		return nil, ErrCap
	}
	if int64(len(chunk)) > expectedRows-start || (family == 5 && expectedRows > rawChapters) ||
		((family == 4 || family == 6 || family == 9) && expectedRows != 1) {
		return nil, ErrInvalid
	}
	remaining := int64(maxCanonicalRequestBytes)
	texts := [...]string{source.assetToken, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.fingerprint}
	for _, text := range texts {
		if int64(len(text)) > remaining {
			return nil, ErrCap
		}
		remaining -= int64(len(text))
	}
	// Admit every raw field before interface backing storage, string clones,
	// argument slices or JSON validation. No caller-owned native string escapes.
	for _, r := range chunk {
		fields, kind, _, base, err := basicMediaTextFields(source, r)
		if err != nil || kind != family {
			return nil, ErrInvalid
		}
		if base > remaining {
			return nil, ErrCap
		}
		remaining -= base
		for _, text := range fields {
			if int64(len(text)) > remaining {
				return nil, ErrCap
			}
			remaining -= int64(len(text))
		}
	}
	for _, text := range texts {
		if text == "" || !utf8.ValidString(text) {
			return nil, ErrInvalid
		}
	}
	if source.changeID <= 0 || source.inputRevision <= 0 || source.assetID <= 0 || source.libraryID <= 0 || source.expectedEpoch <= 0 {
		return nil, ErrInvalid
	}
	for n, r := range chunk {
		if _, _, _, _, err := basicMediaValues(source, start+int64(n), r); err != nil {
			return nil, err
		}
	}
	// The fixed 4096-slot maximum is an index of native input ordinals, not a
	// copied family payload. All unselected positions remain nil and fail closed
	// if a caller attempts to use them as a row or complete staging manifest.
	slots := make([]basicMediaRow, int(expectedRows))
	for n, r := range chunk {
		slots[int(start)+n] = cloneBasicMediaRow(r)
	}
	return &basicMediaFamily{source: cloneBasicSource(source), family: family, rawChapterCount: rawChapters, rows: slots, valid: true}, nil
}
