//go:build linux

// Package scancomponent contains disposable-runner controls only. No product
// runtime imports this package or receives its root/systemd privileges.
package scancomponent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"golang.org/x/sys/unix"
)

const proofBytes = 64 << 20

type ioCounters struct{ ReadBytes, ReadIOs uint64 }
type readProof struct {
	Label                                   string
	Qualification                           bool
	PID                                     int
	Device, DeviceKey, Cgroup, HelperCgroup string
	AncestorIOMax                           map[string]string
	Before, After                           ioCounters
	ReadBytes, ReadCalls                    int64
	DurationNS                              int64
	NativeHelperSameCgroup                  bool
	NativeOwnerActiveAfter                  int
}

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		data, err := os.ReadFile("/proc/self/cgroup")
		if err == nil {
			err = os.WriteFile(filepath.Join(os.TempDir(), "native-helper-cgroup"), data, 0600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := storage.Helper(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	os.Exit(m.Run())
}

func ownCgroup() (string, string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::/") {
			relative := strings.TrimPrefix(line, "0::")
			if strings.Contains(relative, "..") {
				return "", "", fmt.Errorf("unexpected namespace path")
			}
			return relative, filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(relative, "/")), nil
		}
	}
	return "", "", fmt.Errorf("cgroup v2 unavailable")
}
func counters(path, device string) (ioCounters, error) {
	data, err := os.ReadFile(filepath.Join(path, "io.stat"))
	if err != nil {
		return ioCounters{}, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != device {
			continue
		}
		out := ioCounters{}
		for _, field := range fields[1:] {
			kv := strings.SplitN(field, "=", 2)
			if len(kv) != 2 {
				continue
			}
			n, e := strconv.ParseUint(kv[1], 10, 64)
			if e != nil {
				return out, e
			}
			if kv[0] == "rbytes" {
				out.ReadBytes = n
			}
			if kv[0] == "rios" {
				out.ReadIOs = n
			}
		}
		return out, nil
	}
	return ioCounters{}, nil // the fresh unit may have issued no IO yet
}

func TestEC4DirectReadChild(t *testing.T) {
	if os.Getenv("EC4_DIRECT_READ_CHILD") != "1" {
		t.Skip("private proof subprocess only")
	}
	filePath, deviceKey := os.Getenv("EC4_READ_FILE"), os.Getenv("EC4_DEVICE_KEY")
	cg, path, err := ownCgroup()
	if err != nil {
		t.Fatal(err)
	}
	p := readProof{Label: "COMPONENT_ONLY", PID: os.Getpid(), Device: os.Getenv("EC4_DEVICE"), DeviceKey: deviceKey, Cgroup: cg, AncestorIOMax: map[string]string{}}
	for at := path; at != "/sys/fs"; at = filepath.Dir(at) {
		data, e := os.ReadFile(filepath.Join(at, "io.max"))
		if e == nil {
			if len(data) > 8192 {
				t.Fatal("oversized io.max")
			}
			p.AncestorIOMax[at] = string(data)
		} else if at != "/sys/fs/cgroup" {
			t.Fatal(e)
		}
	}
	// Actual existing native observer launches its real supervised helper here.
	// Both observer and helper must inherit this unit's physical IO controls.
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := storage.New(binary)
	owner := storage.NewOperationScope()
	_, err = client.NativeInventoryPage(t.Context(), owner, "ec4-io-profile", storage.NativeInventoryRequest{Root: []byte(filepath.Dir(filePath)), RelativePath: []byte("."), Limit: 1})
	owner.Close()
	joinErr := owner.Wait(t.Context())
	if err != nil || joinErr != nil {
		t.Fatal(err, joinErr)
	}
	data, err := os.ReadFile(filepath.Join(os.TempDir(), "native-helper-cgroup"))
	if err != nil {
		t.Fatal(err)
	}
	p.HelperCgroup = string(data)
	p.NativeHelperSameCgroup = strings.Contains(p.HelperCgroup, "0::"+cg+"\n")
	p.NativeOwnerActiveAfter = owner.Active()
	if !p.NativeHelperSameCgroup || p.NativeOwnerActiveAfter != 0 {
		t.Fatal("helper escaped profile or retirement", p)
	}
	p.Before, err = counters(path, deviceKey)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(filePath, unix.O_RDONLY|unix.O_DIRECT|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	buffer, err := unix.Mmap(-1, 0, 64<<10, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Munmap(buffer)
	start := time.Now()
	for {
		n, e := unix.Read(fd, buffer)
		if e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			break
		}
		p.ReadBytes += int64(n)
		p.ReadCalls++
	}
	p.DurationNS = time.Since(start).Nanoseconds()
	p.After, err = counters(path, deviceKey)
	if err != nil {
		t.Fatal(err)
	}
	if p.ReadBytes != proofBytes || p.After.ReadBytes-p.Before.ReadBytes < proofBytes {
		t.Fatal("direct IO bytes not observed on mapped device", p)
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("EC4_READ_PROOF %s\n", encoded)
}

func deviceFor(path string) (string, string, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return "", "", err
	}
	key := fmt.Sprintf("%d:%d", unix.Major(st.Dev), unix.Minor(st.Dev))
	sys, err := filepath.EvalSymlinks(filepath.Join("/sys/dev/block", key))
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(filepath.Join(sys, "partition")); err == nil {
		sys = filepath.Dir(sys)
		data, e := os.ReadFile(filepath.Join(sys, "dev"))
		if e != nil {
			return "", "", e
		}
		key = strings.TrimSpace(string(data))
	}
	device := filepath.Join("/dev", filepath.Base(sys))
	if _, err := os.Stat(device); err != nil {
		return "", "", err
	}
	return device, key, nil
}

func runReadUnit(t *testing.T, file, device, key, cell string, rbps, iops int64) readProof {
	t.Helper()
	nonce := make([]byte, 5)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	unit := "portico-ec4-" + hex.EncodeToString(nonce)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cleanup := func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = exec.CommandContext(stopCtx, "sudo", "-n", "systemctl", "stop", unit+".service").Run()
	}
	defer cleanup()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	args := []string{"-n", "systemd-run", "--quiet", "--wait", "--pipe", "--collect", "--unit=" + unit, "--uid=" + strconv.Itoa(os.Getuid()), "--gid=" + strconv.Itoa(os.Getgid()), "--property=IOAccounting=yes"}
	if rbps > 0 {
		args = append(args, "--property=IOReadBandwidthMax="+device+" "+strconv.FormatInt(rbps, 10))
	}
	if iops > 0 {
		args = append(args, "--property=IOReadIOPSMax="+device+" "+strconv.FormatInt(iops, 10))
	}
	for name, value := range map[string]string{"EC4_DIRECT_READ_CHILD": "1", "EC4_READ_FILE": file, "EC4_DEVICE": device, "EC4_DEVICE_KEY": key, "TMPDIR": tmp, "GOMAXPROCS": "2"} {
		args = append(args, "--setenv="+name+"="+value)
	}
	args = append(args, binary, "-test.run=^TestEC4DirectReadChild$", "-test.count=1", "-test.timeout=80s")
	var outputBuffer cappedProofOutput
	cmd := exec.CommandContext(ctx, "sudo", args...)
	cmd.Stdout, cmd.Stderr = &outputBuffer, &outputBuffer
	err = cmd.Run()
	output := outputBuffer.data
	if outputBuffer.truncated {
		t.Fatal("oversized child proof output")
	}
	if err != nil {
		t.Fatalf("%s: %v: %.8192s", cell, err, output)
	}
	var proof readProof
	found := false
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("EC4_READ_PROOF ")) {
			if err := json.Unmarshal(bytes.TrimPrefix(line, []byte("EC4_READ_PROOF ")), &proof); err != nil {
				t.Fatal(err)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("missing child receipt: %.8192s", output)
	}
	if rbps > 0 || iops > 0 {
		leaf := ""
		for path, raw := range proof.AncestorIOMax {
			if strings.HasSuffix(path, unit+".service") {
				leaf = raw
			}
		}
		expected := "rbps=" + strconv.FormatInt(rbps, 10)
		if iops > 0 {
			expected = "riops=" + strconv.FormatInt(iops, 10)
		}
		if !strings.Contains(leaf, key+" ") || !strings.Contains(leaf, expected) {
			t.Fatal("requested unit did not enforce actual io.max", proof)
		}
	}
	return proof
}

