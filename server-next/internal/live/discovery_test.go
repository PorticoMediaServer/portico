package live

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestTunerDiscoveryReadsRealUDPAndIgnoresOtherDevices(t *testing.T) {
	router, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 8192)
		router.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, from, err := router.ReadFromUDPAddrPort(buf)
		if err == nil && !bytes.Equal(buf[:n], tunerPacket()) {
			done <- io.ErrUnexpectedEOF
			return
		}
		if err != nil {
			done <- err
			return
		}
		router.WriteToUDPAddrPort([]byte("HTTP/1.1 200 OK\r\nSERVER: a television\r\n\r\n"), from)
		reply := []byte{0, 3, 0, 6, 1, 4, 0, 0, 0, 1}
		reply = binary.LittleEndian.AppendUint32(reply, crc32.ChecksumIEEE(reply))
		bad := append([]byte(nil), reply...)
		bad[len(bad)-1] ^= 1
		router.WriteToUDPAddrPort(bad, from)
		_, err = router.WriteToUDPAddrPort(reply, from)
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	devices, err := discoverTuners(ctx, 500*time.Millisecond, []netip.AddrPort{router.LocalAddr().(*net.UDPAddr).AddrPort()})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].BaseURL != "http://127.0.0.1" {
		t.Fatal(devices)
	}
}

func TestTunerFactsStayAtTheRespondingDevice(t *testing.T) {
	var contacted atomic.Bool
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted.Store(true) }))
	defer foreign.Close()
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("redirect") == "yes" {
			http.Redirect(w, r, foreign.URL, 302)
			return
		}
		io.WriteString(w, `{"DeviceID":"10203040","ModelNumber":"HDHR5","FriendlyName":"Antenna","FirmwareVersion":"20261112","TunerCount":4}`)
	}))
	defer device.Close()
	s := &Service{client: device.Client()}
	row := LiveTunerDevice{BaseURL: device.URL}
	s.tunerFacts(context.Background(), &row)
	if row.TunerCount != 4 || row.DeviceID != "10203040" || row.FriendlyName != "Antenna" {
		t.Fatal(row)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, foreign.URL, 302) }))
	defer redirect.Close()
	row = LiveTunerDevice{BaseURL: redirect.URL}
	s.tunerFacts(context.Background(), &row)
	if contacted.Load() || row.DeviceID != "" {
		t.Fatal("discovery followed a device away from its responding address")
	}
}
