/** Native shell labels; shared navigation, Home and action copy stays in its canonical catalogue. */
export const homeNativeMessages = {
  'next.home.native.mainNavigation': 'Main navigation',
  'next.home.native.profileAndServer': 'Profile and server',
  'next.home.native.selectRow': 'Select row',
  'next.home.native.mixedSelection': 'You’ve picked {types}. Their fields are different, so there is no single form for them. Keep one type to edit it.',
  'next.home.native.selectionType': '{kind, select, movie {Movies} show {Shows} season {Seasons} episode {Episodes} artist {Artists} album {Albums} song {Tracks} book {Audiobooks} audiobook_file {Audiobooks} chapter {Chapters} collection {Collections} category {Categories} author {Authors} book_series {Book series} disc {Discs} extra {Extras} channel {Channels} person {People} other {Titles}}',
  'next.home.native.selectedType': '{count, plural, one {{kind, select, movie {a movie} show {a show} season {a season} episode {an episode} artist {an artist} album {an album} song {a song} book {an audiobook} audiobook_file {an audiobook} chapter {a chapter} collection {a collection} category {a category} author {an author} book_series {a book series} disc {a disc} extra {an extra} channel {a channel} person {a person} other {a title}}} other {# {kind, select, movie {movies} show {shows} season {seasons} episode {episodes} artist {artists} album {albums} song {songs} book {audiobooks} audiobook_file {audiobooks} chapter {chapters} collection {collections} category {categories} author {authors} book_series {book series} disc {discs} extra {extras} channel {channels} person {people} other {titles}}}}',
  'next.home.native.editSelection': 'Edit {count, number} {kind, select, movie {movies} show {shows} season {seasons} episode {episodes} artist {artists} album {albums} song {tracks} book {audiobooks} audiobook_file {audiobooks} chapter {chapters} collection {collections} other {titles}}',
  'next.home.native.saveSelection': 'Save {count, number} {kind, select, movie {movies} show {shows} season {seasons} episode {episodes} artist {artists} album {albums} song {tracks} book {audiobooks} audiobook_file {audiobooks} chapter {chapters} collection {collections} other {titles}}',
  'next.home.native.changeScalar': '{field} becomes {value} on all {count, number}',
  'next.home.native.addValues': '{field}: add {values} to all {count, number}',
  'next.home.native.removeValues': '{field}: remove {values} from all {count, number}',
} as const satisfies Record<`next.home.native.${string}`, string>;
