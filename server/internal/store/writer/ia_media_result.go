package writer

import (
	"bytes"
	"math"
	"unicode/utf8"
)

// Immutable accepted facts only. Original owner, root evidence, authority,
// admission and COMMIT resolution remain the caller's responsibility.
type iaMediaResult struct {
	runID, sourceID, sourceIncarnation                                                string
	acceptedRevision, appliedMS                                                       int64
	objectID, objectRevision, cursorKind                                              string
	directoryPath, directoryIdentity, directoryRevision, expectedPayload, nextPayload []byte
	resultVersion, resultCount, memberBytes                                           int64
	members                                                                           []iaMediaMember
}
type iaMediaMember struct {
	ordinal                                                int64
	kind                                                   string
	libraryID, subjectID, subjectKind, subjectRevision     int64
	metadataIncarnation                                    [16]byte
	parentID, parentKind, ancestorID, ancestorKind         *int64
	assetID, assetInfoEpoch, partIndex, linkOriginRevision *int64
	linkOriginIncarnation                                  *[16]byte
	edition, linkEffect                                    *string
	cueOrdinal                                             *int64
	startSeconds, endSeconds                               *float64
	episodeBoundaryStatus, readingFormat                   *string
	readingPathHead, readingPathSequence                   *int64
	extraKind                                              *string
}
type iaEpisodeInventoryResult struct{ facts iaMediaResult }
type iaAudioInventoryResult struct{ facts iaMediaResult }
type iaReadingInventoryResult struct{ facts iaMediaResult }
type iaExtraInventoryResult struct{ facts iaMediaResult }
type iaMembershipResult struct{ facts iaMediaResult }

func iaMediaResultKindName(k CommandID) (string, error) {
	switch k {
	case IAAcceptEpisodeInventory:
		return "episode_inventory_accept", nil
	case IAAcceptAudioInventory:
		return "audio_inventory_accept", nil
	case IAAcceptReadingInventory:
		return "reading_inventory_accept", nil
	case IAAcceptExtraInventory:
		return "extra_inventory_accept", nil
	case IAAcceptMembership:
		return "membership_accept", nil
	}
	return "", invalid("ia_media_result_kind")
}

// The private field walk fixes v1 field order. Nullable values always carry a
// presence byte, including present zero and present empty TEXT.
func iaMediaFields(k CommandID, id CommandIdentity, r RequestDigest, v iaMediaResult, f func(any)) {
	name, _ := iaMediaResultKindName(k)
	f([]byte(canonicalResultCodec))
	f([]byte{0, 1})
	f(name)
	f("media_inventory")
	f("applied")
	f(id.Incarnation[:])
	f(int64(id.Sequence))
	f(id.OperationID[:])
	f(r[:])
	f(v.runID)
	f(v.sourceID)
	f(v.sourceIncarnation)
	f(v.acceptedRevision)
	f(v.appliedMS)
	f(v.objectID)
	f(v.objectRevision)
	f(v.cursorKind)
	for _, b := range [][]byte{v.directoryPath, v.directoryIdentity, v.directoryRevision, v.expectedPayload, v.nextPayload} {
		f(int64(len(b)))
		f(b)
	}
	f(v.resultVersion)
	f(v.resultCount)
	f(v.memberBytes)
	f(int64(len(v.members)))
	for _, m := range v.members {
		f(m.ordinal)
		f(m.kind)
		f(m.libraryID)
		f(m.subjectID)
		f(m.subjectKind)
		f(m.subjectRevision)
		f(m.metadataIncarnation[:])
		for _, p := range []*int64{m.parentID, m.parentKind, m.ancestorID, m.ancestorKind, m.assetID, m.assetInfoEpoch, m.partIndex, m.linkOriginRevision} {
			f(p)
		}
		f(m.linkOriginIncarnation)
		f(m.edition)
		f(m.linkEffect)
		f(m.cueOrdinal)
		f(m.startSeconds)
		f(m.endSeconds)
		f(m.episodeBoundaryStatus)
		f(m.readingFormat)
		f(m.readingPathHead)
		f(m.readingPathSequence)
		f(m.extraKind)
	}
}

