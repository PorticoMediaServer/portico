package main

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// healthCheck asks the server already running on this machine, at the address this one would
// listen on, whether it is ready. A container's health check runs the server's own program
// for this, so the image needs no other tool. It asks without signing in.
func healthCheck(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("the listening address is not host:port: %w", err)
	}
	// An address of every interface says where the server listens, not where to reach it.
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + net.JoinHostPort(host, port) + "/v1/health/ready")
	if err != nil {
		return fmt.Errorf("the server is not answering: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the server is not ready: it answered %d", response.StatusCode)
	}
	return nil
}
