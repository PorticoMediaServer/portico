package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func iaMusicTypeIdentity() CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}
}

func iaMusicTypeRequest() iaMusicInventoryRequest {
	return iaMusicInventoryRequest{
		scope:  inventoryScope{runID: iaMusicTestRunID, libraryKey: iaMusicTestLibraryKey, libraryID: iaMusicTestLibraryID},
		source: inventorySource{id: iaMusicTestSourceID, incarnation: "music-incarnation", rootIdentity: "music-root-identity", resolvedRoot: []byte(iaMusicTestRoot), generation: 3},
		run: inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
			leaseExpiresMS: 4000000000000, lastSequence: 1, acknowledgedSequence: 1, phase: "inventory", scanMode: "first-import"},
		directory: inventoryDirectory{path: iaMusicTestDirectory, identity: "music-directory-identity", revision: "music-directory-revision",
			cursorKind: inventoryLinuxCursor, expectedCursor: "", nextCursor: "42", pages: 2, skipped: 0, rawNames: 1, complete: true},
		files: []inventoryFile{{sourceKey: "asset-source-key", relativePath: "Album/01 Song.flac", assetPath: iaMusicTestRoot + "/Album/01 Song.flac",
			objectIdentity: iaMusicTestObjectID, revision: iaMusicTestObjectRevision, size: 42, modifiedNS: 7, title: "01 Song",
			sortTitle: "", sortKey: "song", sortHead: "s", sortKeyKeepArticles: "song", sortHeadKeepArticles: "s",
			metadataLanguage: "en-US", year: 2020}},
		complete:       true,
		albumKey:       "album-key",
		albumTitle:     "Album",
		albumArtistKey: "artist-key",
		artists:        []iaMusicArtist{{key: "artist-key", title: "Artist"}},
		songArtistKeys: []string{"artist-key"},
		songs:          []iaMusicSong{{key: "song-key", title: "Song", part: 0, cueOrdinal: 0, start: 0}},
	}
}

func TestIAMusicCommandValidation(t *testing.T) {
	q := iaMusicTypeRequest()
	command, err := newIAMusicInventoryCommand(iaMusicTypeIdentity(), q)
	if err != nil || !command.valid || command.digest == (RequestDigest{}) {
		t.Fatal("valid music original", err)
	}
	if err = command.checkOriginal(); err != nil {
		t.Fatal("check original", err)
	}
	if !iaMusicKey("a") || iaMusicKey("") || iaMusicKey("a\x00b") {
		t.Fatal("music key bounds")
	}
}

