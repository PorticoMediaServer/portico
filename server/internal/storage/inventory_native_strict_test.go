//go:build linux

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A real child behind the SAME supervised spawn emits adversarial response
// bytes. Normal roundtrips use the actual Helper in inventory_native_test.go.
func TestNativeResponseFixture(t *testing.T) {
	if os.Args[len(os.Args)-1] != "--native-response-fixture" {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
	if err != nil {
		os.Exit(2)
	}
	var r request
	if json.Unmarshal(raw, &r) != nil || r.NativeInventory == nil {
		os.Exit(3)
	}
	out, err := os.ReadFile(filepath.Join(string(r.NativeInventory.Root), ".response"))
	if err != nil {
		os.Exit(4)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		os.Exit(5)
	}
	os.Exit(0)
}
func nativeResponseClient(t *testing.T, root string, wire []byte) (*Client, *OperationScope) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".response"), wire, 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "response-helper")
	quote := "'" + strings.ReplaceAll(binary, "'", "'\"'\"'") + "'"
	body := "#!/bin/sh\nexec " + quote + " -test.run=^TestNativeResponseFixture$ -- --native-response-fixture\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	owner := NewOperationScope()
	t.Cleanup(func() { owner.Close(); owner.Wait(context.Background()) })
	return New(script), owner
}
func TestNativeInventoryStrictResponse(t *testing.T) {
	for _, name := range []string{"canonical", "noncanonical_name_pad", "issue_pad", "base64_newline", "array_name", "nul_name", "nul_path", "oversized_path", "missing_name", "null_path", "lone_surrogate", "raw_invalid_utf8", "overflow_trailing"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			good := NativeInventoryPage{Version: 1, RootIdentity: "root", DirectoryIdentity: "directory", DirectoryRevision: "revision", Complete: true,
				Entries: []NativeInventoryEntry{{Name: []byte("file"), Path: []byte(filepath.Join(root, "file")), ObjectIdentity: "object"}},
				Issues:  []NativeInventoryIssue{{Path: []byte("."), Reason: []byte("ignored_name")}}}
			raw, err := json.Marshal(good)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "noncanonical_name_pad":
				raw = bytes.Replace(raw, []byte(`"Name":"ZmlsZQ=="`), []byte(`"Name":"ZmlsZR=="`), 1)
			case "issue_pad":
				raw = bytes.Replace(raw, []byte(`"Path":"Lg=="`), []byte(`"Path":"Lh=="`), 1)
			case "base64_newline":
				raw = bytes.Replace(raw, []byte(`"Name":"ZmlsZQ=="`), []byte(`"Name":"ZmlsZQ==\n"`), 1)
			case "array_name":
				raw = bytes.Replace(raw, []byte(`"Name":"ZmlsZQ=="`), []byte(`"Name":[102,105,108,101]`), 1)
			case "nul_name":
				good.Entries[0].Name = []byte{'x', 0, 'y'}
				raw, _ = json.Marshal(good)
			case "nul_path":
				good.Entries[0].Path = []byte{'x', 0, 'y'}
				raw, _ = json.Marshal(good)
			case "oversized_path":
				good.Entries[0].Path = bytes.Repeat([]byte{'x'}, 16385)
				raw, _ = json.Marshal(good)
			case "missing_name":
				raw = bytes.Replace(raw, []byte(`,"Name":"ZmlsZQ=="`), nil, 1)
			case "null_path":
				encoded, _ := json.Marshal(good.Entries[0].Path)
				raw = bytes.Replace(raw, append([]byte(`"Path":`), encoded...), []byte(`"Path":null`), 1)
			case "lone_surrogate":
				raw = bytes.Replace(raw, []byte(`"RootIdentity":"root"`), []byte(`"RootIdentity":"\ud800"`), 1)
			case "raw_invalid_utf8":
				raw = bytes.Replace(raw, []byte(`"RootIdentity":"root"`), append([]byte(`"RootIdentity":"`), 0xff, '"'), 1)
			case "overflow_trailing":
				raw = append(raw, bytes.Repeat([]byte{' '}, (2<<20)-len(raw))...)
				raw = append(raw, []byte("{}")...)
			}
			c, owner := nativeResponseClient(t, root, raw)
			p, err := c.NativeInventoryPage(t.Context(), owner, "response", NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: 1})
			if name == "canonical" {
				if err != nil || len(p.Entries) != 1 {
					t.Fatal(p, err)
				}
			} else if err == nil || len(p.Entries) != 0 {
				t.Fatalf("accepted %s: %+v %v", name, p, err)
			}
			if c.Supervisor.Active() != 0 || owner.Active() != 0 {
				t.Fatal("call returned before its own physical retirement")
			}
			if owner.Context().Err() != nil {
				t.Fatal("call closed shared owner")
			}
		})
	}
}
func TestNativeWireRejectsTextRepairAndAlternateByteForms(t *testing.T) {
	root := t.TempDir()
	w := nativeInventoryWire{NativeInventoryRequest: NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: 1}}
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{`"Lh=="`, `"Lg==\n"`, `[46]`, `null`} {
		damaged := bytes.Replace(raw, []byte(`"RelativePath":"Lg=="`), []byte(`"RelativePath":`+replacement), 1)
		var out nativeInventoryWire
		if err := json.Unmarshal(damaged, &out); err == nil {
			t.Fatal("accepted native request alternative", replacement)
		}
	}
	for _, value := range []string{`"\ud800"`, `"\udc00"`, `"\ud800x"`, fmt.Sprintf("\"%s\"", string([]byte{0xff}))} {
		damaged := bytes.Replace(raw, []byte(`"RootIdentity":""`), []byte(`"RootIdentity":`+value), 1)
		var out nativeInventoryWire
		if err := json.Unmarshal(damaged, &out); err == nil {
			t.Fatal("repaired damaged native JSON", value)
		}
	}
	// A genuine encoded surrogate PAIR and literal U+FFFD are well-formed text.
	for _, value := range []string{`"\ud83d\ude80"`, `"\ufffd"`} {
		changed := bytes.Replace(raw, []byte(`"RootIdentity":""`), []byte(`"RootIdentity":`+value), 1)
		var out nativeInventoryWire
		if err := json.Unmarshal(changed, &out); err != nil {
			t.Fatal("rejected valid Unicode", err)
		}
	}
}
