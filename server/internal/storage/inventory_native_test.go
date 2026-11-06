//go:build linux

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func nativeClient(t *testing.T) (*Client, *OperationScope) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(binary)
	owner := NewOperationScope()
	t.Cleanup(func() {
		owner.Close()
		if err := owner.Wait(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return c, owner
}

func TestNativeInventoryRealHelperPreservesBytes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root-\xff")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	names := []string{"Film-\xff.mkv", "Film-\xef\xbf\xbd.mkv", "adjacent-\x01\xff\x01.mkv"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Include issue-only output: hidden names must preserve their exact bytes too.
	if err := os.WriteFile(filepath.Join(root, ".hidden-\xff"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	c, owner := nativeClient(t)
	q := NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: 1}
	seen := map[string]bool{}
	issue := false
	for turns := 0; ; turns++ {
		if turns > 10 {
			t.Fatal("no EOF")
		}
		p, err := c.NativeInventoryPage(t.Context(), owner, "native", q)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range p.Entries {
			if !bytes.Equal(e.Path, []byte(filepath.Join(root, string(e.Name)))) {
				t.Fatalf("path changed: %x", e.Path)
			}
			seen[string(e.Name)] = true
		}
		for _, i := range p.Issues {
			if bytes.Equal(i.Path, []byte(".hidden-\xff")) {
				issue = true
			}
		}
		if p.Complete {
			break
		}
		q.RootIdentity = p.RootIdentity
		q.DirectoryRevision = p.DirectoryRevision
		q.Cursor = p.NextCursor
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatalf("lost/replaced native bytes %x: %#v", name, seen)
		}
	}
	if len(seen) != len(names) || !issue {
		t.Fatal("entry/issue roundtrip", seen, issue)
	}
	owner.Close()
	if err := owner.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if owner.Active() != 0 || c.Supervisor.Active() != 0 || c.Supervisor.Stats().InProcess != 0 {
		t.Fatal("real helper did not retire", owner.Active(), c.Supervisor.Active())
	}
}

func TestNativeInventoryNULAndUndecodableWireRefuse(t *testing.T) {
	c, owner := nativeClient(t)
	q := NativeInventoryRequest{Root: []byte(t.TempDir()), RelativePath: []byte("."), Limit: 1}
	// NUL and bytes immediately on either side survive encoding, then fail native
	// path policy. They cannot become a truncated valid path or cursor progress.
	for _, raw := range [][]byte{{0, 0xff}, {0xff, 0}, {1, 0, 1}} {
		q.RelativePath = raw
		wire, err := marshalNativeRequest(&nativeInventoryWire{NativeInventoryRequest: q})
		if err != nil {
			t.Fatal(err)
		}
		var again struct{ NativeInventory struct{ RelativePath []byte } }
		if err := json.Unmarshal(wire, &again); err != nil || !bytes.Equal(again.NativeInventory.RelativePath, raw) {
			t.Fatal("NUL byte transport changed", err)
		}
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, "--portico-storage-helper")
		cmd.Stdin = bytes.NewReader(wire)
		if out, err := cmd.Output(); err == nil || len(out) != 0 {
			t.Fatalf("helper admitted NUL: %x %v", out, err)
		}
		if p, err := c.NativeInventoryPage(t.Context(), owner, "nul", q); !errors.Is(err, ErrNativeInventory) || len(p.Entries) != 0 {
			t.Fatal("NUL client admission", p, err)
		}
	}
	for _, wire := range []string{`{"operation":"inventory-page-native-v1","nativeInventory":{"Root":"%%%","RelativePath":"Lg==","Limit":1}}`,
		`{"operation":"inventory-page-native-v1","nativeInventory":{"Root":"\ud800","RelativePath":"Lg==","Limit":1}}`} {
		binary, _ := os.Executable()
		cmd := exec.Command(binary, "--portico-storage-helper")
		cmd.Stdin = bytes.NewBufferString(wire)
		if out, err := cmd.Output(); err == nil || len(out) != 0 {
			t.Fatal("undecodable native bytes accepted", string(out), err)
		}
	}
}

