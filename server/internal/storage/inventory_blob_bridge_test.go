package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// This records storage delegation, not production source/actor authority. The
// remote package's real job/metadata/NAS tests qualify its separate resolver.
type inventoryBlobBridge struct {
	RemoteSources
	read     func(context.Context, string, InventoryRequest, InventoryBlobAdmission, int64) ([]byte, error)
	acquired bool
}

func (*inventoryBlobBridge) Handles(string) bool { return true }
func (r *inventoryBlobBridge) ReadInventoryBlob(ctx context.Context, source string, request InventoryRequest, a InventoryBlobAdmission, limit int64) ([]byte, error) {
	return r.read(ctx, source, request, a, limit)
}
func (r *inventoryBlobBridge) Acquire(context.Context, string, string) (RemoteObject, error) {
	r.acquired = true
	return nil, ErrRemoteConfig
}

func TestInventoryBlobStorageDelegatesExactFactsWithoutPoolFallback(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sidecar.nfo")
	client := New("")
	facts := InventoryBlobAdmission{Purpose: InventoryBlobAnalysisSidecar, JobID: "job", ObjectID: "media-object", SourceRevision: "catalogue-revision", Operation: "local_metadata"}
	remote := &inventoryBlobBridge{}
	client.Remote = remote
	var calls int
	remote.read = func(ctx context.Context, source string, request InventoryRequest, a InventoryBlobAdmission, limit int64) ([]byte, error) {
		calls++
		if ctx.Err() != nil || source != "folder" || request.Root != root || request.RelativePath != "sidecar.nfo" || a != facts || limit != 64 {
			t.Fatal("storage changed actual invocation", source, request, a, limit)
		}
		return []byte("metadata"), nil
	}
	ctx := WithScanInput(context.Background(), client, "folder", InventoryRequest{Root: root}, facts)
	data, err := client.ReadSmall(ctx, "folder", path, 64)
	if err != nil || string(data) != "metadata" || calls != 1 || remote.acquired {
		t.Fatal("inventory escaped retained producer", string(data), err, calls, remote.acquired)
	}
	missing := WithScanInput(context.Background(), client, "folder", InventoryRequest{Root: root})
	missing = WithScanReadGuard(missing, func(context.Context, string) error {
		t.Fatal("missing invocation entered an I/O-bearing read guard")
		return nil
	})
	if _, err = client.ReadSmall(missing, "folder", path, 64); !errors.Is(err, ErrRemoteConfig) || calls != 1 || remote.acquired {
		t.Fatal("missing facts fell back to a pool", err, calls, remote.acquired)
	}
	other := WithScanInput(context.Background(), New(""), "folder", InventoryRequest{Root: root}, facts)
	if _, err = client.ReadSmall(other, "folder", path, 64); !errors.Is(err, ErrRemoteConfig) || calls != 1 {
		t.Fatal("a different storage source inherited the invocation", err, calls)
	}
}

func TestInventoryBlobStorageDiscardsFailedOrOverboundProducerBytes(t *testing.T) {
	for _, failure := range []string{"provider", "size", "post-guard"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			client := New("")
			remote := &inventoryBlobBridge{}
			client.Remote = remote
			data := []byte("private-data")
			remote.read = func(context.Context, string, InventoryRequest, InventoryBlobAdmission, int64) ([]byte, error) {
				if failure == "provider" {
					return data, ErrRemoteChanged
				}
				return data, nil
			}
			limit := int64(64)
			if failure == "size" {
				limit = 1
			}
			ctx := WithScanInput(context.Background(), client, "folder", InventoryRequest{Root: root}, InventoryBlobAdmission{Purpose: InventoryBlobIgnore})
			checks := 0
			ctx = WithScanReadGuard(ctx, func(context.Context, string) error {
				checks++
				if failure == "post-guard" && checks > 1 {
					return ErrRemoteChanged
				}
				return nil
			})
			got, err := client.ReadSmall(ctx, "folder", filepath.Join(root, ".ignore"), limit)
			if err == nil || len(got) != 0 || remote.acquired {
				t.Fatal("failed inventory exposed bytes or used legacy pool", got, err, remote.acquired)
			}
			for _, value := range data {
				if value != 0 {
					t.Fatal("failed producer bytes were retained")
				}
			}
		})
	}
}
