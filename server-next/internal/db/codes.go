package db

import "fmt"

// The large tables store small integer codes where a small table would list allowed values in
// a CHECK, so adding a media type or a role never rewrites a million rows. The numbers are
// stored: never change or reuse one. Each table's one insert function refuses a code that is
// not named here.

// ItemKind is what an items row is.
type ItemKind int

const (
	KindMovie      ItemKind = 1
	KindShow       ItemKind = 2
	KindSeason     ItemKind = 3
	KindEpisode    ItemKind = 4
	KindArtist     ItemKind = 5
	KindAlbum      ItemKind = 6
	KindTrack      ItemKind = 7
	KindAudiobook  ItemKind = 8
	KindBook       ItemKind = 9
	KindComic      ItemKind = 10
	KindIssue      ItemKind = 11
	KindSeries     ItemKind = 12
	KindCollection ItemKind = 13
)

var itemKindNames = map[ItemKind]string{
	KindMovie: "movie", KindShow: "show", KindSeason: "season", KindEpisode: "episode",
	KindArtist: "artist", KindAlbum: "album", KindTrack: "track", KindAudiobook: "audiobook",
	KindBook: "book", KindComic: "comic", KindIssue: "issue", KindSeries: "series",
	KindCollection: "collection",
}

func (k ItemKind) String() string { return itemKindNames[k] }

// ParseItemKind turns a wire name such as "movie" into its code.
func ParseItemKind(name string) (ItemKind, bool) { return parseCode(itemKindNames, name) }

// Playable reports whether an item of this kind is itself played or read, as opposed to
// holding other items. Watched and progress live on playable items.
func (k ItemKind) Playable() bool {
	switch k {
	case KindMovie, KindEpisode, KindTrack, KindAudiobook, KindBook, KindComic, KindIssue:
		return true
	}
	return false
}

// TopLevel reports whether an item of this kind never sits under another item, so it has
// nothing to inherit a tag or a label from.
func (k ItemKind) TopLevel() bool {
	switch k {
	case KindMovie, KindShow, KindArtist, KindAudiobook, KindBook, KindComic, KindSeries, KindCollection:
		return true
	}
	return false
}

// playableKindsSQL is the playable kinds as an SQL list, for queries over a container's leaves.
const playableKindsSQL = "(1, 4, 7, 8, 9, 10, 11)"

// FileRole is what a file on disk is for.
type FileRole int

const (
	FileMedia    FileRole = 1
	FileSubtitle FileRole = 2
	FileArtwork  FileRole = 3
	FileMetadata FileRole = 4
	FileLyrics   FileRole = 5
	FileCueSheet FileRole = 6
)

var fileRoleNames = map[FileRole]string{
	FileMedia: "media", FileSubtitle: "subtitle", FileArtwork: "artwork", FileMetadata: "metadata",
	FileLyrics: "lyrics", FileCueSheet: "cue_sheet",
}

func (r FileRole) String() string { return fileRoleNames[r] }

// StreamKind is the type of a track inside a file.
type StreamKind int

const (
	StreamVideo    StreamKind = 1
	StreamAudio    StreamKind = 2
	StreamSubtitle StreamKind = 3
)

var streamKindNames = map[StreamKind]string{StreamVideo: "video", StreamAudio: "audio", StreamSubtitle: "subtitle"}

func (k StreamKind) String() string { return streamKindNames[k] }

// MarkerKind is what a marked span of a version is.
type MarkerKind int

const (
	MarkerChapter MarkerKind = 1
	MarkerIntro   MarkerKind = 2
	MarkerCredits MarkerKind = 3
	MarkerRecap   MarkerKind = 4
	MarkerAdvert  MarkerKind = 5
)

var markerKindNames = map[MarkerKind]string{
	MarkerChapter: "chapter", MarkerIntro: "intro", MarkerCredits: "credits", MarkerRecap: "recap", MarkerAdvert: "advert",
}

func (k MarkerKind) String() string { return markerKindNames[k] }

// MarkerSource is where a marker came from.
type MarkerSource int

const (
	MarkerFromFile     MarkerSource = 1
	MarkerFromAnalysis MarkerSource = 2
	MarkerFromProvider MarkerSource = 3
	MarkerFromPerson   MarkerSource = 4
)

var markerSourceNames = map[MarkerSource]string{
	MarkerFromFile: "file", MarkerFromAnalysis: "analysis", MarkerFromProvider: "provider", MarkerFromPerson: "person",
}

func (s MarkerSource) String() string { return markerSourceNames[s] }

// CreditRole is what a person did on an item.
type CreditRole int

const (
	RoleActor    CreditRole = 1
	RoleDirector CreditRole = 2
	RoleWriter   CreditRole = 3
	RoleProducer CreditRole = 4
	RoleComposer CreditRole = 5
	RoleAuthor   CreditRole = 6
	RoleNarrator CreditRole = 7
	RoleArtist   CreditRole = 8
	RoleGuest    CreditRole = 9
	RoleCrew     CreditRole = 10
)

var creditRoleNames = map[CreditRole]string{
	RoleActor: "actor", RoleDirector: "director", RoleWriter: "writer", RoleProducer: "producer",
	RoleComposer: "composer", RoleAuthor: "author", RoleNarrator: "narrator", RoleArtist: "artist",
	RoleGuest: "guest", RoleCrew: "crew",
}

func (r CreditRole) String() string { return creditRoleNames[r] }

// RelationKind is how one item relates to another.
type RelationKind int

