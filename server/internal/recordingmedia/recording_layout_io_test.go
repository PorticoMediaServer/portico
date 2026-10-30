//go:build linux || darwin

package recordingmedia

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/artifactlease"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func layoutFixture(t *testing.T) (string, layoutBinding) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "first"}
	id := strings.Repeat("a", 64)
	s, err := mediaartifact.New(filepath.Join(root, owner.Key(), "captures", id))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	w, err := s.Begin(1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("validated closed recording bytes")); err != nil {
		t.Fatal(err)
	}
	object, err := w.Seal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.ObjectPath(object)
	r, err := openLayoutDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rootID, err := storage.FileObjectIdentity(r.file)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sourceID, err := storage.FileObjectIdentity(f)
	if err != nil {
		t.Fatal(err)
	}
	return root, layoutBinding{OwnerKey: owner.Key(), RecordingID: id, ArtifactKey: id + ".1", RootIdentity: rootID, SourceIdentity: sourceID, Source: owner.Key() + "/captures/" + id + "/objects/" + object.Digest, Target: "Example/Season 03/Example S03E07.ts", Object: object}
}
func TestLayoutAtomicLinkRetiresOnlyExactOccurrence(t *testing.T) {
	root, b := layoutFixture(t)
	ctx := context.Background()
	out, err := linkLayout(ctx, root, b)
	if err != nil {
		t.Fatal(err)
	}
	if out.TargetIdentity != b.SourceIdentity {
		t.Fatal("missing linked identity")
	}
	a, _ := os.Stat(filepath.Join(root, b.Source))
	z, _ := os.Stat(filepath.Join(root, b.Target))
	if !os.SameFile(a, z) {
		t.Fatal("publication copied media bytes")
	}
	if _, err = linkLayout(ctx, root, out); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	b.allowExisting = true
	if _, err = linkLayout(ctx, root, b); err != nil {
		t.Fatalf("prepared recovery: %v", err)
	}
	f, err := os.Open(filepath.Join(root, b.Target))
	if err != nil {
		t.Fatal(err)
	}
	if err = artifactlease.Shared(f); err != nil {
		t.Fatal(err)
	}
	if err = removeLayout(ctx, root, out); !errors.Is(err, mediaartifact.ErrLeased) {
		t.Fatalf("active reader removal: %v", err)
	}
	f.Close()
	if err = removeLayout(ctx, root, out); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, b.Target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target remains: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, b.Source)); err != nil {
		t.Fatalf("source custody lost: %v", err)
	}
	if err = removeLayout(ctx, root, out); err != nil {
		t.Fatalf("delete retry: %v", err)
	}
}
func TestLayoutRefusesUnownedEqualByteCollision(t *testing.T) {
	root, b := layoutFixture(t)
	path := filepath.Join(root, b.Target)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(root, b.Source))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, bytes, 0400); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if _, err = linkLayout(context.Background(), root, b); !errors.Is(err, dvr.ErrRecordingLayoutCollision) {
		t.Fatalf("adopted collision: %v", err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) {
		t.Fatal("overwrote another recording")
	}
	b.TargetIdentity = b.SourceIdentity
	if err = removeLayout(context.Background(), root, b); !errors.Is(err, mediaartifact.ErrIdentity) {
		t.Fatalf("deleted unowned equal bytes: %v", err)
	}
}
func TestLayoutRejectsSymlinksAndEscapesWithoutTouchingOutside(t *testing.T) {
	for _, kind := range []string{"root", "ancestor", "leaf", "source"} {
		t.Run(kind, func(t *testing.T) {
			root, b := layoutFixture(t)
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(sentinel, []byte("untouched"), 0400); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "root":
				link := filepath.Join(t.TempDir(), "root")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				root = link
			case "ancestor":
				if err := os.Symlink(outside, filepath.Join(root, "Example")); err != nil {
					t.Fatal(err)
				}
			case "leaf":
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, b.Target)), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(sentinel, filepath.Join(root, b.Target)); err != nil {
					t.Fatal(err)
				}
			case "source":
				if err := os.Remove(filepath.Join(root, b.Source)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(sentinel, filepath.Join(root, b.Source)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := linkLayout(context.Background(), root, b); err == nil {
				t.Fatal("followed symlink")
			}
			b.TargetIdentity = b.SourceIdentity
			if err := removeLayout(context.Background(), root, b); err == nil && kind != "source" {
				t.Fatal("delete followed symlink")
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "untouched" {
				t.Fatal("outside changed")
			}
		})
	}
	for _, target := range []string{"../escape", "/absolute", "owner/../escape", "owner//leaf", "owner\\leaf", "owner/\x00leaf", "owner/leaf\n"} {
		t.Run(target, func(t *testing.T) {
			root, b := layoutFixture(t)
			b.Target = target
			if _, err := linkLayout(context.Background(), root, b); err == nil {
				t.Fatal("accepted escape")
			}
		})
	}
}
func TestLayoutReceiptBindsRootSourceOwnerAndTarget(t *testing.T) {
	for _, kind := range []string{"owner", "recording", "generation", "root", "source", "digest", "size", "target"} {
		t.Run(kind, func(t *testing.T) {
			root, b := layoutFixture(t)
			linked, err := linkLayout(context.Background(), root, b)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "owner":
				linked.OwnerKey = strings.Repeat("b", 64)
			case "recording":
				linked.RecordingID = strings.Repeat("b", 64)
			case "generation":
				linked.ArtifactKey = linked.RecordingID + ".01"
			case "root":
				linked.RootIdentity = "replaced"
			case "source":
				linked.SourceIdentity = "replaced"
			case "digest":
				linked.Object.Digest = strings.Repeat("b", 64)
			case "size":
				linked.Object.Size++
			case "target":
				linked.TargetIdentity = "replaced"
			}
			if _, err = linkLayout(context.Background(), root, linked); err == nil {
				t.Fatal("publication accepted bad receipt")
			}
			if err = removeLayout(context.Background(), root, linked); err == nil {
				t.Fatal("deletion accepted bad receipt")
			}
			if _, err = os.Stat(filepath.Join(root, b.Target)); err != nil {
				t.Fatal("bad receipt removed target")
			}
		})
	}
}
func TestLayoutCancellationNeverPublishesOrDeletes(t *testing.T) {
	root, b := layoutFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := linkLayout(ctx, root, b); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, b.Target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel published")
	}
	linked, err := linkLayout(context.Background(), root, b)
	if err != nil {
		t.Fatal(err)
	}
	if err = removeLayout(ctx, root, linked); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, b.Target)); err != nil {
		t.Fatal("cancel deleted")
	}
}
func TestLayoutTargetSubstitutionAndRootReplacementStayUntouched(t *testing.T) {
	root, b := layoutFixture(t)
	linked, err := linkLayout(context.Background(), root, b)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, b.Target)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, b.Source))
	if err = os.WriteFile(path, data, 0400); err != nil {
		t.Fatal(err)
	}
	if err = removeLayout(context.Background(), root, linked); !errors.Is(err, mediaartifact.ErrIdentity) {
		t.Fatalf("deleted replacement: %v", err)
	}
	if err = os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root + "-old") })
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = removeLayout(context.Background(), root, linked); !errors.Is(err, mediaartifact.ErrIdentity) {
		t.Fatalf("accepted replaced root: %v", err)
	}
}

