package writer

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
)

// Three named domain caps, not a claim that every unconstrained text is MAX.
// Reuse SC's observation revision and immutable constructor, never patch a hash.
func n1DomainCapCommand(c *inventoryAcceptCommand) (*inventoryAcceptCommand, error) {
	r := inventoryTestClone(c.request)
	f := &r.files[0]
	f.objectIdentity = strings.Repeat("&", 8192)
	f.changeToken = strings.Repeat("\x00", 4096)
	f.title = strings.Repeat("<", MaxTitleCharacters)
	f.sortTitle = f.title
	evidence := mediasource.InventoryEvidence{Kind: "local", Scope: r.source.id + ":" + r.source.incarnation,
		Object: objectidentity.StableObjectIdentity(f.objectIdentity, r.source.rootIdentity), Size: f.size, ModifiedNS: f.modifiedNS, ChangeToken: f.changeToken}
	var err error
	f.revision, err = evidence.Revision()
	if err != nil {
		return nil, err
	}
	return newInventoryAcceptCommand(c.identity, r)
}

// Count with the ACTUAL typed encoder. The fixed prefix lengths mirror its
// codec/kind/version/identity header; acceptance and exact+1 rejection below
// independently guard this test-only count against a codec change.
func n1CanonicalBytes(c *inventoryAcceptCommand) (uint64, error) {
	e := requestEncoder{h: sha256.New(), path: map[uintptr]bool{}}
	e.bytes([]byte(canonicalRequestCodec))
	e.bytes(make([]byte, 4+len(c.identity.Incarnation)+8+len(c.identity.OperationID)))
	e.value(reflect.ValueOf(c.request), 0)
	return e.written, e.err
}

type n1ConstructorOffer struct {
	CaseID            string          `json:"case_id"`
	OfferID           string          `json:"offer_id"`
	Ordinal           int             `json:"constructor_ordinal"`
	Boundary          string          `json:"boundary"`
	Identity          CommandIdentity `json:"input_identity"`
	CapBytes          uint64          `json:"canonical_limit_bytes"`
	OfferedBytes      uint64          `json:"offered_canonical_bytes"`
	MeasureSource     string          `json:"measure_source"`
	ChangedField      string          `json:"changed_field"`
	ChangedFieldBytes int             `json:"changed_field_bytes"`
	OfferedNS         *int64          `json:"offered_ns"`
}

type n1ConstructorResult struct {
	Offer        n1ConstructorOffer `json:"offer"`
	StartedNS    *int64             `json:"constructor_started_ns"`
	ReturnedNS   *int64             `json:"constructor_returned_ns"`
	ValuePresent bool               `json:"value_present"`
	InputDigest  *RequestDigest     `json:"returned_input_digest"`
	Error        string             `json:"actual_constructor_error"`
	PolicyField  string             `json:"actual_policy_field"`
	PolicyLimit  *int               `json:"actual_policy_limit"`
}

type n1ConstructorCell struct {
	CaseID               string            `json:"case_id"`
	Offers               int               `json:"constructor_offers"`
	Results              int               `json:"constructor_results"`
	Cell                 string            `json:"cell"`
	Reference            *ReceiptReference `json:"accepted_constructor_reference"`
	Input                *n1ProfileInput   `json:"input_facts"`
	CanonicalBytes       *uint64           `json:"canonical_bytes"`
	Limit                int               `json:"canonical_limit_bytes"`
	LimitPlusOneRejected bool              `json:"exact_limit_plus_one_rejected"`
	LimitPlusOneError    string            `json:"exact_limit_plus_one_error"`
	ForecastBytes        *uint64           `json:"allocation_free_forecast_bytes"`
	PreparationBytes     uint64            `json:"unchanged_preparation_bytes"`
	PhysicalExecution    bool              `json:"physical_execution"`
	CapacityGate         string            `json:"capacity_gate"`
	StartedNS            *int64            `json:"started_ns"`
	ReturnedNS           *int64            `json:"returned_ns"`
	Error                string            `json:"error"`
	Complete             bool              `json:"constructor_proof_complete"`
}

