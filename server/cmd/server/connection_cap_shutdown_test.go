package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func waitForDeferredAccept(t *testing.T, listener *cappedListener) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for listener.deferred.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("accept never reached the capacity wait")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConnectionCapCloseWakesCapacityWait(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := capConnections(raw, 1)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() { conn, _ := listener.Accept(); accepted <- conn }()
	client, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("first connection was not accepted before the deadline")
	}
	if conn == nil {
		t.Fatal("first connection was not accepted")
	}
	defer conn.Close()
	done := make(chan error, 1)
	go func() { _, err := listener.Accept(); done <- err }()
	waitForDeferredAccept(t, listener)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("closed accept: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing a saturated listener did not wake accept")
	}
	if listener.held.Load() != 1 {
		t.Fatal("closing the listener interrupted its retained connection")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if listener.held.Load() != 0 || !listener.take() {
		t.Fatal("connection accounting failed to recover")
	}
}

func TestHTTPShutdownDeadlineAtConnectionCapacity(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := capConnections(raw, 1)
	release := make(chan struct{})
	entered := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(entered); <-release })}
	defer server.Close()
	defer close(release)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	client, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	waitForDeferredAccept(t, listener)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Shutdown(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown of an active request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown hung before observing its deadline")
	}
	select {
	case err := <-served:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not exit")
	}
}

// The owner's saved limit arrives after the listener is already serving; a
// larger limit must release an accept parked at the startup ceiling.
func TestConnectionLimitGrowsInPlace(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := consoleConnections(raw, 1)
	defer listener.Close()
	for listener.take() {
	}
	accepted := make(chan net.Conn, 1)
	go func() { conn, _ := listener.Accept(); accepted <- conn }()
	client, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	waitForDeferredAccept(t, listener)
	listener.setOrdinaryLimit(2)
	select {
	case conn := <-accepted:
		if conn == nil {
			t.Fatal("accept failed after the limit grew")
		}
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("a larger limit did not wake the parked accept")
	}
	if open, limit := listener.ordinaryUsage(); open != 0 || limit != 2 || listener.ceiling() != 2+consoleConnectionReserve {
		t.Fatalf("ordinary=%d/%d ceiling=%d", open, limit, listener.ceiling())
	}
}