func TestLayoutPinnedParentCannotBeRedirected(t *testing.T) {
	root, b := layoutFixture(t)
	if _, err := linkLayout(context.Background(), root, b); err != nil {
		t.Fatal(err)
	}
	r, err := openLayoutDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p, _, err := r.parent(b.Target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	original := filepath.Dir(filepath.Join(root, b.Target))
	if err = os.Rename(original, original+"-retained"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	if err = sameLayoutParent(root, r, b.Target, p); !errors.Is(err, mediaartifact.ErrIdentity) {
		t.Fatalf("accepted replaced ancestor: %v", err)
	}
}
func TestLayoutRejectsCorruptClosedBytesAndNonRegularTarget(t *testing.T) {
	t.Run("corrupt-source", func(t *testing.T) {
		root, b := layoutFixture(t)
		path := filepath.Join(root, b.Source)
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Repeat("x", int(b.Object.Size))), 0400); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0400); err != nil {
			t.Fatal(err)
		}
		if _, err := linkLayout(context.Background(), root, b); !errors.Is(err, mediaartifact.ErrIdentity) {
			t.Fatalf("accepted corruption: %v", err)
		}
	})
	t.Run("directory-target", func(t *testing.T) {
		root, b := layoutFixture(t)
		if err := os.MkdirAll(filepath.Join(root, b.Target), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := linkLayout(context.Background(), root, b); err == nil {
			t.Fatal("accepted directory")
		}
	})
}

