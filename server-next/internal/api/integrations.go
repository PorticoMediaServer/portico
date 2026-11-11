package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The integrations settings document: the switches for outside tools. It is read from memory
// on every request, so a change applies at once. (When the console's shared settings package
// is built, this document moves onto it unchanged.)

// IntegrationSettings are the owner's switches for outside tools.
type IntegrationSettings struct {
	// Assistants opens the assistant (MCP) address to keys.
	Assistants bool `json:"assistants"`
	// CompatibleRoutes opens the Jellyfin-compatible routes for companion tools. They are off
	// by default; turning them off closes them and ends every sign-in made through them.
	CompatibleRoutes bool `json:"compatibleRoutes"`
	revision
}

// IntegrationSettingsEdit changes the switches; a field left out stays as it is.
type IntegrationSettingsEdit struct {
	Assistants       *bool `json:"assistants,omitempty"`
	CompatibleRoutes *bool `json:"compatibleRoutes,omitempty"`
}

const integrationSettingsKey = "integrations"

// storedIntegrations is the document as stored. CompatibleEpoch moves each time the compatible
// routes are turned off; a compatible sign-in's token is worked out with it, so the tokens
// made before stop working.
type storedIntegrations struct {
	Assistants       bool  `json:"assistants"`
	CompatibleRoutes bool  `json:"compatibleRoutes"`
	CompatibleEpoch  int64 `json:"compatibleEpoch"`
}

type integrationState struct {
	atomic.Pointer[storedIntegrations]
}

func (i *integrationState) get() storedIntegrations {
	if p := i.Load(); p != nil {
		return *p
	}
	return storedIntegrations{}
}

func (s *Server) loadIntegrations(ctx context.Context) error {
	raw, _, err := s.db.SettingDocument(ctx, integrationSettingsKey)
	if err != nil || raw == "" {
		return err
	}
	var stored storedIntegrations
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return errors.New("the stored integration settings are damaged")
	}
	s.integrations.Store(&stored)
	return nil
}

func (s *Server) integrationRoutes() {
	Handle(s, "GET /v1/admin/settings/integrations", Owner, func(r *http.Request, _ None) (IntegrationSettings, error) {
		_, saved, err := s.db.SettingDocument(r.Context(), integrationSettingsKey)
		cur := s.integrations.get()
		return IntegrationSettings{cur.Assistants, cur.CompatibleRoutes, revision(saved)}, err
	})
	// Switching tools on or off is the owner's, signed in: a key cannot open more doors.
	Handle(s, "PUT /v1/admin/settings/integrations", Owner, s.saveIntegrations).Scope = ScopeSession
}

func (s *Server) saveIntegrations(r *http.Request, req IntegrationSettingsEdit) (IntegrationSettings, error) {
	match, err := settingsIfMatch(r)
	if err != nil {
		return IntegrationSettings{}, err
	}
	next := s.integrations.get()
	if req.Assistants != nil {
		next.Assistants = *req.Assistants
	}
	if req.CompatibleRoutes != nil {
		if next.CompatibleRoutes && !*req.CompatibleRoutes {
			next.CompatibleEpoch++
		}
		next.CompatibleRoutes = *req.CompatibleRoutes
	}
	raw, _ := json.Marshal(next)
	saved, err := s.db.SaveSettingDocument(r.Context(), CallerFrom(r.Context()).UserID, integrationSettingsKey, string(raw), match, s.now())
	if errors.Is(err, db.ErrStale) {
		return IntegrationSettings{}, errStale()
	}
	if err != nil {
		return IntegrationSettings{}, err
	}
	s.integrations.Store(&next)
	return IntegrationSettings{next.Assistants, next.CompatibleRoutes, revision(saved)}, nil
}