// Pure source-bound constructor/refusal proof. It opens no database, takes no
// grant and emits no fabricated physical HOLD/attempt/commit. Oversized physical
// preparation remains gated by W's unchanged 4MiB partition.
func n1ConstructorMAX(t *testing.T, stream *n1Stream, stopAfterOffer ...int) *n1ConstructorCell {
	origin := stream.origin
	t.Helper()
	out := &n1ConstructorCell{Cell: "constructor_cap_refusal", Limit: maxCanonicalRequestBytes, PreparationBytes: 4 << 20,
		CapacityGate: "physical canonical-MAX execution CLOSED: forecast exceeds unchanged preparation; no owner recipe/partition increase", StartedNS: n1NS(origin, time.Now())}
	epoch, ok := stream.base["process_epoch"].(string)
	if !ok || epoch == "" || len(stopAfterOffer) > 1 {
		out.Error = "invalid constructor-only source epoch/control"
		return out
	}
	out.CaseID = epoch + "/constructor/canonical-cap"
	defer func() { out.ReturnedNS = n1NS(origin, time.Now()) }()
	c, err := n1DomainCapCommand(inventoryFreshRequest(t))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	// Grow only one legal text: no Cartesian sweep and no search of timing data.
	used, err := n1CanonicalBytes(c)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	seedCanonicalBytes, seedFieldBytes := used, len(c.request.files[0].metadataLanguage)
	// This observer always calls SC's actual constructor. No constructor callback
	// or factory is accepted. Sync the offer BEFORE entry and the actual result
	// BEFORE any later probe/work, so interruption leaves a joinable raw prefix.
	construct := func(boundary string, ordinal int, request inventoryAcceptRequest) (*inventoryAcceptCommand, error) {
		offer := n1ConstructorOffer{CaseID: out.CaseID, OfferID: fmt.Sprintf("%s/%d/%s", out.CaseID, ordinal, boundary), Ordinal: ordinal, Boundary: boundary, Identity: c.identity,
			CapBytes: maxCanonicalRequestBytes, OfferedBytes: seedCanonicalBytes + uint64(len(request.files[0].metadataLanguage)-seedFieldBytes),
			MeasureSource: "actual requestEncoder(seed) plus exact byte growth of one unchanged typed metadataLanguage field; source hashes in start", ChangedField: "files[0].metadataLanguage", ChangedFieldBytes: len(request.files[0].metadataLanguage), OfferedNS: n1NS(origin, time.Now())}
		fields := map[string]any{"case_id": offer.CaseID, "offer_id": offer.OfferID, "constructor_offer": offer}
		if err := stream.append("constructor_offer", fields); err != nil {
			return nil, err
		}
		out.Offers++
		if len(stopAfterOffer) == 1 && stopAfterOffer[0] == ordinal {
			t.Fatal("injected constructor interruption after persisted offer")
		}
		started := time.Now()
		value, actualErr := newInventoryAcceptCommand(c.identity, request)
		returned := time.Now()
		result := n1ConstructorResult{Offer: offer, StartedNS: n1NS(origin, started), ReturnedNS: n1NS(origin, returned), ValuePresent: value != nil, Error: n1Error(actualErr)}
		if value != nil {
			digest := value.digest
			result.InputDigest = &digest
		}
		var policy *PolicyError
		if errors.As(actualErr, &policy) {
			result.PolicyField = policy.Field
			limit := policy.Limit
			result.PolicyLimit = &limit
		}
		out.Results++
		if err := stream.append("constructor_result", map[string]any{"case_id": offer.CaseID, "offer_id": offer.OfferID, "constructor_result": result}); err != nil {
			return value, errors.Join(actualErr, err)
		}
		return value, actualErr
	}
	r := inventoryTestClone(c.request)
	r.files[0].metadataLanguage += strings.Repeat("\x00", maxCanonicalRequestBytes-int(used))
	c, err = construct("at_limit", 1, r)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	used, err = n1CanonicalBytes(c)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.CanonicalBytes = &used
	out.Input = n1InputProfile(c, n1Case{"dual", "current", "absent", "canonical-max"})
	q, err := c.receiptRequest()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Reference = &q.Receipt
	required, err := inventoryFreshRequiredBytes(c)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.ForecastBytes = &required
	r = inventoryTestClone(c.request)
	r.files[0].metadataLanguage += "\x00"
	next, err := construct("limit_plus_one", 2, r)
	out.LimitPlusOneError = n1Error(err)
	var policy *PolicyError
	out.LimitPlusOneRejected = next == nil && errors.As(err, &policy) && policy.Field == "canonical_request_bytes"
	out.Complete = used == maxCanonicalRequestBytes && out.Offers == 2 && out.Results == 2 && stream.err == nil && out.LimitPlusOneRejected && required > out.PreparationBytes
	if !out.Complete {
		out.Error = "canonical cap/adjacent rejection or unchanged preparation gate not proven"
	}
	return out
}

