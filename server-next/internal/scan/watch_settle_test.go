package scan

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A folder in which a file is still being written is reported again each time it is found
// larger, the last time after the last byte, and is then left alone.
func TestAFolderIsFollowedUntilItSettles(t *testing.T) {
	folder := t.TempDir()
	film := filepath.Join(folder, "film.mkv")
	write := func(size int) {
		t.Helper()
		if err := os.WriteFile(film, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(10)
	var s settling
	now := time.Now()
	reports := 0
	look := func() int {
		now = now.Add(settleEvery)
		before := reports
		s.poll(now, func(library string) {
			if library != "lib_a" {
				t.Fatalf("reported %q", library)
			}
			reports++
		})
		return reports - before
	}
	s.touch(folder, "lib_a", now) // the copy starts: the system says the folder changed
	write(20)
	if look() != 1 {
		t.Fatal("a file that grew was not reported")
	}
	write(30)
	if look() != 1 {
		t.Fatal("a file that grew again was not reported")
	}
	// The copy has ended: two looks with no difference, and the folder is let go.
	if look() != 0 || look() != 0 || len(s.folders) != 0 {
		t.Fatalf("a settled folder was reported or is still followed: %d followed", len(s.folders))
	}
	// Before its time has come a folder is not looked at.
	s.touch(folder, "lib_a", now)
	write(40)
	if s.poll(now.Add(settleEvery/2), func(string) { reports++ }); reports != 2 {
		t.Fatal("a folder was looked at before its time")
	}
}
