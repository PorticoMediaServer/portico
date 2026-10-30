package main

import (
	"net/http"
	"time"
)

// Handler admission happens after the HTTP/2 transport has allocated a stream.
// Bound that earlier allocation and upload flow-control windows too. These
// windows govern incoming request data, not outgoing media throughput.
func serverHTTP2Policy() *http.HTTP2Config {
	return &http.HTTP2Config{
		MaxConcurrentStreams:          32,
		MaxReadFrameSize:              16 << 10,
		MaxReceiveBufferPerConnection: 256 << 10,
		MaxReceiveBufferPerStream:     64 << 10,
		// This is a rolling socket-progress deadline, not a maximum playback
		// duration. A peer that stalls socket writes is disconnected; a viewer
		// receiving bytes can continue for hours. Media and SSE handlers also
		// bound stalls caused by HTTP/2 flow control rather than the socket.
		WriteByteTimeout: 15 * time.Second,
	}
}

// Reserve an eighth of discoverable host/process memory for connection
// overhead. The 2 MiB allowance includes 32 streams' header/state/buffer costs
// and the connection receive window; it is a planning estimate, not a hard
// memory guarantee. Handler admission and request-body limits remain necessary.
// A 256 MiB host gets 16 connections, 1 GiB gets 64, and 32 GiB gets 2,048.
func connectionBudget(memoryBytes int64) int {
	if memoryBytes <= 0 {
		return 64 // Unknown hosts start with a bounded, modest working budget.
	}
	limit := memoryBytes / 8 / (2 << 20)
	if limit < 16 {
		return 16
	}
	if limit > maximumConnections {
		return maximumConnections
	}
	return int(limit)
}
