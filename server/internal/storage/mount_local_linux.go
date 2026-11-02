//go:build linux

package storage

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"sync"
	"time"
)

// mountIsLocal answers from /proc/self/mountinfo (localUnder), never touching
// path itself. The table is re-read at most every few seconds.
func mountIsLocal(path string) bool { return localUnder(path, currentMounts()) }

var mountTable struct {
	sync.Mutex
	at      time.Time
	entries []mountEntry
}

func currentMounts() []mountEntry {
	mountTable.Lock()
	defer mountTable.Unlock()
	if time.Since(mountTable.at) < 5*time.Second && mountTable.entries != nil {
		return mountTable.entries
	}
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	mountTable.entries, mountTable.at = parseMountInfo(raw), time.Now()
	return mountTable.entries
}

// parseMountInfo reads the mount point (field 5) and the filesystem type (the
// first field after the " - " separator) of each line.
func parseMountInfo(raw []byte) []mountEntry {
	out := []mountEntry{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		head, tail, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields, rest := strings.Fields(head), strings.Fields(tail)
		if len(fields) < 5 || len(rest) < 1 {
			continue
		}
		out = append(out, mountEntry{point: unescapeMount(fields[4]), remote: remoteFilesystemType(rest[0])})
	}
	return out
}

// unescapeMount decodes the octal escapes mountinfo uses for space, tab,
// newline and backslash.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			v := 0
			valid := true
			for _, c := range s[i+1 : i+4] {
				if c < '0' || c > '7' {
					valid = false
					break
				}
				v = v*8 + int(c-'0')
			}
			if valid {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// remoteFilesystemType names the filesystems a stat can block on indefinitely.
func remoteFilesystemType(kind string) bool {
	if strings.HasPrefix(kind, "fuse") {
		return true
	}
	switch kind {
	case "nfs", "nfs4", "cifs", "smb3", "smbfs", "ceph", "9p", "afs", "coda", "ncpfs", "glusterfs", "lustre", "davfs", "sshfs", "virtiofs", "beegfs", "gpfs", "ocfs2", "gfs2", "orangefs":
		return true
	}
	return false
}
