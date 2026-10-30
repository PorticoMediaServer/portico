package playbackv1

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"strings"
	"testing"
)

func TestReceiverMusicBakedGainAndPrivateNextR29(t *testing.T) {
	f := newCarryFixture(t, true)
	ctx := context.Background()
	f.catalog.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=1`)
	caps := playback.BaselineCapabilityDocument()
	caps.Client.Family = "cast"
	caps.Client.Form = "cast"
	caps.Client.Platform = "cast"
	caps.AudioDecode = nil
	caps.Engines.Native.Evidence = playback.EvidenceDeclared
	caps.Engines.Native.Audio = []playback.ClientAudioCodec{{Codec: "flac", MaxChannels: 2, MaxSampleRate: 96000, MaxBitDepth: 24, Evidence: playback.EvidenceDeclared}, {Codec: "aac", MaxChannels: 2, MaxSampleRate: 48000, Evidence: playback.EvidenceDeclared}}
	caps.Engines.Native.Transports = []playback.ClientTransport{{Transport: playback.TransportDirect, Containers: []string{"flac", "mp4", "aac"}, Audio: []string{"flac", "aac"}}}
	if _, e := f.service.Playback.PutCapabilities(ctx, f.caller.Principal, f.caller.DeviceID, -1, caps); e != nil {
		t.Fatal(e)
	}
	f.service.Playback.ConfigureDelivery(nil, func(tx *sql.Tx, v identity.Viewer, _ string) (playback.DeliveryPreferenceReader, error) {
		return operations.PreferenceValues{"music.audioNormalization": "track", "music.neverBoost": true}, nil
	}, nil, "")
	for _, item := range f.items {
		f.catalog.Exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('carry-library',?,'replaygain_track_gain','embedded','-12 dB'),('carry-library',?,'replaygain_track_peak','embedded','0.9')`, item.Token, item.Token)
	}
	q := f.queue()
	first := q.Session.Presentation
	plan := first.AudioRender
	if plan == nil || plan.Mode != "converted" || plan.Codec != "flac" || plan.LoudnessAppliedDB == nil || *plan.LoudnessAppliedDB != -12 || plan.Gain != nil || first.URL != plan.URL || first.Container != plan.Container {
		t.Fatalf("unbaked receiver presentation %+v %+v", first, plan)
	}
	next, e := f.service.PrepareNext(ctx, f.caller, q.Queue.ID, q.Queue.Revision, "receiver-private-next-r29", PrepareNextRequest{SessionID: q.Session.ID, SessionGeneration: q.Session.Presentation.Generation})
	if e != nil {
		t.Fatal(e)
	}
	if next.Presentation.URL == "" || next.Presentation.URL != next.Presentation.AudioRender.URL {
		t.Fatalf("ordinary private URL missing %+v", next)
	}
	grant := strings.Split(next.Presentation.URL, "/")[3]
	if _, _, _, e = f.service.Playback.ResolveGrant(grant); e == nil {
		t.Fatal("private grant accepted by ordinary unlimited route")
	}
	if _, _, _, e = f.service.Playback.ResolveDecodeGrant(grant); e != nil {
		t.Fatal(e)
	}
	committed, e := f.service.CommitNext(ctx, f.caller, q.Queue.ID, CommitNextRequest{Token: next.Token})
	if e != nil {
		t.Fatal(e)
	}
	if committed.Session == nil || committed.Session.Presentation.URL != next.Presentation.URL {
		t.Fatal("commit changed prepared stream identity", committed)
	}
	replay, e := f.service.CommitNext(ctx, f.caller, q.Queue.ID, CommitNextRequest{Token: next.Token})
	if e != nil || replay.Session.ID != committed.Session.ID {
		t.Fatal(replay, e)
	}
}
