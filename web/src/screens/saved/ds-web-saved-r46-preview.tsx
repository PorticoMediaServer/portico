// Controlled source fixture for the Saved screen's frame lift. It mounts the
// genuine SavedScreen, services and kit over a synthetic read boundary, so the
// same-size capture compares the real screen rather than a second renderer.
// Artwork URLs name the external design inputs; no artwork or frame is committed.
import {createRoot} from 'react-dom/client';
import {useState} from 'react';
import type {SavedApi} from '@core/saved.ts';
import {IconSprite} from '../../kit/Icon';
import {createArtworkRuntime} from '../../kit/artwork/source';
import {FreshnessProvider} from '../../platform/freshness';
import type {HttpLocalApi} from '@core/index.ts';
import type {ShellArtwork} from '../../shell/artwork';
import {SavedScreen, type SavedEffects} from './Saved';
import {createSavedRuntime, type SavedScreenRoute} from './runtime';
import {parseSavedScreenRoute} from './route';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

const assets = '/__design-assets__/assets';
type Wire = Record<string, unknown>;
const picture = (name: string) => `${assets}/pool/${name}.jpg`;
const art = (poster: string, backdrop: string) => ({posterUrl: picture(poster), backdropUrl: picture(backdrop)});
const card = (id: string, kind: string, title: string, pictures: {posterUrl: string; backdropUrl: string}, extra: Wire = {}): Wire => ({
  id, kind, title, libraryId: 'fixture', available: true, watched: false, navigation: {view: 'item', entityId: id}, playback: {itemId: id}, ...pictures, ...extra,
});
const movie = (id: string, title: string, year: number, poster: string, backdrop: string) => card(id, 'movie', title, art(poster, backdrop), {year, durationMs: 8_640_000});
const show = (id: string, title: string, poster: string, backdrop: string) => card(id, 'show', title, art(poster, backdrop), {seasonCount: 1});
const episode = (id: string, title: string, showName: string, poster: string, backdrop: string) => card(id, 'episode', title, art(poster, backdrop), {
  show: {id: 'the-last-of-us', name: showName}, seasonNumber: 1, episodeNumber: 1, durationMs: 4_920_000, progressMs: 1_082_400,
});
const book = (id: string, title: string, author: string, poster: string) => card(id, 'book', title, art(poster, poster), {author, durationMs: 34_800_000});
const album = (id: string, title: string, artist: string, poster: string) => card(id, 'album', title, art(poster, poster), {artist: {id: artist.toLowerCase().replace(/\W+/g, '-'), name: artist}});

const posters: Wire[] = [
  movie('dune-part-two', 'Dune: Part Two', 2024, 'movies/dune-part-two', 'movies/dune-part-two--bd'),
  show('severance', 'Severance', 'tv/severance', 'tv/severance--bd'),
  episode('the-last-of-us-s1e1', 'When You’re Lost in the Darkness', 'The Last of Us', 'tv/the-last-of-us', 'tv/the-last-of-us--bd'),
  movie('spirited-away', 'Spirited Away', 2001, 'movies/spirited-away', 'movies/spirited-away--bd'),
  movie('interstellar', 'Interstellar', 2014, 'movies/interstellar', 'movies/interstellar--bd'),
  show('silo', 'Silo', 'tv/silo', 'tv/silo--bd'),
  show('attack-on-titan', 'Attack on Titan', 'anime/attack-on-titan', 'anime/attack-on-titan--bd'),
  movie('blade-runner', 'Blade Runner', 1982, 'movies/blade-runner', 'movies/blade-runner--bd'),
  show('slow-horses', 'Slow Horses', 'tv/slow-horses', 'tv/slow-horses--bd'),
  movie('oppenheimer', 'Oppenheimer', 2023, 'movies/oppenheimer', 'movies/oppenheimer--bd'),
  movie('your-name', 'Your Name.', 2016, 'movies/your-name', 'movies/your-name--bd'),
  show('dark-matter', 'Dark Matter', 'tv/dark-matter', 'tv/dark-matter--bd'),
  movie('alien', 'Alien', 1979, 'movies/alien', 'movies/alien--bd'),
  show('ted-lasso', 'Ted Lasso', 'tv/ted-lasso', 'tv/ted-lasso--bd'),
];
const squares: Wire[] = [
  book('project-hail-mary', 'Project Hail Mary (Unabridged)', 'Andy Weir', 'books/project-hail-mary-unabridged'),
  book('the-hobbit', 'The Hobbit', 'J. R. R. Tolkien', 'books/the-hobbit'),
  album('rumours', 'Rumours', 'Fleetwood Mac', 'albums/rumours'),
  album('random-access-memories', 'Random Access Memories', 'Daft Punk', 'albums/random-access-memories'),
];

