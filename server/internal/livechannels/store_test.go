package livechannels

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, e := persistence.Open(filepath.Join(t.TempDir(), "guide.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s, e := New(db)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func testAuthority(fence string, owner bool, allowed bool) Authority {
	return func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		if !owner {
			return "", nil, ErrDenied
		}
		return fence, func(string, string) bool { return allowed }, nil
	}
}
func testInput() SourceInput {
	return SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("cd", 24), Name: "Test broadcast", TunerCount: 1, Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\" group-title=\"Local\",Channel one\nhttps://provider.invalid/stream?secret=private-source-value\n#EXTINF:-1 tvg-id=\"two\",Channel two\nhttps://provider.invalid/two\n", Guide: `<?xml version="1.0"?><tv><channel id="one"/><programme id="show-1" channel="one" start="20260905120000 +0000" stop="20260905130000 +0000"><title>Afternoon programme</title></programme><programme channel="two" start="20260905120000 +0000" stop="20260905130000 +0000"><title>Second programme</title></programme></tv>`}
}
func testQuery() GuideQuery {
	start, _ := time.Parse(time.RFC3339, "2026-09-05T12:00:00Z")
	return GuideQuery{Start: start, End: start.Add(2 * time.Hour), Timezone: "America/Halifax", Kind: LiveSource, Limit: 1}
}
func TestPreviewSavePlaintextAndConsumerBoundary(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer-a", true, true)
	in := testInput()
	p, e := PreviewSource(in)
	if e != nil || p.Channels != 2 || p.Programmes != 2 {
		t.Fatalf("preview %#v %v", p, e)
	}
	sources, e := s.Sources(ctx, a)
	if e != nil || len(sources) != 0 {
		t.Fatal("preview published source")
	}
	saved, e := s.Save(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	var stored []byte
	if e = s.db.QueryRow(`SELECT locator_sealed FROM live_channel_versions WHERE generation_id=? ORDER BY position LIMIT 1`, saved.Generation).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	// Plain storage: the locator sits in the row as-is.
	if !bytes.Contains(stored, []byte("private-source-value")) {
		t.Fatal("locator not stored plainly")
	}
	locator, e := s.unseal(stored, id(saved.ID, "one")+":"+saved.Generation)
	if e != nil || !strings.Contains(locator, "private-source-value") {
		t.Fatal("locator not recoverable", e)
	}
	guide, e := s.Guide(ctx, a, testQuery())
	if e != nil || len(guide.Channels) != 1 || len(guide.Channels[0].Programmes) != 1 {
		t.Fatalf("guide %#v %v", guide, e)
	}
	body, _ := json.Marshal(guide)
	if bytes.Contains(body, []byte("provider.invalid")) || bytes.Contains(body, []byte("private-source-value")) {
		t.Fatal("consumer leaks locator")
	}
	if guide.Channels[0].TuneAvailable {
		t.Fatal("unimplemented tune advertised")
	}
}
func TestRefreshCASAndStableCursor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer-a", true, true)
	in := testInput()
	saved, e := s.Save(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.Guide(ctx, a, testQuery())
	if e != nil || first.NextCursor == "" {
		t.Fatal("missing continuation", e)
	}
	q := testQuery()
	q.Cursor = first.NextCursor
	second, e := s.Guide(ctx, a, q)
	if e != nil || len(second.Channels) != 1 || second.Channels[0].ID == first.Channels[0].ID {
		t.Fatal("bad continuation", e)
	}
	if _, e = s.Guide(ctx, testAuthority("viewer-b", true, true), q); !errors.Is(e, ErrCursor) {
		t.Fatal("cross-viewer cursor accepted")
	}
	in.ID = saved.ID
	in.ExpectedRevision = saved.Revision
	in.RequestID = strings.Repeat("bb", 24)
	in.Guide = strings.Replace(in.Guide, "Afternoon programme", "Updated programme", 1)
	updated, e := s.Save(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Guide(ctx, a, q); !errors.Is(e, ErrCursor) {
		t.Fatal("old generation cursor accepted")
	}
	in.RequestID = strings.Repeat("cc", 24)
	if _, e = s.Save(ctx, a, in); !errors.Is(e, ErrConflict) {
		t.Fatal("stale write accepted")
	}
	in.ExpectedRevision = updated.Revision
	// Odd, well-formed programme rows are skipped. A structurally broken
	// document must still leave the last published guide intact.
	in.Guide = "<tv><programme>broken</tv>"
	if _, e = s.Save(ctx, a, in); !errors.Is(e, ErrInvalid) {
		t.Fatal("invalid refresh accepted")
	}
	current, e := s.Guide(ctx, a, testQuery())
	if e != nil || current.Channels[0].Generation != updated.Generation || current.Channels[0].Programmes[0].Title != "Updated programme" {
		t.Fatal("last good guide lost", e)
	}
}
func TestDeniedCommitRollsBackAndDisableHides(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	calls := 0
	a := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		calls++
		if calls == 2 {
			return "", nil, ErrDenied
		}
		return "viewer", func(string, string) bool { return true }, nil
	}
	if _, e := s.Save(ctx, a, testInput()); !errors.Is(e, ErrDenied) {
		t.Fatal("late denial ignored")
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM live_sources`).Scan(&n)
	if n != 0 {
		t.Fatal("denied save committed")
	}
	owner := testAuthority("viewer", true, true)
	saved, e := s.Save(ctx, owner, testInput())
	if e != nil {
		t.Fatal(e)
	}
	guide, e := s.Guide(ctx, testAuthority("other", true, false), testQuery())
	if e != nil || len(guide.Channels) != 0 {
		t.Fatal("denied channel disclosed")
	}
	if e = s.SetEnabled(ctx, owner, saved.ID, saved.Revision, false, strings.Repeat("dd", 24)); e != nil {
		t.Fatal(e)
	}
	guide, e = s.Guide(ctx, owner, testQuery())
	if e != nil || len(guide.Channels) != 0 {
		t.Fatal("disabled source visible")
	}
}
func TestParserRejectsHostileAndAmbiguousData(t *testing.T) {
	base := testInput()
	for name, change := range map[string]func(*SourceInput){"entity": func(v *SourceInput) { v.Guide = `<!DOCTYPE tv [<!ENTITY x SYSTEM "file:///etc/passwd">]><tv/>` }, "oversize": func(v *SourceInput) { v.Limits.Bytes = 32768; v.Guide = strings.Repeat("x", 32769) }} {
		t.Run(name, func(t *testing.T) {
			in := base
			change(&in)
			if _, e := PreviewSource(in); !errors.Is(e, ErrInvalid) {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

func TestOperationReplayDisableRefreshAndCoverage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer", true, true)
	in := testInput()
	saved, e := s.Save(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Save(ctx, a, in)
	if e != nil || again.Generation != saved.Generation || again.Revision != saved.Revision {
		t.Fatal("lost-response replay changed operation", e)
	}
	in.Name = "Different input"
	if _, e = s.Save(ctx, a, in); !errors.Is(e, ErrConflict) {
		t.Fatal("reused request accepted different input")
	}
	request := strings.Repeat("11", 24)
	if e = s.SetEnabled(ctx, a, saved.ID, saved.Revision, false, request); e != nil {
		t.Fatal(e)
	}
	if e = s.SetEnabled(ctx, a, saved.ID, saved.Revision, false, request); e != nil {
		t.Fatal("enablement replay failed", e)
	}
	in = testInput()
	in.ExpectedRevision = saved.Revision + 1
	in.RequestID = strings.Repeat("22", 24)
	refresh, e := s.Save(ctx, a, in)
	if e != nil || refresh.State != "disabled" {
		t.Fatal("refresh re-enabled source", e)
	}
	if e = s.SetEnabled(ctx, a, refresh.ID, refresh.Revision, true, strings.Repeat("33", 24)); e != nil {
		t.Fatal(e)
	}
	guide, e := s.Guide(ctx, a, testQuery())
	if e != nil || len(guide.Sources) != 1 || guide.Sources[0].AvailableStart != "2026-09-05T12:00:00Z" || guide.Sources[0].AvailableEnd != "2026-09-05T13:00:00Z" {
		t.Fatalf("coverage %#v %v", guide.Sources, e)
	}
	if refresh.DeliveryValidation != "not-validated" {
		t.Fatal("source validation fabricated")
	}
}
func TestInterruptedStageResumesAndConfigurationChangeFences(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	in := testInput()
	owner := testAuthority("viewer", true, true)
	var channelStage *sql.Tx
	interrupted := func(ctx context.Context, tx *sql.Tx, admin bool) (string, func(string, string) bool, error) {
		var staged int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM live_channel_versions c JOIN live_operations o ON o.generation_id=c.generation_id WHERE o.request_id=? AND o.status='staging'`, in.RequestID).Scan(&staged); err != nil {
			t.Fatal(err)
		}
		if staged == 2 {
			if channelStage == nil {
				channelStage = tx
			} else if channelStage != tx {
				// The channel batch is now committed. Interrupt the next batch,
				// independent of how many authorization checks preceded it.
				return "", nil, context.DeadlineExceeded
			}
		}
		return owner(ctx, tx, admin)
	}
	if _, e := s.Save(ctx, interrupted, in); !errors.Is(e, ErrUnavailable) {
		t.Fatal("stage interruption classification", e)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM live_channel_versions`).Scan(&n)
	if n != 2 {
		t.Fatal("expected durable staged channels", n)
	}
	sources, e := s.Sources(ctx, owner)
	if e != nil || len(sources) != 0 {
		t.Fatal("staging appeared published")
	}
	saved, e := s.Save(ctx, owner, in)
	if e != nil {
		t.Fatal("resume failed", e)
	}
	in.ExpectedRevision = saved.Revision
	in.RequestID = strings.Repeat("44", 24)
	channelStage = nil
	if _, e = s.Save(ctx, interrupted, in); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if e = s.SetEnabled(ctx, owner, saved.ID, saved.Revision, false, strings.Repeat("55", 24)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Save(ctx, owner, in); !errors.Is(e, ErrConflict) {
		t.Fatal("old staging overwrote newer source", e)
	}
	sources, e = s.Sources(ctx, owner)
	if e != nil || len(sources) != 1 || sources[0].State != "disabled" || sources[0].Generation != saved.Generation {
		t.Fatal("source fence failed")
	}
}

// Rows sealed under the old source key migrate to plaintext, and the key
// file goes away.
func TestSealedPreviewMigratesToPlain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	dir := t.TempDir()
	db, e := persistence.OpenFresh(filepath.Join(dir, "guide.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		t.Fatal(e)
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		t.Fatal(e)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		t.Fatal(e)
	}
	sealed := aead.Seal(nonce, nonce, []byte("https://provider.invalid/guide"), []byte("preview:preview-one"))
	if _, e = db.Exec(`INSERT INTO live_remote_previews(id,source_id,expected_revision,fence,sealed,expires_ms) VALUES('preview-one','source-one',1,'',?,0)`, sealed); e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(storagepaths.Path(dir, "keys"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(storagepaths.Path(dir, "keys", "live-sources.v1.key"), key, 0600); e != nil {
		t.Fatal(e)
	}
	MigrateSealedToPlain(db, dir)
	var stored []byte
	if e = db.QueryRow(`SELECT sealed FROM live_remote_previews WHERE id='preview-one'`).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if string(stored) != "https://provider.invalid/guide" {
		t.Fatal("sealed preview not migrated to plaintext")
	}
	if _, e = os.Stat(storagepaths.Path(dir, "keys", "live-sources.v1.key")); !os.IsNotExist(e) {
		t.Fatal("key file left behind")
	}
}

// The XMLTV format's own files name its DTD on the second line. That
// declaration declares nothing, so a guide carrying it is read; any
// declaration that could define an entity, and any reference to one, is still
// refused, and nothing is ever fetched or expanded.
func TestGuideAcceptsOnlyTheHarmlessDocumentTypeDeclaration(t *testing.T) {
	base := testInput()
	body := strings.TrimPrefix(base.Guide, `<?xml version="1.0"?>`)
	want, e := PreviewSource(base)
	if e != nil {
		t.Fatal(e)
	}
	for name, doctype := range map[string]string{
		"system":        `<!DOCTYPE tv SYSTEM "xmltv.dtd">`,
		"single-quoted": `<!DOCTYPE tv SYSTEM 'xmltv.dtd'>`,
		"public":        `<!DOCTYPE tv PUBLIC "-//XMLTV//DTD XMLTV//EN" "http://xmltv.invalid/xmltv.dtd">`,
		"bare":          `<!DOCTYPE tv>`,
		"spaced":        "<!DOCTYPE  tv\n SYSTEM \"xmltv.dtd\" >",
	} {
		t.Run("accepts-"+name, func(t *testing.T) {
			in := base
			in.Guide = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + doctype + "\n" + body
			got, e := PreviewSource(in)
			if e != nil {
				t.Fatalf("a standard XMLTV declaration was refused: %v", e)
			}
			if got.Channels != want.Channels || got.Programmes != want.Programmes {
				t.Fatalf("the declaration changed what was read: %+v, want %+v", got, want)
			}
		})
	}
	for name, guide := range map[string]string{
		"internal-entity":      `<!DOCTYPE tv [<!ENTITY x "expanded">]>` + body,
		"external-entity":      `<!DOCTYPE tv [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + body,
		"parameter-entity":     `<!DOCTYPE tv [<!ENTITY % x SYSTEM "http://provider.invalid/x.dtd"> %x;]>` + body,
		"subset-after-system":  `<!DOCTYPE tv SYSTEM "xmltv.dtd" [<!ENTITY x "expanded">]>` + body,
		"empty-subset":         `<!DOCTYPE tv SYSTEM "xmltv.dtd" []>` + body,
		"other-root":           `<!DOCTYPE html SYSTEM "xmltv.dtd">` + body,
		"bare-entity":          `<!ENTITY x "expanded">` + body,
		"element-declaration":  `<!ELEMENT tv ANY>` + body,
		"two-declarations":     `<!DOCTYPE tv SYSTEM "xmltv.dtd"><!DOCTYPE tv SYSTEM "xmltv.dtd">` + body,
		"after-the-root":       body + `<!DOCTYPE tv SYSTEM "xmltv.dtd">`,
		"inside-the-root":      strings.Replace(body, "<tv>", `<tv><!DOCTYPE tv SYSTEM "xmltv.dtd">`, 1),
		"inside-a-programme":   strings.Replace(body, "<title>", `<!DOCTYPE tv SYSTEM "xmltv.dtd"><title>`, 1),
		"unterminated-literal": `<!DOCTYPE tv SYSTEM "xmltv.dtd>` + body,
		"trailing-content":     `<!DOCTYPE tv SYSTEM "xmltv.dtd" "extra">` + body,
		"undeclared-reference": `<!DOCTYPE tv SYSTEM "xmltv.dtd">` + strings.Replace(body, "Afternoon programme", "&x;", 1),
	} {
		t.Run("refuses-"+name, func(t *testing.T) {
			in := base
			in.Guide = guide
			if _, e := PreviewSource(in); !errors.Is(e, ErrInvalid) {
				t.Fatalf("accepted %s: %v", name, e)
			}
		})
	}
}

// A guide refresh publishes a new generation and a new source revision even
// when no channel's stream changed. What a running producer depends on is the
// channel's own input, and that must read the same before and after, so a
// refresh alone is no reason to retune anyone; a changed address is.
func TestInputFenceFollowsTheStreamNotTheGuideGeneration(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer", true, true)
	fences := func(source Source) map[string]string {
		t.Helper()
		out := map[string]string{}
		gated, e := dbwork.BeginSnapshot(ctx, s.db)
		if e != nil {
			t.Fatal(e)
		}
		defer gated.Rollback()
		tx := gated.Tx()
		rows, e := tx.QueryContext(ctx, `SELECT channel_id,provider_key FROM live_channel_versions WHERE generation_id=?`, source.Generation)
		if e != nil {
			t.Fatal(e)
		}
		keys := map[string]string{}
		for rows.Next() {
			var id, key string
			if e = rows.Scan(&id, &key); e != nil {
				t.Fatal(e)
			}
			keys[id] = key
		}
		rows.Close()
		for id, key := range keys {
			input, e := s.InputTx(ctx, tx, source.ID, id, source.Generation)
			if e != nil {
				t.Fatal(e)
			}
			if out[key] = input.Fence(); out[key] == "" {
				t.Fatal("an input has no fence")
			}
		}
		return out
	}
	in := testInput()
	first, e := s.Save(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	before := fences(first)
	if len(before) != 2 || before["one"] == before["two"] {
		t.Fatalf("two channels with different streams share a fence: %v", before)
	}
	// The same line-up with a new guide: a new generation, the same streams.
	in.RequestID, in.ExpectedRevision = strings.Repeat("ce", 24), first.Revision
	in.Guide = strings.Replace(in.Guide, "Afternoon programme", "Evening programme", 1)
	refreshed, e := s.Save(ctx, a, in)
	if e != nil || refreshed.Generation == first.Generation || refreshed.Revision == first.Revision {
		t.Fatalf("the refresh did not publish a new generation: %v", e)
	}
	if after := fences(refreshed); after["one"] != before["one"] || after["two"] != before["two"] {
		t.Fatalf("a guide refresh changed the fence of an unchanged stream: %v then %v", before, after)
	}
	// One channel's address changes: that channel's input is different, the other's is not.
	in.RequestID, in.ExpectedRevision = strings.Repeat("cf", 24), refreshed.Revision
	in.Playlist = strings.Replace(in.Playlist, "https://provider.invalid/two", "https://provider.invalid/two-moved", 1)
	moved, e := s.Save(ctx, a, in)
	if e != nil {
		t.Fatal(e)
	}
	if after := fences(moved); after["two"] == before["two"] || after["one"] != before["one"] {
		t.Fatalf("a changed stream address did not change exactly that channel's fence: %v then %v", before, after)
	}
}
