package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"
)

func iaConfigResultFixture() (CommandIdentity, RequestDigest, iaConfigurationResult) {
	id := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}
	r := RequestDigest{3}
	return id, r, iaConfigurationResult{original: iaConfigurationReceiptOriginal{operationID: id.OperationID, requestDigest: r, purpose: IAConfigureLibrary, libraryID: 7, libraryKey: "lib"}, acceptedRevision: 9, appliedMS: 123, libraryName: "é", libraryKind: "movie", resultCount: 1, sources: []iaConfigurationSourceResult{{id: "s", name: "n", incarnation: "i", generation: 1, configuredRoot: "/a/../b", resolvedRoot: "/b", rootIdentity: "r", kind: "local", classification: "local", enabled: true, health: "ok", identityConfirmed: true}}}
}

// Independent framing oracle, with no production encoder or byte constants.
func iaConfigResultWire(id CommandIdentity, r RequestDigest, v iaConfigurationResult, command string) []byte {
	var b bytes.Buffer
	n := func(v int64) { _ = binary.Write(&b, binary.BigEndian, v) }
	text := func(v string) { n(int64(len(v))); b.WriteString(v) }
	flag := func(v bool) {
		if v {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
	}
	b.WriteString("portico-writer-result-sha256-v1\x00")
	b.Write([]byte{0, 1})
	text(command)
	text("configuration")
	text("applied")
	b.Write(id.Incarnation[:])
	n(id.Sequence)
	b.Write(id.OperationID[:])
	b.Write(r[:])
	o := v.original
	b.Write(o.operationID[:])
	b.Write(o.requestDigest[:])
	n(int64(o.purpose))
	n(o.libraryID)
	text(o.libraryKey)
	flag(command == "configure_source")
	text(o.sourceID)
	n(v.acceptedRevision)
	n(v.appliedMS)
	flag(command == "configure_library")
	text(v.libraryName)
	text(v.libraryKind)
	n(v.resultCount)
	n(int64(len(v.sources)))
	for _, s := range v.sources {
		n(s.ordinal)
		text(s.id)
		text(s.name)
		text(s.incarnation)
		n(s.generation)
		text(s.configuredRoot)
		text(s.resolvedRoot)
		text(s.rootIdentity)
		text(s.kind)
		text(s.classification)
		flag(s.enabled)
		text(s.health)
		flag(s.followSymlinks)
		n(s.intervalSeconds)
		flag(s.identityConfirmed)
		text(s.nextScanAt)
		text(s.lastCompleteAt)
		text(s.lastProgressAt)
	}
	return b.Bytes()
}
func TestIAConfigurationResultWireAndDebt(t *testing.T) {
	id, r, v := iaConfigResultFixture()
	for _, kind := range []CommandID{IAConfigureLibrary, IAConfigureSource} {
		command := "configure_library"
		if kind == IAConfigureSource {
			command = "configure_source"
			v.original.purpose = kind
			v.original.sourceID = "s"
			v.libraryName = ""
			v.libraryKind = ""
		}
		if err := preflightIAConfigurationResult(kind, id, r, v); err != nil {
			t.Fatal(err)
		}
		var out iaResultRecorder
		e := requestEncoder{h: &out}
		if err := writeIAConfigurationResult(&e, kind, id, r, v); err != nil {
			t.Fatal(err)
		}
		wire := iaConfigResultWire(id, r, v, command)
		if !bytes.Equal(out.Bytes(), wire) {
			t.Fatal("wire oracle mismatch")
		}
		textBytes := len(command) + len(v.original.libraryKey) + len(v.original.sourceID) + len(v.libraryName) + len(v.libraryKind)
		for _, s := range v.sources {
			for _, x := range []string{s.id, s.name, s.incarnation, s.configuredRoot, s.resolvedRoot, s.rootIdentity, s.kind, s.classification, s.health, s.nextScanAt, s.lastCompleteAt, s.lastProgressAt} {
				textBytes += len(x)
			}
		}
		if len(wire)-textBytes != iaConfigurationResultFixedBytes+iaConfigurationSourceResultFixedBytes {
			t.Fatal("envelope overhead drift", len(wire)-textBytes)
		}
		digest := ResultDigest(sha256.Sum256(wire))
		if err := checkIAConfigurationResultDigest(kind, id, r, v, digest); err != nil {
			t.Fatal(err)
		}
		digest[0]++
		if err := checkIAConfigurationResultDigest(kind, id, r, v, digest); err == nil {
			t.Fatal("digest drift accepted")
		}
		// Independently: header 160+17+2+5=184; source 96+17+
		// (1+1+1+7+2+1+5+5+2)=138. 101 command is one byte shorter.
		h, _ := iaConfigurationHeaderRetainedBytes(kind, v)
		s, _ := iaConfigurationSourceRetainedBytes(kind, v.sources[0])
		wh, ws := int64(184), int64(138)
		if kind == IAConfigureSource {
			wh, ws = 176, 137
		}
		if h != wh || s != ws {
			t.Fatal("native octet charge", h, s)
		}
	}
}
func TestIAConfigurationResultCohortAndVariants(t *testing.T) {
	id, r, v := iaConfigResultFixture()
	for _, change := range []func(*iaConfigurationResult){
		func(v *iaConfigurationResult) { v.resultCount++ }, func(v *iaConfigurationResult) { v.sources = nil },
		func(v *iaConfigurationResult) { v.sources[0].ordinal = 1 }, func(v *iaConfigurationResult) { v.sources[0].generation = 0 },
		func(v *iaConfigurationResult) { v.sources[0].configuredRoot = "\xff" }, func(v *iaConfigurationResult) { v.sources[0].health = "x\x00" },
		func(v *iaConfigurationResult) { v.original.sourceID = "s" }, func(v *iaConfigurationResult) { v.original.requestDigest[0]++ },
		func(v *iaConfigurationResult) { v.original.operationID[0]++ }, func(v *iaConfigurationResult) { v.original.purpose = IAConfigureSource },
		func(v *iaConfigurationResult) { v.libraryKind = "bad" }, func(v *iaConfigurationResult) { v.appliedMS = -1 },
		func(v *iaConfigurationResult) {
			v.sources = append(v.sources, v.sources[0])
			v.sources[1].ordinal = 1
			v.resultCount = 2
		},
		func(v *iaConfigurationResult) {
			v.sources[0].lastProgressAt = strings.Repeat("x", maxCanonicalResultBytes)
		},
	} {
		bad := v
		bad.sources = append([]iaConfigurationSourceResult(nil), v.sources...)
		change(&bad)
		if err := preflightIAConfigurationResult(IAConfigureLibrary, id, r, bad); err == nil {
			t.Fatal("invalid history accepted")
		}
	}
	v.sources = make([]iaConfigurationSourceResult, 64)
	for i := range v.sources {
		v.sources[i] = iaConfigurationSourceResult{id: strings.Repeat("s", i+1), incarnation: "i", generation: 1, ordinal: int64(i), configuredRoot: "/a", resolvedRoot: "/a", rootIdentity: "r", kind: "local", classification: "local", health: "ok"}
	}
	v.resultCount = 64
	if err := preflightIAConfigurationResult(IAConfigureLibrary, id, r, v); err != nil {
		t.Fatal(err)
	}
	v.sources = append(v.sources, v.sources[0])
	v.resultCount = 65
	if err := preflightIAConfigurationResult(IAConfigureLibrary, id, r, v); err == nil {
		t.Fatal("65 source cohort accepted")
	}
	_, _, v = iaConfigResultFixture()
	v.original.purpose = IAConfigureSource
	v.original.sourceID = "other"
	v.libraryName, v.libraryKind = "", ""
	if err := preflightIAConfigurationResult(IAConfigureSource, id, r, v); err == nil {
		t.Fatal("101 original source mismatch accepted")
	}
}
func TestIAConfigurationResultBindsPersistedFields(t *testing.T) {
	id, r, v := iaConfigResultFixture()
	baseline := sha256.Sum256(iaConfigResultWire(id, r, v, "configure_library"))
	changes := []func(*iaConfigurationResult){
		func(v *iaConfigurationResult) { v.original.libraryID++ }, func(v *iaConfigurationResult) { v.original.libraryKey += "x" }, func(v *iaConfigurationResult) { v.acceptedRevision++ }, func(v *iaConfigurationResult) { v.appliedMS++ }, func(v *iaConfigurationResult) { v.libraryName += "x" }, func(v *iaConfigurationResult) { v.libraryKind = "tv" },
	}
	for field := 0; field < 17; field++ {
		f := field
		changes = append(changes, func(v *iaConfigurationResult) {
			s := &v.sources[0]
			switch f {
			case 0:
				s.id += "x"
			case 1:
				s.name += "x"
			case 2:
				s.incarnation += "x"
			case 3:
				s.generation++
			case 4:
				s.configuredRoot += "x"
			case 5:
				s.resolvedRoot += "x"
			case 6:
				s.rootIdentity += "x"
			case 7:
				s.kind = "remote"
			case 8:
				s.classification = "network"
			case 9:
				s.enabled = false
			case 10:
				s.health = "offline"
			case 11:
				s.followSymlinks = true
			case 12:
				s.intervalSeconds = 900
			case 13:
				s.identityConfirmed = false
			case 14:
				s.nextScanAt = "next"
			case 15:
				s.lastCompleteAt = "complete"
			case 16:
				s.lastProgressAt = "progress"
			case 17:
				v.original.libraryKey = "é"
			}
		})
	}
	for i, change := range changes {
		x := v
		x.sources = append([]iaConfigurationSourceResult(nil), v.sources...)
		change(&x)
		if err := preflightIAConfigurationResult(IAConfigureLibrary, id, r, x); err != nil {
			t.Fatal(i, err)
		}
		var out iaResultRecorder
		e := requestEncoder{h: &out}
		if err := writeIAConfigurationResult(&e, IAConfigureLibrary, id, r, x); err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(out.Bytes()) == baseline {
			t.Fatal("unbound persisted field", i)
		}
	}
}

func TestIAConfigurationResultExactEnvelope(t *testing.T) {
	id, r, v := iaConfigResultFixture()
	overhead := len(iaConfigResultWire(id, r, v, "configure_library"))
	v.sources[0].lastProgressAt = strings.Repeat("x", maxCanonicalResultBytes-overhead)
	if err := preflightIAConfigurationResult(IAConfigureLibrary, id, r, v); err != nil {
		t.Fatal("exact envelope refused", err)
	}
	v.sources[0].lastProgressAt += "x"
	// Invalid UTF-8 at the front must not preempt the prospective byte cap.
	v.original.libraryKey = "\xff" + v.original.libraryKey[1:]
	if err := preflightIAConfigurationResult(IAConfigureLibrary, id, r, v); err == nil || !strings.Contains(err.Error(), "ia_configuration_result_bytes") {
		t.Fatal("complete cap did not precede text scan", err)
	}
}
