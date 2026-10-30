import {createRoot} from 'react-dom/client';
import {RouterProvider, createMemoryHistory} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import type {HttpLocalApi, Library, LocalSession, Viewer} from '@core/index.ts';
import {parsePreferenceSnapshot} from '@core/preferences.ts';
import bootstrapWire from '../../../../server/testdata/contracts/get-bootstrap.json?raw';
import type {EventsClient} from '@core/playback-v1/events.ts';
import type {ShellArtwork} from '../../shell/artwork';
import {createArtworkRuntime} from '../../kit/artwork/source';
import {router} from '../../router';
import {WebScreenProvider} from '../../route-registry-view';
import {ShellBindingsProvider, type ShellBindings} from '../../shell/bindings';
import {FreshnessProvider} from '../../platform/freshness';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {createOwnerActivity} from '../../shell/Activity';
import type {ShellIdentity} from '../../shell/Shell';
import type {HomeBindings} from '../../screens/home/Home';
import {IconSprite} from '../../kit/Icon';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

/**
 * Frame 4's public DEV specimen: the genuine production Show screen, mounted through the
 * registered library route and shell. This module only supplies the answers a public fixture
 * would send (The Last of Us, its four stored episodes, the owner's show settings) and records
 * the real commands the screen issues. It fails closed outside the development server; no
 * production module imports it and no private artwork, credential or server state is used.
 * Facts and copy come from `design/ui-v1/desktop-detail.html` frame 4 and
 * `design/ui-v1/notes/detail-desktop.md`; every image slot carries one neutral inline image.
 */
if (!import.meta.env.DEV) throw new Error('Show specimens require the development server.');
const frame = new URLSearchParams(location.search).get('frame');
if (frame !== null && frame !== '4' && frame !== '5' && frame !== '4b' && frame !== '6') throw new Error('The show specimen measures desktop frames 4, 4b, 5 and 6.');

type SpecimenEvent = Readonly<{kind: string; at: number; value?: unknown}>;

const serverId = 'specimen-server';
const libraryId = 'tv';
const showId = 'the-last-of-us';
const seasonId = 'season-1';
const simpsonsId = 'the-simpsons';
const simpsonsSeasonId = 'simpsons-season-3';
const activeShowId = frame === '6' ? simpsonsId : showId;
const viewerFence = 'specimen-viewer-fence';
const image = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Zl+0AAAAASUVORK5CYII=';
const artPath = (id: string, role: 'poster' | 'backdrop' | 'still', query: string) => `/v1/metadata/show/${encodeURIComponent(id)}/art/${role}?${query}`;
const imageArt = (kind: string, id: string) => `specimen-art://${kind}/${encodeURIComponent(id)}`;

const overview = 'After a global pandemic destroys civilization, a hardened survivor takes charge of a 14-year-old girl who may be humanity’s last hope.';
const episodeOverview = 'Joel and Tess head to the Capitol to retrieve something from a dead drop, but their plans change when they meet a young girl who may hold the key to a cure.'; // lint-strings-allow

