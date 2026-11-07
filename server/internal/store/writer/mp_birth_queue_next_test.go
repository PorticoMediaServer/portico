package writer

import (
	"strings"
	"testing"
)

func mpBirthHeaderFixture() mpPublicBirthHeader {
	return mpPublicBirthHeader{birthID: [16]byte{1}, identity: CommandIdentity{Incarnation: [16]byte{2}, OperationID: [16]byte{3}, Sequence: 1}, request: RequestDigest{4}, appliedMS: 1000, scope: mpPublicBirthScope{authority: "local", account: "a", profile: "p", member: strings.Repeat("1", 32), profileLifetime: strings.Repeat("2", 32), installation: strings.Repeat("3", 32), root: strings.Repeat("r", 22), family: "f", device: "d", registration: 1, issuedMS: 900, keyDigest: [32]byte{5}}}
}
func TestMPQueueBirthPreservesOrderedSnapshotCohort(t *testing.T) {
	v := mpQueueBirthReceipt{header: mpBirthHeaderFixture(), rootIncarnation: [16]byte{6}, effect: mpQueueBirthEffect{result: mpQueueEffect{queue: "queue", revision: 1, count: 3, changed: true}, snapshots: []mpQueueSnapshotBirthResult{{id: "s1", sourceRevision: "r1", count: 1}, {id: "s2", sourceRevision: "r2", count: 2}}}}
	d, err := mpQueueBirthDigest(v)
	if err != nil {
		t.Fatal(err)
	}
	changed := v
	changed.effect.snapshots = []mpQueueSnapshotBirthResult{v.effect.snapshots[1], v.effect.snapshots[0]}
	other, err := mpQueueBirthDigest(changed)
	if err != nil || other == d {
		t.Fatal("lost ordered birth result", err)
	}
	changed = v
	changed.header.scope.profileLifetime = strings.Repeat("4", 32)
	other, err = mpQueueBirthDigest(changed)
	if err != nil || other == d {
		t.Fatal("lost original birth lifetime", err)
	}
	changed = v
	changed.rootIncarnation = changed.header.identity.Incarnation
	if _, err = mpQueueBirthDigest(changed); err == nil {
		t.Fatal("birth reused native root incarnation")
	}
	changed = v
	changed.effect.result.count++
	if _, err = mpQueueBirthDigest(changed); err == nil {
		t.Fatal("lost exact snapshot count")
	}
}
func TestMPNextBirthPreservesDistinctPredecessor(t *testing.T) {
	p := mpNextPrepared{token: "next", queue: "queue", device: "d", family: "f", session: "logical", sessionState: "playing", currentEntry: "s-0", entry: "s-1", media: "new-media", sourceRevision: "revision", state: "prepared", predecessorMedia: "old-media", sessionGeneration: 4, queueRevision: 5, custody: 6, nextPosition: 1, nextLap: 2, item: 3, mediaGeneration: 7, preparedRevision: 8, predecessorMediaGeneration: 9, expires: 2000, created: 1000}
	v := mpNextBirthReceipt{header: mpBirthHeaderFixture(), rootIncarnation: [16]byte{6}, presentationURL: "/v1/media/grant", effect: mpNextBirthEffect{prepared: p, result: mpQueueEffect{queue: p.queue, session: p.session, token: p.token, entry: p.entry, revision: p.queueRevision, custody: p.custody, position: p.nextPosition, lap: p.nextLap, changed: true}}}
	d, err := mpNextBirthDigest(v)
	if err != nil {
		t.Fatal(err)
	}
	changed := v
	changed.effect.prepared.predecessorMediaGeneration++
	other, err := mpNextBirthDigest(changed)
	if err != nil || other == d {
		t.Fatal("lost predecessor physical generation", err)
	}
	changed = v
	changed.effect.prepared.mediaGeneration++
	other, err = mpNextBirthDigest(changed)
	if err != nil || other == d {
		t.Fatal("lost successor physical generation", err)
	}
	changed = v
	changed.effect.prepared.family = "foreign"
	if _, err = mpNextBirthDigest(changed); err == nil {
		t.Fatal("birth family detached from original scope")
	}
}
