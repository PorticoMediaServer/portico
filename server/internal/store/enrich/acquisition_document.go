package enrich

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrAcquisitionMetadata = errors.New("original enrichment acquisition metadata unavailable or invalid")

// AcquisitionMetadata contains ORIGINAL response observation values. A fetch
// adapter captures these once; cache reuse never substitutes its own wall time,
// provider Date, TTL or later request values. SourceURL is sanitized on entry.
type AcquisitionMetadata struct {
	ObservedAt, ETag, LastModified, SourceURL string
}

// AcquisitionDocument owns a private entity body, request key and metadata.
// Its observation is data, not a source-stage/seal capability or permission.
type AcquisitionDocument struct {
	body           []byte
	key            RequestKey
	metadata       AcquisitionMetadata
	metadataCharge int64
}

func (d AcquisitionDocument) Body() []byte {
	if len(d.body) == 0 {
		return nil
	}
	out := make([]byte, len(d.body))
	copy(out, d.body)
	return out
}
func (d AcquisitionDocument) Key() RequestKey               { return d.key }
func (d AcquisitionDocument) Metadata() AcquisitionMetadata { return d.metadata }

// AcquisitionMetadataSizeError describes a configured RESOURCE allocation
// budget, not a new provider/body/normalized-content cap. No field is truncated.
// The bound is conservative noscan string-object cost, not a heap/RSS sample.
type AcquisitionMetadataSizeError struct{ LimitAllocationBytes, ObservedAllocationBytes int64 }

func (e *AcquisitionMetadataSizeError) Error() string {
	if e == nil {
		return ErrAcquisitionMetadata.Error()
	}
	return fmt.Sprintf("enrichment original metadata allocation bound %d exceeds budget %d", e.ObservedAllocationBytes, e.LimitAllocationBytes)
}

// A configured allocation-budget refusal is schedulable capacity, never a
// malformed provider document or poison-file diagnosis.
func (*AcquisitionMetadataSizeError) Unwrap() error { return ErrBusy }

func metadataAllocation(m AcquisitionMetadata, limit int64) (int64, error) {
	var charge int64
	for _, field := range []string{m.ObservedAt, m.ETag, m.LastModified, m.SourceURL} {
		cost := cacheBodyCharge(len(field))
		if cost > math.MaxInt64-charge {
			return 0, &AcquisitionMetadataSizeError{limit, math.MaxInt64}
		}
		charge += cost
		if charge > limit {
			return 0, &AcquisitionMetadataSizeError{limit, charge}
		}
		if !utf8.ValidString(field) || strings.IndexByte(field, 0) >= 0 {
			return 0, ErrAcquisitionMetadata
		}
	}
	return charge, nil
}

func freezeAcquisitionMetadata(key RequestKey, m AcquisitionMetadata, limit int64) (AcquisitionMetadata, int64, error) {
	// Bound raw metadata BEFORE parsing/cloning any credential-bearing URL.
	if _, err := metadataAllocation(m, limit); err != nil {
		return AcquisitionMetadata{}, 0, err
	}
	if m.ObservedAt == "" {
		return AcquisitionMetadata{}, 0, ErrAcquisitionMetadata
	}
	if _, err := time.Parse(time.RFC3339Nano, m.ObservedAt); err != nil {
		return AcquisitionMetadata{}, 0, ErrAcquisitionMetadata
	}
	url, err := SafeTMDBSourceURL(m.SourceURL, key.ExternalID)
	if err != nil {
		return AcquisitionMetadata{}, 0, errors.Join(ErrAcquisitionMetadata, err)
	}
	m.SourceURL = url
	charge, err := metadataAllocation(m, limit)
	if err != nil {
		return AcquisitionMetadata{}, 0, err
	}
	return cloneAcquisitionMetadata(m), charge, nil
}

func cloneAcquisitionMetadata(m AcquisitionMetadata) AcquisitionMetadata {
	return AcquisitionMetadata{strings.Clone(m.ObservedAt), strings.Clone(m.ETag), strings.Clone(m.LastModified), strings.Clone(m.SourceURL)}
}

func cloneAcquisitionDocument(key RequestKey, body []byte, metadata AcquisitionMetadata, charge int64) AcquisitionDocument {
	var owned []byte
	if len(body) > 0 {
		owned = make([]byte, len(body))
		copy(owned, body)
	}
	return AcquisitionDocument{owned, key.owned(), cloneAcquisitionMetadata(metadata), charge}
}
