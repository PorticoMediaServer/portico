package mediatools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixtureBundle(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	bundle := filepath.Join(root, "third_party", "ffmpeg", "bundle")
	m := manifest{SchemaVersion: 1, LicenseMode: "gpl3", Target: map[string]string{"darwin": "macos", "linux": "linux", "windows": "windows"}[runtime.GOOS] + "-" + map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH], Files: map[string]string{}}
	for _, name := range []string{"bin/" + Name("ffmpeg"), "bin/" + Name("ffprobe"), "bin/" + Name("portico-disc"), "corresponding-source.tar.xz", "NOTICE.md", "sources.lock.json", "requirements.v1.json", "LICENSES/GPL", "LICENSES/portico-disc.txt"} {
		path := filepath.Join(bundle, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		body := []byte("fixture: " + name)
		if err := os.WriteFile(path, body, 0700); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		m.Files[name] = hex.EncodeToString(digest[:])
	}
	libs := map[string]any{}
	for _, name := range []string{"libbluray", "libudfread", "dvdnav", "dvdread"} {
		libs[name] = map[string]string{"version": "fixture", "archive": "lib/" + name + ".a", "sha256": strings.Repeat("a", 64)}
	}
	record := map[string]any{"schema": 1, "tool": "portico-disc", "target": m.Target,
		"sourceCommit": strings.Repeat("b", 40), "sourceSha256": strings.Repeat("c", 64),
		"binarySha256": m.Files["bin/"+Name("portico-disc")], "decryption": false, "staticLibraries": libs}
	recordRaw, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(bundle, "portico-disc-build.json"), recordRaw, 0600); err != nil {
		t.Fatal(err)
	}
	recordHash := sha256.Sum256(recordRaw)
	m.Files["portico-disc-build.json"] = hex.EncodeToString(recordHash[:])
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(bundle, "toolchain-manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return root, bundle
}
func TestRepositoryToolVerifiesAncestorsAndRejectsTampering(t *testing.T) {
	root, bundle := fixtureBundle(t)
	nested := filepath.Join(root, "services", "server", "internal", "decoder")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"ffmpeg", "ffprobe", "portico-disc"} {
		path, err := RepositoryTool(tool, t.TempDir(), nested)
		if err != nil || path != filepath.Join(bundle, "bin", Name(tool)) {
			t.Fatalf("%s %s %v", tool, path, err)
		}
	}
	executable := filepath.Join(bundle, "bin", Name("ffmpeg"))
	if err := os.WriteFile(executable, []byte("modified"), 0700); err != nil {
		t.Fatal(err)
	}
	if path, err := RepositoryTool("ffmpeg", nested); err == nil || path != "" || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("tampered binary accepted: %s %v", path, err)
	}
}
func TestBundleInventoryAndProvenanceAreRequired(t *testing.T) {
	for _, kind := range []string{"extra", "missing", "traversal", "license", "target", "manifest-symlink", "directory-symlink"} {
		t.Run(kind, func(t *testing.T) {
			_, bundle := fixtureBundle(t)
			path := filepath.Join(bundle, "toolchain-manifest.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var m manifest
			if err = json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "extra":
				err = os.WriteFile(filepath.Join(bundle, "unexpected"), nil, 0600)
			case "missing":
				err = os.Remove(filepath.Join(bundle, "NOTICE.md"))
			case "traversal":
				m.Files["../outside"] = "00"
			case "license":
				m.LicenseMode = "nonfree"
			case "target":
				m.Target = "wrong-host"
			case "manifest-symlink":
				target := filepath.Join(t.TempDir(), "manifest")
				if err = os.WriteFile(target, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(target, path)
			case "directory-symlink":
				err = os.Symlink(t.TempDir(), filepath.Join(bundle, "linked"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "traversal" || kind == "license" || kind == "target" {
				raw, _ = json.Marshal(m)
				if err = os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err = VerifyBundle(bundle); err == nil {
				t.Fatal("invalid bundle passed verification")
			}
		})
	}
}
func TestExplicitOperatorOverrideWins(t *testing.T) {
	t.Setenv("PORTICO_FFMPEG", "/operator/ffmpeg")
	if got := Resolve("ffmpeg"); got != "/operator/ffmpeg" {
		t.Fatal(got)
	}
}

func TestResolvePairSharesVerifiedBundleAndHonorsProbeOverride(t *testing.T) {
	root, bundle := fixtureBundle(t)
	t.Chdir(root)
	t.Setenv("PORTICO_FFMPEG", "")
	t.Setenv("PORTICO_FFPROBE", "")
	ffmpeg, probe := ResolvePair()
	if ffmpeg != filepath.Join(bundle, "bin", Name("ffmpeg")) || probe != filepath.Join(bundle, "bin", Name("ffprobe")) {
		t.Fatal(ffmpeg, probe)
	}
	t.Setenv("PORTICO_FFPROBE", "/operator/probe")
	_, probe = ResolvePair()
	if probe != "/operator/probe" {
		t.Fatal(probe)
	}
	t.Setenv("PORTICO_FFPROBE", "")
	if err := os.WriteFile(filepath.Join(bundle, "bin", Name("ffprobe")), []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	ffmpeg, probe = ResolvePair()
	if !strings.ContainsRune(ffmpeg, 0) || !strings.ContainsRune(probe, 0) {
		t.Fatal("invalid bundle fell through to executable paths", ffmpeg, probe)
	}
}

func TestConfiguredOwnerToolsReachLazyConsumers(t *testing.T) {
	before := activePair.Load()
	defer activePair.Store(before)
	root := t.TempDir()
	ffmpeg, ffprobe := filepath.Join(root, Name("ffmpeg")), filepath.Join(root, Name("ffprobe"))
	for _, path := range []string{ffmpeg, ffprobe} {
		if err := os.WriteFile(path, []byte("owner fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := ConfigurePair(ffmpeg, ffprobe); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORTICO_FFMPEG", "/unused/override")
	got, probe := ResolvePair()
	if got != ffmpeg || probe != ffprobe {
		t.Fatal(got, probe)
	}
	if err := ConfigurePair(root, ffprobe); err == nil {
		t.Fatal("accepted directory as executable")
	}
	got, probe = ResolvePair()
	if got != ffmpeg || probe != ffprobe {
		t.Fatal("invalid candidate replaced qualified pair")
	}
	if got := Resolve("portico-disc"); !strings.ContainsRune(got, 0) {
		t.Fatal("missing selected companion fell through", got)
	}
	disc := filepath.Join(root, Name("portico-disc"))
	if err := os.WriteFile(disc, []byte("owner disc fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := Resolve("portico-disc"); got != disc {
		t.Fatal("did not select the configured toolchain companion", got)
	}
}

func TestDiscBundleRequiresCompanionAndBoundStaticInputs(t *testing.T) {
	for _, kind := range []string{"helper", "record", "license", "binary", "source", "decryption", "missing-decryption", "library", "outside-archive"} {
		t.Run(kind, func(t *testing.T) {
			_, bundle := fixtureBundle(t)
			manifestPath := filepath.Join(bundle, "toolchain-manifest.json")
			raw, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var m manifest
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			remove := map[string]string{"helper": "bin/" + Name("portico-disc"), "record": "portico-disc-build.json", "license": "LICENSES/portico-disc.txt"}[kind]
			if remove != "" {
				if err := os.Remove(filepath.Join(bundle, filepath.FromSlash(remove))); err != nil {
					t.Fatal(err)
				}
				delete(m.Files, remove)
			} else {
				recordPath := filepath.Join(bundle, "portico-disc-build.json")
				raw, err := os.ReadFile(recordPath)
				if err != nil {
					t.Fatal(err)
				}
				var record map[string]any
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "binary":
					record["binarySha256"] = strings.Repeat("d", 64)
				case "source":
					record["sourceCommit"] = "unchecked"
				case "decryption":
					record["decryption"] = true
				case "missing-decryption":
					delete(record, "decryption")
				case "library":
					delete(record["staticLibraries"].(map[string]any), "libudfread")
				case "outside-archive":
					record["staticLibraries"].(map[string]any)["dvdread"].(map[string]any)["archive"] = "../libdvdread.a"
				}
				raw, _ = json.Marshal(record)
				if err := os.WriteFile(recordPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256(raw)
				m.Files["portico-disc-build.json"] = hex.EncodeToString(hash[:])
			}
			raw, _ = json.Marshal(m)
			if err := os.WriteFile(manifestPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := VerifyBundle(bundle); err == nil {
				t.Fatal("accepted invalid disc bundle")
			}
		})
	}
}