func preflightIAMediaResult(k CommandID, id CommandIdentity, r RequestDigest, v iaMediaResult) error {
	if _, err := iaMediaResultKindName(k); err != nil {
		return err
	}
	if len(v.members) < 1 || len(v.members) > 4096 || v.resultCount != int64(len(v.members)) {
		return invalid("ia_media_result_count")
	}
	remaining := uint64(maxCanonicalResultBytes)
	tooBig := false
	iaMediaFields(k, id, r, v, func(x any) {
		var n uint64
		switch p := x.(type) {
		case string:
			n = 8 + uint64(len(p))
		case []byte:
			n = uint64(len(p))
		case int64:
			n = 8
		case *int64:
			n = 1
			if p != nil {
				n += 8
			}
		case *float64:
			n = 1
			if p != nil {
				n += 8
			}
		case *string:
			n = 1
			if p != nil {
				n += 8 + uint64(len(*p))
			}
		case *[16]byte:
			n = 1
			if p != nil {
				n += 16
			}
		}
		if n > remaining {
			tooBig = true
		} else {
			remaining -= n
		}
	})
	if tooBig {
		return invalid("ia_media_result_bytes")
	}
	// No text scan, copy or hash precedes the complete length bound.
	var debt int64
	for i, m := range v.members {
		if m.ordinal != int64(i) {
			return invalid("ia_media_result_ordinal")
		}
		n, err := iaMediaMemberRetainedBytes(k, v.runID, m)
		if err != nil {
			return err
		}
		if n > math.MaxInt64-debt {
			return invalid("ia_media_result_debt")
		}
		debt += n
	}
	if debt != v.memberBytes {
		return invalid("ia_media_result_debt")
	}
	if id.Sequence <= 0 || id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) || v.runID == "" || v.sourceID == "" || v.sourceIncarnation == "" || v.acceptedRevision <= 0 || v.appliedMS < 0 || v.resultVersion != 1 || v.objectID == "" || v.objectRevision == "" || len(v.directoryPath) == 0 || bytes.IndexByte(v.directoryPath, 0) >= 0 || len(v.directoryIdentity) == 0 || len(v.directoryRevision) == 0 || len(v.nextPayload) == 0 {
		return invalid("ia_media_result_header")
	}
	switch v.cursorKind {
	case "local_getdents64_cookie_v1", "darwin_directory_stream_v1", "windows_find_stream_v1", "directory_snapshot_keyset_v1":
	default:
		return invalid("ia_media_result_cursor")
	}
	valid := true
	iaMediaFields(k, id, r, v, func(x any) {
		switch p := x.(type) {
		case string:
			valid = valid && utf8.ValidString(p)
		case *string:
			if p != nil {
				valid = valid && utf8.ValidString(*p)
			}
		}
	})
	if !valid {
		return invalid("ia_media_result_text")
	}
	for _, m := range v.members {
		if !iaMediaMemberValid(k, m) {
			return invalid("ia_media_result_member")
		}
	}
	return nil
}

// Exact generated native scalar payload charge, independently of wire limits.
func iaMediaMemberRetainedBytes(k CommandID, runID string, m iaMediaMember) (int64, error) {
	name, err := iaMediaResultKindName(k)
	if err != nil {
		return 0, err
	}
	n := int64(80) // six mandatory INTEGERs and two mandatory 16-byte BLOBs
	add := func(v int) bool {
		if uint64(v) > uint64(math.MaxInt64-n) {
			return false
		}
		n += int64(v)
		return true
	}
	for _, s := range []string{runID, name, m.kind} {
		if !add(len(s)) {
			return 0, invalid("ia_media_retained_bytes")
		}
	}
	for _, p := range []*int64{m.parentID, m.parentKind, m.ancestorID, m.ancestorKind, m.assetID, m.assetInfoEpoch, m.partIndex, m.linkOriginRevision, m.cueOrdinal, m.readingPathHead, m.readingPathSequence} {
		if p != nil && !add(8) {
			return 0, invalid("ia_media_retained_bytes")
		}
	}
	for _, p := range []*float64{m.startSeconds, m.endSeconds} {
		if p != nil && !add(8) {
			return 0, invalid("ia_media_retained_bytes")
		}
	}
	for _, p := range []*string{m.edition, m.linkEffect, m.episodeBoundaryStatus, m.readingFormat, m.extraKind} {
		if p != nil && !add(len(*p)) {
			return 0, invalid("ia_media_retained_bytes")
		}
	}
	if m.linkOriginIncarnation != nil && !add(16) {
		return 0, invalid("ia_media_retained_bytes")
	}
	return n, nil
}

