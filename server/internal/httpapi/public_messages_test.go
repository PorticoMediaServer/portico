package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/lyrics"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func TestWrappedDomainErrorsNeverExposeCauses(t *testing.T) {
	cases := []struct {
		name  string
		write func(http.ResponseWriter, error)
		err   error
		code  string
	}{
		{"access", accessFailure, access.ErrConflict, "administration_conflict"},
		{"administration", administrationFailure, administration.ErrInput, "invalid_administration_input"},
		{"activity", activityFailure, playback.ErrActivityConflict, "playback_command_conflict"},
		{"console", consoleError, operations.ErrConflict, "console_conflict"},
		{"download", func(w http.ResponseWriter, e error) { policyErrors.Write(w, "", downloadError(e)) }, downloads.ErrConflict, "download_conflict"},
		{"library", libraryFailure, librarychannels.ErrConflict, "library_channel_changed"},
		{"live", liveFailure, livechannels.ErrCapacity, "source_capacity_unavailable"},
		{"lyrics", lyricFailure, lyrics.ErrInput, "invalid_lyrics"},
		{"analysis", analysisFailure, mediaanalysis.ErrInput, "invalid_analysis"},
		{"analysis body", analysisFailure, lyrics.ErrInput, "invalid_analysis"},
		{"storage", storageFailure, mounts.ErrCommandConflict, "storage_command_conflict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.write(w, fmt.Errorf("private-database-path-and-token: %w", tc.err))
			if w.Code < 400 || strings.Contains(w.Body.String(), "private-database-path-and-token") || !strings.Contains(w.Body.String(), publicErrorMessage(tc.code)) {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
}
