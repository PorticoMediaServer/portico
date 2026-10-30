//go:build linux || darwin

package playbackdisc

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// These regression sources exercise persistence and collection only. The
// production subtitle writer must independently prove the original personal
// owner and active presentation; a relation is never a media grant.
func seedPrivateDiscSubtitle(t *testing.T, f *discFixture, id string) {
	t.Helper()
	owner := persistence.PersonalOwnerKey(f.snapshot.Actor.Authority, f.snapshot.Actor.AccountID, f.snapshot.Actor.ProfileID)
	if err := f.exec(`INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision) VALUES(?,7,?,'personal',?,1)`, id, discAssetID, owner); err != nil {
		t.Fatal(err)
	}
	if err := f.exec(`INSERT INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) VALUES(?,1,'',0,'pgs','eng','English','embedded','','',?,?,1,?)`, id, discSize, discModifiedNS, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func mapPrivateDiscSubtitle(f *discFixture, id string, index int) error {
	return f.exec(`INSERT INTO playback_disc_subtitle_resources(snapshot_id,resource_id,stream_index) VALUES(?,?,?)`, f.snapshot.ID, id, index)
}

func TestPrivateDiscSubtitleAttachesInOriginalPendingWriter(t *testing.T) {
	f := newDiscFixture(t)
	seedPrivateDiscSubtitle(t, f, "private-subtitle")
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		if err := f.store.PublishPendingTx(ctx, tx, f.snapshot, plainCheck()); err != nil {
			return err
		}
		// PublishPendingTx has sealed the facts but not activated the input.
		_, err := tx.ExecContext(ctx, `INSERT INTO playback_disc_subtitle_resources(snapshot_id,resource_id,stream_index) VALUES(?,?,2)`, f.snapshot.ID, "private-subtitle")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(f.snapshot.key(), f.snapshot.ID, plainCheck()); err != nil {
		t.Fatal(err)
	}
	if got := mustInt(t, f.db, `SELECT stream_index FROM playback_disc_subtitle_resources WHERE resource_id='private-subtitle'`); got != 2 {
		t.Fatal("measured subtitle index changed", got)
	}
	seedPrivateDiscSubtitle(t, f, "late-subtitle")
	if err := mapPrivateDiscSubtitle(f, "late-subtitle", 2); err == nil {
		t.Fatal("active snapshot accepted a late subtitle")
	}
}

func TestPrivateDiscSubtitleRefusesWrongMeasuredOrPersonalResource(t *testing.T) {
	for _, name := range []string{"missing stream", "audio stream", "shared", "deleted", "other source", "other item"} {
		t.Run(name, func(t *testing.T) {
			f := newDiscFixture(t)
			seedPrivateDiscSubtitle(t, f, "private-subtitle")
			if err := f.publish(f.snapshot, plainCheck()); err != nil {
				t.Fatal(err)
			}
			index := 2
			var err error
			switch name {
			case "missing stream":
				index = 99
			case "audio stream":
				index = 1
			case "shared":
				err = f.exec(`UPDATE subtitle_resources SET scope='shared',owner='' WHERE id='private-subtitle'`)
			case "deleted":
				err = f.exec(`UPDATE subtitle_resources SET deleted=1 WHERE id='private-subtitle'`)
			case "other source":
				err = f.exec(`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES('another-asset','/other.iso',?,?,'iso','','',0,0,0)`, discSize, discModifiedNS)
				if err == nil {
					err = f.exec(`UPDATE subtitle_resources SET source_id='another-asset' WHERE id='private-subtitle'`)
				}
			case "other item":
				err = f.exec(`INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_title,sort_key,sort_head) SELECT randomblob(16),library_id,kind,'Other','Other','other','O' FROM catalog_entities WHERE id=7`)
				if err == nil {
					err = f.exec(`UPDATE subtitle_resources SET item_id=(SELECT id FROM catalog_entities WHERE title='Other') WHERE id='private-subtitle'`)
				}
			}
			if err != nil {
				t.Fatal("prepare refusal", err)
			}
			if err := mapPrivateDiscSubtitle(f, "private-subtitle", index); err == nil {
				t.Fatal("mismatched resource accepted")
			}
			if got := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_subtitle_resources`); got != 0 {
				t.Fatal("refused mapping persisted", got)
			}
		})
	}
}

func TestPrivateDiscSubtitleMappingAndResourceIdentityAreImmutable(t *testing.T) {
	f := newDiscFixture(t)
	seedPrivateDiscSubtitle(t, f, "private-subtitle")
	seedPrivateDiscSubtitle(t, f, "duplicate-stream")
	if err := f.publish(f.snapshot, plainCheck()); err != nil {
		t.Fatal(err)
	}
	if err := mapPrivateDiscSubtitle(f, "private-subtitle", 2); err != nil {
		t.Fatal(err)
	}
	if err := mapPrivateDiscSubtitle(f, "duplicate-stream", 2); err == nil {
		t.Fatal("one measured stream received two resources")
	}
	for _, query := range []string{
		`UPDATE playback_disc_subtitle_resources SET stream_index=1 WHERE resource_id='private-subtitle'`,
		`UPDATE subtitle_resources SET owner='another-owner' WHERE id='private-subtitle'`,
		`UPDATE subtitle_resources SET scope='shared',owner='' WHERE id='private-subtitle'`,
		`DELETE FROM playback_disc_subtitle_resources WHERE resource_id='private-subtitle'`,
		`DELETE FROM subtitle_resources WHERE id='private-subtitle'`,
	} {
		if err := f.exec(query); err == nil {
			t.Fatalf("private relation changed: %s", query)
		}
	}
}

func TestPrivateDiscSubtitleCollectionRetainsPinsAndRetiresOnlyOwnResource(t *testing.T) {
	f := newDiscFixture(t)
	seedPrivateDiscSubtitle(t, f, "private-subtitle")
	seedPrivateDiscSubtitle(t, f, "unrelated-subtitle")
	if err := f.publish(f.snapshot, plainCheck()); err != nil {
		t.Fatal(err)
	}
	if err := mapPrivateDiscSubtitle(f, "private-subtitle", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(f.snapshot.key(), f.snapshot.ID, plainCheck()); err != nil {
		t.Fatal(err)
	}
	pin, err := f.pin(f.snapshot.key(), f.snapshot.ID, "subtitle-reader", time.UnixMilli(f.until-1), plainCheck())
	if err != nil {
		t.Fatal(err)
	}
	if err = f.exec(`INSERT INTO playback_subtitle_pins(session_id,resource_id,resource_revision) VALUES(?,'private-subtitle',1)`, discSessionID); err != nil {
		t.Fatal(err)
	}
	if err = f.retire(f.snapshot.key(), f.snapshot.ID); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"snapshot reader and subtitle pin", "subtitle pin"} {
		if _, got, err := f.collect("", 64); err != nil || got != 0 {
			t.Fatal(phase, got, err)
		}
		if got := mustInt(t, f.db, `SELECT deleted FROM subtitle_resources WHERE id='private-subtitle'`); got != 0 {
			t.Fatal("pinned resource retired", got)
		}
		if err := f.exec(`DELETE FROM playback_disc_subtitle_resources WHERE resource_id='private-subtitle'`); err == nil {
			t.Fatal("pinned private resource mapping deleted")
		}
		if phase == "snapshot reader and subtitle pin" {
			if err := f.release(pin); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Model the existing subtitle cleanup's exact release, not a new worker.
	if err = f.exec(`DELETE FROM playback_subtitle_pins WHERE session_id=? AND resource_id='private-subtitle' AND resource_revision=1`, discSessionID); err != nil {
		t.Fatal(err)
	}
	// Parent deletion cannot silently cascade the scope discriminator away.
	if err = f.exec(`DELETE FROM playback_disc_snapshots WHERE id=?`, f.snapshot.ID); err == nil {
		t.Fatal("snapshot deleted before its own resource cleanup")
	}
	if _, got, err := f.collect("", 64); err != nil || got != 1 {
		t.Fatal("released snapshot was not collected", got, err)
	}
	if got := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_subtitle_resources`); got != 0 {
		t.Fatal("mapping retained after collection", got)
	}
	if got := mustInt(t, f.db, `SELECT deleted FROM subtitle_resources WHERE id='private-subtitle'`); got != 1 {
		t.Fatal("private resource became generic after losing its scope", got)
	}
	if got := mustInt(t, f.db, `SELECT deleted FROM subtitle_resources WHERE id='unrelated-subtitle'`); got != 0 {
		t.Fatal("unrelated personal resource retired", got)
	}
	if got := mustInt(t, f.db, `SELECT count(*) FROM subtitle_revisions WHERE resource_id='private-subtitle'`); got != 1 {
		t.Fatal("snapshot collection took over subtitle revision/artifact cleanup", got)
	}
}
