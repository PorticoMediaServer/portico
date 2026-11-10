package api

import "net/http"

// ItemFiles is where a title's files are on the server and what reading them found: File
// info's owner view. Everyone else sees the format facts on the title itself.
type ItemFiles struct {
	Versions []VersionFiles `json:"versions"`
}

// VersionFiles is one version and its files, in the order they play.
type VersionFiles struct {
	ID    string     `json:"id"`
	Name  string     `json:"name,omitempty"`
	Files []ItemFile `json:"files"`
}

// ItemFile is one file as the scanner recorded it. MissingSinceAt is set while the file is
// gone; a part of a longer file names its range with StartMs and EndMs.
type ItemFile struct {
	Path           string   `json:"path"`
	Size           int64    `json:"size"`
	Container      string   `json:"container,omitempty"`
	DurationMs     int64    `json:"durationMs,omitempty"`
	MissingSinceAt string   `json:"missingSinceAt,omitempty"`
	StartMs        *int64   `json:"startMs,omitempty"`
	EndMs          *int64   `json:"endMs,omitempty"`
	Streams        []Stream `json:"streams"`
}

// itemFileRoutes adds GET /v1/items/{id}/files. A path says how the server's disks are laid
// out, so only the owner and administrators are told it; anyone else is refused whether or not
// the title exists. An administrator still sees only what the profile in use may see.
func (s *Server) itemFileRoutes() {
	Handle(s, "GET /v1/items/{id}/files", Admin, func(r *http.Request, _ None) (ItemFiles, error) {
		versions, err := s.db.ItemFiles(r.Context(), viewer(r), r.PathValue("id"))
		if err != nil {
			return ItemFiles{}, refuse(err)
		}
		out := ItemFiles{Versions: make([]VersionFiles, len(versions))}
		for n, v := range versions {
			out.Versions[n] = VersionFiles{ID: v.PublicID, Name: v.Name, Files: make([]ItemFile, len(v.Files))}
			for k, f := range v.Files {
				out.Versions[n].Files[k] = ItemFile{Path: f.Path, Size: f.Size, Container: f.Container, DurationMs: f.DurationMS,
					MissingSinceAt: timeString(f.MissingSince), StartMs: f.StartMS, EndMs: f.EndMS, Streams: streamShapes(f.Streams)}
			}
		}
		return out, nil
	})
}
