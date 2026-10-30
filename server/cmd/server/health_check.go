package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// A container needs a way to ask "are you up?" that does not require curl in
// the image, does not need credentials, and does not touch the database. The
// readiness route is already all three — it is deliberately unauthenticated and
// answers while the database is still opening, reporting the phase — so the
// health check is the server's own binary asking it.
//
// Exit codes are the ones a container runtime reads: 0 ready, 1 not.
func healthCheck() int {
	address := os.Getenv("PORTICO_BIND")
	if address == "" {
		address = defaultBind
	}
	// A bind of 0.0.0.0 or [::] says where the server listens, not where to
	// reach it. From inside the container that is the loopback address.
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "PORTICO_BIND is not a host:port: %v\n", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + net.JoinHostPort(host, port) + "/v1/readiness")
	if err != nil {
		fmt.Fprintf(os.Stderr, "not answering: %v\n", err)
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		// 503 with a phase is the honest "still starting" answer, which is not
		// ready but is also not broken; the runtime's start period covers it.
		fmt.Fprintf(os.Stderr, "not ready: %d\n", response.StatusCode)
		return 1
	}
	return 0
}
