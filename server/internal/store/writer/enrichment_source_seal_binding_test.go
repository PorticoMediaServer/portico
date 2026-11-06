package writer

import (
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
)

// Native DATA is EN's frozen spec-derived5dbaf784 fixture, never codec output.
// These test projections do not construct or encode canonical expectations.
func evidencePayloadFromSealVector(t *testing.T, in NormalizedEvidenceInput) EvidenceStagePayload {
	t.Helper()
	h := in.Header
	raw, err := hex.DecodeString(h.Digest)
	if err != nil || len(raw) != 32 {
		t.Fatal("bad independent observation field", err)
	}
	v := EvidenceHeaderInput{ID: h.ID, SubjectID: h.SubjectID, ParserVersion: h.ParserVersion, Provider: h.Provider, SubjectKind: h.SubjectKind, ProviderID: h.ProviderID, Language: h.Language, Region: h.Region, RequestShape: h.RequestShape,
		ObservedAt: h.ObservedAt, SourceURL: h.SourceURL, ETag: h.ETag, LastModified: h.LastModified, Title: h.Title, OriginalTitle: h.OriginalTitle, Overview: h.Overview, Tagline: h.Tagline, ContentRating: h.ContentRating, Studio: h.Studio, Network: h.Network, Country: h.Country,
		ReleaseDate: h.ReleaseDate, Year: h.Year, Artist: h.Artist, LengthMillis: h.LengthMillis, ArtistType: h.ArtistType, BeginYear: h.BeginYear, EndYear: h.EndYear, WikidataID: h.WikidataID, Biography: h.Biography, BiographySource: h.BiographySource, BiographySourceURL: h.BiographySourceURL,
		Disambiguation: h.Disambiguation, ReleaseGroupID: h.ReleaseGroupID, GroupTitle: h.GroupTitle, PrimaryType: h.PrimaryType, Barcode: h.Barcode}
	copy(v.ObservationDigest[:], raw)
	header, err := NewEvidenceHeader(v)
	if err != nil {
		t.Fatal(err)
	}
	credits := make([]EvidenceCreditInput, len(in.Credits))
	for i, r := range in.Credits {
		if !r.SourceOrdinal.Present {
			t.Fatal("positive source ordinal missing")
		}
		credits[i] = EvidenceCreditInput{Ordinal: r.Ordinal, SourceOrdinal: r.SourceOrdinal.Value, Billing: r.Billing, Class: CreditClass(r.CreditClass), ProviderPersonID: r.ProviderPersonID, Name: r.Name, Role: r.Role, Department: r.Department, CreditID: r.CreditID}
	}
	payload, err := PrepareEvidenceStagePayload(header, credits)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func sourceContentFromSealVector(in NormalizedSourceInput) normalizedSourceContent {
	u, h := in.Upstream, in.Header
	out := normalizedSourceContent{upstream: normalizedEvidenceReceipt{ownerID: u.ProviderOperationID, commandKind: u.CommandKind, identity: CommandIdentity{Incarnation: u.CommandIncarnation, Sequence: u.CommandSequence, OperationID: u.OperationID}, request: RequestDigest(u.RequestDigest), entityID: u.EntityID, version: u.SealVersion, evidenceID: u.SealEvidenceID, subjectID: u.SealSubjectID, parser: u.SealParserVersion, provider: u.SealProvider, kind: u.SealSubjectKind, providerID: u.SealProviderID, language: u.SealLanguage, region: u.SealRegion, shape: u.SealRequestShape, observation: u.SealObservationDigest, ownerGeneration: u.SealOwnerGeneration, buildID: u.SealBuildID, buildGeneration: u.SealBuildGeneration, revision: u.SealContentRevision, manifest: normalizedSealManifest{u.SealAlgorithm, u.SealVersion, u.SealVerifiedRows, u.SealCanonicalBytes, normalizedSealCommitment(u.SealCommitment)}},
		header: normalizedSourceHeader{h.PublicationID, h.EntityID, h.Provider, h.AcceptedRecordReference, h.ObservedAt, h.MemberCount}, members: make([]normalizedSourceMember, len(in.Members)), intent: in.Intent}
	for i, v := range in.Members {
		m, r := v.Member, v.Row
		out.members[i] = normalizedSourceMember{m.PublicationID, m.EntityID, m.Provider, m.Position, m.SourceRowID, m.ReferenceStatus, normalizedSourceRow{id: r.SourceRowID, entityID: r.EntityID, provider: r.Provider, tupleDigest: r.TupleDigest, record: r.OriginalRecordReference, creditID: r.OriginalCreditID, providerPersonID: r.OriginalProviderPersonID, identityKey: r.OriginalPersonIdentityKey, personToken: r.OriginalPersonToken, targetKind: r.OriginalTargetKind, targetID: r.OriginalTargetID, name: r.OriginalCreditedName, role: r.OriginalRole, department: r.OriginalDepartment, class: CreditClass(r.OriginalCreditClass), rawOrdinal: r.OriginalSourceOrdinal, billing: r.OriginalBilling}}
	}
	return out
}

func TestENSealBindingMatchesIndependentNativeVectors(t *testing.T) {
	for _, v := range loadNormalizedVectors(t).Positive {
		t.Run(v.Name, func(t *testing.T) {
			expected, err := vectorDeclaration(v)
			if err != nil {
				t.Fatal(err)
			}
			var got normalizedSealManifest
			switch v.Kind {
			case NormalizedEvidenceKind:
				var input NormalizedEvidenceInput
				if err := decodeNormalizedNative(v.NativeInput, &input); err != nil {
					t.Fatal(err)
				}
				payload := evidencePayloadFromSealVector(t, input)
				if projected := projectNormalizedEvidence(payload); !reflect.DeepEqual(projected, input) {
					t.Fatal("independent native evidence projection changed", projected, input)
				}
				got, err = prepareEvidenceSealManifest(t.Context(), payload, enrichmentControlCapture{})
			case NormalizedSourceKind:
				var input NormalizedSourceInput
				// Native expected bytes/commitment are W's separate Verify
				// argument, not fields in the borrowed INPUT or C stream.
				// Classify/compare both explicitly rather than ignoring them.
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(v.NativeInput, &fields); err != nil {
					t.Fatal(err)
				}
				var intent map[string]json.RawMessage
				if err := json.Unmarshal(fields["intent"], &intent); err != nil {
					t.Fatal(err)
				}
				var declaredBytes int64
				var declaredCommitment [32]byte
				if err := decodeNormalizedNative(intent["source_expected_bytes"], &declaredBytes); err != nil {
					t.Fatal(err)
				}
				if err := decodeNormalizedNative(intent["source_expected_commitment"], &declaredCommitment); err != nil {
					t.Fatal(err)
				}
				if declaredBytes != expected.CanonicalBytes || declaredCommitment != expected.Commitment {
					t.Fatal("independent source declaration differs from golden")
				}
				delete(intent, "source_expected_bytes")
				delete(intent, "source_expected_commitment")
				fields["intent"], err = json.Marshal(intent)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				if err := decodeNormalizedNative(raw, &input); err != nil {
					t.Fatal(err)
				}
				content := sourceContentFromSealVector(input)
				if projected := projectNormalizedSource(content); !reflect.DeepEqual(projected, input) {
					t.Fatal("independent native source/intent projection changed", projected, input)
				}
				var owned normalizedSourceContent
				owned, got, err = prepareSourceSealManifest(t.Context(), content, enrichmentControlCapture{})
				if err == nil && !reflect.DeepEqual(projectNormalizedSource(owned), input) {
					t.Fatal("owning altered frozen native source/intent")
				}
			default:
				t.Fatal("unexpected frozen seal kind", v.Kind)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.algorithm != v.Expected.Algorithm || got.version != 1 || got.rows != expected.VerifiedRows || got.bytes != expected.CanonicalBytes || [32]byte(got.commitment) != expected.Commitment {
				t.Fatal("EN did not call W codec with frozen native values", got, expected)
			}
		})
	}
}

func TestENSealHeaderProjectionCoversAllDistinctHeaderFields(t *testing.T) {
	// Existing independent fully-populated header values cover excluded fields
	// too. This DATA projection is never admitted as a FIRST descriptor/proof.
	input := evidenceHeaderVectors(t)[0].Input
	got := projectNormalizedEvidence(EvidenceStagePayload{header: EvidenceHeader{input: input}}).Header
	gv, iv := reflect.ValueOf(got), reflect.ValueOf(input)
	if gv.NumField() != 38 {
		t.Fatal("native header projection shape changed")
	}
	for i := 0; i < gv.NumField(); i++ {
		name := gv.Type().Field(i).Name
		if name == "Digest" {
			if gv.Field(i).String() != hex.EncodeToString(input.ObservationDigest[:]) {
				t.Fatal("observation TEXT64 representation changed")
			}
			continue
		}
		want := iv.FieldByName(name)
		if !want.IsValid() || !reflect.DeepEqual(gv.Field(i).Interface(), want.Interface()) {
			t.Fatal("native header projection field changed", name)
		}
	}
}
