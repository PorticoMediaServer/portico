package remotesources

type SourceKind string

const (
	SourceRclone SourceKind = "rclone"
	SourceWebDAV SourceKind = "webdav"
	SourceSFTP   SourceKind = "sftp"
	SourceSMB    SourceKind = "smb"
)

func (SourceKind) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"rclone", "webdav", "sftp", "smb"}}
}
