package decoder

import (
	"context"
	"time"
)

func nativeDeinterlace(backend HardwareBackend) string {
	return map[HardwareBackend]string{
		BackendNVENC:        "bwdif_cuda=mode=send_frame:parity=auto:deint=interlaced",
		BackendVAAPI:        "deinterlace_vaapi=mode=motion_adaptive:rate=frame:auto=1",
		BackendQSV:          "vpp_qsv=deinterlace=advanced",
		BackendVideoToolbox: "bwdif_videotoolbox=mode=send_frame:parity=auto:deint=interlaced",
	}[backend]
}

func (d *HardwareDetector) probeDeinterlace(ctx context.Context, executable string, spec backendSpec, backend HardwareBackend, device string, timeout time.Duration) bool {
	filter := nativeDeinterlace(backend)
	if filter == "" || spec.upload == "" {
		return false
	}
	args := probeArgs(spec, device, "")
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-vf" {
			// Mark the synthetic frames before upload so auto/interlaced mode
			// executes the filter rather than bypassing progressive input.
			args[i+1] = "setfield=tff," + args[i+1] + "," + filter
		}
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return d.run(bounded, executable, args) == nil
}