func iaMediaMemberValid(k CommandID, m iaMediaMember) bool {
	in := func(n int64, vs ...int64) bool {
		for _, v := range vs {
			if n == v {
				return true
			}
		}
		return false
	}
	textIn := func(p *string, vs ...string) bool {
		if p == nil {
			return false
		}
		for _, v := range vs {
			if *p == v {
				return true
			}
		}
		return false
	}
	if m.libraryID <= 0 || m.subjectID <= 0 || m.subjectRevision <= 0 || !in(m.subjectKind, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 20, 21) {
		return false
	}
	allowed := false
	switch k {
	case IAAcceptEpisodeInventory:
		allowed = m.kind == "identity" && in(m.subjectKind, 2, 3, 4) || m.kind == "membership" && m.subjectKind == 4
	case IAAcceptAudioInventory:
		allowed = m.kind == "identity" && in(m.subjectKind, 5, 6, 7, 8, 9, 13) || m.kind == "membership" && in(m.subjectKind, 7, 9) || m.kind == "artist" && m.subjectKind == 7
	case IAAcceptReadingInventory:
		allowed = m.kind == "reading" && in(m.subjectKind, 20, 21)
	case IAAcceptExtraInventory:
		allowed = m.kind == "extra" && m.subjectKind == 11
	case IAAcceptMembership:
		allowed = m.kind == "identity" || m.kind == "membership" && in(m.subjectKind, 1, 4, 7, 9, 11, 12, 20, 21)
	}
	if !allowed || (m.parentID == nil) != (m.parentKind == nil) || (m.ancestorID == nil) != (m.ancestorKind == nil) {
		return false
	}
	if m.parentID != nil {
		if *m.parentID <= 0 || *m.parentID == m.subjectID {
			return false
		}
		p := *m.parentKind
		s := m.subjectKind
		if !(m.kind == "identity" && ((s == 3 && p == 2) || (s == 4 && p == 3) || (s == 6 && p == 5) || (s == 7 && p == 6) || (s == 8 && p == 13) || (s == 9 && p == 8)) || m.kind == "artist" && p == 5 || m.kind == "extra" && in(p, 1, 2, 3, 4)) {
			return false
		}
	}
	if m.kind == "artist" && m.parentID == nil {
		return false
	}
	if m.ancestorID != nil && (*m.ancestorID <= 0 || *m.ancestorID == m.subjectID || m.parentID != nil && *m.ancestorID == *m.parentID || m.kind != "identity" || m.subjectKind != 4 || *m.ancestorKind != 2) {
		return false
	}
	asset := m.kind == "membership" || m.kind == "reading" || m.kind == "extra"
	for _, present := range []bool{m.assetID != nil, m.assetInfoEpoch != nil, m.partIndex != nil, m.linkOriginRevision != nil, m.linkOriginIncarnation != nil, m.edition != nil, m.linkEffect != nil, m.startSeconds != nil} {
		if present != asset {
			return false
		}
	}
	if asset && (*m.assetID <= 0 || *m.assetInfoEpoch <= 0 || *m.partIndex < 0 || *m.linkOriginRevision <= 0 || !textIn(m.linkEffect, "present", "removed") || *m.linkEffect == "removed" && k != IAAcceptMembership || math.IsNaN(*m.startSeconds) || math.IsInf(*m.startSeconds, 0) || *m.startSeconds < 0) {
		return false
	}
	if m.endSeconds != nil && (!asset || math.IsNaN(*m.endSeconds) || math.IsInf(*m.endSeconds, 0) || *m.endSeconds <= *m.startSeconds) {
		return false
	}
	if m.cueOrdinal != nil && (!asset || !in(m.subjectKind, 7, 9) || *m.cueOrdinal <= 0) {
		return false
	}
	episode := m.kind == "membership" && m.subjectKind == 4
	if episode != (m.episodeBoundaryStatus != nil) || episode && !textIn(m.episodeBoundaryStatus, "whole_source", "unknown_multi_episode") {
		return false
	}
	reading := m.kind == "reading"
	if reading != (m.readingFormat != nil) || reading != (m.readingPathHead != nil) || reading && (!textIn(m.readingFormat, "epub", "pdf", "cbz", "cb7", "mobi", "azw3", "cbr") || !in(*m.readingPathHead, 0, 1)) {
		return false
	}
	head := m.readingPathHead != nil && *m.readingPathHead == 1
	if head != (m.readingPathSequence != nil) || head && *m.readingPathSequence <= 0 {
		return false
	}
	extra := m.kind == "extra"
	return extra == (m.extraKind != nil) && (!extra || textIn(m.extraKind, "trailer", "featurette", "deleted_scene", "behind_the_scenes", "interview", "scene", "short", "other"))
}

// Caller must preflight the complete cohort before invoking this writer.
func writeIAMediaResult(e *requestEncoder, k CommandID, id CommandIdentity, r RequestDigest, v iaMediaResult) error {
	if _, err := iaMediaResultKindName(k); err != nil {
		return err
	}
	iaMediaFields(k, id, r, v, func(x any) {
		switch p := x.(type) {
		case string:
			e.text(p)
		case []byte:
			e.bytes(p)
		case int64:
			e.number(uint64(p))
		case *int64:
			e.flag(p != nil)
			if p != nil {
				e.number(uint64(*p))
			}
		case *float64:
			e.flag(p != nil)
			if p != nil {
				e.number(math.Float64bits(*p))
			}
		case *string:
			e.flag(p != nil)
			if p != nil {
				e.text(*p)
			}
		case *[16]byte:
			e.flag(p != nil)
			if p != nil {
				e.bytes(p[:])
			}
		}
	})
	return e.err
}