func TestEC4IOMaxRunnerProof(t *testing.T) {
	if os.Getenv("PORTICO_LANE_EVIDENCE_DIR") == "" || flag.Lookup("test.run").Value.String() != "^TestEC4IOMaxRunnerProof$" {
		t.Skip("exact disposable-runner component selection only")
	}
	scratch := t.TempDir()
	root := filepath.Join(scratch, "media")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "dense.mkv")
	device, key, err := deviceFor(root)
	if err != nil {
		t.Fatal("real block mapping required", err)
	}
	out, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < proofBytes/len(payload); n++ {
		if _, err := out.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := out.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	baseline := runReadUnit(t, file, device, key, "baseline", 0, 0)
	bandwidth := runReadUnit(t, file, device, key, "4MiB-per-second", 4<<20, 0)
	operations := runReadUnit(t, file, device, key, "32-read-IOPS", 0, 32)
	// Component falsifiers, not a production service/hold budget. Account for
	// kernel burst granularity, while requiring clear separation from baseline.
	bandwidthLower := time.Duration(float64(proofBytes) / float64(4<<20) * 0.7 * float64(time.Second))
	ioCount := operations.After.ReadIOs - operations.Before.ReadIOs
	iopsLower := time.Duration(float64(ioCount) / 32 * 0.7 * float64(time.Second))
	passed := time.Duration(bandwidth.DurationNS) >= bandwidthLower && time.Duration(operations.DurationNS) >= iopsLower && bandwidth.DurationNS > 3*baseline.DurationNS && operations.DurationNS > 3*baseline.DurationNS && ioCount > 0
	evidence := struct {
		Label                                          string
		Qualification                                  bool
		DenseBytes                                     int64
		DirectIO                                       bool
		RequestedReadBytesPerSecond, RequestedReadIOPS int64
		Baseline, Bandwidth, Operations                readProof
		ActualThrottleProved                           bool
		Limitations                                    []string
	}{"COMPONENT_ONLY", false, proofBytes, true, 4 << 20, 32, baseline, bandwidth, operations, passed, []string{"not physical HDD seek geometry", "no database/SSD-placement or Basic/filename-visible measurement", "disposable runner only; no production profile or qualification"}}
	data, _ := json.MarshalIndent(evidence, "", "  ")
	dest := filepath.Join(os.Getenv("PORTICO_LANE_EVIDENCE_DIR"), "ec4-io-max-proof.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("baseline=%s rbps=%s riops=%s readIOs=%d real native helper inherited=true qualification=false", time.Duration(baseline.DurationNS), time.Duration(bandwidth.DurationNS), time.Duration(operations.DurationNS), ioCount)
	if !passed {
		t.Fatal("io.max did not demonstrate requested throttle separation")
	}
}

type cappedProofOutput struct {
	data      []byte
	truncated bool
}

func (w *cappedProofOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (64 << 10) - len(w.data)
	if len(p) > remaining {
		p = p[:remaining]
		w.truncated = true
	}
	w.data = append(w.data, p...)
	return n, nil
}
