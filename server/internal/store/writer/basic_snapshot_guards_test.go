package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBasicOriginalSnapshotIndependentFences(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drift func(*CommandIdentity, *scanRunSnapshot, *scanSourceFacts)
	}{
		{"invalid_snapshot", func(_ *CommandIdentity, n *scanRunSnapshot, _ *scanSourceFacts) { n.run.revision = 0 }},
		{"zero_incarnation", func(i *CommandIdentity, _ *scanRunSnapshot, _ *scanSourceFacts) { i.Incarnation = [16]byte{} }},
		{"zero_operation", func(i *CommandIdentity, _ *scanRunSnapshot, _ *scanSourceFacts) { i.OperationID = [16]byte{} }},
		{"lease_owner", func(_ *CommandIdentity, n *scanRunSnapshot, _ *scanSourceFacts) { n.run.leaseOwner = 0 }},
		{"lease_generation", func(_ *CommandIdentity, n *scanRunSnapshot, _ *scanSourceFacts) { n.run.leaseGeneration = 0 }},
		{"lease_expiry", func(_ *CommandIdentity, n *scanRunSnapshot, _ *scanSourceFacts) { n.run.leaseExpiresMS = 0 }},
		{"finished", func(_ *CommandIdentity, n *scanRunSnapshot, _ *scanSourceFacts) { n.run.finishedAt = "finished" }},
		{"rebound", func(_ *CommandIdentity, n *scanRunSnapshot, _ *scanSourceFacts) { n.run.rootRebound = 1 }},
		{"status", func(_ *CommandIdentity, n *scanRunSnapshot, _ *scanSourceFacts) { n.job.status = "queued" }},
		{"phase", func(_ *CommandIdentity, n *scanRunSnapshot, _ *scanSourceFacts) { n.run.phase = "complete" }},
		{"library_id", func(_ *CommandIdentity, _ *scanRunSnapshot, f *scanSourceFacts) { f.libraryID++ }},
		{"library_key", func(_ *CommandIdentity, _ *scanRunSnapshot, f *scanSourceFacts) { f.libraryKey = "another-library" }},
		{"source_id", func(_ *CommandIdentity, _ *scanRunSnapshot, f *scanSourceFacts) { f.source.id = "another-source" }},
		{"source_incarnation", func(_ *CommandIdentity, _ *scanRunSnapshot, f *scanSourceFacts) {
			f.source.incarnation = "another-incarnation"
		}},
		{"follow_symlinks", func(_ *CommandIdentity, _ *scanRunSnapshot, f *scanSourceFacts) { f.source.followSymlinks = true }},
		{"resolved_root", func(_ *CommandIdentity, _ *scanRunSnapshot, f *scanSourceFacts) {
			f.source.resolvedRoot = []byte("/different/media")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, n, f, _ := basicSnapshotValues()
			tc.drift(&i, &n, &f)
			got, err := basicOriginalFromSnapshot(i, n, f)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(got, basicOriginalScope{}) {
				t.Fatal("drift released original", got, err)
			}
		})
	}
	// Both admitted phases are real positive controls: replacing either side of
	// the conjunction with OR must not turn a permitted phase into a refusal.
	for _, phase := range []string{"inventory", "verifying"} {
		i, n, f, _ := basicSnapshotValues()
		n.run.phase = phase
		got, err := basicOriginalFromSnapshot(i, n, f)
		if err != nil || got.run.phase != phase {
			t.Fatal(phase, got, err)
		}
	}
}

func TestBasicOriginalSnapshotIndependentByteCaps(t *testing.T) {
	for _, field := range []string{"raw_root", "identity_root", "text", "aggregate"} {
		t.Run(field, func(t *testing.T) {
			i, n, f, _ := basicSnapshotValues()
			switch field {
			case "raw_root":
				f.source.resolvedRoot = make([]byte, maxCanonicalRequestBytes+1)
			case "identity_root":
				f.identityRoot = make([]byte, maxCanonicalRequestBytes+1)
			case "text":
				n.job.errorText = strings.Repeat("x", maxCanonicalRequestBytes+1)
			case "aggregate":
				f.identityRoot = make([]byte, maxCanonicalRequestBytes/2)
				n.job.errorText = strings.Repeat("x", maxCanonicalRequestBytes/2)
			}
			// The valid sequence is deliberately retained; no overflow masks this cap.
			if got, err := basicOriginalFromSnapshot(i, n, f); !errors.Is(err, ErrCap) || !reflect.DeepEqual(got, basicOriginalScope{}) {
				t.Fatal(field, got, err)
			}
		})
	}
}
