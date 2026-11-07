package scanbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// Compile against W's actual draft, rather than a replacement issuer or scope.
var _ N1Preparation = (*writer.N1Preparation)(nil)

func TestEC4JournalPreservesOriginalBeforeEffects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "receipts")
	journal, err := NewReceiptJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	p, options, manifest := draftN1()
	a, err := ObserveN1(t.Context(), p, options, manifest)
	if err != nil {
		t.Fatal(err)
	}
	original := a.Original()
	if err := journal.save(original, writer.InventoryAccept, nil); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var saved journalEntry
	if err := json.Unmarshal(data, &saved); err != nil || saved.Original != original || saved.Qualification || saved.Consumed != nil {
		t.Fatal(saved, err)
	}
	if err := journal.save(original, writer.InventoryAccept, nil); !errors.Is(err, os.ErrExist) {
		t.Fatal("overwrote saved original", err)
	}
	a.blocked = os.ErrPermission
	if _, err := a.Accept(t.Context()); err == nil || p.accepted != (writer.ScanReceiptRequest{}) {
		t.Fatal("journal failure submitted", err)
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestEC4UnissuedDriverCannotMintRunOrLease(t *testing.T) {
	journal, err := NewReceiptJournal(filepath.Join(t.TempDir(), "receipts"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	var driver writer.ScanDriver
	if a, err := PrepareCreate(t.Context(), &driver, writer.ScanRunInput{LibraryID: 7, LibraryKey: "library", SourceID: "source", ScanMode: "first-import"}, journal); err == nil || a != nil {
		t.Fatal("unissued run", a, err)
	}
	if a, err := PrepareClaim(t.Context(), &driver, writer.ScanRunReference{}, journal); err == nil || a != nil {
		t.Fatal("unissued lease", a, err)
	}
}

func (p *draftPreparation) AckScan(_ context.Context, original writer.ScanReceiptRequest) (writer.AckResult, error) {
	p.events = append(p.events, "ack")
	if original != p.want {
		return writer.AckResult{}, writer.ErrConflict
	}
	return writer.AckResult{AcknowledgedThrough: original.Receipt.Identity.Sequence}, nil
}

func TestEC4N1ConsumesExactOriginalBeforeAck(t *testing.T) {
	p, options, manifest := draftN1()
	a, err := ObserveN1(t.Context(), p, options, manifest)
	if err != nil {
		t.Fatal(err)
	}
	p.want = a.Original()
	p.result = draftApplied(p.want)
	journal, err := NewReceiptJournal(filepath.Join(t.TempDir(), "receipts"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if _, err := a.AckConsumed(t.Context(), p); err == nil {
		t.Fatal("ACK before consumption")
	}
	if err := journal.save(a.Original(), writer.InventoryAccept, nil); err != nil {
		t.Fatal(err)
	}
	value, err := a.SubmitAndConsume(t.Context(), journal)
	if err != nil || value.Outcome.Reference != p.want.Receipt {
		t.Fatal(value, err)
	}
	ack, err := a.AckConsumed(t.Context(), p)
	if err != nil || ack.AcknowledgedThrough != p.want.Receipt.Identity.Sequence {
		t.Fatal(ack, err)
	}
	if !reflect.DeepEqual(p.events, []string{"snapshot", "observe", "accept", "ack"}) {
		t.Fatal(p.events)
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestEC4N1AmbiguityKeepsOriginalAndDoesNotReobserve(t *testing.T) {
	p, options, manifest := draftN1()
	a, err := ObserveN1(t.Context(), p, options, manifest)
	if err != nil {
		t.Fatal(err)
	}
	p.want = a.Original()
	p.result, p.acceptErr = draftApplied(p.want), context.DeadlineExceeded
	journal, err := NewReceiptJournal(filepath.Join(t.TempDir(), "receipts"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := journal.save(a.Original(), writer.InventoryAccept, nil); err != nil {
		t.Fatal(err)
	}
	if value, err := a.SubmitAndConsume(t.Context(), journal); !errors.Is(err, context.DeadlineExceeded) || value.Outcome.State != 0 {
		t.Fatal(value, err)
	}
	if _, err := a.AckConsumed(t.Context(), p); err == nil {
		t.Fatal("uncertain result ACKed")
	}
	p.result.Outcome.Replayed = true
	if _, err := a.ResolveAndConsume(t.Context(), p, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AckConsumed(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if p.resolved != p.want || a.Report().AppliedObserved || !reflect.DeepEqual(p.events, []string{"snapshot", "observe", "accept", "resolve", "ack"}) {
		t.Fatal(p.events, a.Report())
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestEC4LifecycleOnlyDraftDoesNotStartN1(t *testing.T) {
	journal, err := NewReceiptJournal(filepath.Join(t.TempDir(), "receipts"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	var driver writer.ScanDriver
	_, options, manifest := draftN1()
	manifest.Run = "" // only actual W create can fill this
	run, err := NewEC4Target(&driver, writer.ScanRunInput{LibraryID: 7, LibraryKey: "library", SourceID: "source", ScanMode: "first-import"}, journal, options, manifest)
	var refusal *writer.Refusal
	if run != nil || !errors.As(err, &refusal) || refusal.Code != writer.RefusalUnqualified {
		t.Fatal("lifecycle-only draft became N1 ingress", run, err)
	}
	directory, err := journal.root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	files, err := directory.ReadDir(-1)
	if err != nil || len(files) != 0 {
		t.Fatal("unready ingress wrote an original", files, err)
	}
}