const (
	RelationCollectionMember RelationKind = 1
	RelationExtra            RelationKind = 2
	RelationTrailer          RelationKind = 3
	RelationGuestArtist      RelationKind = 4
	RelationSeriesMember     RelationKind = 5
)

var relationKindNames = map[RelationKind]string{
	RelationCollectionMember: "collection_member", RelationExtra: "extra", RelationTrailer: "trailer",
	RelationGuestArtist: "guest_artist", RelationSeriesMember: "series_member",
}

func (k RelationKind) String() string { return relationKindNames[k] }

// ImageRole is where a picture is used.
type ImageRole int

const (
	ImagePoster   ImageRole = 1
	ImageBackdrop ImageRole = 2
	ImageLogo     ImageRole = 3
	ImageThumb    ImageRole = 4
	ImageBanner   ImageRole = 5
	ImageCover    ImageRole = 6
	ImagePortrait ImageRole = 7
	ImageAvatar   ImageRole = 8
)

var imageRoleNames = map[ImageRole]string{
	ImagePoster: "poster", ImageBackdrop: "backdrop", ImageLogo: "logo", ImageThumb: "thumb",
	ImageBanner: "banner", ImageCover: "cover", ImagePortrait: "portrait", ImageAvatar: "avatar",
}

func (r ImageRole) String() string { return imageRoleNames[r] }

// ImageSource is where a picture came from.
type ImageSource int

const (
	ImageFromFile     ImageSource = 1
	ImageFromEmbedded ImageSource = 2
	ImageFromProvider ImageSource = 3
	ImageFromUpload   ImageSource = 4
	ImageFromFrame    ImageSource = 5
)

var imageSourceNames = map[ImageSource]string{
	ImageFromFile: "file", ImageFromEmbedded: "embedded", ImageFromProvider: "provider",
	ImageFromUpload: "upload", ImageFromFrame: "frame",
}

func (s ImageSource) String() string { return imageSourceNames[s] }

// KeyKind is what kind of fact identifies an item across rescans.
type KeyKind int

const (
	KeyFolder   KeyKind = 1 // a folder path inside the library
	KeyFile     KeyKind = 2 // a file path inside the library, with a track or episode number where one file holds several
	KeyEmbedded KeyKind = 3 // an id written in the file's own tags
	KeyProvider KeyKind = 4 // a provider id the item was matched to
	KeyName     KeyKind = 5 // a parent and a name or number, such as an artist name or "show key/season 2"
)

var keyKindNames = map[KeyKind]string{
	KeyFolder: "folder", KeyFile: "file", KeyEmbedded: "embedded", KeyProvider: "provider", KeyName: "name",
}

func (k KeyKind) String() string { return keyKindNames[k] }

// ChangeTopic is what kind of thing a changes row is about.
type ChangeTopic int

const (
	TopicItem    ChangeTopic = 1
	TopicLibrary ChangeTopic = 2
	TopicState   ChangeTopic = 3
	TopicPerson  ChangeTopic = 4
	TopicAccess  ChangeTopic = 5 // what a profile may see changed: its clients throw their cache away
	TopicList    ChangeTopic = 6
)

var changeTopicNames = map[ChangeTopic]string{
	TopicItem: "item", TopicLibrary: "library", TopicState: "state", TopicPerson: "person",
	TopicAccess: "access", TopicList: "list",
}

func (t ChangeTopic) String() string { return changeTopicNames[t] }

// ChangeOp is what happened to it.
type ChangeOp int

const (
	OpCreated ChangeOp = 1
	OpUpdated ChangeOp = 2
	OpDeleted ChangeOp = 3
)

var changeOpNames = map[ChangeOp]string{OpCreated: "created", OpUpdated: "updated", OpDeleted: "deleted"}

func (o ChangeOp) String() string { return changeOpNames[o] }

// MatchState is how sure the server is about which title an item is.
type MatchState int

const (
	MatchNone           MatchState = 0
	MatchFound          MatchState = 1
	MatchChosen         MatchState = 2 // a person picked the match; a refresh never changes it
	MatchNeedsAttention MatchState = 3
)

var matchStateNames = map[MatchState]string{
	MatchNone: "unmatched", MatchFound: "matched", MatchChosen: "chosen", MatchNeedsAttention: "needs_attention",
}

func (s MatchState) String() string { return matchStateNames[s] }

// Library kinds. libraries is a small table and stores the name itself.
const (
	LibraryMovies     = "movies"
	LibraryShows      = "shows"
	LibraryMusic      = "music"
	LibraryBooks      = "books"
	LibraryAudiobooks = "audiobooks"
)

var libraryKinds = map[string]bool{
	LibraryMovies: true, LibraryShows: true, LibraryMusic: true, LibraryBooks: true, LibraryAudiobooks: true,
}

// Tag kinds. tags is a small table and stores the name itself.
const (
	TagGenre   = "genre"
	TagStudio  = "studio"
	TagNetwork = "network"
	TagCountry = "country"
	TagLabel   = "label"
	TagMood    = "mood"
)

var tagKinds = map[string]bool{TagGenre: true, TagStudio: true, TagNetwork: true, TagCountry: true, TagLabel: true, TagMood: true}

func parseCode[C comparable](names map[C]string, name string) (C, bool) {
	for code, n := range names {
		if n == name {
			return code, true
		}
	}
	var zero C
	return zero, false
}

// checkCode is the refusal every large-table insert function makes for a code it does not know.
func checkCode[C comparable](column string, names map[C]string, code C) error {
	if _, ok := names[code]; !ok {
		return fmt.Errorf("%s: unknown code %v", column, code)
	}
	return nil
}
