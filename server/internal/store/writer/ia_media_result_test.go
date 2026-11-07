package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func iaMediaPtr[T any](v T) *T { return &v }
func iaMediaFixture(k CommandID) (CommandIdentity, RequestDigest, iaMediaResult) {
	id := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 3}
	v := iaMediaResult{runID: "run", sourceID: "source", sourceIncarnation: "inc", acceptedRevision: 4, appliedMS: 0, objectID: "obj\x00", objectRevision: "rev\x00", cursorKind: "directory_snapshot_keyset_v1", directoryPath: []byte("/媒体"), directoryIdentity: []byte{0, 1}, directoryRevision: []byte{0, 2}, expectedPayload: []byte{}, nextPayload: []byte{0, 3}, resultVersion: 1, resultCount: 1}
	m := iaMediaMember{kind: "membership", libraryID: 1, subjectID: 2, subjectKind: 7, subjectRevision: 3, metadataIncarnation: [16]byte{4}, assetID: iaMediaPtr(int64(5)), assetInfoEpoch: iaMediaPtr(int64(6)), partIndex: iaMediaPtr(int64(0)), linkOriginRevision: iaMediaPtr(int64(7)), linkOriginIncarnation: &[16]byte{8}, edition: iaMediaPtr("版\x00"), linkEffect: iaMediaPtr("present"), startSeconds: iaMediaPtr(0.0)}
	switch k {
	case IAAcceptEpisodeInventory:
		m.subjectKind = 4
		m.episodeBoundaryStatus = iaMediaPtr("whole_source")
	case IAAcceptReadingInventory:
		m.kind = "reading"
		m.subjectKind = 21
		m.readingFormat = iaMediaPtr("cbr")
		m.readingPathHead = iaMediaPtr(int64(1))
		m.readingPathSequence = iaMediaPtr(int64(9))
	case IAAcceptExtraInventory:
		m.kind = "extra"
		m.subjectKind = 11
		m.extraKind = iaMediaPtr("other")
	case IAAcceptMembership:
		m.linkEffect = iaMediaPtr("removed")
	}
	v.members = []iaMediaMember{m}
	v.memberBytes, _ = iaMediaMemberRetainedBytes(k, v.runID, m)
	return id, RequestDigest{9}, v
}

