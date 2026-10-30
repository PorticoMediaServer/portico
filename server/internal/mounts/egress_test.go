package mounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestLegacyHelpersRequirePrivatePolicyRoutes(t *testing.T) {
	for _, proxy := range []string{"", "http://proxy.example:8080", "http://user:pass@127.0.0.1:1/path", "http://user:pass@127.0.0.1:1?token=secret", "https://user:pass@127.0.0.1:1"} {
		request, err := json.Marshal(nativeRequest{Executable: "/unused/rclone", ConfigPath: "/private/source.conf", PrivateHome: "/private", Remote: "remote:", Operation: "list", HTTPProxy: proxy})
		if err != nil {
			t.Fatal(err)
		}
		if err = NativeHelper(bytes.NewReader(request), &bytes.Buffer{}); !errors.Is(err, storage.ErrRemoteConfig) {
			t.Fatal("native helper accepted missing or untrusted egress", err)
		}
		request, err = json.Marshal(launch{Executable: "/unused/rclone", Included: true, HTTPProxy: proxy})
		if err != nil {
			t.Fatal(err)
		}
		if err = Guardian(bytes.NewReader(request)); err == nil {
			t.Fatal("guardian accepted missing or untrusted egress")
		}
	}
}

func TestLegacyEgressKeepsExactConfigAndRetiresTransientCopy(t *testing.T) {
	s, input := fixture(t)
	row, err := s.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := s.Native(context.Background(), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, egress, err := s.prepareBackendEgress(context.Background(), row.ID, backend.Generation())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil || string(raw) != input.Config || file == backend.config || !validPolicyProxy(egress.HTTPProxy) {
		egress.Close()
		t.Fatal("helper did not retain the exact admitted private config", err)
	}
	egress.Close()
	egress.Close()
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("transient config survived retirement", err)
	}
	raw, err = os.ReadFile(backend.config)
	if err != nil || string(raw) != input.Config {
		t.Fatal("retained source configuration was modified", err)
	}
	s.PrepareSourceEgress = nil
	if _, _, err = s.prepareBackendEgress(context.Background(), row.ID, backend.Generation()); !errors.Is(err, storage.ErrRemoteConfig) {
		t.Fatal("legacy native command admitted without saved-source policy", err)
	}
}

func TestLegacyEgressUsesPinnedCopyForNativeAndMountHelpers(t *testing.T) {
	s, input := fixture(t)
	input.Config = "[cloud]\ntype=smb\nhost=nas.example\nuser=fixture\n"
	row, err := s.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	pinned := []byte("[cloud]\ntype=smb\nhost=192.168.42.7\nuser=fixture\n")
	s.PrepareSourceEgress = func(ctx context.Context, material ServeSource) (*SourceEgress, error) {
		if string(material.Configuration) != input.Config {
			t.Fatal("policy did not receive original digest material")
		}
		egress, err := fixtureEgress(ctx)
		if err == nil {
			egress.Configuration = pinned
		}
		return egress, err
	}
	backend, err := s.Native(context.Background(), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, egress, err := s.prepareBackendEgress(context.Background(), row.ID, backend.Generation())
	if err != nil {
		t.Fatal(err)
	}
	defer egress.Close()
	child, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(child, pinned) {
		t.Fatal("shared native/mount launch file did not receive the pin", string(child), err)
	}
	retained, err := os.ReadFile(backend.config)
	if err != nil || string(retained) != input.Config {
		t.Fatal("pin was written into retained configuration", err)
	}
}