func TestIAMusicCommandRejectsIncompleteOrUnbounded(t *testing.T) {
	cue := func() *iaMusicCue {
		return &iaMusicCue{complete: true, file: inventoryFile{sourceKey: "cue-source-key", relativePath: "Album/album.cue",
			assetPath: iaMusicTestRoot + "/Album/album.cue", objectIdentity: "object:cue:1", revision: "cue-revision", size: 1, modifiedNS: 2,
			metadataLanguage: "en-US", title: "album"}}
	}
	for name, mutate := range map[string]func(*iaMusicInventoryRequest){
		"missing_file":         func(q *iaMusicInventoryRequest) { q.files = nil },
		"missing_artist":       func(q *iaMusicInventoryRequest) { q.artists = nil },
		"unknown_album_artist": func(q *iaMusicInventoryRequest) { q.albumArtistKey = "missing" },
		"duplicate_artist":     func(q *iaMusicInventoryRequest) { q.artists = append(q.artists, q.artists[0]) },
		"unknown_song_artist":  func(q *iaMusicInventoryRequest) { q.songArtistKeys = []string{"missing"} },
		"duplicate_song_artist": func(q *iaMusicInventoryRequest) {
			q.songArtistKeys = []string{"artist-key", "artist-key"}
		},
		"empty_album":      func(q *iaMusicInventoryRequest) { q.albumTitle = "" },
		"empty_song_title": func(q *iaMusicInventoryRequest) { q.songs[0].title = "" },
		"duplicate_song":   func(q *iaMusicInventoryRequest) { q.songs = append(q.songs, q.songs[0]) },
		"two_whole_songs": func(q *iaMusicInventoryRequest) {
			q.songs = append(q.songs, iaMusicSong{key: "song-2", title: "Song 2"})
		},
		"whole_with_cue_ordinal": func(q *iaMusicInventoryRequest) { q.songs[0].cueOrdinal = 1 },
		"cue_without_sheet":      func(q *iaMusicInventoryRequest) { q.songs[0].cueOrdinal = 1; q.songs[0].part = 0 },
		"cue_missing_complete":   func(q *iaMusicInventoryRequest) { q.cue = cue(); q.cue.complete = false; q.songs[0].cueOrdinal = 1 },
		"cue_same_file": func(q *iaMusicInventoryRequest) {
			q.cue = cue()
			q.cue.file.objectIdentity = iaMusicTestObjectID
			q.songs[0].cueOrdinal = 1
		},
		"cue_control_text": func(q *iaMusicInventoryRequest) {
			q.cue = cue()
			q.cue.file.changeToken = "x\x00y"
			q.songs[0].cueOrdinal = 1
		},
		"cue_missing_part": func(q *iaMusicInventoryRequest) { q.cue = cue(); q.songs[0].cueOrdinal = 1; q.songs[0].part = 2 },
		"cue_overlap": func(q *iaMusicInventoryRequest) {
			q.cue = cue()
			end := float64(40)
			q.songs = []iaMusicSong{
				{key: "a", title: "A", part: 0, cueOrdinal: 1, start: 0, end: &end},
				{key: "b", title: "B", part: 1, cueOrdinal: 2, start: 30},
			}
		},
		"cue_out_of_order": func(q *iaMusicInventoryRequest) {
			q.cue = cue()
			q.songs = []iaMusicSong{
				{key: "a", title: "A", part: 0, cueOrdinal: 1, start: 20},
				{key: "b", title: "B", part: 1, cueOrdinal: 2, start: 10},
			}
		},
		"windows_root": func(q *iaMusicInventoryRequest) {
			q.source.resolvedRoot = []byte(`C:\Media`)
			q.files[0].assetPath = `C:\Media\Album\01 Song.flac`
		},
		"unclean_root":   func(q *iaMusicInventoryRequest) { q.source.resolvedRoot = []byte("/media/../music") },
		"control_text":   func(q *iaMusicInventoryRequest) { q.artists[0].title = "Art\x00ist" },
		"negative_range": func(q *iaMusicInventoryRequest) { q.songs[0].start = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			q := iaMusicTypeRequest()
			mutate(&q)
			if _, err := newIAMusicInventoryCommand(iaMusicTypeIdentity(), q); !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrCap) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

func TestIAMusicCommandCapsPayload(t *testing.T) {
	q := iaMusicTypeRequest()
	var size uint64
	iaEpisodeWireSize(reflect.ValueOf(q), &size)
	q.artists[0].title = strings.Repeat("x", int(maxCanonicalRequestBytes-size)+64)
	if _, err := newIAMusicInventoryCommand(iaMusicTypeIdentity(), q); !errors.Is(err, ErrCap) {
		t.Fatalf("oversized payload accepted: %v", err)
	}
}

// The command clones every mutable input shape it retains and binds the digest
// to the exact request bytes.
func TestIAMusicCommandClonesAndBindsRequest(t *testing.T) {
	q := iaMusicTypeRequest()
	end := float64(10)
	q.songs[0].start = 0
	q.songs[0].end = &end
	command, err := newIAMusicInventoryCommand(iaMusicTypeIdentity(), q)
	if err != nil {
		t.Fatal(err)
	}
	base := command.digest
	q.songs[0].key = "changed"
	*q.songs[0].end = 99
	q.files[0].revision = "changed"
	if command.request.songs[0].key != "song-key" || command.request.songs[0].end == nil || *command.request.songs[0].end != 10 || command.request.files[0].revision != iaMusicTestObjectRevision {
		t.Fatal("request was not cloned")
	}
	changed := iaMusicTypeRequest()
	changed.songs[0].key = "changed"
	other, err := newIAMusicInventoryCommand(iaMusicTypeIdentity(), changed)
	if err != nil || other.digest == base {
		t.Fatal("request digest did not bind the song key", err)
	}
}
