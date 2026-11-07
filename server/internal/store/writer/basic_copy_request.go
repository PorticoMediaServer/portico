package writer

import (
	"reflect"
	"strings"
)

type basicCopyOperation uint8

const (
	basicCopyPrefence basicCopyOperation = iota + 1
	basicCopyStreamFact
	basicCopyMediaRow
	basicCopyChapterFact
	basicCopySubtitleFact
	basicCopyEvidence
	basicCopyEmptyFamily
	basicCopyCleanupStreams
	basicCopyCleanupMedia
	basicCopySubtitleRow
	basicCopyCleanupSubtitles
)

// Values only: no authority, identity, or derived interface rows are retained.
type basicCopyRequest struct {
	operation                                   basicCopyOperation
	source                                      basicStreamFactStage
	position                                    basicCopyPosition
	shapeID, fileSizeRevision, invalidation     int64
	expectedRows, startOrdinal, rawChapterCount int64
	rows                                        basicRowsRequest // family + selected chunk; raw count lives above
	hadBefore                                   bool
	beforeStreamFact                            basicCanonicalStreamFact
	beforeStreamRow                             basicStreamRow
	beforeBreadth                               basicStreamBreadth
	beforeChapter                               basicChapter
	beforeChapterFact                           basicCanonicalChapterFact
	beforeSubtitleFact                          basicCanonicalSubtitleFact
	beforeSidecar                               basicSubtitleSidecar
	beforeSubtitle                              basicSubtitle
	beforeCleanupSubtitles                      []basicSubtitle
	beforeEvidence                              basicSubtitleEvidence
	prefence                                    basicOrdinaryInputAccept
}

// Raw text plus fixed scalar/length payload bytes; not a grant, quota or fit
// claim. Admit the whole native payload before clones, maps or JSON validation.
func basicCopyRequestBytes(in basicCopyRequest) (uint64, error) {
	if len(in.beforeCleanupSubtitles) > 8 {
		return 0, ErrCap
	}
	if in.expectedRows < 0 || in.startOrdinal < 0 || in.rawChapterCount < 0 {
		return 0, ErrInvalid
	}
	if in.expectedRows > basicMediaFamilyLimit(in.rows.family) || basicRowsCount(in.rows) > 8 {
		return 0, ErrCap
	}

	for _, n := range [...]int{len(in.rows.streams), len(in.rows.breadth), len(in.rows.chapterFacts), len(in.rows.chapters), len(in.rows.subtitleFacts), len(in.rows.sidecars), len(in.rows.subtitles), len(in.rows.evidence)} {
		if n > 8 {
			return 0, ErrCap
		}
	}
	s := in.prefence.stream.rawShape
	for _, c := range [...][2]int{{len(s.CUEOrdinals), MaxCUEChildren}, {len(s.EpisodeIDs), MaxEpisodes}, {len(s.VersionAssetIDs), MaxVersions}, {len(s.Streams), 3 * MaxStreamsPerClass}, {len(s.AuthoredParentIDs), MaxAuthoredParents}} {
		if c[0] > c[1] {
			return 0, ErrCap
		}
	}
	if in.rawChapterCount > MaxRawChapters || in.rows.rawChapterCount > MaxRawChapters || s.RawChapterCount > MaxRawChapters {
		return 0, ErrCap
	}
	remaining := uint64(maxCanonicalRequestBytes)
	if err := basicCopyPayloadBytes(reflect.ValueOf(&in).Elem(), &remaining); err != nil {
		return 0, err
	}
	return uint64(maxCanonicalRequestBytes) - remaining, nil
}

func basicCopyPayloadBytes(v reflect.Value, remaining *uint64) error {
	var size uint64
	switch v.Kind() {
	case reflect.Struct:
		for n := 0; n < v.NumField(); n++ {
			if err := basicCopyPayloadBytes(v.Field(n), remaining); err != nil {
				return err
			}
		}
		return nil
	case reflect.Array, reflect.Slice:
		if v.Kind() == reflect.Slice {
			if *remaining < 8 {
				return ErrCap
			}
			*remaining -= 8
		}
		for n := 0; n < v.Len(); n++ {
			if err := basicCopyPayloadBytes(v.Index(n), remaining); err != nil {
				return err
			}
		}
		return nil
	case reflect.String:
		size = uint64(v.Len())
	case reflect.Bool, reflect.Uint8:
		size = 1
	case reflect.Int, reflect.Int64, reflect.Float64:
		size = 8
	default:
		return ErrInvalid
	}
	if size > *remaining {
		return ErrCap
	}
	*remaining -= size
	return nil
}

