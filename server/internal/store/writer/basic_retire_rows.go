package writer

// A restart cleanup prefix is a bounded set of ALREADY STORED Basic input rows
// identified by their captured native input_ordinal keys. Those keys are native
// database keys, not slice offsets: gaps are expected because an earlier
// command may already have removed an earlier prefix. This is a values-only
// adapter. It invents no raw chapter count, activation witness, staging request,
// SQL statement or dispatcher state. The derived family below is retained for
// the existing reclaimer only.
//
// It deliberately does not reuse prepareBasicRowsRequest, prepareBasicMediaFamily
// or freezeBasicRowsRequest: those constructs require authoritative RAW
// provenance (including a real raw chapter count), which a restart cleanup of a
// deleted prefix no longer has.
func basicRetirePrefixShape(rows basicRowsRequest, ordinals []int64, expectedRows int64) (int, error) {
	if rows.family < 2 || rows.family > 9 || rows.rawChapterCount != 0 {
		return 0, ErrInvalid
	}
	counts := [...]int{len(rows.streams), len(rows.breadth), len(rows.chapterFacts), len(rows.chapters), len(rows.subtitleFacts), len(rows.sidecars), len(rows.subtitles), len(rows.evidence)}
	present := [...]bool{rows.streams != nil, rows.breadth != nil, rows.chapterFacts != nil, rows.chapters != nil, rows.subtitleFacts != nil, rows.sidecars != nil, rows.subtitles != nil, rows.evidence != nil}
	for n := range present {
		if int64(n)+2 != rows.family {
			if present[n] {
				return 0, ErrInvalid
			}
			continue
		}
		if !present[n] || counts[n] < 1 {
			return 0, ErrInvalid
		}
		if counts[n] > 8 {
			return 0, ErrCap
		}
	}
	count := counts[rows.family-2]
	if len(ordinals) != count || expectedRows <= 0 {
		return 0, ErrInvalid
	}
	for n, ordinal := range ordinals {
		if ordinal < 0 || ordinal >= expectedRows || (n > 0 && ordinal <= ordinals[n-1]) {
			return 0, ErrInvalid
		}
	}
	return count, nil
}

// basicRetirePrefixBytes admits the complete retained value before any clones,
// interface backing storage, argument validation or derived family. The source
// text, the captured ordinal slice, and every concrete raw field/base/text
// charge are bounded together. This is not a physical-memory grant.
func basicRetirePrefixBytes(source basicStreamFactStage, rows basicRowsRequest, ordinals []int64, expectedRows int64) (uint64, error) {
	count, err := basicRetirePrefixShape(rows, ordinals, expectedRows)
	if err != nil {
		return 0, err
	}
	remaining := uint64(maxCanonicalRequestBytes)
	for _, text := range [...]string{source.assetToken, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.fingerprint} {
		if uint64(len(text)) > remaining {
			return 0, ErrCap
		}
		remaining -= uint64(len(text))
	}
	if uint64(count) > remaining/8 {
		return 0, ErrCap
	}
	remaining -= uint64(count) * 8
	for n := 0; n < count; n++ {
		var texts [9]string
		var family, base int64
		// Concrete values reach the existing text helper without an escaping
		// interface carrier. Policy/UTF-8 checks remain in the family helper.
		switch rows.family {
		case 2:
			texts, family, _, base, err = basicMediaTextFields(source, rows.streams[n])
		case 3:
			texts, family, _, base, err = basicMediaTextFields(source, rows.breadth[n])
		case 4:
			texts, family, _, base, err = basicMediaTextFields(source, rows.chapterFacts[n])
		case 5:
			texts, family, _, base, err = basicMediaTextFields(source, rows.chapters[n])
		case 6:
			texts, family, _, base, err = basicMediaTextFields(source, rows.subtitleFacts[n])
		case 7:
			texts, family, _, base, err = basicMediaTextFields(source, rows.sidecars[n])
		case 8:
			texts, family, _, base, err = basicMediaTextFields(source, rows.subtitles[n])
		case 9:
			texts, family, _, base, err = basicMediaTextFields(source, rows.evidence[n])
		}
		if err != nil {
			return 0, err
		}
		if family != rows.family {
			return 0, ErrInvalid
		}
		if uint64(base) > remaining {
			return 0, ErrCap
		}
		remaining -= uint64(base)
		for _, text := range texts {
			if uint64(len(text)) > remaining {
				return 0, ErrCap
			}
			remaining -= uint64(len(text))
		}
	}
	return uint64(maxCanonicalRequestBytes) - remaining, nil
}

