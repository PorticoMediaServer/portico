package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// A recording folder never lives inside the data folder, including through a link.
func (s *Server) checkRecordingSettings(name string, change JSON) error {
	if name != "dvr" {
		return nil
	}
	var v struct {
		Folder string `json:"folder"`
	}
	if json.Unmarshal(change, &v) != nil || v.Folder == "" {
		return nil
	}
	folder, data := filepath.Clean(strings.TrimSpace(v.Folder)), filepath.Clean(s.dataDir)
	for parent := folder; ; parent = filepath.Dir(parent) {
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			rest, _ := filepath.Rel(parent, folder)
			folder = filepath.Join(resolved, rest)
			break
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	if resolved, err := filepath.EvalSymlinks(data); err == nil {
		data = resolved
	}
	rel, err := filepath.Rel(data, folder)
	if err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return ErrField("folder", "Choose a recordings folder outside Portico's data folder.")
	}
	return nil
}
