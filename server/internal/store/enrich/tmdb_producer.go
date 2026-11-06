package enrich

import "github.com/PorticoMediaServer/portico/server/internal/store/writer"

// TMDBMovieTarget is an original native provider-subject capture, not authority.
// ProviderID/language must match the acquired request; Region remains the exact
// native namespace input, separately rechecked by W against current policy.
type TMDBMovieTarget struct {
	EvidenceID, ProviderID, Language, Region string
	SubjectID                                int64
}

// PrepareAcquiredTMDBMovie preserves the acquired document's ORIGINAL metadata;
// callers cannot substitute a clock, TTL, validator or URL during preparation.
// It produces immutable draft009 stage DATA, not a command/proof/receipt/seal.
func PrepareAcquiredTMDBMovie(policy TMDBPolicy, target TMDBMovieTarget, acquired AcquisitionDocument) (writer.EvidenceStagePayload, error) {
	key := acquired.key
	if key.Provider != "tmdb" || key.Kind != "movie" || key.Shape != TMDBMovieRequestShape ||
		key.ExternalID != target.ProviderID || key.Locale != target.Language || !key.valid() {
		return writer.EvidenceStagePayload{}, ErrAcquisitionMetadata
	}
	m := acquired.metadata
	doc, err := PrepareTMDBMovie(policy, TMDBMovieObservation{
		EvidenceID: target.EvidenceID, SubjectID: target.SubjectID, ProviderID: target.ProviderID,
		Language: target.Language, Region: target.Region, ObservedAt: m.ObservedAt,
		ETag: m.ETag, LastModified: m.LastModified, SourceURL: m.SourceURL,
	}, acquired.body)
	if err != nil {
		return writer.EvidenceStagePayload{}, err
	}
	selected := doc.Credits().Rows()
	rows := make([]writer.EvidenceCreditInput, len(selected))
	for i, row := range selected {
		billing := writer.EvidenceInteger{}
		if row.Class == Cast {
			billing = writer.EvidenceInteger{Value: row.Billing, Present: true}
		}
		rows[i] = writer.EvidenceCreditInput{
			Ordinal: int64(i), SourceOrdinal: row.SourceOrdinal, Billing: billing, Class: row.Class,
			ProviderPersonID: row.Person.ExternalID, Name: row.Name, Role: row.Role,
			Department: row.Department, CreditID: row.CreditID,
		}
	}
	return writer.PrepareEvidenceStagePayload(doc.Header(), rows)
}