func TestNativeInventoryCancellationJoinsRealOwner(t *testing.T) {
	root := t.TempDir()
	// A real helper blocks opening configuration until cancellation kills/reaps it.
	if err := syscall.Mkfifo(filepath.Join(root, ".ignore"), 0600); err != nil {
		t.Fatal(err)
	}
	c, owner := nativeClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := c.NativeInventoryPage(ctx, owner, "blocked", NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: 1})
		result <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for owner.Active() != 1 || c.Supervisor.Active() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("owner did not account for actual helper")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if owner.Active() != 0 || c.Supervisor.Active() != 0 {
		t.Fatal("canceled call returned before physical retirement", owner.Active(), c.Supervisor.Active())
	}
	if owner.Context().Err() != nil {
		t.Fatal("operation closed the shared owner")
	}
	release, err := owner.Begin()
	if err != nil {
		t.Fatal("owner cannot be reused", err)
	}
	release()
}

func TestNativeInventoryRejectsDuplicateAliasAndUnknownEnvelope(t *testing.T) {
	root := t.TempDir()
	good, err := marshalNativeRequest(&nativeInventoryWire{NativeInventoryRequest: NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"duplicate_limit", "relative_alias", "operation_alias", "payload_alias", "unknown_envelope", "unknown_body", "native_then_legacy", "truncated_native"} {
		t.Run(name, func(t *testing.T) {
			wire := bytes.Clone(good)
			switch name {
			case "duplicate_limit":
				wire = bytes.Replace(wire, []byte(`"Limit":1`), []byte(`"Limit":0,"Limit":1`), 1)
			case "relative_alias":
				wire = bytes.Replace(wire, []byte(`"RelativePath":"Lg=="`), []byte(`"RelativePath":"Lg==","relativepath":"Lg=="`), 1)
			case "operation_alias":
				wire = bytes.Replace(wire, []byte(`"operation":`), []byte(`"Operation":`), 1)
			case "payload_alias":
				wire = bytes.Replace(wire, []byte(`"nativeInventory":`), []byte(`"NativeInventory":`), 1)
			case "unknown_envelope":
				wire = append(wire[:len(wire)-1], []byte(`,"Unknown":true}`)...)
			case "unknown_body":
				wire = bytes.Replace(wire, []byte(`"Limit":1`), []byte(`"Limit":1,"Unknown":true`), 1)
			case "native_then_legacy":
				wire = append(wire[:len(wire)-1], []byte(`,"operation":"inventory-page"}`)...)
			case "truncated_native":
				wire = wire[:len(wire)-2]
			}
			var out bytes.Buffer
			if err := Helper(bytes.NewReader(wire), &out); !errors.Is(err, ErrNativeInventory) || out.Len() != 0 {
				t.Fatalf("native protocol refusal lost sentinel: %v, %q", err, out.String())
			}
			binary, _ := os.Executable()
			cmd := exec.Command(binary, "--portico-storage-helper")
			cmd.Stdin = bytes.NewReader(wire)
			if out, err := cmd.Output(); err == nil || len(out) != 0 {
				t.Fatal("real helper accepted native alternate frame", err, string(out))
			}
		})
	}
}

func TestNativeEnvelopeDoesNotTightenLegacyDecoding(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(request{Operation: "stat", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	// Legacy remains permissive about aliases, duplicate keys and extra fields.
	pathJSON, _ := json.Marshal(path)
	raw = append(raw[:len(raw)-1], append([]byte(`,"Path":`), append(pathJSON, []byte(`,"Unused":true}`)...)...)...)
	var out bytes.Buffer
	if err := Helper(bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	var result Snapshot
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Path != path || result.Name != "file" || result.Size != 1 {
		t.Fatal("legacy behavior changed", result, err)
	}
}