func TestLayoutMeasureCountsPhysicalPayloadOnceAcrossAllNames(t *testing.T) {
	root, b := layoutFixture(t)
	linked, err := linkLayout(context.Background(), root, b)
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(root, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	before, err := d.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.UsedBytes != b.Object.Size {
		t.Fatalf("aliases inflated physical use: got%d want%d", before.UsedBytes, b.Object.Size)
	}
	if err = removeLayout(context.Background(), root, linked); err != nil {
		t.Fatal(err)
	}
	after, err := d.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.UsedBytes != before.UsedBytes {
		t.Fatal("reported reclaimed bytes while capture retains inode")
	}
	if err = os.Remove(filepath.Join(root, b.Source)); err != nil {
		t.Fatal(err)
	}
	final, err := d.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if final.UsedBytes != 0 {
		t.Fatal("final payload bytes not reclaimed")
	}
}

func TestRecordingLayoutDriverPreparesBeforeIOAndRecoversExactLink(t *testing.T) {
	root, b := layoutFixture(t)
	driver, err := New(root, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "first"}
	in := dvr.CaptureRequest{Owner: owner, Recording: dvr.Recording{ID: b.RecordingID}, ArtifactKey: b.ArtifactKey}
	result := dvr.CaptureResult{CaptureCheckpoint: dvr.CaptureCheckpoint{Object: b.Object, WriterRetired: true}, Root: filepath.Join(root, owner.Key()), Path: filepath.Join(root, b.Source)}
	prepared, err := driver.PrepareRecordingLayout(context.Background(), in, result, b.Target, 7)
	if err != nil || !prepared.Valid() {
		t.Fatal(prepared, err)
	}
	if _, err = os.Stat(filepath.Join(root, b.Target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("prepare wrote target")
	}
	linked, err := driver.LinkRecordingLayout(context.Background(), prepared, false)
	if err != nil || linked.Phase != "linked" || linked.TargetIdentity != prepared.SourceIdentity {
		t.Fatal(linked, err)
	}
	// A brand-new reservation cannot adopt a preexisting path, even a hard link
	// to the same payload. Only replay of the durable prior reservation may.
	if _, err = driver.LinkRecordingLayout(context.Background(), prepared, false); !errors.Is(err, dvr.ErrRecordingLayoutCollision) {
		t.Fatal("new reservation adopted existing same inode", err)
	}
	if _, err = driver.LinkRecordingLayout(context.Background(), prepared, true); err != nil {
		t.Fatal("prior prepared crash recovery failed", err)
	}
	if err = driver.RemoveRecordingLayout(context.Background(), prepared); err != nil {
		t.Fatal("prepared crash cleanup failed", err)
	}
	if _, err = os.Stat(filepath.Join(root, b.Source)); err != nil {
		t.Fatal("cleanup lost source", err)
	}
	result.WriterRetired = false
	if _, err = driver.PrepareRecordingLayout(context.Background(), in, result, b.Target, 7); err == nil {
		t.Fatal("accepted unretired writer")
	}
}

func TestLayoutConcurrentOwnersNeverReplaceOrAdoptEachOther(t *testing.T) {
	root, first := layoutFixture(t)
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "second"}
	id := strings.Repeat("b", 64)
	store, err := mediaartifact.New(filepath.Join(root, owner.Key(), "captures", id))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer, err := store.Begin(1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("a different owner's sealed bytes")); err != nil {
		t.Fatal(err)
	}
	object, err := writer.Seal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source := owner.Key() + "/captures/" + id + "/objects/" + object.Digest
	file, err := os.Open(filepath.Join(root, source))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := storage.FileObjectIdentity(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	second := layoutBinding{OwnerKey: owner.Key(), RecordingID: id, ArtifactKey: id + ".1", RootIdentity: first.RootIdentity, SourceIdentity: identity, Source: source, Target: first.Target, Object: object}
	type outcome struct {
		binding layoutBinding
		err     error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for _, binding := range []layoutBinding{first, second} {
		go func(b layoutBinding) {
			<-start
			out, e := linkLayout(context.Background(), root, b)
			results <- outcome{out, e}
		}(binding)
	}
	close(start)
	successes, collisions := 0, 0
	var winner layoutBinding
	for i := 0; i < 2; i++ {
		out := <-results
		if out.err == nil {
			successes++
			winner = out.binding
		} else if errors.Is(out.err, dvr.ErrRecordingLayoutCollision) {
			collisions++
		} else {
			t.Fatal(out.err)
		}
	}
	if successes != 1 || collisions != 1 {
		t.Fatal("concurrent custody crossed owners", successes, collisions)
	}
	if err = removeLayout(context.Background(), root, winner); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []layoutBinding{first, second} {
		if _, err = os.Stat(filepath.Join(root, binding.Source)); err != nil {
			t.Fatal("other occurrence source lost", err)
		}
	}
}
