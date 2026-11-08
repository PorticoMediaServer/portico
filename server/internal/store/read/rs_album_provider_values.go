package read

// RSAlbumProviderFact describes one accepted release observation. A caller
// cannot authorize a source by constructing this DTO; the provider read recipe
// must establish its current selected publication and complete sealed evidence.
type RSAlbumProviderFact struct {
	ReleaseID, RequestedReleaseID, ReleaseGroupID  string
	ObservedAt, RawPrimaryType, DisplayPrimaryType string
	SecondaryTypes                                 []string
}

const (
	RSArtistAlbumAlbum       RSArtistAlbumRole = "album"
	RSArtistAlbumCompilation RSArtistAlbumRole = "compilation"
	RSArtistAlbumEP          RSArtistAlbumRole = "ep"
	RSArtistAlbumSingle      RSArtistAlbumRole = "single"
)

// rsAlbumProviderValues preserves provider identities and the original ordered
// vocabulary. Its input is a decoded accepted source, never an authority proof.
func rsAlbumProviderValues(release, requested, group, observed, primary string, secondaries []string) RSAlbumProviderFact {
	v := RSAlbumProviderFact{ReleaseID: release, RequestedReleaseID: requested, ReleaseGroupID: group, ObservedAt: observed, RawPrimaryType: primary, DisplayPrimaryType: primary, SecondaryTypes: append([]string(nil), secondaries...)}
	for _, secondary := range secondaries {
		if rsAlbumLabelEqual(secondary, "Compilation") {
			v.DisplayPrimaryType = "Compilation"
			break
		}
	}
	return v
}

// The actual primary-artist relationship takes precedence over release type.
// An absent accepted source still has the local album section fallback.
func rsAlbumRole(primaryArtist, requestedArtist int64, source *RSAlbumProviderFact) RSArtistAlbumRole {
	if primaryArtist != requestedArtist {
		return RSArtistAlbumAppearance
	}
	if source != nil {
		for _, secondary := range source.SecondaryTypes {
			if rsAlbumLabelEqual(secondary, "Compilation") {
				return RSArtistAlbumCompilation
			}
		}
		if rsAlbumLabelEqual(source.RawPrimaryType, "EP") {
			return RSArtistAlbumEP
		}
		if rsAlbumLabelEqual(source.RawPrimaryType, "Single") {
			return RSArtistAlbumSingle
		}
	}
	return RSArtistAlbumAlbum
}

func rsAlbumLabelEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
