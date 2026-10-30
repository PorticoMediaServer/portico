//go:build rcloneintegration

package remotesources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// This fixture exercises the production controller/config/metadata/transport
// path with upstream rclone. Its authority callback checks a real owner family;
// it does not stand in for the Reading owner's book/content-access resolver.
func TestSourceControllerRealSharedProcessAndScanPublication(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	program := lockedRcloneFixture(t)
	t.Setenv("PORTICO_RCLONE", program)
	for _, sequential := range []bool{false, true} {
		t.Run(fmt.Sprint("sequential=", sequential), func(t *testing.T) {
			f := newObservationFixture(t)
			state := t.TempDir()
			managed, err := mounts.New(f.s.db, state, "", program, storage.New(program))
			if err != nil {
				t.Fatal(err)
			}
			f.s.mounts = managed
			f.s.private = filepath.Join(state, "serve")
			if err = os.Mkdir(f.s.private, 0700); err != nil {
				t.Fatal(err)
			}
			identities, owner, family := observationOwner(t, f)
			// Fixture construction is not provider I/O; keep the controller
			// operation budget separate from cold race-instrumented SQLite setup.
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			payload := bytes.Repeat([]byte("source-range-"), 6000)
			modified := time.Unix(1700000000, 0).UTC()
			var active, peak, gets atomic.Int32
			var badAuth, providerBackoff atomic.Bool
			type originGate struct {
				started chan struct{}
				release chan struct{}
			}
			var gatePointer atomic.Pointer[originGate]
			blockOrigin := func() (*originGate, func()) {
				gate := &originGate{make(chan struct{}, 1), make(chan struct{})}
				gatePointer.Store(gate)
				var once sync.Once
				release := func() { once.Do(func() { gatePointer.Store(nil); close(gate.release) }) }
				t.Cleanup(release)
				return gate, release
			}
			origin := newPrivateSourceServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := active.Add(1)
				defer active.Add(-1)
				for prior := peak.Load(); n > prior && !peak.CompareAndSwap(prior, n); prior = peak.Load() {
				}
				u, p, ok := r.BasicAuth()
				if !ok || u != "fixture" || p != "private-fixture" {
					badAuth.Store(true)
					w.WriteHeader(401)
					return
				}
				if r.URL.Path != "/media/" && r.URL.Path != "/media/film.mkv" {
					http.NotFound(w, r)
					return
				}
				if r.Method == "PROPFIND" {
					if r.URL.Path == "/media/film.mkv" && providerBackoff.Load() {
						w.Header().Set("Retry-After", "17")
						w.WriteHeader(http.StatusTooManyRequests)
						fmt.Fprint(w, "fixture-private-provider-detail")
						return
					}
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(207)
					fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`)
					if r.URL.Path == "/media/" {
						fmt.Fprint(w, `<d:response><d:href>/media/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
					}
					if r.URL.Path != "/media/" || r.Header.Get("Depth") == "1" {
						fmt.Fprintf(w, `<d:response><d:href>/media/film.mkv</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>%d</d:getcontentlength><d:getlastmodified>%s</d:getlastmodified><d:getetag>"origin-one"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, len(payload), modified.Format(http.TimeFormat))
					}
					fmt.Fprint(w, `</d:multistatus>`)
					return
				}
				if r.Method == http.MethodOptions {
					w.Header().Set("DAV", "1")
					return
				}
				if r.Method == http.MethodGet {
					gets.Add(1)
					if gate := gatePointer.Load(); gate != nil {
						select {
						case gate.started <- struct{}{}:
						default:
						}
						select {
						case <-gate.release:
						case <-r.Context().Done():
							return
						}
					}
				}
				w.Header().Set("ETag", `"origin-one"`)
				if sequential {
					// A genuine range-unsupported origin ignores Range. No Portico
					// ReaderAt or prefix spool is introduced to disguise this200.
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
					w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
					if r.Method != http.MethodHead {
						_, _ = w.Write(payload)
					}
					return
				}
				http.ServeContent(w, r, "film.mkv", modified, bytes.NewReader(payload))
			}))
			t.Cleanup(origin.Close)
			config, err := remotemedia.ValidateSourceDAVConfig(ctx, remotemedia.DAVConfig{Root: origin.URL + "/media/", Username: "fixture", Password: "private-fixture", InsecureLocal: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = storeControllerDAV(f, managed, config, sequential); err != nil {
				t.Fatal(err)
			}
			profile, _ := SourceReadPreset("list")
			profile.Limits = ReadLimits{Connections: 1, RequestsPerSecond: 50}
			writer, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.s.ApplySourceReadProfileTx(ctx, writer.Tx(), identities, owner, SourceReadProfileChange{SourceID: "remote", Generation: "1", Profile: profile})
			if err == nil {
				err = writer.Commit()
			} else {
				writer.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			// This running fixture job starts under the newly saved policy.
			if _, err = f.s.db.Exec(`UPDATE inventory_runs SET policy_revision=(SELECT revision FROM library_scan_policies WHERE library_id=?)`, f.library); err != nil {
				t.Fatal(err)
			}
			if err = f.s.startReadRuntime(ctx); err != nil {
				t.Fatal(err)
			}
			defer f.s.stopServeProcesses()
			observed, err := f.s.ObserveScanFile(ctx, f.job, f.file)
			if err != nil {
				t.Fatal("real factory/metadata", err)
			}
			process := observed.process
			defer func() {
				process.Stop()
				wait, stop := context.WithTimeout(context.Background(), 3*time.Second)
				defer stop()
				_ = process.Wait(wait)
			}()
			writer, err = dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := observed.PublishTx(ctx, writer.Tx())
			if err == nil {
				err = writer.Commit()
			} else {
				writer.Rollback()
			}
			if err != nil {
				t.Fatal("scan publication", err)
			}
			if observed.Snapshot().Size != int64(len(payload)) || ref.Revision != 1 {
				t.Fatal(observed.Snapshot(), ref)
			}
			stat, err := f.s.Stat(ctx, f.file)
			if err != nil || stat.Revision != observed.Snapshot().Revision || stat.ObjectIdentity != observed.Snapshot().ObjectIdentity || stat.Size != observed.Snapshot().Size {
				t.Fatal("storage Stat and normalized scan disagreed", stat, observed.Snapshot(), err)
			}
			root, err := f.s.InspectRoot(ctx, filepath.Dir(f.file))
			if err != nil || !root.Directory || root.ObjectIdentity == "" {
				t.Fatal("storage root inspection escaped the source producer", root, err)
			}
			b, _, _ := f.s.find(f.file)
			listingID := "streamed-directory"
			if _, err = f.s.db.ExecContext(ctx, `INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,created_at) VALUES(?,?,'remote','1',?,0)`, listingID, f.job, b.Root); err != nil {
				t.Fatal(err)
			}
			if err = f.s.buildServeSnapshot(ctx, process, b, "", listingID, "1"); err != nil {
				t.Fatal("actual inventory streaming snapshot", err)
			}
			var ready bool
			var children int
			if err = f.s.db.QueryRowContext(ctx, `SELECT ready,(SELECT COUNT(*) FROM remote_listing_entries WHERE listing_id=?) FROM remote_listing_sessions WHERE id=?`, listingID, listingID).Scan(&ready, &children); err != nil || !ready || children != 1 {
				t.Fatal("incomplete streaming snapshot published", ready, children, err)
			}
			page, err := f.s.ListPage(ctx, f.job, b.Root, "")
			if err != nil || page.Pending || len(page.Entries) != 1 || !page.Complete {
				t.Fatal("actual retained listing page", page, err)
			}
			for _, mutation := range []string{"page-bytes", "profile", "observation-aba", "source-root"} {
				writer, err = dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.s.CommitPage(ctx, writer.Tx(), page); err != nil {
					writer.Rollback()
					t.Fatal("positive publication prerequisite", mutation, err)
				}
				writer.Rollback()
				writer, err = dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
				if err != nil {
					t.Fatal(err)
				}
				forged := page
				forged.Entries = append([]storage.Snapshot(nil), page.Entries...)
				switch mutation {
				case "page-bytes":
					forged.Entries[0].Size++
				case "profile":
					_, err = writer.Tx().ExecContext(ctx, `UPDATE remote_source_read_profiles SET revision=revision+1 WHERE source_id='remote'`)
				case "observation-aba":
					_, err = writer.Tx().ExecContext(ctx, `UPDATE remote_scan_observations SET revision=revision+2 WHERE source_id='remote'`)
				case "source-root":
					_, err = writer.Tx().ExecContext(ctx, `UPDATE remote_sources SET root=root||'/replacement' WHERE id='remote'`)
				}
				if err != nil {
					writer.Rollback()
					t.Fatal(err)
				}
				err = f.s.CommitPage(ctx, writer.Tx(), forged)
				writer.Rollback()
				if err == nil {
					t.Fatal("page publication ignored changed bytes/authority", mutation)
				}
			}
			writer, err = dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
			if err == nil {
				err = f.s.CommitPage(ctx, writer.Tx(), page)
			}
			if err == nil {
				err = writer.Commit()
			} else if writer != nil {
				writer.Rollback()
			}
			if err != nil {
				t.Fatal("actual catalogue transaction did not publish observations", err)
			}
			if !sequential {
				// The artwork state reader is synthetic here. The typed resolver,
				// current owner, target, lazy retained lease and actual process/range
				// adapters are production paths; this is not full engine composition.
				revision := f.catalogObservationRevision(t)
				for _, statement := range []struct {
					sql  string
					args []any
				}{
					{`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES('episode-asset',?,?,?,'mkv','h264','aac',1920,1080,60)`, []any{f.file, len(payload), modified.UnixNano()}},
					{`INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head) SELECT X'00112233445566778899AABBCCDDEEFF',id,4,'Episode','episode','e' FROM catalog_libraries WHERE library_id=?`, []any{f.library}},
					{`INSERT INTO catalog_asset_links(entity_id,asset_id) SELECT e.id,a.id FROM catalog_entities e CROSS JOIN catalog_assets a WHERE a.token='episode-asset'`, nil},
					{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) SELECT 'episode-object',id,'episode-asset',incarnation,'film.mkv',?,'{}',?,?,'available' FROM library_sources WHERE id=?`, []any{revision, len(payload), modified.UnixNano(), f.library}},
				} {
					if _, err = f.s.db.ExecContext(ctx, statement.sql, statement.args...); err != nil {
						t.Fatal(err)
					}
				}
				var episode string
				if err = f.s.db.QueryRowContext(ctx, `SELECT pid(public_id) FROM catalog_entities WHERE kind=4`).Scan(&episode); err != nil {
					t.Fatal(err)
				}
				resolver := EpisodeAuthorityResolver{Sources: f.s, Identities: identities, Episodes: &episodeFactsFixture{mode: "off"}}
				request := EpisodeAuthorityRequest{Target: EpisodeTarget{LibraryID: f.library, LibrarySourceID: f.library, ObjectID: "episode-object", SourceRevision: revision, EpisodeID: episode}, Purpose: EpisodeExplicitOneEpisode, Owner: owner, Operation: "episode_thumbnail", CostClass: "decode"}
				view, err := dbwork.BeginSnapshot(ctx, f.s.db)
				if err != nil {
					t.Fatal(err)
				}
				source, err := resolver.ResolveEpisodeReadSourceTx(ctx, view.Tx(), request, func() { t.Error("actor revocation became changed-file notice") })
				view.Rollback()
				if err != nil || source == nil {
					t.Fatal("explicit Off/list-only lazy source", err)
				}
				defer source.Close()
				if source.remote.processGeneration != "" {
					t.Fatal("capture invented an opened physical epoch")
				}
				input, err := source.Open(ctx)
				if err != nil {
					t.Fatal("real lazy Episode range input", err)
				}
				got, err := input.ReadExtent(ctx, 127, 257)
				if err != nil || !bytes.Equal(got, payload[127:384]) {
					input.Close()
					t.Fatal("actual Episode extent", len(got), err)
				}
				if err = input.Validate(ctx); err != nil {
					input.Close()
					t.Fatal("fresh post-decode validation", err)
				}
				if err = input.Close(); err != nil {
					t.Fatal(err)
				}
				errorView, err := dbwork.BeginSnapshot(ctx, f.s.db)
				if err != nil {
					t.Fatal(err)
				}
				failedSource, err := resolver.ResolveEpisodeReadSourceTx(ctx, errorView.Tx(), request, func() {})
				errorView.Rollback()
				if err != nil {
					t.Fatal(err)
				}
				failedInput, err := failedSource.Open(ctx)
				if err != nil {
					failedSource.Close()
					t.Fatal(err)
				}
				stopper, ok := failedInput.(interface{ StopAndRecheck(context.Context) error })
				if !ok {
					failedInput.Close()
					failedSource.Close()
					t.Fatal("actual Episode range input has no terminal recheck")
				}
				beforeFailureGets := gets.Load()
				if err = stopper.StopAndRecheck(ctx); err != nil || gets.Load() != beforeFailureGets {
					t.Fatal("Episode error recheck reopened media", err, gets.Load(), beforeFailureGets)
				}
				if err = failedInput.Validate(ctx); err == nil {
					t.Fatal("failed Episode input remained usable")
				}
				failedInput.Close()
				failedSource.Close()
				publication, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
				if err != nil {
					t.Fatal(err)
				}
				if err = source.CheckTx(ctx, publication.Tx()); err != nil {
					publication.Rollback()
					t.Fatal("closing child input retired retained publication source", err)
				}
				if _, err = publication.Tx().ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
					publication.Rollback()
					t.Fatal(err)
				}
				if err = source.CheckTx(ctx, publication.Tx()); err == nil {
					publication.Rollback()
					t.Fatal("publication missed actual current owner revocation")
				}
				publication.Rollback()
				if err = source.Authorize(ctx, "episode_thumbnail", "decode", true); err == nil {
					t.Fatal("rollback revived failed retained Episode source")
				}
				if err = source.Close(); err != nil {
					t.Fatal(err)
				}
			}
			first := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
			defer first.Close()
			second := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
			defer second.Close()
			if !f.s.PlaybackActive("remote") {
				t.Fatal("interactive source leases disappeared from removal admission")
			}
			if first.Binding().Sequential != sequential || second.Binding().Profile.Limits.Connections != 1 {
				t.Fatal(first.Binding(), second.Binding())
			}
			childContext, cancelChild := context.WithCancel(ctx)
			if sequential {
				child, err := first.OpenSequential(childContext)
				if err != nil {
					t.Fatal(err)
				}
				cancelChild()
				if n, err := child.Read(make([]byte, 16)); n != 0 || !errors.Is(err, context.Canceled) {
					t.Fatal("canceled progressive child exposed bytes", n, err)
				}
				child.Close()
			} else {
				child, err := first.OpenRange(childContext)
				if err != nil {
					t.Fatal(err)
				}
				cancelChild()
				if n, err := child.ReadAt(childContext, make([]byte, 16), 0); n != 0 || !errors.Is(err, context.Canceled) {
					t.Fatal("canceled range child exposed bytes", n, err)
				}
				child.Close()
			}
			if err := first.Check(ctx); err != nil {
				t.Fatal("child cancellation killed its Reading session", err)
			}
			beforeReadGets := gets.Load()
			if sequential {
				if _, err = first.OpenRange(ctx); !errors.Is(err, storage.ErrRemoteRange) {
					t.Fatal("sequential became ReadAt", err)
				}
				stream, err := first.OpenSequential(ctx)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(stream)
				stream.Close()
				if err != nil || !bytes.Equal(got, payload) {
					t.Fatal("progressive bytes", len(got), err)
				}
				if gets.Load() != beforeReadGets+1 {
					t.Fatal("from-start body replayed", gets.Load(), beforeReadGets)
				}
			} else {
				rangeReader, err := first.OpenRange(ctx)
				if err != nil {
					t.Fatal(err)
				}
				out := make([]byte, 256)
				if n, err := rangeReader.ReadAt(ctx, out, 123); err != nil || n != len(out) || !bytes.Equal(out, payload[123:123+len(out)]) {
					t.Fatal("range bytes", n, err)
				}
				rangeReader.Close()
			}
			if err = first.Close(); err != nil {
				t.Fatal(err)
			}
			if !f.s.PlaybackActive("remote") {
				t.Fatal("closing one reader hid another active session")
			}
			if err = second.Check(ctx); err != nil {
				t.Fatal("closing one logical reader killed shared process", err)
			}
			if next, err := second.process(ctx); err != nil || next != process {
				t.Fatal("shared source process changed", err)
			}
			if peak.Load() != 1 || badAuth.Load() {
				t.Fatal("upstream source cap/auth", peak.Load(), badAuth.Load())
			}
			if counts := process.OperationCounts(); counts.Capacity != 1 || counts.Peak != 1 || counts.Active != 0 {
				t.Fatal("actual source pools did not share cap1", counts)
			}
			if !sequential {
				endpoint, err := process.Endpoint(ctx)
				if err != nil {
					t.Fatal(err)
				}
				entry, err := endpoint.Stat(ctx, "film.mkv", process.Check)
				if err != nil {
					t.Fatal(err)
				}
				beforeBaseline := gets.Load()
				baseline, err := endpoint.ReadRange(ctx, "film.mkv", entry.ETag, int64(len(payload)), 456, 128, process.Check)
				if err != nil || !bytes.Equal(baseline, payload[456:584]) {
					t.Fatal("single-viewer origin baseline", baseline, err)
				}
				baselineGETs := gets.Load() - beforeBaseline
				if baselineGETs < 1 {
					t.Fatal("single-viewer baseline contacted no origin")
				}
				beforeShared, beforeGET := endpoint.CoalescedRanges(), gets.Load()
				gate, unblockShared := blockOrigin()
				read := func() <-chan error {
					done := make(chan error, 1)
					go func() {
						data, e := endpoint.ReadRange(ctx, "film.mkv", entry.ETag, int64(len(payload)), 456, 128, process.Check)
						if e == nil && !bytes.Equal(data, payload[456:584]) {
							e = errors.New("shared source extent differs")
						}
						done <- e
					}()
					return done
				}
				leader := read()
				select {
				case <-gate.started:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				joiner := read()
				deadline := time.NewTimer(2 * time.Second)
				tick := time.NewTicker(time.Millisecond)
				for endpoint.CoalescedRanges() == beforeShared {
					select {
					case <-deadline.C:
						t.Fatal("same-file viewer did not join admitted transfer")
					case <-tick.C:
					}
				}
				tick.Stop()
				deadline.Stop()
				unblockShared()
				if a, b := <-leader, <-joiner; a != nil || b != nil || gets.Load()-beforeGET != baselineGETs {
					t.Fatal("same-file viewers duplicated the admitted origin workload", a, b, gets.Load()-beforeGET, baselineGETs)
				}
				t.Logf("shared_range_joiners=%d single_reader_origin_gets=%d shared_reader_origin_gets=%d admitted_peak=%d", endpoint.CoalescedRanges()-beforeShared, baselineGETs, gets.Load()-beforeGET, process.OperationCounts().Peak)

				// The actual retained leases must share at cap1 too, rather
				// than waiting behind a duplicate pre-read metadata request.
				leaseA := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
				leaseB := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
				defer leaseA.Close()
				defer leaseB.Close()
				rangeA, e := leaseA.OpenRange(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer rangeA.Close()
				rangeB, e := leaseB.OpenRange(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer rangeB.Close()
				gate, releaseLeases := blockOrigin()
				beforeLeaseGets, beforeJoiners := gets.Load(), f.s.sharedRangeJoiners.Load()
				readLease := func(reader *SourceRange) <-chan error {
					done := make(chan error, 1)
					go func() {
						data := make([]byte, 128)
						n, e := reader.ReadAt(ctx, data, 456)
						if e == nil && (n != len(data) || !bytes.Equal(data, payload[456:584])) {
							e = errors.New("retained shared extent differs")
						}
						done <- e
					}()
					return done
				}
				leaderLease := readLease(rangeA)
				select {
				case <-gate.started:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if n, e := rangeB.ReadAt(ctx, make([]byte, 128), 100); n != 0 || !errors.Is(e, storage.ErrBusy) {
					t.Fatal("distinct retained extent escaped cap1", n, e)
				}
				if e := leaseB.Check(ctx); e != nil {
					t.Fatal("resource contention retired retained authority", e)
				}
				joinerLease := readLease(rangeB)
				limit := time.NewTimer(2 * time.Second)
				tick = time.NewTicker(time.Millisecond)
				for f.s.sharedRangeJoiners.Load() == beforeJoiners {
					select {
					case <-limit.C:
						t.Fatal("retained viewer did not join at cap1")
					case <-tick.C:
					}
				}
				tick.Stop()
				limit.Stop()
				releaseLeases()
				if a, b := <-leaderLease, <-joinerLease; a != nil || b != nil || gets.Load()-beforeLeaseGets != baselineGETs {
					t.Fatal("retained viewers duplicated origin work", a, b, gets.Load()-beforeLeaseGets, baselineGETs)
				}
				t.Logf("retained_shared_joiners=%d origin_gets=%d admitted_peak=%d", f.s.sharedRangeJoiners.Load()-beforeJoiners, gets.Load()-beforeLeaseGets, process.OperationCounts().Peak)

				// Closing an actual in-flight child must not retire its
				// owner's retained lease. No synthetic physical epoch is used.
				gate, releaseClosed := blockOrigin()
				closing := readLease(rangeA)
				select {
				case <-gate.started:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				joinedBeforeClose := f.s.sharedRangeJoiners.Load()
				otherReader := readLease(rangeB)
				closeDeadline := time.NewTimer(2 * time.Second)
				closeTick := time.NewTicker(time.Millisecond)
				for f.s.sharedRangeJoiners.Load() == joinedBeforeClose {
					select {
					case <-closeDeadline.C:
						t.Fatal("other retained viewer did not join before leader close")
					case <-closeTick.C:
					}
				}
				closeTick.Stop()
				closeDeadline.Stop()
				rangeA.Close()
				releaseClosed()
				if e := <-closing; e == nil {
					t.Fatal("closed active child returned bytes")
				}
				if e := leaseA.Check(ctx); e != nil {
					t.Fatal("closing active child retired parent", e)
				}
				if e := <-otherReader; e == nil {
					t.Fatal("aborted shared transfer exposed bytes to another reader")
				}
				if e := leaseB.Check(ctx); e != nil {
					t.Fatal("leader child-close retired another reader's authority", e)
				}
			}
			for _, closedBeforeError := range []bool{false, true} {
				failedReader := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
				var child io.Closer
				if sequential {
					child, err = failedReader.OpenSequential(ctx)
				} else {
					child, err = failedReader.OpenRange(ctx)
				}
				if err != nil {
					t.Fatal(err)
				}
				if closedBeforeError {
					child.Close()
				}
				beforeErrorGets := gets.Load()
				if err = failedReader.StopAndRecheck(ctx); err != nil {
					t.Fatal("decode-error metadata recheck", closedBeforeError, err)
				}
				if gets.Load() != beforeErrorGets || failedReader.Check(ctx) == nil {
					t.Fatal("terminal recheck reopened media or retained authority", gets.Load(), beforeErrorGets)
				}
				child.Close()
				if err = second.Check(ctx); err != nil {
					t.Fatal("decode-error reader killed another session", err)
				}
			}
			if !sequential {
				metadata, err := process.Metadata(ctx)
				if err != nil {
					t.Fatal(err)
				}
				providerBackoff.Store(true)
				_, err = metadata.Stat(ctx, "film.mkv", process.Check)
				if err == nil || strings.Contains(err.Error(), "fixture-private") || strings.Contains(err.Error(), origin.URL) {
					t.Fatal("provider failure lost or private upstream detail escaped", err)
				}
				deadline := time.Now().Add(3 * time.Second)
				for !process.PacerObservation().Backoff && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if report := process.PacerObservation(); !report.Backoff || report.Sequence < 1 || report.ObservedAt.IsZero() {
					t.Fatal("actual upstream pacer report unavailable", report)
				}
				if !f.s.sourcePacerBackoff("remote", "1") || f.s.sourcePacerBackoff("remote", "2") {
					t.Fatal("live report ignored or attributed to replacement generation")
				}
				rows, err := f.s.List(ctx)
				if err != nil || len(rows) != 1 || rows[0].State != "rate_limited" || rows[0].ErrorCode != "rate_limited" {
					t.Fatal("owner status did not project actual asynchronous report", rows, err)
				}
				if state, err := f.s.ViewerStatus(ctx, f.file); err != nil || state != "unavailable" {
					t.Fatal("viewer status exposed a false ready state", state, err)
				}
				providerBackoff.Store(false)
				if _, err = observeProcessFile(ctx, process, "film.mkv", process.Check); err != nil {
					t.Fatal("actual fresh backend recovery", err)
				}
				if f.s.sourcePacerBackoff("remote", "1") {
					t.Fatal("fresh success retained older backoff report")
				}
			}
			writer, err = dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
			if err != nil {
				t.Fatal(err)
			}
			profile.Limits.RequestsPerSecond = 49
			updated, err := f.s.ApplySourceReadProfileTx(ctx, writer.Tx(), identities, owner, SourceReadProfileChange{SourceID: "remote", Generation: "1", ExpectedRevision: result.Profile.Revision, Profile: profile})
			if err == nil {
				err = writer.Commit()
			} else {
				writer.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = f.s.ReadProfileCommitted(ctx, "remote", updated.Profile.Revision); err != nil {
				t.Fatal(err)
			}
			if f.s.PlaybackActive("remote") {
				t.Fatal("revoked profile left a live interactive activity record")
			}
			if err = second.Check(ctx); !errors.Is(err, storage.ErrRemoteChanged) {
				t.Fatal("old profile lease survived", err)
			}
			if err = process.Wait(ctx); !errors.Is(err, rcloneprogram.ErrServeStopped) {
				t.Fatal("physical retirement", err)
			}
			f.s.serveMu.Lock()
			remaining := len(f.s.serveLeases)
			f.s.serveMu.Unlock()
			if remaining != 0 {
				t.Fatal("physical slot retained after Wait", remaining)
			}
			if _, err = second.OpenSequential(ctx); err == nil {
				t.Fatal("old reader reacquired a replacement process")
			}
			files, err := os.ReadDir(f.s.private)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range files {
				if strings.HasPrefix(file.Name(), "serve-config-") || strings.HasPrefix(file.Name(), "serve-cache-") {
					t.Fatal("owned transient survived Wait", file.Name())
				}
			}
		})
	}
}

func storeControllerDAV(f observationFixture, managed *mounts.Service, config remotemedia.DAVConfig, sequential bool) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	stored, err := managed.Seal(raw)
	clear(raw)
	if err != nil {
		return err
	}
	defer clear(stored)
	_, err = f.s.db.Exec(`UPDATE remote_sources SET config=?,range_support=CASE WHEN ? THEN 'unsupported' ELSE 'random' END,error_code=CASE WHEN ? THEN 'range_unsupported' ELSE '' END`, stored, sequential, sequential)
	return err
}
