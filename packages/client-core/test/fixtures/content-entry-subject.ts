import type {ContentEntry} from '../../src/library-content.ts';
import type {CardSubject} from '../../src/presentation/kit-model/facts.ts';

const episode: ContentEntry = {id: 'episode', kind: 'episode', title: 'Actual title', show: {id: 'show', name: 'Actual show'}, seasonNumber: 2, episodeNumber: 3};
const subject: CardSubject = {id: 'episode', kind: 'episode', title: 'Actual title', seasonNumber: 2,
  year: undefined, contentRating: undefined, durationSeconds: undefined, positionSeconds: undefined,
  genres: undefined, seasonCount: undefined, artist: undefined, album: undefined, author: undefined,
  show: {id: 'show', title: 'Actual show'}};
export const contentEntrySubjectVectors: readonly Readonly<{name: string; entry: ContentEntry; subject: CardSubject}>[] = [
  {name: 'daily precedes absolute and special', entry: {...episode, dateBased: true, airDate: '2026-10-23', absoluteNumber: 77, special: true},
    subject: {...subject, episode: {mode: 'date', day: '2026-10-23'}}},
  {name: 'daily without a date does not invent one', entry: {...episode, dateBased: true, absoluteNumber: 77, special: true},
    subject: {...subject, episode: {mode: 'date', day: undefined}}},
  {name: 'absolute precedes air date and special', entry: {...episode, episodeNumber: undefined, absoluteNumber: 77, airDate: '2026-10-23', special: true},
    subject: {...subject, episode: {mode: 'absolute', number: 77}}},
  {name: 'absolute zero is retained', entry: {...episode, absoluteNumber: 0}, subject: {...subject, episode: {mode: 'absolute', number: 0}}},
  {name: 'air date without an episode number precedes special', entry: {...episode, episodeNumber: undefined, airDate: '2026-10-23', special: true},
    subject: {...subject, episode: {mode: 'date', day: '2026-10-23'}}},
  {name: 'explicit special', entry: {...episode, special: true}, subject: {...subject, episode: {mode: 'special'}}},
  {name: 'season zero is special', entry: {...episode, seasonNumber: 0}, subject: {...subject, seasonNumber: 0, episode: {mode: 'special'}}},
  {name: 'seasonal with an air date retains numbering', entry: {...episode, airDate: '2026-10-23'}, subject: {...subject, episode: {mode: 'seasonal', season: 2, episode: 3}}},
  {name: 'missing metadata stays absent', entry: {id: 'episode', kind: 'episode', title: 'Actual title'},
    subject: {...subject, seasonNumber: undefined, show: undefined, episode: {mode: 'seasonal', season: undefined, episode: undefined}}},
  {name: 'season has no episode numbering', entry: {...episode, kind: 'season', seasonNumber: 0}, subject: {...subject, kind: 'season', seasonNumber: 0, episode: undefined}},
  {name: 'radio keeps its minimal subject and display title', entry: {id: 'radio', kind: 'radio', title: 'Fallback', displayTitle: 'Radio title', year: 2026},
    subject: {id: 'radio', kind: 'radio', title: 'Radio title'}},
  {name: 'music and title facts are copied verbatim', entry: {id: 'song', kind: 'song', title: 'Fallback', displayTitle: 'Track (Live)', year: 2026,
    contentRating: 'PG', duration: 180, progressSeconds: 30, genres: ['Rock'], seasonCount: 4, artist: {id: 'artist', name: 'Artist'}, album: {id: 'album', name: 'Album'}, author: 'Author'},
    subject: {id: 'song', kind: 'song', title: 'Track (Live)', seasonNumber: undefined, year: 2026, contentRating: 'PG', durationSeconds: 180,
      positionSeconds: 30, genres: ['Rock'], seasonCount: 4, artist: 'Artist', album: 'Album', author: 'Author', show: undefined, episode: undefined}},
];
