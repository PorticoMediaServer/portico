package downloads

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// A public, deterministic test key: never a server credential. This vector is
// stored like an already-downloaded v1 copy, independent of a live database.
func TestOfflineV1PackageCompatibility(t *testing.T) {
	seed := sha256.Sum256([]byte("Portico public offline v1 compatibility test vector"))
	secret := ed25519.NewKeyFromSeed(seed[:])
	issued := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	key := ReceiptKey{ID: strings.Repeat("1", 32), Algorithm: "ed25519", PublicKey: base64.RawURLEncoding.EncodeToString(secret.Public().(ed25519.PublicKey)), CreatedAt: issued.Format(time.RFC3339)}
	digest := sha256.Sum256([]byte(filmBody))
	claims := ReceiptClaims{Kind: ReceiptKind, Version: ReceiptVersion, ReceiptID: strings.Repeat("2", 64), KeyID: key.ID,
		Viewer: ReceiptViewer{Authority: "local", AccountID: strings.Repeat("4", 32), ProfileID: strings.Repeat("5", 32), ServerID: strings.Repeat("6", 32)},
		ItemID: entityid.Encode(bytes.Repeat([]byte{7}, 16)), PreparationID: strings.Repeat("3", 64), Quality: "original", QualityLabel: "Original", Artifact: ReceiptArtifact{SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(filmBody))}, IssuedAt: issued.Format(time.RFC3339), ExpiresAt: issued.Add(30 * 24 * time.Hour).Format(time.RFC3339)}
	receipt, err := sign(key.ID, secret, claims, 1)
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{PreparationID: claims.PreparationID, ItemID: claims.ItemID, Quality: "original", QualityLabel: "Original",
		Title: Title{Kind: "movie", Name: "Compatibility film", Year: 2026, DurationSeconds: 6, OnServer: true}, Overview: "Stored copy", Media: BundleMedia{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080},
		Artwork:  []BundleArtwork{{Role: "poster", BundleFile: BundleFile{Path: "artwork/poster", ContentType: "image/jpeg"}}},
		Chapters: []BundleChapter{{Index: 1, Title: "Opening", StartSeconds: 0, EndSeconds: 6, Image: &BundleFile{Path: ChapterImagePath("source", 1), ContentType: "image/jpeg"}}},
		Markers:  []BundleMarker{{Kind: "intro", StartSeconds: 0, EndSeconds: 2, Automatic: true}},
		Audio:    []BundleAudio{{Index: 1, Language: "eng", Title: "English", Codec: "aac", Channels: 2, Default: true}},
		Subtitles: []BundleSubtitle{{ID: "subtitle", Revision: 3, Language: "eng", Title: "Authored captions", Default: true, Files: map[string]BundleFile{
			"vtt": {Path: "subtitles/subtitle/3.vtt", ContentType: "text/vtt"}, "ass": {Path: "subtitles/subtitle/3/original.ass", ContentType: "text/x-ssa"}, "font0": {Path: "subtitles/subtitle/3/fonts/font0", ContentType: "font/ttf"},
		}}},
		Trickplay: &BundleTrickplay{ID: "preview", TileWidth: 160, TileHeight: 90, Columns: 2, Rows: 1, IntervalSeconds: 2, FrameCount: 3, DurationSeconds: 6, SourceOffsetSeconds: 2, Thumbnails: BundleFile{Path: TrickplayThumbnailsPath("preview"), ContentType: "text/vtt"}, Tiles: []BundleFile{{Path: TrickplayTilePath("preview", 0), ContentType: "image/jpeg"}, {Path: TrickplayTilePath("preview", 1), ContentType: "image/jpeg"}}},
	}
	type storedPackage struct {
		Key     ReceiptKey `json:"key"`
		Receipt Receipt    `json:"receipt"`
		Bundle  Bundle     `json:"bundle"`
	}
	actual := storedPackage{key, receipt, bundle}
	raw, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	file := filepath.Join("testdata", "offline_package_v1.json")
	if os.Getenv("PORTICO_PLAYBACK_COMPAT_RECORD") == "1" {
		if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, expected) {
		t.Fatal("offline v1 signed payload, envelope or bundle changed; do not regenerate during the data-layer rebuild")
	}
	var old storedPackage
	if err = json.Unmarshal(expected, &old); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyReceipt([]ReceiptKey{old.Key}, old.Receipt, issued.Add(time.Hour))
	if err != nil {
		t.Fatal("existing stored receipt no longer verifies", err)
	}
	if !reflect.DeepEqual(verified, claims) {
		t.Fatal("stored claims lost viewer/server, quality, artifact bytes or expiry")
	}
	if old.Bundle.ItemID != verified.ItemID || old.Bundle.PreparationID != verified.PreparationID {
		t.Fatal("stored package binding changed")
	}
	changed := old.Receipt
	payload, err := base64.RawURLEncoding.DecodeString(changed.Payload)
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.Replace(payload, []byte(`"profileId":"`+claims.Viewer.ProfileID), []byte(`"profileId":"other-profile`), 1)
	changed.Payload = base64.RawURLEncoding.EncodeToString(payload)
	if _, err = VerifyReceipt([]ReceiptKey{old.Key}, changed, issued.Add(time.Hour)); err == nil {
		t.Fatal("changed stored viewer escaped signed payload binding")
	}
}
