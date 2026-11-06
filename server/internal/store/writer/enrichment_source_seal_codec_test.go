package writer

import (
	"errors"
	"strings"
	"testing"
	"unsafe"
)

func normalizedSourceDataFixture() normalizedSourceContent {
	s := normalizedSourceContent{header: normalizedSourceHeader{id: strings.Repeat("1", 32), entityID: 227, provider: "tmdb", record: "opaque-native-reference:193", observed: "2026-11-06T12:00:00Z", count: 1},
		upstream: normalizedEvidenceReceipt{ownerID: "owner:173", identity: CommandIdentity{Sequence: 7}, entityID: 101, version: 1, evidenceID: "evidence:179", subjectID: 181, parser: "tmdb.movie.header-selected-credits.v1", provider: "tmdb", kind: "movie", providerID: "42", language: "en-CA", region: "CA", shape: "tmdb.movie.detail+credits.header-selected-credits.v1", observation: strings.Repeat("a", 64), ownerGeneration: 43, buildID: "build:191", buildGeneration: 47, revision: 53, manifest: normalizedSealManifest{normalizedEvidenceAlgorithm, 1, 2, 1000, normalizedSealCommitment{1}}}}
	r := normalizedSourceRow{id: strings.Repeat("2", 32), entityID: 227, provider: "tmdb", tupleDigest: strings.Repeat("b", 64), record: "opaque-native-reference:193", creditID: "credit:197", providerPersonID: "199", identityKey: "native-person-key:211", personToken: "native-person-token:223", targetKind: "person", targetID: "native-person-token:223", name: "Name:227", role: "Pilot:229", department: "Acting:233", class: Cast, rawOrdinal: 7, billing: EvidenceInteger{0, true}}
	s.members = []normalizedSourceMember{{s.header.id, 227, "tmdb", 0, r.id, "new", r}}
	return s
}
func TestSourceCodecSeamOwnsInputAndKeepsHistoricalOriginDistinct(t *testing.T) {
	input := normalizedSourceDataFixture()
	backing := strings.Repeat("x", 1<<20) + input.members[0].row.name
	input.members[0].row.name = backing[len(backing)-len(input.members[0].row.name):]
	spy := &sealCodecSpy{}
	owned, m, err := prepareSourceSealManifest(t.Context(), spy, input, enrichmentControlCapture{})
	if err != nil || m.algorithm != normalizedSourceAlgorithm || m.rows != 1 || spy.calls != 1 {
		t.Fatal(m, err, spy.calls)
	}
	if owned.upstream.entityID == owned.header.entityID || owned.upstream.ownerGeneration != 43 {
		t.Fatal("historical E owner coerced to source owner", owned.upstream)
	}
	if unsafe.StringData(owned.members[0].row.name) == unsafe.StringData(input.members[0].row.name) {
		t.Fatal("retained source caller buffer")
	}
	input.members[0].row.name = "caller mutation"
	if owned.members[0].row.name != "Name:227" {
		t.Fatal("retained member slice")
	}
}
func TestSourceCodecSeamRefusesOrdinalBillingAndUnboundedControlBeforeClone(t *testing.T) {
	for name, mutate := range map[string]func(*normalizedSourceContent){
		"position_alias":     func(p *normalizedSourceContent) { p.members[0].position = p.members[0].row.rawOrdinal },
		"cast_missing_zero":  func(p *normalizedSourceContent) { p.members[0].row.billing = EvidenceInteger{} },
		"tuple_bytes":        func(p *normalizedSourceContent) { p.members[0].row.role = strings.Repeat("x", 2001) },
		"unbounded_upstream": func(p *normalizedSourceContent) { p.upstream.ownerID = strings.Repeat("x", maxCanonicalRequestBytes+1) },
	} {
		t.Run(name, func(t *testing.T) {
			p := normalizedSourceDataFixture()
			mutate(&p)
			spy := &sealCodecSpy{}
			out, m, err := prepareSourceSealManifest(t.Context(), spy, p, enrichmentControlCapture{})
			if !errors.Is(err, ErrInvalid) || spy.calls != 0 || out.members != nil || m != (normalizedSealManifest{}) {
				t.Fatal("malformed source reached codec", err, spy.calls)
			}
		})
	}
}