const credits = [
  {id: 'pedro-pascal', name: 'Pedro Pascal', role: 'Joel Miller', department: 'Acting', ordinal: 0}, // lint-strings-allow
  {id: 'bella-ramsey', name: 'Bella Ramsey', role: 'Ellie Williams', department: 'Acting', ordinal: 1}, // lint-strings-allow
  {id: 'gabriel-luna', name: 'Gabriel Luna', role: 'Tommy Miller', department: 'Acting', ordinal: 2},
  {id: 'isabela-merced', name: 'Isabela Merced', role: 'Dina', department: 'Acting', ordinal: 3},
  {id: 'young-mazino', name: 'Young Mazino', role: 'Jesse', department: 'Acting', ordinal: 4},
  {id: 'kaitlyn-dever', name: 'Kaitlyn Dever', role: 'Abby', department: 'Acting', ordinal: 5},
  {id: 'anna-torv', name: 'Anna Torv', role: 'Tess', department: 'Acting', ordinal: 6},
  {id: 'nick-offerman', name: 'Nick Offerman', role: 'Bill', department: 'Acting', ordinal: 7},
  {id: 'murray-bartlett', name: 'Murray Bartlett', role: 'Frank', department: 'Acting', ordinal: 8},
  {id: 'lamar-johnson', name: 'Lamar Johnson', role: 'Henry', department: 'Acting', ordinal: 9},
  {id: 'melanie-lynskey', name: 'Melanie Lynskey', role: 'Kathleen', department: 'Acting', ordinal: 10},
  {id: 'keivonn-woodard', name: 'Keivonn Montreal Woodard', role: 'Sam', department: 'Acting', ordinal: 11},
  {id: 'storm-reid', name: 'Storm Reid', role: 'Riley', department: 'Acting', ordinal: 12},
  {id: 'scott-shepherd', name: 'Scott Shepherd', role: 'David', department: 'Acting', ordinal: 13},
  {id: 'troy-baker', name: 'Troy Baker', role: 'James', department: 'Acting', ordinal: 14},
  {id: 'ashley-johnson', name: 'Ashley Johnson', role: 'Anna', department: 'Acting', ordinal: 15},
  {id: 'jeffrey-pierce', name: 'Jeffrey Pierce', role: 'Perry', department: 'Acting', ordinal: 16},
  {id: 'graham-greene', name: 'Graham Greene', role: 'Marlon', department: 'Acting', ordinal: 17},
  {id: 'elaine-miles', name: 'Elaine Miles', role: 'Florence', department: 'Acting', ordinal: 18},
  {id: 'rutina-wesley', name: 'Rutina Wesley', role: 'Maria', department: 'Acting', ordinal: 19},
  {id: 'josh-brener', name: 'Josh Brener', role: 'Murray', department: 'Acting', ordinal: 20},
  {id: 'christine-hakim', name: 'Christine Hakim', role: 'Ratna', department: 'Acting', ordinal: 21},
  {id: 'danny-ramirez', name: 'Danny Ramirez', role: 'Manny', department: 'Acting', ordinal: 22},
  {id: 'ariza-lopez', name: 'Ariela Barer', role: 'Mel', department: 'Acting', ordinal: 23},
  {id: 'tati-gabrielle', name: 'Tati Gabrielle', role: 'Nora', department: 'Acting', ordinal: 24},
  {id: 'spencer-lord', name: 'Spencer Lord', role: 'Owen', department: 'Acting', ordinal: 25},
  {id: 'ben-ayers', name: 'Ben Ayers', role: 'Jordan', department: 'Acting', ordinal: 26},
  {id: 'hattie-morahan', name: 'Hattie Morahan', role: 'Julia', department: 'Acting', ordinal: 27},
  {id: 'ian-carr', name: 'Ian Carr', role: 'Isaac', department: 'Acting', ordinal: 28},
  {id: 'philip-philmar', name: 'Philip Philmar', role: 'Ranger', department: 'Acting', ordinal: 29},
  {id: 'robert-smith', name: 'Robert John Burke', role: 'Ranger', department: 'Acting', ordinal: 30},
  {id: 'max-casella', name: 'Max Casella', role: 'Ranger', department: 'Acting', ordinal: 31},
  {id: 'noah-lamanna', name: 'Noah Lamanna', role: 'Ranger', department: 'Acting', ordinal: 32},
  {id: 'catherine-ohara', name: 'Catherine O’Hara', role: 'Ranger', department: 'Acting', ordinal: 33},
  {id: 'jason-ritter', name: 'Jason Ritter', role: 'Ranger', department: 'Acting', ordinal: 34},
  {id: 'aimee-carrero', name: 'Aimee Carrero', role: 'Ranger', department: 'Acting', ordinal: 35},
  {id: 'craig-mazin', name: 'Craig Mazin', role: 'Creator', department: 'Writing', ordinal: 36}, // lint-strings-allow
  {id: 'neil-druckmann', name: 'Neil Druckmann', role: 'Creator', department: 'Writing', ordinal: 37},
] as const;

