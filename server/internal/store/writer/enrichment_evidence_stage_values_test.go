package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func draftMovieStageHeader(t *testing.T) EvidenceHeader {
	t.Helper()
	h, err := NewEvidenceHeader(EvidenceHeaderInput{
		ID: "evidence-native:149", SubjectID: 151, Provider: "tmdb", SubjectKind: "movie", ProviderID: "42",
		ParserVersion: "tmdb.movie.header-selected-credits.v1", RequestShape: "tmdb.movie.detail+credits.header-selected-credits.v1",
		Language: "en-CA", Region: "CA", SourceURL: "https://api.themoviedb.org/3/movie/42",
		ObservedAt: "2026-11-06T10:23:19.000000137+00:00", Title: "Native title:157", ObservationDigest: [32]byte{163},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func draftMovieStageCredits() []EvidenceCreditInput {
	return []EvidenceCreditInput{
		{Ordinal: 0, SourceOrdinal: 7, Billing: EvidenceInteger{Value: 0, Present: true}, Class: Cast, ProviderPersonID: "173", Name: "Cast:179", Role: "Pilot:181", Department: "Acting:191", CreditID: "credit:193"},
		{Ordinal: 1, SourceOrdinal: 3, Billing: EvidenceInteger{Value: 2, Present: true}, Class: Cast, ProviderPersonID: "197", Name: "Cast:199", Role: "Captain:211", CreditID: "credit:223"},
		{Ordinal: 2, SourceOrdinal: 13, Class: Crew, ProviderPersonID: "227", Name: "Crew:229", Role: "Director", Department: "Directing", CreditID: "credit:233"},
	}
}

func TestDraftEvidencePayloadOwnsExactNativeProjection(t *testing.T) {
	header, rows := draftMovieStageHeader(t), draftMovieStageCredits()
	// Legal short views must not retain a much larger caller backing string.
	for _, field := range []*string{&rows[0].ProviderPersonID, &rows[0].Name, &rows[0].Role, &rows[0].Department, &rows[0].CreditID} {
		backing := strings.Repeat("x", 1<<20) + *field
		*field = backing[len(backing)-len(*field):]
	}
	classBacking := strings.Repeat("x", 1<<20) + "cast"
	rows[0].Class = CreditClass(classBacking[len(classBacking)-4:])
	payload, err := PrepareEvidenceStagePayload(header, rows)
	if err != nil || !payload.valid || payload.Header() != header || !reflect.DeepEqual(payload.Credits(), draftMovieStageCredits()) {
		t.Fatal("native projection drift", err)
	}
	got := payload.Credits()
	for i, value := range []string{got[0].ProviderPersonID, got[0].Name, got[0].Role, got[0].Department, got[0].CreditID, string(got[0].Class)} {
		original := []string{rows[0].ProviderPersonID, rows[0].Name, rows[0].Role, rows[0].Department, rows[0].CreditID, string(rows[0].Class)}[i]
		if unsafe.StringData(value) == unsafe.StringData(original) {
			t.Fatal("retained caller text backing", i)
		}
	}
	wantBytes := header.TextBytes() + 64
	for _, row := range draftMovieStageCredits() {
		wantBytes += uint64(len("tmdb") + len("movie") + len(header.ID()) + len(row.Class) + len(row.ProviderPersonID) + len(row.Name) + len(row.Role) + len(row.Department) + len(row.CreditID))
	}
	rows[0].Name, got[0].Name = "caller mutation", "getter mutation"
	if payload.CreditCount() != 3 || payload.BaseContentBytes() != wantBytes || !reflect.DeepEqual(payload.Credits(), draftMovieStageCredits()) {
		t.Fatal("ownership/forecast/order drift")
	}
}

func TestDraftEvidencePayloadRefusesMalformedNativeShape(t *testing.T) {
	for name, mutate := range map[string]func([]EvidenceCreditInput){
		"cast_missing_zero": func(r []EvidenceCreditInput) { r[0].Billing.Present = false },
		"crew_present_zero": func(r []EvidenceCreditInput) { r[2].Billing.Present = true },
		"retained_gap":      func(r []EvidenceCreditInput) { r[1].Ordinal = 7 },
		"raw_duplicate":     func(r []EvidenceCreditInput) { r[2].SourceOrdinal = r[0].SourceOrdinal },
		"invalid_text":      func(r []EvidenceCreditInput) { r[0].Name = "x\x00y" },
	} {
		t.Run(name, func(t *testing.T) {
			rows := draftMovieStageCredits()
			mutate(rows)
			payload, err := PrepareEvidenceStagePayload(draftMovieStageHeader(t), rows)
			if !errors.Is(err, ErrInvalid) || payload.valid || payload.Header().ID() != "" || payload.CreditCount() != 0 {
				t.Fatal("malformed shape became partial payload", err)
			}
		})
	}
	h := draftMovieStageHeader(t)
	h.input.Studio = "undeclared family"
	if _, err := PrepareEvidenceStagePayload(h, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("different descriptor accepted", err)
	}
	if p, err := PrepareEvidenceStagePayload(draftMovieStageHeader(t), nil); err != nil || !p.valid || p.CreditCount() != 0 {
		t.Fatal("complete empty credits rejected", err)
	}
}
