//go:build darwin

package decoder

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type sandboxResult struct{ Allowed, AllowedIPv6, AliasDenied, OtherPortDenied, DatagramDenied, FileDenied bool }

func TestDecoderSandboxHelper(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "decoder-helper" {
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}
	if len(args) != 6 {
		os.Exit(2)
	}
	denied := func(err error) bool { return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) }
	c, err := net.DialTimeout("tcp4", args[1], time.Second)
	result := sandboxResult{Allowed: err == nil}
	if c != nil {
		c.Close()
	}
	c, err = net.DialTimeout("tcp6", args[2], time.Second)
	result.AllowedIPv6 = err == nil
	if c != nil {
		c.Close()
	}
	c, err = net.DialTimeout("tcp4", args[4], time.Second)
	result.AliasDenied = denied(err)
	if c != nil {
		c.Close()
	}
	c, err = net.DialTimeout("tcp4", args[3], time.Second)
	result.OtherPortDenied = denied(err)
	if c != nil {
		c.Close()
	}
	c, err = net.DialTimeout("udp4", "127.0.0.1:19503", time.Second)
	if err == nil {
		_, err = c.Write([]byte{0})
	}
	result.DatagramDenied = denied(err)
	if c != nil {
		c.Close()
	}
	_, err = os.ReadFile(args[5])
	result.FileDenied = denied(err)
	json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}

func TestDecoderSandboxRestrictsEndpointAndFiles(t *testing.T) {
	// The exact granted endpoints and one tiny helper require independent release.
	allowed, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer allowed.Close()
	ipv6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(allowed.Addr().(*net.TCPAddr).Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer ipv6.Close()
	reservation, err := ReserveEndpoints(allowed.(*net.TCPListener), ipv6.(*net.TCPListener))
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	for _, file := range reservation.files {
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_GETFD, 0)
		if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
			t.Fatal("reservation can leak through exec")
		}
	}
	other, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "private-source")
	if err = os.WriteFile(private, []byte("not decoder input"), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestDecoderSandboxHelper$", "--", "decoder-helper", allowed.Addr().String(), ipv6.Addr().String(), other.Addr().String(), net.JoinHostPort("127.0.0.2", strconv.Itoa(allowed.Addr().(*net.TCPAddr).Port)), private}
	built, err := confinedCommand(exe, args, allowed.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, built.Path, built.Args[1:]...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox helper: %v: %s", err, output)
	}
	var result sandboxResult
	if err = json.NewDecoder(strings.NewReader(string(output))).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Allowed || !result.AllowedIPv6 || !result.AliasDenied || !result.OtherPortDenied || !result.DatagramDenied || !result.FileDenied {
		t.Fatalf("confinement incomplete: %+v", result)
	}
}
