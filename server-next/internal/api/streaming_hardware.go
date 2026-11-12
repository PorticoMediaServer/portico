package api

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaengine"
)

type streamingHardware interface {
	HardwareState(context.Context) mediaengine.StreamingHardwareState
	TestHardware()
}

func (s *Server) streamingHardwareRoutes() {
	read := func(r *http.Request) mediaengine.StreamingHardwareState {
		if engine, ok := s.playback.Engine().(streamingHardware); ok {
			return engine.HardwareState(r.Context())
		}
		return mediaengine.StreamingHardwareState{Checking: true, Devices: []mediaengine.StreamingHardwareDevice{}, Formats: []decoder.HardwareFactFormat{}}
	}
	Handle(s, "GET /v1/admin/hardware", Owner, func(r *http.Request, _ None) (mediaengine.StreamingHardwareState, error) { return read(r), nil })
	Handle(s, "POST /v1/admin/hardware/test", Owner, func(r *http.Request, _ None) (mediaengine.StreamingHardwareState, error) {
		if engine, ok := s.playback.Engine().(streamingHardware); ok {
			engine.TestHardware()
		}
		return read(r), nil
	})
}