func validateBasicCopyRequest(in basicCopyRequest) error {
	rest := in
	rest.operation = 0
	rest.source = basicStreamFactStage{}
	if in.operation == basicCopyPrefence {
		rest.prefence = basicOrdinaryInputAccept{}
		if !reflect.ValueOf(rest).IsZero() {
			return ErrInvalid
		}
		return nil
	}
	rest.position = basicCopyPosition{}
	rest.shapeID = 0
	rest.fileSizeRevision = 0
	rest.invalidation = 0
	rest.expectedRows = 0
	rest.startOrdinal = 0
	rest.rawChapterCount = 0
	rest.rows = basicRowsRequest{}
	rest.hadBefore = false
	if in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 || in.rawChapterCount < 0 || in.rows.rawChapterCount != 0 {
		return ErrInvalid
	}
	p := in.position
	f := in.rows.family
	subtitleCleanup := in.operation == basicCopyCleanupSubtitles
	cleanup := in.operation == basicCopyCleanupStreams || in.operation == basicCopyCleanupMedia || subtitleCleanup
	if p.changeID != in.source.changeID || p.changeRevision <= 0 || p.phase != 2 || p.afterEntity <= 0 || p.family != f || (!subtitleCleanup && p.key != "") || p.key2 != "" || p.key3 != "" {
		return ErrInvalid
	}
	if cleanup {
		if p.copyPhase != 1 || p.id < 0 || p.aux < 0 || p.aux > 1 || (p.aux == 0 && p.id != 0) || (subtitleCleanup && (p.id != 0 || (p.aux == 0 && p.key != ""))) || in.startOrdinal != 0 {
			return ErrInvalid
		}
	} else if p.copyPhase != 0 || p.aux != 0 || p.id != in.startOrdinal {
		return ErrInvalid
	}
	switch in.operation {
	case basicCopyStreamFact:
		if f != 0 || in.expectedRows != 0 || in.startOrdinal != 0 || in.rawChapterCount != 0 || !reflect.ValueOf(in.rows).IsZero() {
			return ErrInvalid
		}
		rest.beforeStreamFact = basicCanonicalStreamFact{}
	case basicCopyMediaRow:
		switch f {
		case 2:
			rest.beforeStreamRow = basicStreamRow{}
		case 3:
			rest.beforeBreadth = basicStreamBreadth{}
		case 5:
			rest.beforeChapter = basicChapter{}
		case 7:
			rest.beforeSidecar = basicSubtitleSidecar{}
		default:
			return ErrInvalid
		}
	case basicCopyChapterFact:
		if f != 4 {
			return ErrInvalid
		}
		rest.beforeChapterFact = basicCanonicalChapterFact{}
	case basicCopySubtitleFact:
		if f != 6 {
			return ErrInvalid
		}
		rest.beforeSubtitleFact = basicCanonicalSubtitleFact{}
	case basicCopyEvidence:
		if f != 9 {
			return ErrInvalid
		}
		rest.beforeEvidence = basicSubtitleEvidence{}
	case basicCopySubtitleRow:
		if f != 8 {
			return ErrInvalid
		}
		rest.beforeSubtitle = basicSubtitle{}
	case basicCopyEmptyFamily:
		if f != 2 && f != 3 && f != 5 && f != 7 && f != 8 {
			return ErrInvalid
		}
		if in.expectedRows != 0 || in.startOrdinal != 0 || in.hadBefore {
			return ErrInvalid
		}
	case basicCopyCleanupSubtitles:
		if f != 8 || in.hadBefore {
			return ErrInvalid
		}
		rest.beforeCleanupSubtitles = nil
		if err := preflightBasicSubtitleCleanup(basicSubtitleCleanupEffects{basicStreamCopyFence: basicStreamCopyFence{family: &basicMediaFamily{valid: true, family: 8, source: in.source}, position: p, shapeID: in.shapeID, fileSizeRevision: in.fileSizeRevision, invalidation: in.invalidation}, before: in.beforeCleanupSubtitles}); err != nil {
			return err
		}
	case basicCopyCleanupStreams:
		if f != 2 || in.hadBefore {
			return ErrInvalid
		}
	case basicCopyCleanupMedia:
		if (f != 3 && f != 5) || in.hadBefore {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if !reflect.ValueOf(rest).IsZero() {
		return ErrInvalid
	}
	if !in.hadBefore {
		if in.beforeStreamFact != (basicCanonicalStreamFact{}) || in.beforeStreamRow != (basicStreamRow{}) || in.beforeBreadth != (basicStreamBreadth{}) || in.beforeChapter != (basicChapter{}) || in.beforeChapterFact != (basicCanonicalChapterFact{}) || in.beforeSubtitleFact != (basicCanonicalSubtitleFact{}) || in.beforeSidecar != (basicSubtitleSidecar{}) || in.beforeSubtitle != (basicSubtitle{}) || in.beforeEvidence != (basicSubtitleEvidence{}) {
			return ErrInvalid
		}
	}
	if in.operation == basicCopyStreamFact {
		return nil
	}
	if in.expectedRows > basicMediaFamilyLimit(f) {
		return ErrCap
	}
	if cleanup || in.operation == basicCopyEmptyFamily {
		rows := in.rows
		rows.family = 0
		if !reflect.ValueOf(rows).IsZero() {
			return ErrInvalid
		}
	} else {
		if basicRowsCount(in.rows) != 1 {
			return ErrInvalid
		}
		rows := in.rows
		rows.rawChapterCount = in.rawChapterCount
		if _, err := basicRowsRequestBytes(in.source, rows); err != nil {
			return err
		}
	}
	if (f == 4 || f == 6 || f == 9) && (in.expectedRows != 1 || in.startOrdinal != 0) {
		return ErrInvalid
	}
	return nil
}

func freezeBasicCopyRequest(in basicCopyRequest) (basicCopyRequest, error) {
	if _, err := basicCopyRequestBytes(in); err != nil {
		return basicCopyRequest{}, err
	}
	if err := validateBasicCopyRequest(in); err != nil {
		return basicCopyRequest{}, err
	}
	if in.operation == basicCopyPrefence {
		p := &in.prefence
		if p.stream.source != in.source || p.snapshot.source != in.source || p.snapshot.changeRevision != p.stream.changeRevision || p.snapshot.before != p.snapshot.after || p.stream.changeRevision <= 0 || p.stream.shapeID <= 0 {
			return basicCopyRequest{}, ErrInvalid
		}
		if err := validateBasicOrdinaryCounts(in.source.libraryID, p.stream.rawShape, p.counts); err != nil {
			return basicCopyRequest{}, err
		}
		if err := validateBasicProbeEffects(p.snapshot); err != nil {
			return basicCopyRequest{}, err
		}
		p.stream.source = cloneBasicSource(p.stream.source)
		p.snapshot.source = cloneBasicSource(p.snapshot.source)
		p.snapshot.assetPath = strings.Clone(p.snapshot.assetPath)
		p.snapshot.before = cloneBasicProbeScalars(p.snapshot.before)
		p.snapshot.after = cloneBasicProbeScalars(p.snapshot.after)
		s := &p.stream.rawShape
		s.CUEOrdinals = cloneBasicRequestSlice(s.CUEOrdinals)
		s.EpisodeIDs = cloneBasicRequestSlice(s.EpisodeIDs)
		s.VersionAssetIDs = cloneBasicRequestSlice(s.VersionAssetIDs)
		s.Streams = cloneBasicRequestSlice(s.Streams)
		s.AuthoredParentIDs = cloneBasicRequestSlice(s.AuthoredParentIDs)
	} else {
		family := in.rows.family
		if in.operation == basicCopyStreamFact {
			family = 2
		}
		chunk := make([]basicMediaRow, 0, 1)
		switch in.rows.family {
		case 2:
			for _, r := range in.rows.streams {
				chunk = append(chunk, r)
			}
		case 3:
			for _, r := range in.rows.breadth {
				chunk = append(chunk, r)
			}
		case 4:
			for _, r := range in.rows.chapterFacts {
				chunk = append(chunk, r)
			}
		case 5:
			for _, r := range in.rows.chapters {
				chunk = append(chunk, r)
			}
		case 6:
			for _, r := range in.rows.subtitleFacts {
				chunk = append(chunk, r)
			}
		case 7:
			for _, r := range in.rows.sidecars {
				chunk = append(chunk, r)
			}
		case 8:
			for _, r := range in.rows.subtitles {
				chunk = append(chunk, r)
			}
		case 9:
			for _, r := range in.rows.evidence {
				chunk = append(chunk, r)
			}
		}
		if _, err := prepareBasicCopyFamily(in.source, family, in.expectedRows, in.startOrdinal, in.rawChapterCount, chunk); err != nil {
			return basicCopyRequest{}, err
		}
		switch in.rows.family {
		case 2:
			in.rows.streams = cloneBasicRequestSlice(in.rows.streams)
			for n, r := range in.rows.streams {
				in.rows.streams[n] = cloneBasicMediaRow(r).(basicStreamRow)
			}
		case 3:
			in.rows.breadth = cloneBasicRequestSlice(in.rows.breadth)
			for n, r := range in.rows.breadth {
				in.rows.breadth[n] = cloneBasicMediaRow(r).(basicStreamBreadth)
			}
		case 4:
			in.rows.chapterFacts = cloneBasicRequestSlice(in.rows.chapterFacts)
			for n, r := range in.rows.chapterFacts {
				in.rows.chapterFacts[n] = cloneBasicMediaRow(r).(basicChapterFact)
			}
		case 5:
			in.rows.chapters = cloneBasicRequestSlice(in.rows.chapters)
			for n, r := range in.rows.chapters {
				in.rows.chapters[n] = cloneBasicMediaRow(r).(basicChapter)
			}
		case 6:
			in.rows.subtitleFacts = cloneBasicRequestSlice(in.rows.subtitleFacts)
			for n, r := range in.rows.subtitleFacts {
				in.rows.subtitleFacts[n] = cloneBasicMediaRow(r).(basicSubtitleFact)
			}
		case 7:
			in.rows.sidecars = cloneBasicRequestSlice(in.rows.sidecars)
			for n, r := range in.rows.sidecars {
				in.rows.sidecars[n] = cloneBasicMediaRow(r).(basicSubtitleSidecar)
			}
		case 8:
			in.rows.subtitles = cloneBasicRequestSlice(in.rows.subtitles)
			for n, r := range in.rows.subtitles {
				in.rows.subtitles[n] = cloneBasicMediaRow(r).(basicSubtitle)
			}
		case 9:
			in.rows.evidence = cloneBasicRequestSlice(in.rows.evidence)
			for n, r := range in.rows.evidence {
				in.rows.evidence[n] = cloneBasicMediaRow(r).(basicSubtitleEvidence)
			}
		}
	}
	in.beforeCleanupSubtitles = cloneBasicRequestSlice(in.beforeCleanupSubtitles)
	for n, r := range in.beforeCleanupSubtitles {
		in.beforeCleanupSubtitles[n] = cloneBasicMediaRow(r).(basicSubtitle)
	}
	in.beforeStreamRow = cloneBasicMediaRow(in.beforeStreamRow).(basicStreamRow)
	in.beforeBreadth = cloneBasicMediaRow(in.beforeBreadth).(basicStreamBreadth)
	in.beforeChapter = cloneBasicMediaRow(in.beforeChapter).(basicChapter)
	in.beforeSidecar = cloneBasicMediaRow(in.beforeSidecar).(basicSubtitleSidecar)
	in.beforeSubtitle = cloneBasicMediaRow(in.beforeSubtitle).(basicSubtitle)
	in.beforeEvidence = cloneBasicMediaRow(in.beforeEvidence).(basicSubtitleEvidence)
	in.beforeStreamFact.fingerprint = strings.Clone(in.beforeStreamFact.fingerprint)
	in.beforeChapterFact.status = strings.Clone(in.beforeChapterFact.status)
	in.beforeChapterFact.fingerprint = strings.Clone(in.beforeChapterFact.fingerprint)
	in.beforeSubtitleFact.status = strings.Clone(in.beforeSubtitleFact.status)
	in.beforeSubtitleFact.fingerprint = strings.Clone(in.beforeSubtitleFact.fingerprint)
	in.source = cloneBasicSource(in.source)
	in.position.key = strings.Clone(in.position.key)
	in.position.key2 = strings.Clone(in.position.key2)
	in.position.key3 = strings.Clone(in.position.key3)
	return in, nil
}
