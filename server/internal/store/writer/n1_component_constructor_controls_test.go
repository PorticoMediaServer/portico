package writer

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type n1ConstructorWire struct {
	Sequence int                  `json:"sequence"`
	Kind     string               `json:"kind"`
	CaseID   string               `json:"case_id"`
	OfferID  string               `json:"offer_id"`
	Offer    *n1ConstructorOffer  `json:"constructor_offer"`
	Result   *n1ConstructorResult `json:"constructor_result"`
}

func n1ReadConstructorWire(t *testing.T, path string) []n1ConstructorWire {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []n1ConstructorWire
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var record n1ConstructorWire
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record.Sequence != i+1 || record.CaseID == "" || record.OfferID == "" {
			t.Fatal("missing constructor-only identity/order", record)
		}
		for _, field := range []string{"physical_attempt_ordinal", "owner_clock", "commit_ns", "hold_ns", "original_reference"} {
			if strings.Contains(line, `"`+field+`"`) {
				t.Fatal("constructor probe acquired physical/receipt vocabulary", field)
			}
		}
		records = append(records, record)
	}
	return records
}

func n1CheckConstructorOffer(t *testing.T, record n1ConstructorWire, ordinal int) {
	t.Helper()
	offer := record.Offer
	if record.Kind != "constructor_offer" || offer == nil || offer.CaseID != record.CaseID || offer.OfferID != record.OfferID || offer.Ordinal != ordinal || offer.CapBytes != maxCanonicalRequestBytes || offer.OfferedBytes != uint64(maxCanonicalRequestBytes+ordinal-1) || offer.MeasureSource == "" || offer.ChangedFieldBytes <= 0 || offer.OfferedNS == nil {
		t.Fatal("offer lost exact cap/source", record)
	}
}

func TestN1ConstructorBoundaryWire(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in constructor-only wire control")
	}
	stream := n1ControlStream(t)
	proof := n1ConstructorMAX(t, stream)
	records := n1ReadConstructorWire(t, stream.file.Name())
	if !proof.Complete || proof.Offers != 2 || proof.Results != 2 || len(records) != 4 {
		t.Fatal("constructor census incomplete", proof)
	}
	n1CheckConstructorOffer(t, records[0], 1)
	n1CheckConstructorOffer(t, records[2], 2)
	for _, i := range []int{1, 3} {
		result := records[i].Result
		if records[i].Kind != "constructor_result" || result == nil || records[i].CaseID != proof.CaseID || records[i].OfferID != records[i-1].OfferID || !reflect.DeepEqual(result.Offer, *records[i-1].Offer) || result.StartedNS == nil || result.ReturnedNS == nil || *result.StartedNS < *result.Offer.OfferedNS || *result.ReturnedNS < *result.StartedNS {
			t.Fatal("actual result not joined to its own offer", records[i])
		}
	}
	accepted, rejected := records[1].Result, records[3].Result
	if !accepted.ValuePresent || accepted.Error != "" || accepted.InputDigest == nil || *accepted.InputDigest != proof.Reference.RequestDigest || accepted.PolicyLimit != nil {
		t.Fatal("accepted constructor result lost")
	}
	if rejected.ValuePresent || rejected.Error == "" || rejected.InputDigest != nil || rejected.PolicyField != "canonical_request_bytes" || rejected.PolicyLimit == nil || *rejected.PolicyLimit != 0 {
		t.Fatal("rejected constructor borrowed accepted receipt/digest or defaulted actual error limit")
	}
	if records[0].OfferID == records[2].OfferID || *accepted.ReturnedNS > *records[2].Offer.OfferedNS {
		t.Fatal("next probe preceded persisted actual result")
	}
}

func TestN1ConstructorInterruptedChild(t *testing.T) {
	path := os.Getenv("PORTICO_TEST_N1_CONSTRUCTOR_PREFIX")
	if !*n1EnabledFlag || path == "" {
		return
	}
	file, err := openFirstLightOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	stream := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"process_epoch": "CONSTRUCTOR_PREFIX_CONTROL"}}
	n1ConstructorMAX(t, stream, 2) // test-only Goexit AFTER persisted +1 offer, BEFORE its constructor
	t.Fatal("interruption control did not stop")
}

func TestN1ConstructorInterruptedPrefix(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in constructor-only interruption control")
	}
	path := filepath.Join(t.TempDir(), "constructor-prefix.ndjson")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestN1ConstructorInterruptedChild$", "-n1-component")
	cmd.Env = append(os.Environ(), "PORTICO_TEST_N1_CONSTRUCTOR_PREFIX="+path)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil || err == nil || !strings.Contains(string(output), "injected constructor interruption after persisted offer") {
		t.Fatal("negative control did not stop at the intended boundary", err, ctx.Err(), string(output))
	}
	records := n1ReadConstructorWire(t, path)
	if len(records) != 3 {
		t.Fatal("interrupted constructor prefix lost", len(records))
	}
	n1CheckConstructorOffer(t, records[0], 1)
	n1CheckConstructorOffer(t, records[2], 2)
	if records[1].Kind != "constructor_result" || records[1].Result == nil || !records[1].Result.ValuePresent || records[1].Result.Error != "" || records[1].OfferID != records[0].OfferID || records[2].CaseID != records[0].CaseID {
		t.Fatal("completed first boundary vanished or rejected result invented")
	}
}
