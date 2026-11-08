package writer

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Inert native values only. The final Accept caller owns original authority,
// preparation funding and the single transaction containing these effects.
type basicObjectAnalysis struct {
	source                                           basicStreamFactStage
	libraryKey, algorithm, policyJSON, completedJSON string
	subtitleTextPending                              bool
	valid                                            bool
}

type basicObjectAnalysisProjection struct {
	objectID, sourceID, assetToken, incarnation, objectRevision, runID string
	size, modifiedNS                                                   int64
	revision                                                           string
	policy                                                             int64
	operationsJSON, state, analysisError, algorithm                    string
	generation                                                         int64
}

func prepareBasicObjectAnalysis(source basicStreamFactStage, libraryKey, algorithm, policyJSON, completedJSON string, subtitleTextPending bool) (*basicObjectAnalysis, error) {
	in := basicObjectAnalysis{source: source, libraryKey: libraryKey, algorithm: algorithm, policyJSON: policyJSON, completedJSON: completedJSON, subtitleTextPending: subtitleTextPending}
	if _, err := validateBasicObjectAnalysis(&in); err != nil {
		return nil, err
	}
	in.source = cloneBasicSource(source)
	in.libraryKey, in.algorithm = strings.Clone(libraryKey), strings.Clone(algorithm)
	in.policyJSON, in.completedJSON = strings.Clone(policyJSON), strings.Clone(completedJSON)
	in.valid = true
	return &in, nil
}

func validateBasicObjectAnalysis(in *basicObjectAnalysis) (basicObjectAnalysisProjection, error) {
	if in == nil {
		return basicObjectAnalysisProjection{}, ErrInvalid
	}
	s := in.source
	texts := [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint, in.libraryKey, in.algorithm, in.policyJSON, in.completedJSON}
	remaining := maxCanonicalRequestBytes
	// All raw bytes are admitted before JSON allocation, parsing or cloning.
	for _, text := range texts {
		if len(text) > remaining {
			return basicObjectAnalysisProjection{}, ErrCap
		}
		remaining -= len(text)
	}
	for _, text := range texts {
		if text == "" || !utf8.ValidString(text) {
			return basicObjectAnalysisProjection{}, ErrInvalid
		}
	}
	if s.changeID <= 0 || s.inputRevision <= 0 || s.libraryID <= 0 || s.assetID <= 0 || s.expectedEpoch <= 0 || s.sourceGeneration <= 0 || s.policyRevision <= 0 || s.size < 0 {
		return basicObjectAnalysisProjection{}, ErrInvalid
	}
	var enabled, completed []string
	if json.Unmarshal([]byte(in.policyJSON), &enabled) != nil || enabled == nil || json.Unmarshal([]byte(in.completedJSON), &completed) != nil || completed == nil {
		return basicObjectAnalysisProjection{}, ErrInvalid
	}
	policy := make(map[string]bool, len(enabled))
	for _, op := range enabled {
		if op == "" || policy[op] {
			return basicObjectAnalysisProjection{}, ErrInvalid
		}
		policy[op] = true
	}
	if !policy["probe"] {
		return basicObjectAnalysisProjection{}, ErrInvalid
	}
	done := make(map[string]bool, len(completed))
	for _, op := range completed {
		if !policy[op] || done[op] || (op != "probe" && op != "local_metadata" && op != "subtitles") {
			return basicObjectAnalysisProjection{}, ErrInvalid
		}
		done[op] = true
	}
	if !done["probe"] {
		return basicObjectAnalysisProjection{}, ErrInvalid
	}
	state := "complete"
	if in.subtitleTextPending || len(done) != len(policy) {
		state = "pending"
	}
	revision := s.objectRevision
	if in.subtitleTextPending {
		revision = ""
	}
	return basicObjectAnalysisProjection{s.objectID, s.sourceID, s.assetToken, s.incarnation, s.objectRevision, s.runID, s.size, s.modifiedNS, revision, s.policyRevision, in.completedJSON, state, "", in.algorithm, s.sourceGeneration}, nil
}

func applyBasicObjectAnalysis(ctx context.Context, turn *commandTurn, in *basicObjectAnalysis) (value basicObjectAnalysisProjection, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || in == nil || !in.valid {
		return value, ErrInvalid
	}
	want, err := validateBasicObjectAnalysis(in)
	if err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicObjectAnalysisPolicy, in.libraryKey, in.source.policyRevision, in.policyJSON)
	if err != nil {
		return value, err
	}
	var revision int64
	if err = row.Scan(&revision); err != nil {
		return value, basicStageError(err)
	}
	if revision != want.policy {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicObjectAnalysisCAS, want.revision, want.policy, want.operationsJSON, want.state, want.analysisError, want.algorithm, want.generation, want.objectID, want.sourceID, want.assetToken, want.incarnation, want.objectRevision, want.size, want.modifiedNS, want.runID)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.objectID, &value.sourceID, &value.assetToken, &value.incarnation, &value.objectRevision, &value.runID, &value.size, &value.modifiedNS, &value.revision, &value.policy, &value.operationsJSON, &value.state, &value.analysisError, &value.algorithm, &value.generation); err != nil {
		return value, basicStageError(err)
	}
	if value != want {
		return value, ErrConflict
	}
	return value, nil
}
