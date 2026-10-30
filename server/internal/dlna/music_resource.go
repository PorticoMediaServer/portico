package dlna

// The scanner records demuxer identities, which are not always filename
// suffixes. Keep original containers identifiable to name-based players;
// this says nothing about the codec or its measured technical properties.
func resourceExtension(container, video string) string {
	if video == "" {
		switch container {
		case "wavpack":
			return ".wv"
		case "dsdiff":
			return ".dff"
		case "mpc7", "mpc8":
			return ".mpc"
		case "matroska", "mkv":
			return ".mka"
		case "mov":
			return ".m4a"
		case "asf":
			return ".wma"
		}
	} else {
		switch container {
		case "matroska":
			return ".mkv"
		case "mpegts":
			return ".ts"
		}
	}
	return extension(container)
}
