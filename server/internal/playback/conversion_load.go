package playback

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type ConversionLoad struct {
	Hardware, Software, Unknown int
	Available                   bool
}

// ConversionLoad reads active producers and their actual encode execution.
// A detected GPU or intended backend does not establish hardware use.
func (s *Service) ConversionLoad(ctx context.Context) (ConversionLoad, error) {
	out := ConversionLoad{}
	if s == nil || s.hls == nil {
		return out, nil
	}
	h := s.hls
	if !h.mu.TryLock() {
		return out, ErrDeliveryDiagnosticsBusy
	}
	ids := []string{}
	for id, w := range h.windows {
		if _, ok := h.active[id]; !ok {
			continue
		}
		if w.videoConversion {
			ids = append(ids, id)
		} else if w.audioConversion {
			out.Software++
		}
	}
	h.mu.Unlock()
	out.Available = true
	if len(ids) == 0 {
		return out, nil
	}
	raw, _ := json.Marshal(ids)
	rows, e := dbwork.Query(ctx, s.db, `SELECT selected.value,COALESCE(h.stages_json,'') FROM json_each(?) selected LEFT JOIN playback_hls_stages h ON h.session_id=selected.value`, string(raw))
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			return out, e
		}
		record := readConversionStages(raw)
		known := false
		if record.Stages != nil {
			for _, stage := range record.Stages {
				if stage.Operation == "encode" {
					if stage.Execution == "hardware" {
						out.Hardware++
						known = true
					} else if stage.Execution == "software" {
						out.Software++
						known = true
					}
					break
				}
			}
		}
		if !known {
			out.Unknown++
		}
	}
	return out, rows.Err()
}
