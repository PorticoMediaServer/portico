package social

import (
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
)

func TestReceiverEventsIsolationAndCoalescing(t *testing.T) {
	db := new(sql.DB)
	otherDB := new(sql.DB)
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p"}}
	other := p
	other.ProfileID = "other"
	events, cancel := SubscribeReceiverEvents(db, p)
	defer cancel()
	<-events
	isolated, stop := SubscribeReceiverEvents(db, other)
	defer stop()
	<-isolated
	otherServer, stopServer := SubscribeReceiverEvents(otherDB, p)
	defer stopServer()
	<-otherServer
	s := &Store{DB: db}
	s.publishReceiverEvent(p, "receiver", "grant")
	if event := <-events; event.Kind != "grant" || event.ReceiverID != "receiver" {
		t.Fatal(event)
	}
	select {
	case event := <-isolated:
		t.Fatal("profile leak", event)
	default:
	}
	select {
	case event := <-otherServer:
		t.Fatal("server leak", event)
	default:
	}
	s.publishReceiverEvent(p, "one", "grant")
	s.publishReceiverEvent(p, "two", "handoff")
	if event := <-events; event.Kind != "resync" || event.ReceiverID != "" {
		t.Fatal(event)
	}
	cancel()
	cancel()
}