func TestN1ConstructorMAXBoundary(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in bounded 32MiB constructor control")
	}
	stream := n1ControlStream(t)
	out := n1ConstructorMAX(t, stream)
	if !out.Complete || out.Error != "" || out.PhysicalExecution || out.CanonicalBytes == nil || *out.CanonicalBytes != maxCanonicalRequestBytes || out.ReturnedNS == nil {
		t.Fatal("MAX proof incomplete", out)
	}
	if out.Input.LegalMAX != nil || out.Input.LegalMIN != nil || out.Reference == nil || out.Input.Escaping["text_26"] == 0 {
		t.Fatal("single cap promoted to whole MAX or escaping lost")
	}
}

func TestN1DomainCapFullPrefix(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in native full-prefix control")
	}
	stream := n1ControlStream(t)
	now := time.Now().Add(time.Hour)
	summary := &n1SteadySummary{}
	runN1Steady(t, t.Context(), stream, 1, 0, "native-counters", n1SteadyControls{fullPrefix: true, clock: func() time.Time { now = now.Add(time.Microsecond); return now }}, summary)
	records := n1SteadyControlWire(t, stream)
	if !summary.Complete || summary.FullPrefix == nil || !*summary.FullPrefix || len(records) != 3 || summary.KnownPhysicalTurns != 3 || summary.Measured != (n1OriginalCensus{1, 1, 1}) || summary.Warmup != (n1OriginalCensus{}) {
		t.Fatal("full-prefix census/join incomplete", summary)
	}
	for i, record := range records {
		if record.Case != n1FullPrefixCase || record.Continuity.SteadyEligible || record.Reference == nil || *record.Reference != *records[0].Reference || record.OriginalID != records[0].OriginalID || record.PhysicalAttemptOrdinal == nil || *record.PhysicalAttemptOrdinal != i+1 || record.InputProfile.TextRunes["text_20"] != MaxTitleCharacters {
			t.Fatal("original/role/cap/ordinal mismatch", record)
		}
		if i < 2 && record.FullPrefix != nil {
			t.Fatal("premature prefix claim")
		}
	}
	ack := records[2]
	if ack.FullPrefix == nil || !*ack.FullPrefix || ack.Profile.FullPrefix == nil || !*ack.Profile.FullPrefix || ack.CurrentSnapshot == nil || ack.AcknowledgedThrough == nil || *ack.AcknowledgedThrough != ack.Reference.Identity.Sequence {
		t.Fatal("actual ACK/current snapshot absent")
	}
	if records[0].Result == nil || records[0].Result.ResultCount != 1 || records[1].HistoricalResult == nil || records[1].HistoricalDigest == nil {
		t.Fatal("effects or saved-original resolution absent")
	}
}

func TestN1DomainCapFailedPrefixStaysIncomplete(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in failed native full-prefix control")
	}
	stream := n1ControlStream(t)
	var query string
	for _, def := range inventoryCommandDefinitions() {
		if def.ID == scanReceiptResults {
			query = def.SQL
		}
	}
	now := time.Now().Add(time.Hour)
	summary := &n1SteadySummary{}
	runN1Steady(t, t.Context(), stream, 1, 0, "timing", n1SteadyControls{fullPrefix: true, clock: func() time.Time { now = now.Add(time.Microsecond); return now }, failOriginal: 1, failRole: "resolve", failQuery: query}, summary)
	records := n1SteadyControlWire(t, stream)
	if summary.Complete || summary.FullPrefix != nil || summary.StopReason == "" || len(records) != 2 || records[1].Error == "" || records[1].Outcome != "rolled_back" || records[1].FullPrefix != nil || *records[0].Reference != *records[1].Reference {
		t.Fatal("failed source prefix dropped/promoted", summary)
	}
}