// basicRetirePrefixOwnedRows derives at most eight interfaces only after the
// whole value passed admission, validates each against its ACTUAL native
// ordinal, and replaces it with the existing deep clone. The returned rows own
// their text and never alias caller storage.
func basicRetirePrefixOwnedRows(source basicStreamFactStage, rows basicRowsRequest, ordinals []int64) ([]basicMediaRow, error) {
	owned := make([]basicMediaRow, len(ordinals))
	switch rows.family {
	case 2:
		for n, row := range rows.streams {
			owned[n] = row
		}
	case 3:
		for n, row := range rows.breadth {
			owned[n] = row
		}
	case 4:
		for n, row := range rows.chapterFacts {
			owned[n] = row
		}
	case 5:
		for n, row := range rows.chapters {
			owned[n] = row
		}
	case 6:
		for n, row := range rows.subtitleFacts {
			owned[n] = row
		}
	case 7:
		for n, row := range rows.sidecars {
			owned[n] = row
		}
	case 8:
		for n, row := range rows.subtitles {
			owned[n] = row
		}
	case 9:
		for n, row := range rows.evidence {
			owned[n] = row
		}
	}
	for n, row := range owned {
		family, _, _, _, err := basicMediaValues(source, ordinals[n], row)
		if err != nil {
			return nil, err
		}
		if family != rows.family {
			return nil, ErrInvalid
		}
		owned[n] = cloneBasicMediaRow(row)
	}
	return owned, nil
}

// freezeBasicRetirePrefixRows retains only the selected typed rows and their
// captured native ordinals. rawChapterCount stays zero: inactivity is not a raw
// observation. Unselected slices remain nil and no synthetic offsets are
// materialized.
func freezeBasicRetirePrefixRows(source basicStreamFactStage, rows basicRowsRequest, ordinals []int64, expectedRows int64) (basicRowsRequest, []int64, error) {
	if _, err := basicRetirePrefixBytes(source, rows, ordinals, expectedRows); err != nil {
		return basicRowsRequest{}, nil, err
	}
	owned, err := basicRetirePrefixOwnedRows(source, rows, ordinals)
	if err != nil {
		return basicRowsRequest{}, nil, err
	}
	out := basicRowsRequest{family: rows.family}
	switch rows.family {
	case 2:
		out.streams = make([]basicStreamRow, len(owned))
		for n, row := range owned {
			out.streams[n] = row.(basicStreamRow)
		}
	case 3:
		out.breadth = make([]basicStreamBreadth, len(owned))
		for n, row := range owned {
			out.breadth[n] = row.(basicStreamBreadth)
		}
	case 4:
		out.chapterFacts = make([]basicChapterFact, len(owned))
		for n, row := range owned {
			out.chapterFacts[n] = row.(basicChapterFact)
		}
	case 5:
		out.chapters = make([]basicChapter, len(owned))
		for n, row := range owned {
			out.chapters[n] = row.(basicChapter)
		}
	case 6:
		out.subtitleFacts = make([]basicSubtitleFact, len(owned))
		for n, row := range owned {
			out.subtitleFacts[n] = row.(basicSubtitleFact)
		}
	case 7:
		out.sidecars = make([]basicSubtitleSidecar, len(owned))
		for n, row := range owned {
			out.sidecars[n] = row.(basicSubtitleSidecar)
		}
	case 8:
		out.subtitles = make([]basicSubtitle, len(owned))
		for n, row := range owned {
			out.subtitles[n] = row.(basicSubtitle)
		}
	case 9:
		out.evidence = make([]basicSubtitleEvidence, len(owned))
		for n, row := range owned {
			out.evidence[n] = row.(basicSubtitleEvidence)
		}
	}
	frozen := make([]int64, len(ordinals))
	copy(frozen, ordinals)
	return out, frozen, nil
}

// prepareBasicRetirePrefixFamily builds the private reclaim-only family. It is
// not an activation/new raw observation and grants no authority: the existing
// reclaimer still owns its live-run, receipt, admission and rollback fences.
func prepareBasicRetirePrefixFamily(source basicStreamFactStage, rows basicRowsRequest, ordinals []int64, expectedRows int64) (*basicMediaFamily, error) {
	if _, err := basicRetirePrefixBytes(source, rows, ordinals, expectedRows); err != nil {
		return nil, err
	}
	owned, err := basicRetirePrefixOwnedRows(source, rows, ordinals)
	if err != nil {
		return nil, err
	}
	return &basicMediaFamily{source: cloneBasicSource(source), family: rows.family, rawChapterCount: 0, rows: owned, valid: true}, nil
}
