package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_artwork_upload",
		resultTypes: []string{
			"ipArtworkUploadCommandResult", "ipArtworkUploadSavedResult", "ipArtworkUploadOutcome",
		},
		names: []string{
			"ipArtworkUploadLookup", "ipArtworkUploadFresh", "ipArtworkUploadBody",
			"ipArtworkUploadRecord",
		},
		bypasses: []string{
			`func applyIPArtworkUpload(ctx context.Context)(out ipArtworkUploadCommandResult,err error){return out,nil}`,
			`func ipArtworkUploadLookup(ctx context.Context)(out ipArtworkUploadOutcome,err error){return out,nil}`,
		},
	})
}
