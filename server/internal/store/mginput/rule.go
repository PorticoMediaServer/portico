// Package mginput prepares legacy public inputs outside the native writer.
package mginput

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/mglimits"
)

// PrepareRuleConfig validates through the public DTO and serializes a copy with
// only surrounding keyword whitespace removed, as public Matches already does.
// The caller must hash the ORIGINAL full request before normalization or default
// resolution. This JSON is descriptive data, not authority or a public receipt.
// The byte ceiling is validation, not a resource reservation; consumers reserve
// actual bytes. This function resolves no defaults and grants no admission.
func PrepareRuleConfig(input dvr.RuleConfig) (string, error) {
	if !input.Valid() {
		return "", dvr.ErrInvalid
	}
	input.Keywords = slices.Clone(input.Keywords)
	input.BlockedKeywords = slices.Clone(input.BlockedKeywords)
	for i, keyword := range input.Keywords {
		input.Keywords[i] = strings.TrimSpace(keyword)
	}
	for i, keyword := range input.BlockedKeywords {
		input.BlockedKeywords[i] = strings.TrimSpace(keyword)
	}
	data, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	if len(data) > mglimits.RuleConfigBytes {
		return "", dvr.ErrInvalid
	}
	return string(data), nil
}