const episodes = [
  {id: 'ep-1', title: 'When You’re Lost in the Darkness', airDate: '2023-01-15', duration: 4860, progressSeconds: 3256, watched: false, overview: episodeOverview}, // lint-strings-allow
  {id: 'ep-2', title: 'Infected', airDate: '2023-01-22', duration: 3180, progressSeconds: 0, watched: false, overview: 'A horrifying discovery forces Joel and Tess to change course.'}, // lint-strings-allow
  {id: 'ep-3', title: 'Long, Long Time', airDate: '2023-01-29', duration: 4560, progressSeconds: 0, watched: false, overview: 'Doomsday prepper Bill prepares to ride out the apocalypse alone, until a mysterious stranger upends his plans. Years later, Joel and Ellie seek Bill’s guidance and find themselves at a crossroads.'}, // lint-strings-allow
  {id: 'ep-4', title: 'Please Hold to My Hand', airDate: '2023-02-05', duration: 2760, progressSeconds: 0, watched: false, overview: 'After a harrowing trek across a desolate United States, Joel and Ellie find themselves navigating a dangerous Kansas City on foot, while a rebel leader hunts them.'}, // lint-strings-allow
] as const;

const episodeEntry = (index: number) => {
  const episode = episodes[index];
  return {id: episode.id, kind: 'episode', libraryId, title: episode.title, seasonNumber: 1, episodeNumber: index + 1,
    airDate: episode.airDate, durationMs: episode.duration * 1000, progressMs: episode.progressSeconds * 1000, watched: episode.watched,
    overview: episode.overview, stillUrl: imageArt('still', episode.id), available: true,
    navigation: {view: 'item', entityId: episode.id}, playback: {itemId: episode.id}};
};

