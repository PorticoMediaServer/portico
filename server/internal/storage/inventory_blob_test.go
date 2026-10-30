package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestInventoryBlobContextRetainsMediaTargetSeparatelyFromCandidate(t *testing.T) {
	root := t.TempDir()
	client := New("")
	facts := InventoryBlobAdmission{Purpose: InventoryBlobAnalysisSidecar, JobID: "actual-job", LibraryID: "library", LibrarySourceID: "folder", RootIncarnation: "incarnation", RootIdentity: "root", SourceGeneration: 3, PolicyRevision: 7, ObjectID: "media-object", SourceRevision: "catalog-media-revision", Operation: "local_metadata"}
	ctx := WithScanInput(context.Background(), client, "folder", InventoryRequest{Root: root, RootIdentity: "root"}, facts)
	facts.ObjectID, facts.Operation = "replacement", "decode"
	candidate := filepath.Join(root, "one.nfo")
	request, input, err := inventoryReadRequest(ctx, candidate)
	if err != nil || input == nil || input.blob == nil || input.blob.ObjectID != "media-object" || input.blob.Operation != "local_metadata" || request.RelativePath != "one.nfo" || request.RootIdentity != "root" || input.blob.ListingID != "" {
		t.Fatal("captured target changed or aliased the candidate", request, input, err)
	}
	if _, _, err = inventoryReadRequest(ctx, filepath.Join(root, "..", "outside.nfo")); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("blob facts allowed a root escape", err)
	}
}

func TestInventoryBlobContextKeepsExactIgnoreListingAndRejectsAmbiguity(t *testing.T) {
	root := t.TempDir()
	client := New("")
	member := Snapshot{Path: filepath.Join(root, ".plexignore"), Size: 12, ChangeToken: "transport-observation", ObjectIdentity: "remote-member"}
	facts := InventoryBlobAdmission{Purpose: InventoryBlobIgnore, JobID: "inventory-job", ListingID: "ready-listing", Member: member}
	ctx := WithScanInput(context.Background(), client, "folder", InventoryRequest{Root: root}, facts)
	facts.Member.ChangeToken, facts.ListingID = "replacement", "new-listing"
	_, input, err := inventoryReadRequest(ctx, member.Path)
	if err != nil || input == nil || input.blob == nil || input.blob.Member != member || input.blob.ListingID != "ready-listing" || input.blob.ObjectID != "" || input.blob.Operation != "" {
		t.Fatal("ignore snapshot changed or acquired media authority", input, err)
	}
	ambiguous := WithScanInput(context.Background(), client, "folder", InventoryRequest{Root: root}, facts, facts)
	if _, _, err = inventoryReadRequest(ambiguous, member.Path); !errors.Is(err, ErrRemoteConfig) {
		t.Fatal("ambiguous invocation selected one grant", err)
	}
	without := WithScanInput(context.Background(), client, "folder", InventoryRequest{Root: root})
	if _, input, err = inventoryReadRequest(without, member.Path); err != nil || input == nil || input.blob != nil {
		t.Fatal("local/root context fabricated blob facts", input, err)
	}
}
