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
	KindExtra      ItemKind = 14 // a trailer, featurette or other extra of a title: played from its page, never listed as a title
)

var itemKindNames = map[ItemKind]string{
	KindMovie: "movie", KindShow: "show", KindSeason: "season", KindEpisode: "episode",
	KindArtist: "artist", KindAlbum: "album", KindTrack: "track", KindAudiobook: "audiobook",
	KindBook: "book", KindComic: "comic", KindIssue: "issue", KindSeries: "series",
	KindCollection: "collection", KindExtra: "extra",
}

func (k ItemKind) String() string { return itemKindNames[k] }

// ParseItemKind turns a wire name such as "movie" into its code.
func ParseItemKind(name string) (ItemKind, bool) { return parseCode(itemKindNames, name) }

// Playable reports whether an item of this kind is itself played or read, as opposed to
// holding other items. Watched and progress live on playable items.
func (k ItemKind) Playable() bool {
	switch k {
	case KindMovie, KindEpisode, KindTrack, KindAudiobook, KindBook, KindComic, KindIssue, KindExtra:
		return true
	}
	return false
}

// playableKindsSQL is the playable kinds as an SQL list, for queries over a container's leaves.
const playableKindsSQL = "(1, 4, 7, 8, 9, 10, 11, 14)"

// FileRole is what a file on disk is for.
type FileRole int

const (
	FileMedia    FileRole = 1
	FileSubtitle FileRole = 2
	FileArtwork  FileRole = 3
	FileMetadata FileRole = 4
	FileLyrics   FileRole = 5
	FileCueSheet FileRole = 6
	FileOther    FileRole = 7 // a file the scan could not use, kept so Files skipped can list it
)

var fileRoleNames = map[FileRole]string{
	FileMedia: "media", FileSubtitle: "subtitle", FileArtwork: "artwork", FileMetadata: "metadata",
	FileLyrics: "lyrics", FileCueSheet: "cue_sheet", FileOther: "other",
}

func (r FileRole) String() string { return fileRoleNames[r] }

// FileProblem is why a file is in a library's Files skipped list.
type FileProblem int

const (
	ProblemNone           FileProblem = 0
	ProblemDamaged        FileProblem = 1 // it could not be read as media
	ProblemUnsupported    FileProblem = 2 // a path too long or too deep, an encrypted disc
	ProblemNotMedia       FileProblem = 3 // it is named like media and is not
	ProblemUnreadableName FileProblem = 4 // its name is not valid text; the stored name is a readable stand-in
)

var fileProblemNames = map[FileProblem]string{
	ProblemDamaged: "damaged", ProblemUnsupported: "not_supported", ProblemNotMedia: "not_media", ProblemUnreadableName: "unreadable_name",
}

func (p FileProblem) String() string { return fileProblemNames[p] }

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

// MarkerSource is where a marker came from, which also says whether it is trusted. Skipping
// without asking and "watched when the credits start" only ever use trusted markers.
type MarkerSource int

const (
	MarkerFromFile MarkerSource = 1 // a chapter or marker written in the file
	// MarkerFromAnalysis is detected in this one file and not yet trusted.
	MarkerFromAnalysis MarkerSource = 2
	MarkerFromProvider MarkerSource = 3
	MarkerFromPerson   MarkerSource = 4 // made or approved by the owner
	// MarkerConfirmed is detected and confirmed by the show's other episodes.
	MarkerConfirmed MarkerSource = 5
)

var markerSourceNames = map[MarkerSource]string{
	MarkerFromFile: "file", MarkerFromAnalysis: "analysis", MarkerFromProvider: "provider", MarkerFromPerson: "person",
	MarkerConfirmed: "analysis_confirmed",
}

// Trusted reports whether a marker from this source may be acted on without asking.
func (s MarkerSource) Trusted() bool { return s != MarkerFromAnalysis }

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

// KeyKind is what kind of fact identifies an item across rescans. A key is unique in its
// library, so whatever a key names, it names one item. A scan looks an item up by its keys in
// the order file, provider, embedded, name, folder: the first that is stored wins, and the
// others are then stored for the same item. Merging two titles moves every key of one to the
// other; splitting a file off moves its file key to the new title. Keys are only removed with
// their item or when the file they name has moved.
type KeyKind int

const (
	// KeyFolder: "this folder is this title's": a show's folder, an artist's, an album's, a
	// book's, or the folder a film has to itself. It guarantees that a file later found in the
	// folder, whose own name does not say otherwise, joins the same title, and that extras in
	// the folder's Extras or Trailers folder find their owner.
	// The key is the library path's id, a slash and the folder's path.
	KeyFolder KeyKind = 1
	// KeyFile: "this file plays this title". With "#n" after the path, the nth episode or cue
	// track of a file that holds several. It guarantees that a rescan, a second run of a job or
	// a restart after a kill finds the same item, and that a file the owner split into its own
	// title stays split: this key is looked up first, so nothing the name says can pull the
	// file back. The key is the library path's id, a slash and the file's path.
	KeyFile KeyKind = 2
	// KeyEmbedded: an id written in the file's own tags, such as a MusicBrainz release id. It
	// guarantees that the files of one release are one album however they are named or foldered.
	KeyEmbedded KeyKind = 3
	// KeyProvider: a provider id, from the name ("{tmdb-603}"), an NFO file or a match. It
	// guarantees that two files anywhere in the library with the same id are versions of one
	// title, and that a matched title is found again whatever its files are renamed to.
	KeyProvider KeyKind = 4
	// KeyName: what the names say. At the top of a library, the kind, the folded title and the
	// year ("movie|heat|1995"). Under a parent, the parent's item id and the place in it
	// ("812/s2", "813/e5", "90/d1t4"). It guarantees that two files of the same film or episode
	// anywhere in the library are versions of one title, and that a show's seasons and an
	// album's tracks are each made once.
	KeyName KeyKind = 5
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
	// TopicPreferences: a profile's own settings changed: its other devices read them again.
	TopicPreferences ChangeTopic = 7
	// TopicHistory: an entry was removed from a profile's History, or it was cleared: its
	// other devices read History again.
	TopicHistory ChangeTopic = 8
)

var changeTopicNames = map[ChangeTopic]string{
	TopicItem: "item", TopicLibrary: "library", TopicState: "state", TopicPerson: "person",
	TopicAccess: "access", TopicList: "list", TopicPreferences: "preferences", TopicHistory: "history",
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
	LibraryAnime      = "anime"
	LibraryComics     = "comics"
)

var libraryKinds = map[string]bool{
	LibraryMovies: true, LibraryShows: true, LibraryMusic: true, LibraryBooks: true, LibraryAudiobooks: true,
	LibraryAnime: true, LibraryComics: true,
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
