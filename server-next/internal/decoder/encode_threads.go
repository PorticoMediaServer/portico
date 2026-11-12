package decoder

import (
	"runtime"
	"sync"
)

// Threads for a software video encode. One started for playback (a title, a live channel)
// gets the machine's share for the encodes running when it starts: with C logical processors
// and k playback encodes already running, clamp(C/(k+1), 2, 8). One alone gets the whole
// machine; as more start each newcomer gets less, and none gets fewer than the two every
// encode had before. A running encode cannot be given more or fewer threads: it keeps what it
// started with, and takes the share in force at its next window, which the throttle and seeks
// start often. The kernel shares the processor fairly between them meanwhile.
//
// Hardware encodes are neither counted nor given threads. Background work (prepared versions,
// analysis) keeps its two threads and low priority.
const (
	minEncodeThreads = 2
	// maxEncodeThreads is past where x265 and SVT-AV1 stop gaining at 4K and 1080p: on a
	// 12-thread machine (mini-2, i7-8700T, six cores) x265 gained up to four to six threads
	// and SVT-AV1 little past two; nothing more was found to give. Not measured on the
	// 16-thread reference machine.
	maxEncodeThreads = 8
)

var encodes struct {
	mu      sync.Mutex
	running int
}

// processors is the machine's logical processors; a variable for tests.
var processors = runtime.NumCPU

// encodeShare is the threads a new encode gets with running playback encodes already going.
func encodeShare(running int) int {
	return min(max(processors()/(running+1), minEncodeThreads), maxEncodeThreads)
}

// ReservePlaybackEncode counts one more software video encode for playback and says how many
// threads it gets. release, called once its converter has exited, counts it out again.
func ReservePlaybackEncode() (threads int, release func()) {
	encodes.mu.Lock()
	threads = encodeShare(encodes.running)
	encodes.running++
	encodes.mu.Unlock()
	var once sync.Once
	return threads, func() {
		once.Do(func() {
			encodes.mu.Lock()
			encodes.running--
			encodes.mu.Unlock()
		})
	}
}
