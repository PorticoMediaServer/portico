package writer

// Values-only closed union. The parent request owns the frozen source and its
// original binding; no derived interfaces or runtime authority are retained.
type basicRowsRequest struct {
	family          int64
	rawChapterCount int64
	streams         []basicStreamRow
	breadth         []basicStreamBreadth
	chapterFacts    []basicChapterFact
	chapters        []basicChapter
	subtitleFacts   []basicSubtitleFact
	sidecars        []basicSubtitleSidecar
	subtitles       []basicSubtitle
	evidence        []basicSubtitleEvidence
}

// basicRowsRequestBytes admits the complete raw family before row/interface
// slices, validation maps, clones or hashing. This is source text plus the
// existing base-row/text charge, not a physical-memory grant or peak estimate.
func basicRowsRequestBytes(source basicStreamFactStage, in basicRowsRequest) (uint64, error) {
	if in.family < 2 || in.family > 9 || in.rawChapterCount < 0 {
		return 0, ErrInvalid
	}
	counts := [...]int{len(in.streams), len(in.breadth), len(in.chapterFacts), len(in.chapters), len(in.subtitleFacts), len(in.sidecars), len(in.subtitles), len(in.evidence)}
	present := [...]bool{in.streams != nil, in.breadth != nil, in.chapterFacts != nil, in.chapters != nil, in.subtitleFacts != nil, in.sidecars != nil, in.subtitles != nil, in.evidence != nil}
	for n, populated := range present {
		if int64(n)+2 != in.family && populated {
			return 0, ErrInvalid
		}
	}
	count := counts[in.family-2]
	if int64(count) > basicMediaFamilyLimit(in.family) || in.rawChapterCount > MaxRawChapters {
		return 0, ErrCap
	}
	if (in.family == 5 && int64(count) > in.rawChapterCount) || ((in.family == 4 || in.family == 6 || in.family == 9) && count != 1) {
		return 0, ErrInvalid
	}
	remaining := uint64(maxCanonicalRequestBytes)
	for _, text := range [...]string{source.assetToken, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.fingerprint} {
		if uint64(len(text)) > remaining {
			return 0, ErrCap
		}
		remaining -= uint64(len(text))
	}
	for n := 0; n < count; n++ {
		var texts [9]string
		var base int64
		var err error
		// Concrete values reach the existing text helper without an escaping
		// interface carrier. Policy/UTF-8 checks remain in the family helper.
		switch in.family {
		case 2:
			texts, _, _, base, err = basicMediaTextFields(source, in.streams[n])
		case 3:
			texts, _, _, base, err = basicMediaTextFields(source, in.breadth[n])
		case 4:
			texts, _, _, base, err = basicMediaTextFields(source, in.chapterFacts[n])
		case 5:
			texts, _, _, base, err = basicMediaTextFields(source, in.chapters[n])
		case 6:
			texts, _, _, base, err = basicMediaTextFields(source, in.subtitleFacts[n])
		case 7:
			texts, _, _, base, err = basicMediaTextFields(source, in.sidecars[n])
		case 8:
			texts, _, _, base, err = basicMediaTextFields(source, in.subtitles[n])
		case 9:
			texts, _, _, base, err = basicMediaTextFields(source, in.evidence[n])
		}
		if err != nil {
			return 0, err
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

// prepareBasicRowsRequest builds the bounded derived effect input. The parent
// calls this only after covering the prospective whole peak with its memory
// check. Reuse the existing complete-family validation and ownership semantics.
func prepareBasicRowsRequest(source basicStreamFactStage, in basicRowsRequest) (*basicMediaFamily, error) {
	if _, err := basicRowsRequestBytes(source, in); err != nil {
		return nil, err
	}
	count := len(in.streams) + len(in.breadth) + len(in.chapterFacts) + len(in.chapters) + len(in.subtitleFacts) + len(in.sidecars) + len(in.subtitles) + len(in.evidence)
	rows := make([]basicMediaRow, count)
	for n := range rows {
		switch in.family {
		case 2:
			rows[n] = in.streams[n]
		case 3:
			rows[n] = in.breadth[n]
		case 4:
			rows[n] = in.chapterFacts[n]
		case 5:
			rows[n] = in.chapters[n]
		case 6:
			rows[n] = in.subtitleFacts[n]
		case 7:
			rows[n] = in.sidecars[n]
		case 8:
			rows[n] = in.subtitles[n]
		case 9:
			rows[n] = in.evidence[n]
		}
	}
	return prepareBasicMediaFamily(source, in.family, in.rawChapterCount, rows)
}

// freezeBasicRowsRequest retains only the typed rows cloned by the existing
// family helper. A selected nil/empty slice keeps its exact request identity.
func freezeBasicRowsRequest(source basicStreamFactStage, in basicRowsRequest) (basicRowsRequest, error) {
	family, err := prepareBasicRowsRequest(source, in)
	if err != nil {
		return basicRowsRequest{}, err
	}
	out := basicRowsRequest{family: in.family, rawChapterCount: in.rawChapterCount}
	switch in.family {
	case 2:
		if in.streams != nil {
			out.streams = make([]basicStreamRow, len(family.rows))
		}
		for n, row := range family.rows {
			out.streams[n] = row.(basicStreamRow)
		}
	case 3:
		if in.breadth != nil {
			out.breadth = make([]basicStreamBreadth, len(family.rows))
		}
		for n, row := range family.rows {
			out.breadth[n] = row.(basicStreamBreadth)
		}
	case 4:
		if in.chapterFacts != nil {
			out.chapterFacts = make([]basicChapterFact, len(family.rows))
		}
		for n, row := range family.rows {
			out.chapterFacts[n] = row.(basicChapterFact)
		}
	case 5:
		if in.chapters != nil {
			out.chapters = make([]basicChapter, len(family.rows))
		}
		for n, row := range family.rows {
			out.chapters[n] = row.(basicChapter)
		}
	case 6:
		if in.subtitleFacts != nil {
			out.subtitleFacts = make([]basicSubtitleFact, len(family.rows))
		}
		for n, row := range family.rows {
			out.subtitleFacts[n] = row.(basicSubtitleFact)
		}
	case 7:
		if in.sidecars != nil {
			out.sidecars = make([]basicSubtitleSidecar, len(family.rows))
		}
		for n, row := range family.rows {
			out.sidecars[n] = row.(basicSubtitleSidecar)
		}
	case 8:
		if in.subtitles != nil {
			out.subtitles = make([]basicSubtitle, len(family.rows))
		}
		for n, row := range family.rows {
			out.subtitles[n] = row.(basicSubtitle)
		}
	case 9:
		if in.evidence != nil {
			out.evidence = make([]basicSubtitleEvidence, len(family.rows))
		}
		for n, row := range family.rows {
			out.evidence[n] = row.(basicSubtitleEvidence)
		}
	}
	return out, nil
}
