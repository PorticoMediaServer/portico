package writer

import (
	"strings"
	"unicode/utf8"
)

// Local family observations are the inert native values the Basic producer
// carries into W. They are not source revisions, identities or prepared
// effects; the final Accept caller owns original authority and the single
// transaction containing these families.
const (
	basicLocalNFOMaxRows      = 4096
	basicLocalNFOMaxTextBytes = 65536
)

type basicNFORowObservation struct {
	locator, kind, coordinate, digest, payload string
	size, modifiedNS                           int64
	observedAt                                 string
}

// prepareBasicNFORows validates and clones one complete observed NFO family.
// No NFO family observed is legal and yields a nil set, but a partial set is
// never synthesized: every supplied row must be independently valid.
func prepareBasicNFORows(observed []basicNFORowObservation) ([]basicNFORow, error) {
	if len(observed) == 0 {
		return nil, nil
	}
	if len(observed) > basicLocalNFOMaxRows {
		return nil, ErrCap
	}
	for _, r := range observed {
		for _, s := range [...]string{r.locator, r.kind, r.coordinate, r.digest, r.payload, r.observedAt} {
			if len(s) > basicLocalNFOMaxTextBytes {
				return nil, ErrCap
			}
			if !utf8.ValidString(s) {
				return nil, ErrInvalid
			}
		}
		if r.size < 0 || r.observedAt == "" {
			return nil, ErrInvalid
		}
	}
	out := make([]basicNFORow, len(observed))
	for n, r := range observed {
		out[n] = basicNFORow{
			locator:    strings.Clone(r.locator),
			kind:       strings.Clone(r.kind),
			coordinate: strings.Clone(r.coordinate),
			digest:     strings.Clone(r.digest),
			payload:    strings.Clone(r.payload),
			size:       r.size,
			modifiedNS: r.modifiedNS,
			observedAt: strings.Clone(r.observedAt),
		}
	}
	return out, nil
}

type basicArtworkObservation struct {
	role, imageID, origin, attribution, locale, effectiveLanguage, sourceFence string
}

// prepareBasicArtworkSelection binds one locally observed artwork choice onto
// the caller's template. Only the observed family fields are overwritten;
// source, entity, job and revision identity stay exactly the template's, and
// prepareBasicArtwork remains the one authority for role, cap and UTF-8 rules.
func prepareBasicArtworkSelection(template basicArtworkInput, observed basicArtworkObservation) (*basicArtworkInput, error) {
	template.role = observed.role
	template.imageID = observed.imageID
	template.origin = observed.origin
	template.attribution = observed.attribution
	template.locale = observed.locale
	template.effectiveLanguage = observed.effectiveLanguage
	template.sourceFence = observed.sourceFence
	template.valid = false
	return prepareBasicArtwork(template)
}

// prepareBasicSubtitleTextPending is the producer's single named switch over
// the observed subtitle-text-pending state.
func prepareBasicSubtitleTextPending(observed bool) bool {
	return observed
}
