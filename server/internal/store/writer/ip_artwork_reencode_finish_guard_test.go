package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_artwork_reencode",
		resultTypes: []string{
			"ipArtworkReencodeCommandResult", "ipArtworkReencodeSavedResult", "ipArtworkReencodeOutcome",
		},
		names: []string{
			"ipArtworkReencodeLookup", "ipArtworkReencodeFresh", "ipArtworkReencodeBody",
			"ipArtworkReencodeRecord",
		},
		bypasses: []string{
			`func applyIPArtworkReencode(ctx context.Context)(out ipArtworkReencodeCommandResult,err error){return out,nil}`,
			`func ipArtworkReencodeLookup(ctx context.Context)(out ipArtworkReencodeOutcome,err error){return out,nil}`,
		},
	})
}
