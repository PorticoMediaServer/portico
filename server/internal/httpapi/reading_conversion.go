package httpapi

import "github.com/PorticoMediaServer/portico/server/internal/reading"

// ReadingConversionProvenance is evidence about this actual derived publication,
// not a location, tool path, reusable capability or alternate original-file grant.
type ReadingConversionProvenance struct {
	OriginalRevision string `json:"originalRevision"`
	OriginalSourceID string `json:"originalSourceId"`
	OriginalFormat   string `json:"originalFormat"`
	OriginalSize     int64  `json:"originalSize"`
	InputSHA256      string `json:"inputSHA256"`
	Converter        string `json:"converter"`
	WorkerSHA256     string `json:"workerSHA256"`
	Options          string `json:"options"`
	OutputSHA256     string `json:"outputSHA256"`
	OutputSize       int64  `json:"outputSize"`
	Revision         string `json:"revision"`
}

func (ReadingConversionProvenance) JSONSchema() map[string]any {
	hash := map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$", "minLength": 64, "maxLength": 64}
	return map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"originalRevision", "originalSourceId", "originalFormat", "originalSize", "inputSHA256", "converter", "workerSHA256", "options", "outputSHA256", "outputSize", "revision"},
		"properties": map[string]any{
			"originalRevision": hash, "originalSourceId": map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"originalFormat": map[string]any{"type": "string", "enum": []string{"mobi", "azw3"}},
			"originalSize":   map[string]any{"type": "integer", "minimum": 1, "maximum": 64 << 20},
			"inputSHA256":    hash, "converter": map[string]any{"type": "string", "enum": []string{"libmobi:85dcfe803fc2a21020ddcf15c3eb66b93d388add"}},
			"workerSHA256": hash, "options": map[string]any{"type": "string", "enum": []string{"portico-epub-v1;unencrypted-only;no-replica;default-kf8"}},
			"outputSHA256": hash, "outputSize": map[string]any{"type": "integer", "minimum": 1, "maximum": 128 << 20}, "revision": hash,
		},
	}
}

func readingConversionProjection(c *reading.MobiProvenance) *ReadingConversionProvenance {
	if c == nil {
		return nil
	}
	return &ReadingConversionProvenance{OriginalRevision: c.OriginalRevision, OriginalSourceID: c.OriginalSourceID, OriginalFormat: c.OriginalFormat, OriginalSize: c.OriginalSize, InputSHA256: c.InputSHA256, Converter: c.Converter, WorkerSHA256: c.WorkerSHA256, Options: c.Options, OutputSHA256: c.OutputSHA256, OutputSize: c.OutputSize, Revision: c.Revision}
}