function showWorkspace() {
  const revision = {catalog: 1, viewer: 1};
  return {
    scope: {serverId, libraryId, viewerFence}, revision,
    show: {id: showId, libraryId, title: 'The Last of Us', year: 2023, libraryKind: 'tv', providerMatchStatus: 'matched', // lint-strings-allow
      overview, network: 'HBO', studio: 'PlayStation Productions', contentRating: 'TV-MA', // lint-strings-allow
      status: 'Continuing', firstAired: '2023-01-15', originalLanguage: 'en', addedAt: '2026-10-17T00:00:00Z', episodeCount: episodes.length,
      posterUrl: artPath(showId, 'poster', 'w=600'), backdropUrl: artPath(showId, 'backdrop', 'w=1920'),
      genres: ['Drama']}, // lint-strings-allow
    showCredits: credits.map(credit => ({...credit})),
    seasons: [{id: seasonId, showId, number: 1, title: 'Season 1', episodeCount: episodes.length, watchedCount: 0, unwatchedCount: episodes.length, // lint-strings-allow
      firstAired: '2023-01-15', lastAired: '2023-02-05'}],
    seasonTotalCount: 1, nextSeasonCursor: '', groups: [],
    selected: {seasonId},
    episodes: {scope: {serverId, libraryId, libraryKind: 'tv', view: 'season', entityId: seasonId, viewerFence}, revision,
      query: {sort: 'episode', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'},
      sections: [{id: 'episodes', type: 'list', heading: 'Season 1', totalCount: episodes.length, nextCursor: '', entries: episodes.map((_, index) => episodeEntry(index))}]},
    nextUp: episodeEntry(0),
    settings: {hideSeasons: false, absoluteNumbering: false, ranges: false, newestFirst: false, revision: 1},
  };
}

const containerState = () => ({scope: {serverId, libraryId, viewerFence}, revision: 1, watched: false, watermark: '', watchlisted: false, favorite: false, watchedCount: 0, unwatchedCount: episodes.length, libraryId});

function episodeDetail(index = 0) {
  const revision = {catalog: 1, viewer: 1};
  const episode = episodes[index] ?? episodes[0];
  const credits = index === 2
    ? [{id: 'nick-offerman', name: 'Nick Offerman', role: 'Bill', department: 'Acting', provider: 'specimen'}, // lint-strings-allow fixture data
      {id: 'murray-bartlett', name: 'Murray Bartlett', role: 'Frank', department: 'Acting', provider: 'specimen'}, // lint-strings-allow fixture data
      {id: 'peter-hoar', name: 'Peter Hoar', role: 'Director', department: 'Directing', provider: 'specimen'}, // lint-strings-allow fixture data
      {id: 'craig-mazin', name: 'Craig Mazin', role: 'Writer', department: 'Writing', provider: 'specimen'}] // lint-strings-allow fixture data
    : [{id: 'pedro-pascal', name: 'Pedro Pascal', role: 'Joel Miller', department: 'Acting', provider: 'specimen'}, // lint-strings-allow fixture data
      {id: 'bella-ramsey', name: 'Bella Ramsey', role: 'Ellie Williams', department: 'Acting', provider: 'specimen'}]; // lint-strings-allow fixture data
  const neighbours = index === 2 ? {previous: {id: 'ep-2', number: 2, title: 'Infected'}, next: {id: 'ep-4', number: 4, title: 'Please Hold to My Hand'}} : {}; // lint-strings-allow fixture data
  return {
    scope: {serverId, libraryId, itemId: episode.id, viewerFence}, revision,
    item: {id: episode.id, libraryId, kind: 'episode', title: episode.title, year: 2023, durationMs: episode.duration * 1000, progressMs: episode.progressSeconds * 1000,
      available: true, addedAt: '2026-10-17T00:00:00Z', overview: episode.overview,
      posterUrl: imageArt('poster', episode.id), backdropUrl: imageArt('backdrop', episode.id)},
    personal: {watchlisted: false, favorite: false, rating: null, revision: 1, watched: false, progressMs: episode.progressSeconds, lastPlayedAt: '', status: 'current', conflicts: []},
    actions: [{id: 'play', labelKey: 'entry.play', enabled: true, playback: {itemId: episode.id, startMs: 0}},
      {id: 'watchlist', labelKey: 'entry.addToWatchlist', enabled: true},
      {id: 'watched', labelKey: 'entry.markWatched', enabled: true}],
    facts: {contentRating: 'TV-MA', releaseDate: episode.airDate, studio: 'PlayStation Productions', country: 'United States', originalLanguage: 'en'}, // lint-strings-allow fixture data
    episodeDetails: {contentRating: 'TV-MA', ...neighbours}, // lint-strings-allow fixture data
    metadata: {status: 'available', ratings: [], attributions: [], genres: [{id: 'drama', name: 'Drama', provider: 'specimen'}], // lint-strings-allow fixture data
      credits, creditTotals: {cast: credits.filter(credit => credit.department === 'Acting').length, crew: credits.filter(credit => credit.department !== 'Acting').length}},
    languages: {audio: ['en'], subtitles: ['en']},
    collections: [], markers: [],
  };
}

const simpsonsSeasons = [
  {id: 'simpsons-season-1', showId: simpsonsId, number: 1, title: 'Season 1', episodeCount: 13, watchedCount: 13, unwatchedCount: 0, firstAired: '1989-12-17', lastAired: '1990-05-13'}, // lint-strings-allow specimen catalogue data, not interface copy
  {id: 'simpsons-season-2', showId: simpsonsId, number: 2, title: 'Season 2', episodeCount: 22, watchedCount: 22, unwatchedCount: 0, firstAired: '1990-10-11', lastAired: '1991-07-11'}, // lint-strings-allow specimen catalogue data, not interface copy
  {id: simpsonsSeasonId, showId: simpsonsId, number: 3, title: 'Season 3', episodeCount: 24, watchedCount: 4, unwatchedCount: 20, firstAired: '1991-09-19', lastAired: '1992-05-14'}, // lint-strings-allow specimen catalogue data, not interface copy
  {id: 'simpsons-season-4', showId: simpsonsId, number: 4, title: 'Season 4', episodeCount: 22, watchedCount: 0, unwatchedCount: 22, firstAired: '1992-09-24', lastAired: '1993-05-13'}, // lint-strings-allow specimen catalogue data, not interface copy
  {id: 'simpsons-season-5', showId: simpsonsId, number: 5, title: 'Season 5', episodeCount: 22, watchedCount: 0, unwatchedCount: 22, firstAired: '1993-09-30', lastAired: '1994-05-19'}, // lint-strings-allow specimen catalogue data, not interface copy
  {id: 'simpsons-season-6', showId: simpsonsId, number: 6, title: 'Season 6', episodeCount: 25, watchedCount: 0, unwatchedCount: 25, firstAired: '1994-09-04', lastAired: '1995-05-21'}, // lint-strings-allow specimen catalogue data, not interface copy
  {id: 'simpsons-specials', showId: simpsonsId, number: 0, title: 'Specials', episodeCount: 12, watchedCount: 0, unwatchedCount: 12}, // lint-strings-allow specimen catalogue data, not interface copy
] as const;
const simpsonsEpisodeFacts = [
  ['Stark Raving Dad', '1991-09-19', true],
  ['Mr. Lisa Goes to Washington', '1991-09-26', true],
  ['When Flanders Failed', '1991-10-03', true],
  ['Bart the Murderer', '1991-10-10', true],
  ['Homer Defined', '1991-10-17', false],
  ['Like Father, Like Clown', '1991-10-24', false],
  ['Treehouse of Horror II', '1991-10-31', false],
  ["Lisa's Pony", '1991-11-07', false],
] as const;
const simpsonsEpisode = (index: number) => {
  const [title, airDate, watched] = simpsonsEpisodeFacts[index]!;
  const id = `simp-ep-${index + 1}`;
  return {id, kind: 'episode', libraryId, title, seasonNumber: 3, episodeNumber: index + 1, airDate, durationMs: 1_380_000, progressMs: 0, watched,
    overview: 'A fixture episode of the drawn long-running show, with the real season, number and air date.', stillUrl: imageArt('still', id), available: true,
    navigation: {view: 'item', entityId: id}, playback: {itemId: id}};
};
function simpsonsWorkspace() {
  const revision = {catalog: 1, viewer: 1};
  return {
    scope: {serverId, libraryId, viewerFence}, revision,
    show: {id: simpsonsId, libraryId, title: 'The Simpsons', year: 1989, libraryKind: 'tv', providerMatchStatus: 'matched', // lint-strings-allow specimen catalogue data, not interface copy
      overview: 'The satirical adventures of the Simpson family in Springfield.', network: 'Fox', studio: 'Gracie Films', contentRating: 'TV-14',
      status: 'Continuing', firstAired: '1989-12-17', originalLanguage: 'en', addedAt: '2026-10-17T00:00:00Z', episodeCount: 24, genres: ['Animation', 'Comedy'], // lint-strings-allow fixture data
      posterUrl: imageArt('poster', simpsonsId), backdropUrl: imageArt('backdrop', simpsonsId)},
    showCredits: [],
    seasons: simpsonsSeasons.map(season => ({...season})), seasonTotalCount: 36, nextSeasonCursor: 'specimen-season-page-2', groups: [],
    selected: {seasonId: simpsonsSeasonId},
    episodes: {scope: {serverId, libraryId, libraryKind: 'tv', view: 'season', entityId: simpsonsSeasonId, viewerFence}, revision,
      query: {sort: 'episode', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'},
      sections: [{id: 'episodes', type: 'list', heading: 'Season 3', totalCount: simpsonsEpisodeFacts.length, nextCursor: '', entries: simpsonsEpisodeFacts.map((_, index) => simpsonsEpisode(index))}]},
    nextUp: simpsonsEpisode(4),
    settings: {hideSeasons: false, absoluteNumbering: false, ranges: false, newestFirst: false, revision: 1},
  };
}
const simpsonsContainerState = () => ({scope: {serverId, libraryId, viewerFence}, revision: 1, watched: false, watermark: '', watchlisted: false, favorite: false, watchedCount: 4, unwatchedCount: 20, libraryId});
function simpsonsRecommendations() {
  return {showId: simpsonsId, revision: {catalog: 1, viewer: 1}, rows: [
    recommendationRow('more-like', 'More like The Simpsons', cardRow([['Futurama', 1999, 'show'], ['Bob’s Burgers', 2011, 'show'], ['King of the Hill', 1997, 'show']])),
  ]};
}

function activityDocument(now: string) {
  const session = (id: string) => ({id, user: {id: 'specimen-account', username: 'demo'}, profile: {id: 'demo', name: 'Demo'}, // lint-strings-allow
    device: {id: 'specimen-device', name: 'Portico web'}, kind: 'vod', state: 'playing', startedAt: now, // lint-strings-allow
    problem: false, converting: false, positionMs: 60_000, decision: {}});
  return {scope: {serverId, viewerFence: 'a'.repeat(64)}, observedAt: now, serverName: 'Portico demo', // lint-strings-allow
    streams: {count: 2, convertingCount: 0, overflow: {count: 0, convertingCount: 0}, items: [session('stream-1'), session('stream-2')]},
    scans: [], tasks: {count: 0, items: []}, update: {available: false, state: 'unconfigured', current: ''}, health: {degraded: false}, majorIssues: []};
}

const records: SpecimenEvent[] = [];
const record = (kind: string, value?: unknown) => {records.push(Object.freeze({kind, at: Date.now(), ...(value === undefined ? {} : {value})}));};

const cardRow = (items: readonly (readonly [string, number, string])[]) => items.map(([title, year, kind]) => {
  const id = title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
  return {id, kind, libraryId: libraryId, title, year, available: true, posterUrl: imageArt('poster', id), navigation: {view: 'item', entityId: id}, playback: {itemId: id}} as const;
});
const recommendationRow = (id: string, title: string, entries: readonly unknown[], subtitleText?: {code: string; fallback: string}) => ({
  id, viewerFence, title, titleText: {code: `specimen.${id}`, fallback: title}, kind: 'recommendations', artworkShape: 'poster', endpoint: 'show-recommendations',
  privacySensitivity: 'public', policyState: 'current', priority: id === 'more-like' ? 1 : 2, required: false, hideable: true, reorderable: false, critical: false, cursorCapable: false,
  ...(subtitleText ? {subtitleText} : {}), entries, total: entries.length, start: 0, limit: entries.length, hasMore: false, nextCursor: '', revision: {catalog: 1, viewer: 1},
});
function showRecommendations() {
  return {showId, revision: {catalog: 1, viewer: 1}, rows: [
    recommendationRow('more-like', 'More like The Last of Us', cardRow([['Severance', 2022, 'show'], ['Silo', 2023, 'show'], ['Stranger Things', 2016, 'show'], ['The Walking Dead', 2010, 'show'], ['Slow Horses', 2022, 'show'], ['The Mandalorian', 2019, 'show'], ['The Terminator', 1984, 'movie']])),
    recommendationRow('viewers-also-watched', 'Viewers also watched', cardRow([['Breaking Bad', 2008, 'show'], ['Game of Thrones', 2011, 'show'], ['Reacher', 2022, 'show'], ['The Gentlemen', 2024, 'show'], ['MobLand', 2025, 'show'], ['Dark Matter', 2024, 'show'], ['Lioness', 2023, 'show']]), {code: 'specimen.viewersAlsoWatchedNote', fallback: 'What people on this server played after it'}),
  ]};
}
const request = async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
  const route = path.split('?')[0];
  record('request', {path: route, method});
  if (method === 'GET' && route === `/v1/libraries/${libraryId}/show-workspace`) return (frame === '6' ? simpsonsWorkspace() : showWorkspace()) as T;
  if (method === 'GET' && route === `/v1/containers/show/${activeShowId}/personal-state`) return (frame === '6' ? simpsonsContainerState() : containerState()) as T;
  if (method === 'GET' && route === `/v1/shows/${activeShowId}/recommendations`) return (frame === '6' ? simpsonsRecommendations() : showRecommendations()) as T;
  if (method === 'GET' && /^\/v1\/items\/ep-\d+\/detail$/.test(route)) return episodeDetail(Number(route.split('/')[3].slice(3)) - 1) as T;
  if (method === 'PUT' && route === `/v1/admin/libraries/${libraryId}/shows/${showId}/settings`) {
    const settings = (body ?? {}) as Record<string, unknown>;
    record('show-settings', settings);
    return {...showWorkspace().settings, ...settings, revision: 2} as T;
  }
  throw Object.assign(new Error(`The show specimen has no answer for ${method} ${route}.`), {code: 'not_found', status: 404});
};

const requestRaw = async (path: string) => {
  if (path.split('?')[0] === '/v1/admin/activity') return {status: 200, headers: {}, body: activityDocument(new Date().toISOString())};
  return {status: 404, headers: {}, body: {error: {code: 'not_found'}}};
};

const runtime = createArtworkRuntime({
  scopeKey: 'show-specimen',
  classify: () => ({retryable: false}),
  load: async ({url}, signal) => {
    const response = await fetch(url, {signal});
    const blob = await response.blob();
    const bitmap = await createImageBitmap(blob);
    const width = bitmap.width, height = bitmap.height; bitmap.close();
    return {src: URL.createObjectURL(blob), bytes: blob.size, width, height};
  },
  release: imageValue => { try { URL.revokeObjectURL(imageValue.src); } catch { /* A closed page owns nothing. */ } },
});
const artwork = {
  ...runtime,
  foreground(url: string | undefined) {
    return url ? runtime.source({url: image, cacheKey: `foreground:${url}`}) : undefined;
  },
  ambient(url: string | undefined) {
    return url ? runtime.ambient({url: image, cacheKey: `ambient:${url}`, variant: 'ambient-v1', maxEdge: 64, version: 'a'.repeat(64)}) : undefined;
  },
} as unknown as ShellArtwork;

const events = {on: () => () => {}, onResync: () => () => {}, onAny: () => () => {}, getStatus: () => ({connected: true}), subscribeStatus: () => () => {}} as unknown as EventsClient;
const preferences = parsePreferenceSnapshot((JSON.parse(bootstrapWire) as {response: {preferences: unknown}}).response.preferences);
const subscribePreferences = () => () => {};
const api = {
  request, requestRaw, getRouteSnapshot: () => ({phase: 'ready'}), subscribeRoute: () => () => {},
  reading: () => { const life = new AbortController(); return {signal: life.signal, versions: async () => ({versions: []}), dispose: () => life.abort()}; },
  dispose: () => runtime.dispose(),
} as unknown as HttpLocalApi;

const time = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
const viewer: Viewer = {serverId, authority: 'local', accountId: 'specimen-account', profileId: 'demo', role: 'owner'};
const session: LocalSession = {accessToken: 'specimen-public', refreshToken: '', sessionFamilyId: 'specimen-family', tokenGeneration: '1',
  installationId: 'i'.repeat(43), expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer};
const libraries: readonly Library[] = [
  {id: 'movies', name: 'Movies', kind: 'movie'},
  {id: libraryId, name: 'TV Shows', kind: 'show'},
  {id: 'anime', name: 'Anime', kind: 'show'},
  {id: 'music', name: 'Music', kind: 'music'},
  {id: 'audiobooks', name: 'Audiobooks', kind: 'audiobook'},
];
const identity: ShellIdentity = {viewer, username: 'demo', profileName: 'Demo', profileArt: 'blue', serverName: 'Portico demo', // lint-strings-allow
  primaryProfile: true, profiles: [], canManageProfiles: false, accountFacts: [], libraries, liveAvailable: true, unread: 3, showNotificationDot: true,
  capabilities: {manageAccount: false, managePeople: false, manageServer: true, manageSignIn: false}};

const contextBase = {api, session: () => session, events, artwork, facts: {identity, preferences, serverName: identity.serverName},
  preferences: {getSnapshot: () => preferences, subscribe: subscribePreferences}, epoch: 1, isCurrent: () => true,
  signOut: () => record('sign-out'), signOutPorticoAccount: () => record('sign-out-portico'), switchProfile: async () => {}, switchServer: async () => {}, savedServers: []} as unknown as ShellAssemblyContext;
const activity = createOwnerActivity(contextBase, time, () => record('open-dashboard'), () => record('open-update'), destination => record('open-issue', destination));
const homeService = {getSnapshot: () => ({phase: 'idle'}), subscribe: () => () => {}} as unknown as HomeBindings['service'];
const home: HomeBindings = {service: homeService, artwork, time, reducedMotion: true,
  selection: {toolbar: null, card: () => undefined, channel: () => undefined, collection: () => undefined, registerWindow: () => undefined, registerSpotlight: () => undefined, moreAvailable: () => false, moreExpanded: () => false},
  effects: {play: async () => {record('play');}, playChannel: async () => {}, openEntry: () => {}, openSeeAll: () => {}, openMore: () => {},
    setMyList: async () => {}, reportError: () => {record('report-error');}}};
const binding: ShellBindings = {api, events, scopeKey: 'show-specimen', shell: {identity, OwnerActivity: activity.OwnerActivity, dockVisible: false, onSignOut: () => record('sign-out')}, home};

// The fixture's real command receipts are inspectable from the page; no credentials or server data.
(window as Window & {__porticoShowSpecimen?: unknown}).__porticoShowSpecimen = {records, show: showWorkspace, container: containerState, detail: episodeDetail};

router.update({history: createMemoryHistory({initialEntries: [`/library/${libraryId}?view=show&entityId=${activeShowId}`]})});
createRoot(document.getElementById('root')!).render(
  <FreshnessProvider api={api} viewerId={viewerFence} serverId={serverId} events={events}>
    <WebScreenProvider value={{binding, context: contextBase}}>
      <IconSprite />
      <ShellBindingsProvider value={binding}>
        <RouterProvider router={router} />
      </ShellBindingsProvider>
    </WebScreenProvider>
  </FreshnessProvider>,
);

/** Frame 4 is the scrolled Details tab; frame 5 opens the third episode in place. */
let tries = 0;
const activate = () => {
  if (++tries > 600) return;
  if (frame === '4b') {
    const related = [...document.querySelectorAll<HTMLElement>('[role="tab"]')].find(tab => tab.textContent?.trim() === defaultI18n.t('next.controls.moreLikeThis'));
    if (!related) { requestAnimationFrame(activate); return; }
    if (related.getAttribute('aria-selected') !== 'true') { related.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, button: 0})); related.click(); }
    requestAnimationFrame(() => requestAnimationFrame(() => requestAnimationFrame(() => document.documentElement.setAttribute('data-show-frame4b-ready', 'true'))));
    return;
  }
  if (frame === '6') {
    const season = [...document.querySelectorAll<HTMLElement>('button')].find(button => button.textContent?.trim() === defaultI18n.t('next.kit.season', {number: 3}));
    if (!season) { requestAnimationFrame(activate); return; }
    if (season.getAttribute('aria-expanded') !== 'true') season.click();
    requestAnimationFrame(() => requestAnimationFrame(() => requestAnimationFrame(() => document.documentElement.setAttribute('data-show-frame6-ready', 'true'))));
    return;
  }
  if (frame === '5') {
    const tile = document.querySelector<HTMLElement>('button[aria-label="Long, Long Time"]');
    if (!tile) { requestAnimationFrame(activate); return; }
    tile.click();
    requestAnimationFrame(() => requestAnimationFrame(() => document.documentElement.setAttribute('data-show-frame5-ready', 'true')));
    return;
  }
  const details = [...document.querySelectorAll<HTMLElement>('[role="tab"]')].find(tab => tab.textContent?.trim() === defaultI18n.t('next.controls.details'));
  if (!details) { requestAnimationFrame(activate); return; }
  if (details.getAttribute('aria-selected') !== 'true') {
    details.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, button: 0}));
    details.click();
  }
  requestAnimationFrame(() => {
    const change = document.querySelector<HTMLElement>('[data-show-settings] button');
    if (change && change.getAttribute('aria-expanded') !== 'true') change.click();
    requestAnimationFrame(() => document.documentElement.setAttribute('data-show-frame4-ready', 'true'));
  });
};
requestAnimationFrame(activate);
