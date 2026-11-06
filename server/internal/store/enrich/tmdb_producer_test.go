package enrich

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func TestDraftTMDBProducerKeepsAcquiredMetadataAndNativeOrdering(t *testing.T) {
	metadata := documentMetadata()
	tr := broker(t, documentConfig(), admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
		return Response{Status: 200, Body: []byte(tmdbMovieFixture), Metadata: metadata}, nil
	})
	clock := newFakeClock()
	tr.clock = clock
	target := TMDBMovieTarget{"evidence-native:149", "42", "en-CA", "CA", 151}
	document, err := tr.GetDocument(t.Context(), documentKey())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := PrepareAcquiredTMDBMovie(tmdbMoviePolicy(t), target, document)
	if err != nil {
		t.Fatal(err)
	}
	wantRows := []writer.EvidenceCreditInput{
		{Ordinal: 0, SourceOrdinal: 0, Billing: writer.EvidenceInteger{Value: 7, Present: true}, Class: Cast, ProviderPersonID: "201", Name: "Cast:131", Role: "Pilot:137", CreditID: "cast-credit:139"},
		{Ordinal: 1, SourceOrdinal: 1, Class: Crew, ProviderPersonID: "301", Name: "Crew:113", Role: "Director", Department: "Directing", CreditID: "crew-credit:127"},
	}
	if !reflect.DeepEqual(payload.Credits(), wantRows) {
		t.Fatal("native NULL/billing/raw/retained positions aliased", payload.Credits())
	}
	m := document.Metadata()
	want, err := PrepareTMDBMovie(tmdbMoviePolicy(t), TMDBMovieObservation{
		EvidenceID: target.EvidenceID, ProviderID: "42", SubjectID: 151, Language: "en-CA", Region: "CA",
		ObservedAt: m.ObservedAt, SourceURL: m.SourceURL, ETag: m.ETag, LastModified: m.LastModified,
	}, []byte(tmdbMovieFixture))
	if err != nil || payload.Header() != want.Header() || payload.BaseContentBytes() != uint64(want.StoredBytes()) {
		t.Fatal("acquisition projection/forecast drift", err)
	}
	metadata = AcquisitionMetadata{"later", "later", "later", "https://wrong.invalid/?api_key=later"}
	clock.advance(time.Second)
	reused, err := tr.GetDocument(t.Context(), documentKey())
	if err != nil {
		t.Fatal(err)
	}
	again, err := PrepareAcquiredTMDBMovie(tmdbMoviePolicy(t), target, reused)
	if err != nil || again.Header() != payload.Header() || tr.Stats().Reused != 1 {
		t.Fatal("reuse invented new acquisition metadata", err)
	}
}

func TestDraftTMDBProducerRefusesWrongAcquisitionScope(t *testing.T) {
	target := TMDBMovieTarget{"evidence-native:149", "42", "en-CA", "CA", 151}
	if p, err := PrepareAcquiredTMDBMovie(tmdbMoviePolicy(t), target, AcquisitionDocument{}); !errors.Is(err, ErrAcquisitionMetadata) || p.Header().ID() != "" {
		t.Fatal("zero acquisition admitted", err)
	}
	doc := AcquisitionDocument{body: []byte(tmdbMovieFixture), key: documentKey(), metadata: documentMetadata()}
	for _, mutate := range []func(*TMDBMovieTarget){
		func(v *TMDBMovieTarget) { v.ProviderID = "43" },
		func(v *TMDBMovieTarget) { v.Language = "fr" },
	} {
		wrong := target
		mutate(&wrong)
		if p, err := PrepareAcquiredTMDBMovie(tmdbMoviePolicy(t), wrong, doc); !errors.Is(err, ErrAcquisitionMetadata) || p.Header().ID() != "" {
			t.Fatal("crossed acquisition namespace", err)
		}
	}
}

func TestDraftTMDBProducerTrimKeepsRawSourceGaps(t *testing.T) {
	cfg := documentConfig() // explicit harness policy, not a production recipe
	cfg.MaxInFlight, cfg.MaxPending, cfg.MaxWaiters = 1, 1, 1
	cfg.MaxResponseBytes = 4 << 20
	cfg.CacheBytes = 1 << 20 // HARNESS cache must fit this 152-candidate entity.
	tr := broker(t, cfg, admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
		return Response{Status: 200, Body: tmdbCreditsFixture(101, 51), Metadata: documentMetadata()}, nil
	})
	doc, err := tr.GetDocument(t.Context(), documentKey())
	if err != nil {
		t.Fatal(err)
	}
	originalBody, originalMetadata := doc.Body(), doc.Metadata()
	payload, err := PrepareAcquiredTMDBMovie(tmdbMoviePolicy(t), TMDBMovieTarget{"evidence-native:149", "42", "en-CA", "CA", 151}, doc)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := tr.GetDocument(t.Context(), documentKey())
	if err != nil || tr.Stats().Reused != 1 || !bytes.Equal(doc.Body(), originalBody) || !bytes.Equal(cached.Body(), originalBody) || cached.Metadata() != originalMetadata {
		t.Fatal("candidate retirement altered acquisition/cache custody", err)
	}
	rows := payload.Credits()
	if len(rows) != 150 || rows[0].Billing != (writer.EvidenceInteger{Present: true}) || rows[100].Ordinal != 100 || rows[100].SourceOrdinal != 101 || rows[100].Billing.Present {
		t.Fatal("retained position replaced raw source/billing/NULL", rows)
	}
}