// Independent reference: explicit wire order and standard binary primitives;
// neither the production field walk nor the production encoder is reused.
func iaMediaReference(name string, id CommandIdentity, r RequestDigest, v iaMediaResult) []byte {
	var b bytes.Buffer
	raw := func(p []byte) { b.Write(p) }
	num := func(n int64) { binary.Write(&b, binary.BigEndian, uint64(n)) }
	text := func(s string) { num(int64(len(s))); b.WriteString(s) }
	flag := func(ok bool) {
		if ok {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
	}
	ni := func(p *int64) {
		flag(p != nil)
		if p != nil {
			num(*p)
		}
	}
	ns := func(p *string) {
		flag(p != nil)
		if p != nil {
			text(*p)
		}
	}
	nf := func(p *float64) {
		flag(p != nil)
		if p != nil {
			binary.Write(&b, binary.BigEndian, math.Float64bits(*p))
		}
	}
	raw([]byte("portico-writer-result-sha256-v1\x00"))
	raw([]byte{0, 1})
	text(name)
	text("media_inventory")
	text("applied")
	raw(id.Incarnation[:])
	num(int64(id.Sequence))
	raw(id.OperationID[:])
	raw(r[:])
	text(v.runID)
	text(v.sourceID)
	text(v.sourceIncarnation)
	num(v.acceptedRevision)
	num(v.appliedMS)
	text(v.objectID)
	text(v.objectRevision)
	text(v.cursorKind)
	for _, p := range [][]byte{v.directoryPath, v.directoryIdentity, v.directoryRevision, v.expectedPayload, v.nextPayload} {
		num(int64(len(p)))
		raw(p)
	}
	num(v.resultVersion)
	num(v.resultCount)
	num(v.memberBytes)
	num(int64(len(v.members)))
	for _, m := range v.members {
		num(m.ordinal)
		text(m.kind)
		num(m.libraryID)
		num(m.subjectID)
		num(m.subjectKind)
		num(m.subjectRevision)
		raw(m.metadataIncarnation[:])
		ni(m.parentID)
		ni(m.parentKind)
		ni(m.ancestorID)
		ni(m.ancestorKind)
		ni(m.assetID)
		ni(m.assetInfoEpoch)
		ni(m.partIndex)
		ni(m.linkOriginRevision)
		flag(m.linkOriginIncarnation != nil)
		if m.linkOriginIncarnation != nil {
			raw(m.linkOriginIncarnation[:])
		}
		ns(m.edition)
		ns(m.linkEffect)
		ni(m.cueOrdinal)
		nf(m.startSeconds)
		nf(m.endSeconds)
		ns(m.episodeBoundaryStatus)
		ns(m.readingFormat)
		ni(m.readingPathHead)
		ni(m.readingPathSequence)
		ns(m.extraKind)
	}
	return b.Bytes()
}
func iaMediaHash(t *testing.T, k CommandID, id CommandIdentity, r RequestDigest, v iaMediaResult) [32]byte {
	t.Helper()
	if err := preflightIAMediaResult(k, id, r, v); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	e := requestEncoder{h: h}
	if err := writeIAMediaResult(&e, k, id, r, v); err != nil {
		t.Fatal(err)
	}
	var d [32]byte
	copy(d[:], h.Sum(nil))
	return d
}
func TestIAMediaResultReference(t *testing.T) {
	names := []string{"episode_inventory_accept", "audio_inventory_accept", "reading_inventory_accept", "extra_inventory_accept", "membership_accept"}
	for i, name := range names {
		k := CommandID(110 + i)
		id, r, v := iaMediaFixture(k)
		want := sha256.Sum256(iaMediaReference(name, id, r, v))
		if got := iaMediaHash(t, k, id, r, v); got != want {
			t.Fatalf("%s wire mismatch", name)
		}
	}
	// Same valid facts across two purposes must bind the purpose.
	id, r, v := iaMediaFixture(IAAcceptAudioInventory)
	a := iaMediaHash(t, IAAcceptAudioInventory, id, r, v)
	v.memberBytes, _ = iaMediaMemberRetainedBytes(IAAcceptMembership, v.runID, v.members[0])
	if a == iaMediaHash(t, IAAcceptMembership, id, r, v) {
		t.Fatal("purpose unbound")
	}
}
func TestIAMediaResultMutationBinding(t *testing.T) {
	changes := []func(*iaMediaResult){func(v *iaMediaResult) { v.sourceIncarnation += "x" }, func(v *iaMediaResult) { v.objectRevision += "x" }, func(v *iaMediaResult) { v.nextPayload = []byte{0, 4} }, func(v *iaMediaResult) { v.members[0].subjectRevision++ }, func(v *iaMediaResult) { v.members[0].metadataIncarnation[0]++ }, func(v *iaMediaResult) { v.members[0].edition = iaMediaPtr("") }, func(v *iaMediaResult) { v.members[0].endSeconds = iaMediaPtr(1.0) }, func(v *iaMediaResult) { v.members[0].cueOrdinal = iaMediaPtr(int64(1)) }}
	id, r, v := iaMediaFixture(IAAcceptAudioInventory)
	base := iaMediaHash(t, IAAcceptAudioInventory, id, r, v)
	for i, change := range changes {
		_, _, v = iaMediaFixture(IAAcceptAudioInventory)
		change(&v)
		v.memberBytes, _ = iaMediaMemberRetainedBytes(IAAcceptAudioInventory, v.runID, v.members[0])
		if base == iaMediaHash(t, IAAcceptAudioInventory, id, r, v) {
			t.Fatalf("mutation %d unbound", i)
		}
	}
}
func TestIAMediaResultRefusal(t *testing.T) {
	changes := []func(*iaMediaResult){func(v *iaMediaResult) { v.resultCount++ }, func(v *iaMediaResult) { v.memberBytes++ }, func(v *iaMediaResult) { v.members[0].ordinal = 1 }, func(v *iaMediaResult) { v.members[0].kind = "ordinary_batch" }, func(v *iaMediaResult) { v.members[0].edition = nil }, func(v *iaMediaResult) { v.members[0].partIndex = iaMediaPtr(int64(-1)) }, func(v *iaMediaResult) { v.members[0].startSeconds = iaMediaPtr(math.NaN()) }, func(v *iaMediaResult) { v.members[0].endSeconds = iaMediaPtr(0.0) }, func(v *iaMediaResult) { v.members[0].linkEffect = iaMediaPtr("removed") }, func(v *iaMediaResult) { v.members[0].parentID = iaMediaPtr(int64(1)) }, func(v *iaMediaResult) { v.directoryPath = []byte{0} }, func(v *iaMediaResult) { v.sourceID = "\xff" }, func(v *iaMediaResult) { v.members = make([]iaMediaMember, 4097); v.resultCount = 4097 }}
	for i, change := range changes {
		id, r, v := iaMediaFixture(IAAcceptAudioInventory)
		change(&v)
		if preflightIAMediaResult(IAAcceptAudioInventory, id, r, v) == nil {
			t.Fatalf("accepted %d", i)
		}
	}
	for _, format := range []string{"cbr", "cb7", "mobi", "azw3", "pdf", "epub", "cbz", "html"} {
		id, r, v := iaMediaFixture(IAAcceptReadingInventory)
		v.members[0].readingFormat = &format
		v.memberBytes, _ = iaMediaMemberRetainedBytes(IAAcceptReadingInventory, v.runID, v.members[0])
		err := preflightIAMediaResult(IAAcceptReadingInventory, id, r, v)
		if (err == nil) != (format != "html") {
			t.Fatal(format, err)
		}
	}
}
func TestIAMediaResultChargesAndBounds(t *testing.T) {
	id, r, v := iaMediaFixture(IAAcceptAudioInventory)
	// 80 mandatory bytes + repeated PK TEXT + variant TEXT + 5 present
	// INTEGER/REALs + link incarnation + byte-exact edition/effect.
	want := int64(80 + len("run") + len("audio_inventory_accept") + len("membership") + 5*8 + 16 + len("版\x00") + len("present"))
	if v.memberBytes != want {
		t.Fatalf("charge %d want %d", v.memberBytes, want)
	}
	m := iaMediaMember{kind: "identity", libraryID: 1, subjectID: 2, subjectKind: 7, subjectRevision: 1}
	n, _ := iaMediaMemberRetainedBytes(IAAcceptMembership, "run", m)
	if n != 80+3+int64(len("membership_accept"))+8 {
		t.Fatal(n)
	}
	m.parentID = iaMediaPtr(int64(3))
	m.parentKind = iaMediaPtr(int64(6))
	n2, _ := iaMediaMemberRetainedBytes(IAAcceptMembership, "run", m)
	if n2-n != 16 {
		t.Fatal("optional charge")
	}
	// Exact full-wire boundary includes every prefix and presence byte.
	wire := len(iaMediaReference("audio_inventory_accept", id, r, v))
	v.expectedPayload = make([]byte, maxCanonicalResultBytes-wire)
	if err := preflightIAMediaResult(IAAcceptAudioInventory, id, r, v); err != nil {
		t.Fatal(err)
	}
	v.nextPayload = append(v.nextPayload, 0)
	if preflightIAMediaResult(IAAcceptAudioInventory, id, r, v) == nil {
		t.Fatal("wire overflow accepted")
	}
	// Aggregate size, independent of the per-member count ceiling.
	_, _, v = iaMediaFixture(IAAcceptAudioInventory)
	v.members = append(v.members, v.members[0])
	v.members[1].ordinal = 1
	v.resultCount = 2
	s := strings.Repeat("x", maxCanonicalResultBytes/2)
	v.members[0].edition = &s
	v.members[1].edition = &s
	v.memberBytes = 0
	for _, m := range v.members {
		n, _ := iaMediaMemberRetainedBytes(IAAcceptAudioInventory, v.runID, m)
		v.memberBytes += n
	}
	if preflightIAMediaResult(IAAcceptAudioInventory, id, r, v) == nil {
		t.Fatal("aggregate overflow accepted")
	}
}

func TestIAMediaResultVariants(t *testing.T) {
	id, r, v := iaMediaFixture(IAAcceptAudioInventory)
	// Identity and artist rows carry no invented asset/link payload. The native
	// schema permits zero-filled metadata incarnations, which are still present.
	identity := iaMediaMember{kind: "identity", libraryID: 1, subjectID: 7, subjectKind: 4, subjectRevision: 1, parentID: iaMediaPtr(int64(3)), parentKind: iaMediaPtr(int64(3)), ancestorID: iaMediaPtr(int64(2)), ancestorKind: iaMediaPtr(int64(2))}
	artist := iaMediaMember{kind: "artist", libraryID: 1, subjectID: 7, subjectKind: 7, subjectRevision: 1, parentID: iaMediaPtr(int64(5)), parentKind: iaMediaPtr(int64(5))}
	for _, tc := range []struct {
		k CommandID
		m iaMediaMember
	}{{IAAcceptEpisodeInventory, identity}, {IAAcceptAudioInventory, artist}} {
		v.members = []iaMediaMember{tc.m}
		v.memberBytes, _ = iaMediaMemberRetainedBytes(tc.k, v.runID, tc.m)
		iaMediaHash(t, tc.k, id, r, v)
		v.members[0].parentKind = iaMediaPtr(int64(13))
		v.memberBytes, _ = iaMediaMemberRetainedBytes(tc.k, v.runID, v.members[0])
		if preflightIAMediaResult(tc.k, id, r, v) == nil {
			t.Fatal("invalid typed parent accepted")
		}
	}
	for _, head := range []int64{0, 1} {
		id, r, v = iaMediaFixture(IAAcceptReadingInventory)
		v.members[0].readingPathHead = &head
		if head == 0 {
			v.members[0].readingPathSequence = nil
		}
		v.memberBytes, _ = iaMediaMemberRetainedBytes(IAAcceptReadingInventory, v.runID, v.members[0])
		iaMediaHash(t, IAAcceptReadingInventory, id, r, v)
		v.members[0].readingPathSequence = iaMediaPtr(int64(0))
		v.memberBytes, _ = iaMediaMemberRetainedBytes(IAAcceptReadingInventory, v.runID, v.members[0])
		if preflightIAMediaResult(IAAcceptReadingInventory, id, r, v) == nil {
			t.Fatal("invalid path sequence accepted")
		}
	}
	id, r, v = iaMediaFixture(IAAcceptExtraInventory)
	iaMediaHash(t, IAAcceptExtraInventory, id, r, v)
	v.members[0].extraKind = nil
	v.memberBytes, _ = iaMediaMemberRetainedBytes(IAAcceptExtraInventory, v.runID, v.members[0])
	if preflightIAMediaResult(IAAcceptExtraInventory, id, r, v) == nil {
		t.Fatal("missing extra kind accepted")
	}
	// A repeated native PK can produce storage debt above the wire cap even
	// though its run ID appears only once on the wire. No storage cap is invented.
	id, r, v = iaMediaFixture(IAAcceptMembership)
	v.runID = strings.Repeat("r", 9000)
	v.members = make([]iaMediaMember, 4096)
	v.resultCount = 4096
	v.memberBytes = 0
	for i := range v.members {
		m := iaMediaMember{ordinal: int64(i), kind: "identity", libraryID: 1, subjectID: int64(i + 1), subjectKind: 1, subjectRevision: 1}
		v.members[i] = m
		n, _ := iaMediaMemberRetainedBytes(IAAcceptMembership, v.runID, m)
		v.memberBytes += n
	}
	if v.memberBytes <= maxCanonicalResultBytes {
		t.Fatal("fixture must exceed wire cap in storage")
	}
	iaMediaHash(t, IAAcceptMembership, id, r, v)
}
