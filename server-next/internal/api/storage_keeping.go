package api

import (
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server-next/internal/prepare"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"net/http"
)

// StorageKeeping is a preset over the generated-file kinds actually kept today.
// Prepared downloads has one value, in the existing downloads document.
type StorageKeeping struct {
	Preset                string `json:"preset"`
	PreparedDownloadsDays int    `json:"preparedDownloadsDays"`
	revision
}
type StorageKeepingEdit struct {
	Preset                *string `json:"preset,omitempty"`
	PreparedDownloadsDays *int    `json:"preparedDownloadsDays,omitempty"`
}

func storageKeeping(doc settings.Document) StorageKeeping {
	var owner prepare.Settings
	_ = json.Unmarshal(doc.Values, &owner)
	preset := "custom"
	switch owner.KeepDays {
	case 30:
		preset = "balanced"
	case 14:
		preset = "short"
	case 90:
		preset = "long"
	}
	return StorageKeeping{Preset: preset, PreparedDownloadsDays: owner.KeepDays, revision: revision(doc.Revision)}
}
func (s *Server) storageKeepingRoutes() {
	Handle(s, "GET /v1/admin/storage/keeping", Owner, func(r *http.Request, _ None) (StorageKeeping, error) {
		doc, err := s.settings.Read(prepare.Document.Name())
		return storageKeeping(doc), err
	})
	Handle(s, "PUT /v1/admin/storage/keeping", Owner, func(r *http.Request, req StorageKeepingEdit) (StorageKeeping, error) {
		if (req.Preset == nil) == (req.PreparedDownloadsDays == nil) {
			return StorageKeeping{}, ErrInvalid("Choose a preset or change the prepared-download keeping time.")
		}
		days := 0
		if req.PreparedDownloadsDays != nil {
			days = *req.PreparedDownloadsDays
		} else {
			switch *req.Preset {
			case "balanced":
				days = 30
			case "short":
				days = 14
			case "long":
				days = 90
			default:
				return StorageKeeping{}, ErrField("preset", "Choose Balanced, Shorter or Longer; Custom follows the child setting.")
			}
		}
		match, err := settingsIfMatch(r)
		if err != nil {
			return StorageKeeping{}, err
		}
		raw, _ := json.Marshal(map[string]int{"keepDays": days})
		doc, err := s.settings.Write(r.Context(), CallerFrom(r.Context()).UserID, prepare.Document.Name(), raw, match, s.now())
		var invalid *settings.Invalid
		if errors.Is(err, settings.ErrStale) {
			return StorageKeeping{}, errStale()
		}
		if errors.As(err, &invalid) {
			return StorageKeeping{}, errSetting(invalid)
		}
		return storageKeeping(doc), err
	})
}
