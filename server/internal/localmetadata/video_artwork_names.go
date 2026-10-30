package localmetadata

// videoArtworkNames preserves source-name precedence independently of image
// encoding. sidecarKey still checks actual bounded image content through the
// admitted storage reader; a filename extension is never image evidence.
func videoArtworkNames(stems ...string) []string {
	names := make([]string, 0, len(stems)*5)
	for _, stem := range stems {
		for _, extension := range []string{".jpg", ".jpeg", ".png", ".tbn", ".webp"} {
			names = append(names, stem+extension)
		}
	}
	return names
}