const filterTotals = {all: 18, unwatched: 11, inProgress: 1};
function contentPage(view: 'watchlist' | 'favorites', shape: 'posters' | 'squares', sort: string, direction: string, filter: string) {
  const entries = shape === 'squares' ? squares : posters;
  return {
    scope: {serverId: 'r46-saved', libraryId: '', libraryKind: 'mixed', view, entityId: '', viewerFence: 'r46-fence'},
    revision: {catalog: 1, viewer: 1},
    heading: {key: view === 'favorites' ? 'saved.favorites' : 'saved.watchlist', fallback: view === 'favorites' ? 'Favorites' : 'My List'},
    navigation: [{id: 'watchlist', labelKey: 'saved.watchlist', view: 'watchlist'}, {id: 'favorites', labelKey: 'saved.favorites', view: 'favorites'}, {id: 'playlists', labelKey: 'saved.playlists', view: 'playlists'}],
    // The saved parser requires the published filter alone. The current server appends ':shape'
    // to this echo; that cross-package mismatch is reported, not reproduced here.
    query: {sort, direction, category: filter, q: '', limit: 40, searchMode: 'none'},
    sorts: [
      {id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc']},
      {id: 'added', labelKey: 'sort.added', directions: ['asc', 'desc']},
      {id: 'updated', labelKey: 'sort.updated', directions: ['asc', 'desc']},
      {id: 'year', labelKey: 'sort.year', directions: ['asc', 'desc']},
      {id: 'duration', labelKey: 'sort.duration', directions: ['asc', 'desc']},
      {id: 'progress', labelKey: 'sort.progress', directions: ['asc', 'desc']},
    ],
    filters: [{id: 'filter', labelKey: 'saved.filter', options: [
      {id: 'all', label: 'All', count: filterTotals.all}, {id: 'unwatched', label: 'Unwatched', count: filterTotals.unwatched}, {id: 'inProgress', label: 'In progress', count: filterTotals.inProgress}, // lint-strings-allow: server-published wire values, not screen copy
    ]}],
    sections: [{id: 'entries', type: 'grid', heading: {key: 'saved.entries', fallback: 'Entries'}, entries, totalCount: entries.length, nextCursor: ''}],
  };
}

const historyEntries = [
  {id: 'h-tlou', updatedAt: '2026-10-24T15:12:00.000Z', positionMs: 1_082_400, completed: false, media: posters[2]},
  {id: 'h-dreams', updatedAt: '2026-10-24T14:40:00.000Z', positionMs: 0, completed: true, media: card('dreams', 'song', 'Dreams', art('albums/rumours', 'albums/rumours'), {artist: {id: 'fleetwood-mac', name: 'Fleetwood Mac'}, album: {id: 'rumours', name: 'Rumours'}, durationMs: 257_000})},
  {id: 'h-rick', updatedAt: '2026-10-24T13:05:00.000Z', positionMs: 768_600, completed: false, media: card('rick-and-morty-s1e2', 'episode', 'Lawnmower Dog', art('tv/rick-and-morty', 'tv/rick-and-morty--bd'), {show: {id: 'rick-and-morty', name: 'Rick and Morty'}, seasonNumber: 1, episodeNumber: 2, durationMs: 1_260_000})},
  {id: 'h-inside-out', updatedAt: '2026-10-23T21:48:00.000Z', positionMs: 600_000, completed: false, media: card('inside-out', 'movie', 'Inside Out', art('movies/inside-out', 'movies/inside-out--bd'), {year: 2015, durationMs: 5_760_000})},
  {id: 'h-martian', updatedAt: '2026-10-23T20:15:00.000Z', positionMs: 3_120_000, completed: false, media: card('the-martian', 'audiobook_file', 'The Martian (Unabridged)', art('books/the-martian-unabridged', 'books/the-martian-unabridged'), {author: 'Andy Weir', durationMs: 36_240_000})},
  {id: 'h-solo', updatedAt: '2026-10-23T19:30:00.000Z', positionMs: 607_000, completed: false, media: card('solo-leveling-s1e1', 'episode', 'I’m Used to It', art('anime/solo-leveling', 'anime/solo-leveling--bd'), {show: {id: 'solo-leveling', name: 'Solo Leveling'}, seasonNumber: 1, episodeNumber: 1, durationMs: 1_380_000})},
  {id: 'h-spirited', updatedAt: '2026-10-19T22:20:00.000Z', positionMs: 0, completed: true, media: posters[3]},
];

const api: SavedApi = {
  async request<T>(path: string): Promise<T> {
    const url = new URL(path, 'http://fixture.invalid');
    if (url.pathname === '/v1/content') {
      const view = url.searchParams.get('view') === 'favorites' ? 'favorites' as const : 'watchlist' as const;
      const shape = url.searchParams.get('shape') === 'squares' ? 'squares' as const : 'posters' as const;
      return contentPage(view, shape, url.searchParams.get('sort') ?? 'title', url.searchParams.get('direction') ?? 'asc', url.searchParams.get('filter') ?? 'all') as unknown as T;
    }
    if (url.pathname === '/v1/personal-history') return {serverId: 'r46-saved', nextCursor: '', viewerFence: 'r46-fence', revision: 1, entries: historyEntries} as unknown as T;
    throw new Error(`Unexpected Saved fixture request: ${url.pathname}`);
  },
};

const images = createArtworkRuntime({
  scopeKey: 'ds-web-saved-r46-preview',
  classify: () => ({retryable: false}),
  load: async ({url}) => {
    const response = await fetch(url);
    if (!response.ok) throw new Error(`Artwork ${response.status}`);
    const blob = await response.blob();
    return {src: URL.createObjectURL(blob), bytes: blob.size};
  },
});
const artwork = {
  foreground: (url: string | undefined, pixels: number, aspect: number) => url ? images.source({url, cacheKey: `${url}|${pixels}|${aspect}`}) : undefined,
  ambient: (url: string | undefined) => url ? images.ambient({url, cacheKey: url, variant: 'ambient-v1', maxEdge: 64, version: 'a'.repeat(64)}) : undefined,
} as unknown as ShellArtwork;

const scope = {serverId: 'r46-saved', authority: 'local' as const, accountId: 'fixture-account', profileId: 'fixture-profile', sessionFamilyId: 'fixture-family'};
const runtime = createSavedRuntime({api, scope, isCurrent: () => true});

function Preview() {
  const search = new URLSearchParams(location.search);
  const [route, setRoute] = useState<SavedScreenRoute>(() => parseSavedScreenRoute(Object.fromEntries(search)));
  const [view, setView] = useState<'grid' | 'list'>(search.get('layout') === 'list' ? 'list' : 'grid');
  const effects: SavedEffects = {
    openEntry() {}, openItem() {}, openMore() {},
    async playEntry() {}, async saveFile() {},
    selectRoute(next) { setRoute(next); },
    reportError(error) { console.error(error); },
  };
  return <SavedScreen runtime={runtime} route={route} online downloadsOffered={false} artwork={artwork} preparationArtwork={() => undefined}
    presentation={{view, cardWidth: 173.5, backdrops: true, reducedMotion: true, setView}} effects={effects} />;
}

// The screen's quiet refresh reads the shell's freshness runtime; this is the
// same provider with a fixture-transport route snapshot and no event feed.
const freshnessApi = {baseUrl: 'http://fixture.invalid', getRouteSnapshot: () => ({phase: 'ready'}), subscribeRoute: () => () => {}} as unknown as HttpLocalApi;
(window as unknown as {__savedFixture: unknown}).__savedFixture = {runtime, api};
createRoot(document.getElementById('root')!).render(<FreshnessProvider api={freshnessApi} viewerId="fixture-viewer" serverId="r46-saved" events={null}><IconSprite /><Preview /></FreshnessProvider>);
