package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/connectivity"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
)

// memberCeilingReader is how the conversion fallback reads a member's current
// remote bitrate cap: from the member limits, inside the transaction it is
// already in. It re-plans with no request, so nothing else can tell it.
func memberCeilingReader() playback.MemberCeilingReader {
	limits := &access.Enforcer{Store: &access.Store{}}
	return func(ctx context.Context, tx *sql.Tx, account string) (int, error) {
		return limits.RemoteBitrateCap(ctx, tx, identity.Principal{Viewer: identity.Viewer{AccountID: account}})
	}
}

// initializeAdministration wires workstream G: the access store, the message log
// recorder, client log uploads and LAN discovery.
//
// The recorder becomes the standard library log package's output, so every
// existing log.Printf call in the server lands in the message log and the live
// tail without a single call site changing. Containers and interactive runs
// keep stderr; packaged background launchers opt out once this recorder exists.
func initializeAdministration(ctx context.Context, db *sql.DB, console *operations.Store, ident *identity.Service, state string, port int, existing ...*servicelog.Recorder) (httpapi.AccessArea, func()) {
	var recorder *servicelog.Recorder
	if len(existing) > 0 {
		recorder = existing[0]
	}
	if recorder == nil {
		recorder = servicelog.New(servicelog.Options{Directory: storagepaths.Path(state, "logs")})
	}
	installMessageLogOutput(recorder)
	advertiser := connectivity.NewAdvertiser(connectivity.AdvertiserOptions{Instance: ident.Name(), Host: ident.Name(), Port: port,
		TXT: []string{"serverId=" + ident.ID(), "product=portico"}})
	// The DLNA switch reuses the same start/stop controller for its SSDP run.
	dlnaAdvertiser := connectivity.NewAdvertiser(connectivity.AdvertiserOptions{})
	out := httpapi.AccessArea{Access: access.New(db), Logs: recorder, ClientLogs: servicelog.NewClientLogStore(db), Advertiser: advertiser, DLNA: dlnaAdvertiser}
	// Invite-only sign-up spends its invitation through the invitation store.
	ident.RedeemInvitation = out.Access.RegistrationRedeemer()
	if console != nil {
		if document, err := console.Settings(ctx, operations.AllowServerScope); err == nil {
			recorder.SetLevel(document.Effective.LogLevel)
			recorder.SetRetention(document.Effective.LogRetentionDays())
			if err := out.ClientLogs.Prune(ctx, document.Effective.LogRetentionDays()["client"]); err != nil && ctx.Err() == nil {
				log.Printf("warn: Client report retention: %v", err)
			}
			advertiser.Apply(document.Effective.LANDiscoveryEnabled)
			dlnaAdvertiser.Apply(document.Effective.DLNAEnabled)
		}
	}
	// A debug window that was open when the process stopped stays open: an owner
	// who raised the level to catch an intermittent failure should not lose it to
	// the restart that failure caused.
	var expires int64
	if err := db.QueryRowContext(ctx, `SELECT expires_ms FROM diagnostics_debug_window WHERE singleton=1`).Scan(&expires); err == nil {
		recorder.OpenDebugWindow(time.UnixMilli(expires).UTC())
	}
	return out, func() {
		advertiser.Close()
		dlnaAdvertiser.Close()
		_ = recorder.Close()
	}
}

func installMessageLogOutput(recorder *servicelog.Recorder) {
	if os.Getenv("PORTICO_LOG_STDERR") == "off" {
		log.SetOutput(recorder)
		return
	}
	log.SetOutput(recorder.Tee(log.Writer()))
}
