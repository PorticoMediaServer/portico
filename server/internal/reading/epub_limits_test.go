package reading

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestPublicationArchiveMemberAndTotalBoundaries(t *testing.T) {
	// Small real ZIP members exercise exactly the same boundary arithmetic as
	// the 16/256 MiB limits without allocating a 256 MiB publication in unit tests.
	raw := fixtureZIP(t, [][2]string{{"one.bin", "123456"}, {"two.bin", "1234"}})
	for _, tc := range []struct {
		name          string
		member, total uint64
		want          error
	}{
		{"at both limits", 6, 10, nil},
		{"member over limit", 5, 10, ErrPublicationTooLarge},
		{"total over limit", 6, 9, ErrPublicationTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := archiveLimits{tc.member, tc.total, ErrPublicationTooLarge}
			_, err := openArchiveWithLimits(bytes.NewReader(raw), int64(len(raw)), limits)
			if !errors.Is(err, tc.want) {
				t.Fatalf("range: %v, want %v", err, tc.want)
			}
			opens := 0
			_, err = openSequentialArchiveWithLimits(context.Background(), sequentialFixture(raw, &opens), limits)
			if !errors.Is(err, tc.want) {
				t.Fatalf("sequential: %v, want %v", err, tc.want)
			}
		})
	}
}
