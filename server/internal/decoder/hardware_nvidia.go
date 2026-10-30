package decoder

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

var nvidiaUUID = regexp.MustCompile(`(?i)^GPU-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var pciAddress = regexp.MustCompile(`(?i)^([0-9a-f]{4,8}):([0-9a-f]{2}):([0-9a-f]{2})\.([0-7])$`)
var nvidiaDriverVersion = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){1,3}$`)

func normalizedPCIAddress(value string) string {
	parts := pciAddress.FindStringSubmatch(value)
	if len(parts) != 5 {
		return ""
	}
	domain := strings.TrimLeft(strings.ToLower(parts[1]), "0")
	if domain == "" {
		domain = "0"
	}
	return domain + ":" + strings.ToLower(parts[2]+":"+parts[3]+"."+parts[4])
}

func discoverNVIDIADevices(ctx context.Context) []HardwareDevice {
	if ctx.Err() != nil {
		return nil
	}
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil
	}
	raw, err := runNVIDIADiscovery(ctx, path)
	if err != nil {
		return nil
	}
	return parseNVIDIADiscovery(raw)
}

// This fixed read-only system reporter is not a media decoder. It receives no
// source path, network input, private server environment or arbitrary arguments.
func runNVIDIADiscovery(ctx context.Context, path string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, path, "--query-gpu=uuid,pci.bus_id,index,name,driver_version", "--format=csv,noheader,nounits")
	cmd.Env = []string{}
	for _, name := range []string{"PATH", "SystemRoot", "windir"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	childprocess.Configure(cmd)
	defer childprocess.Kill(cmd)
	cmd.WaitDelay = time.Second
	stdout, stderr := &toolOutput{}, &toolOutput{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	if err := bounded.Err(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func parseNVIDIADiscovery(raw string) []HardwareDevice {
	if len(raw) > 1<<20 {
		return nil
	}
	reader := csv.NewReader(strings.NewReader(raw))
	reader.TrimLeadingSpace, reader.FieldsPerRecord = true, 5
	out, seen, indexes, addresses := []HardwareDevice{}, map[string]bool{}, map[uint64]bool{}, map[string]bool{}
	for i := 0; i < 64; i++ {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}
		for j := range row {
			row[j] = strings.TrimSpace(row[j])
		}
		index, err := strconv.ParseUint(row[2], 10, 8)
		pci := normalizedPCIAddress(row[1])
		if err != nil || index >= 64 || !nvidiaUUID.MatchString(row[0]) || pci == "" || len(row[3]) == 0 || len(row[3]) > 256 || !nvidiaDriverVersion.MatchString(row[4]) {
			continue
		}
		validName := true
		for _, r := range row[3] {
			validName = validName && r >= 32 && r != 127
		}
		uuid := strings.ToLower(row[0])
		if !validName {
			continue
		}
		if seen[uuid] || indexes[index] || addresses[pci] {
			return nil // An ambiguous UUID/index/PCI snapshot grants no selector.
		}
		seen[uuid], indexes[index], addresses[pci] = true, true, true
		sum := sha256.Sum256([]byte(uuid))
		out = append(out, HardwareDevice{ID: "nvidia-" + hex.EncodeToString(sum[:16]), Name: row[3], Vendor: "0x10de", Driver: "nvidia", DriverVersion: row[4], PCIAddress: pci,
			DriverDeviceIndex: strconv.FormatUint(index, 10)})
		// NVML's report index is not a qualified CUDA ordinal under reordered
		// visibility masks. Preserve the observed identity but grant no selected
		// NVENC candidate until a CUDA UUID association is actually qualified.
	}
	return out
}
