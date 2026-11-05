package writer

import (
	"cmp"
	"slices"
	"strings"
)

const (
	MaxCastCredits = 100
	MaxCrewCredits = 50
	// Preparation safety budgets, not persisted product credit limits.
	MaxScreenCreditCandidates = 32768
	maxCreditPolicyBytes      = 4 << 20
)

type CreditClass string

const (
	Cast CreditClass = "cast"
	Crew CreditClass = "crew"
)

// ScreenCreditCandidate contains only selection facts. EN owns provider/text
// validation and immutable retained rows. W never imports the EN preparer.
type ScreenCreditCandidate struct {
	Class                      CreditClass
	Billing, SourceOrdinal     int64
	CreditID, Role, Department string
}

// SelectScreenCredits returns retained INPUT INDICES: cast by billing then
// source order; crew selected by frozen priority then restored to source order.
// Input is never mutated. Trim BEFORE persisting evidence/source/member rows.
func SelectScreenCredits(input []ScreenCreditCandidate) ([]int, error) {
	if err := count("screen_credit_candidates", len(input), MaxScreenCreditCandidates); err != nil {
		return nil, err
	}
	if err := validateCreditCandidates(input); err != nil {
		return nil, err
	}
	cast, crew := make([]int, 0, min(len(input), MaxCastCredits)), make([]int, 0, min(len(input), MaxCrewCredits))
	for i, c := range input {
		if c.Class == Cast {
			cast = append(cast, i)
		} else {
			crew = append(crew, i)
		}
	}
	slices.SortFunc(cast, func(a, b int) int {
		if order := cmp.Compare(input[a].Billing, input[b].Billing); order != 0 {
			return order
		}
		return sourceOrder(input[a], input[b])
	})
	cast = cast[:min(len(cast), MaxCastCredits)]
	slices.SortFunc(crew, func(a, b int) int {
		if order := cmp.Compare(crewRank(input[a]), crewRank(input[b])); order != 0 {
			return order
		}
		return sourceOrder(input[a], input[b])
	})
	crew = crew[:min(len(crew), MaxCrewCredits)]
	slices.SortFunc(crew, func(a, b int) int { return sourceOrder(input[a], input[b]) })
	retained := make([]int, 0, len(cast)+len(crew))
	retained = append(retained, cast...)
	retained = append(retained, crew...)
	return retained, nil
}

// ValidateRetainedCreditShape refuses over-limit or noncanonical retained
// images rather than silently trimming them inside the accepting transaction.
// It does not claim knowledge of discarded rows or attest provider authority.
func ValidateRetainedCreditShape(input []ScreenCreditCandidate) error {
	if err := count("retained_credits", len(input), MaxCastCredits+MaxCrewCredits); err != nil {
		return err
	}
	if err := validateCreditCandidates(input); err != nil {
		return err
	}
	cast, crew := 0, 0
	for i, c := range input {
		if c.Class == Cast {
			cast++
			if crew > 0 {
				return invalid("retained_credit_order")
			}
			if i > 0 && (input[i-1].Billing > c.Billing || (input[i-1].Billing == c.Billing && sourceOrder(input[i-1], c) > 0)) {
				return invalid("retained_cast_order")
			}
		} else {
			crew++
			if i > 0 && input[i-1].Class == Crew && sourceOrder(input[i-1], c) > 0 {
				return invalid("retained_crew_order")
			}
		}
	}
	if err := count("cast_credits", cast, MaxCastCredits); err != nil {
		return err
	}
	return count("crew_credits", crew, MaxCrewCredits)
}

func validateCreditCandidates(input []ScreenCreditCandidate) error {
	ordinals := make(map[int64]struct{}, len(input))
	remaining := maxCreditPolicyBytes
	for _, c := range input {
		if c.Class != Cast && c.Class != Crew {
			return invalid("credit_class")
		}
		if c.Billing < 0 || c.SourceOrdinal < 0 {
			return invalid("credit_order")
		}
		if _, exists := ordinals[c.SourceOrdinal]; exists {
			return invalid("credit_source_ordinal")
		}
		ordinals[c.SourceOrdinal] = struct{}{}
		for _, s := range []string{c.CreditID, c.Role, c.Department} {
			if len(s) > remaining {
				return &PolicyError{Field: "credit_policy_bytes", Limit: maxCreditPolicyBytes, Cause: ErrCap}
			}
			remaining -= len(s)
		}
	}
	return nil
}

func sourceOrder(a, b ScreenCreditCandidate) int {
	if order := cmp.Compare(a.SourceOrdinal, b.SourceOrdinal); order != 0 {
		return order
	}
	return strings.Compare(a.CreditID, b.CreditID)
}

// Frozen keyCrewRank: exact existing screen-credit priority, supplied by EN.
func crewRank(c ScreenCreditCandidate) int {
	d, j := strings.ToLower(strings.TrimSpace(c.Department)), strings.ToLower(strings.TrimSpace(c.Role))
	switch {
	case j == "director" || j == "series director":
		return 0
	case d == "writing" || strings.Contains(j, "screenplay") || strings.Contains(j, "writer"):
		return 1
	case d == "creator" || strings.Contains(j, "creator") || strings.Contains(j, "created by"):
		return 2
	case strings.Contains(j, "composer"):
		return 3
	case j == "producer" || j == "executive producer":
		return 4
	default:
		return 5
	}
}
