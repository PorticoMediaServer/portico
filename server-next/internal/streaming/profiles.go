package streaming

import (
	"embed"
	"encoding/json"
)

// The profiles folder holds one file per device profile and, beside each, a case file
// saying how the 25 kinds of file in the matrix are expected to reach that device. A
// profile file is a capability document under "document" with a "source" note saying
// where its facts came from, so a draft is never mistaken for a measurement.
//
// Only the profiles the server itself plans from are built into it: the baseline, for a
// device that has published nothing the server can read, and the AirPlay stream. The
// others are recorded copies of what the apps send. Adding a platform is one profile file
// and one case file there; nothing in planning looks at a platform's name.
//
//go:embed profiles/baseline.json profiles/airplay.json
var builtinProfiles embed.FS

// builtinDocument reads one of the server's own profiles.
func builtinDocument(name string) CapabilityDocument {
	var file struct {
		Document CapabilityDocument `json:"document"`
	}
	raw, err := builtinProfiles.ReadFile("profiles/" + name + ".json")
	if err == nil {
		err = json.Unmarshal(raw, &file)
	}
	if err != nil {
		panic("streaming: built-in profile " + name + ": " + err.Error())
	}
	file.Document.normalize()
	file.Document.Revision = name
	return file.Document
}
