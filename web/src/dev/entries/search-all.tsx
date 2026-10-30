import {createRoot} from 'react-dom/client';
import type {HttpLocalApi} from '@core/index.ts';
import {FreshnessProvider} from '../../platform/freshness';
import {createI18n, deviceRegion, resolveRegion} from '@i18n';
import {setServiceI18n} from '@core/presentation/service-text.ts';
import {SearchScreen} from '../../screens/search/Search';
import {createSearchBindings} from '../../screens/search/bindings';
import {IconSprite} from '../../kit/Icon';
import type {ScreenContext} from '../../screens/detail/context';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// Drawn demo facts exist only in this DEV specimen, never in runtime adapters.
// It mounts the production SearchScreen; every row below is the frame's own public demo state.
if (!import.meta.env.DEV) throw new Error('Search specimens require development.');

const params = new URLSearchParams(location.search);
if (params.get('clock') === 'fixed') {
  const fixed = Date.parse('2026-10-24T19:05:00Z'), RealDate = Date;
  const Construct = RealDate as unknown as new (...args: unknown[]) => Date;
  const FakeDate = function (...args: unknown[]) { return args.length ? new Construct(...args) : new Construct(fixed); } as unknown as DateConstructor;
  FakeDate.now = () => fixed;
  FakeDate.parse = RealDate.parse.bind(RealDate);
  FakeDate.UTC = RealDate.UTC.bind(RealDate);
  window.Date = FakeDate;
}
if (params.get('capture') === '1') {
  const style = document.createElement('style');
  style.textContent = '*,*::before,*::after{animation:none!important;transition:none!important}';
  document.head.append(style);
}

// The shell confirms the viewer's catalogue and device zone before a screen reads.
setServiceI18n(createI18n(resolveRegion(undefined, deviceRegion())));
const serverId = 'specimen-server';
const libraryId = 'library';
const fence = 'specimen-fence';
const artUrl = (id: string) => `/v1/metadata/item/${id}/art/poster?v=${'a'.repeat(64)}&w=400`;
const media = (id: string, kind: string, title: string, view: string, extra: Record<string, unknown> = {}) =>
  ({id, libraryId, kind, title, posterUrl: artUrl(id), navigation: {view, entityId: id}, ...extra});

// desktop-search.html#s-all: "ma", every type, at 4:05 PM on 24 Oct 2026.
const movies = [
  media('mayday', 'movie', 'Mayday', 'item', {year: 2026, durationMs: 6_780_000}), // lint-strings-allow
  media('the-matrix', 'movie', 'The Matrix', 'item', {year: 1999, durationMs: 8_160_000}), // lint-strings-allow
  media('practical-magic-2', 'movie', 'Practical Magic 2', 'item', {year: 2026}), // lint-strings-allow
  media('project-hail-mary', 'movie', 'Project Hail Mary', 'item', {year: 2026}), // lint-strings-allow
  media('spider-man-into', 'movie', 'Spider-Man: Into the Spider-Verse', 'item', {year: 2018}), // lint-strings-allow
  media('spider-man-across', 'movie', 'Spider-Man: Across the Spider-Verse', 'item', {year: 2023}), // lint-strings-allow
  media('spider-man-brand-new-day', 'movie', 'Spider-Man: Brand New Day', 'item', {year: 2026}), // lint-strings-allow
];
const shows = [
  media('dark-matter', 'show', 'Dark Matter', 'show', {year: 2024, seasonCount: 1}), // lint-strings-allow
  media('the-mandalorian', 'show', 'The Mandalorian', 'show', {year: 2019, seasonCount: 1}), // lint-strings-allow
  media('stranger-things', 'show', 'Stranger Things', 'show', {year: 2016, seasonCount: 1}), // lint-strings-allow
  media('the-last-of-us', 'show', 'The Last of Us', 'show', {year: 2023, seasonCount: 1}), // lint-strings-allow
  media('solo-leveling', 'show', 'Solo Leveling', 'show', {year: 2024, seasonCount: 1}), // lint-strings-allow
];
const episode = (id: string, show: Readonly<{id: string; name: string}>, title: string, season: number, number: number, overview: string, minutes: number) =>
  ({...media(id, 'episode', title, 'item', {show, seasonNumber: season, episodeNumber: number, overview, durationMs: minutes * 60_000,
    backdropUrl: `/v1/metadata/item/${id}/art/backdrop?v=${'a'.repeat(64)}&w=1920`}), available: true}); // lint-strings-allow
const episodes = [
  episode('silo-s1e3', {id: 'silo', name: 'Silo'}, 'Machines', 1, 3, 'In her hunt for a new sheriff, Mayor Jahns clashes with Bernard. Juliette strikes a deal to keep the generator running.', 49), // lint-strings-allow
  episode('mandalorian-s1e1', {id: 'the-mandalorian', name: 'The Mandalorian'}, 'Chapter 1: The Mandalorian', 1, 1, 'A Mandalorian bounty hunter tracks a target for a well-paying client.', 39), // lint-strings-allow
  episode('stranger-things-s1e2', {id: 'stranger-things', name: 'Stranger Things'}, 'Chapter Two: The Weirdo on Maple Street', 1, 2, 'Lucas, Mike and Dustin try to talk to the girl they found in the woods. Hopper questions an anxious Joyce about an unsettling phone call.', 55), // lint-strings-allow
  episode('mandalorian-s1e2', {id: 'the-mandalorian', name: 'The Mandalorian'}, 'Chapter 2: The Child', 1, 2, 'Target in-hand, the Mandalorian must now contend with scavengers.', 32), // lint-strings-allow
];
const people = [
  {id: 'makoto-furukawa', kind: 'person', title: 'Makoto Furukawa', subtitle: 'Solo Leveling', navigation: {view: 'person', entityId: 'makoto-furukawa'}}, // lint-strings-allow
  {id: 'gaten-matarazzo', kind: 'person', title: 'Gaten Matarazzo', subtitle: 'Stranger Things', navigation: {view: 'person', entityId: 'gaten-matarazzo'}}, // lint-strings-allow
  {id: 'young-mazino', kind: 'person', title: 'Young Mazino', subtitle: 'The Last of Us', navigation: {view: 'person', entityId: 'young-mazino'}}, // lint-strings-allow
];
const artists = [media('fleetwood-mac', 'artist', 'Fleetwood Mac', 'artist', {albumCount: 1, count: 1})]; // lint-strings-allow
const albums = [media('rumours', 'album', 'Rumours', 'album', {year: 1977, artist: {id: 'fleetwood-mac', name: 'Fleetwood Mac'}})]; // lint-strings-allow
const song = (id: string, title: string, albumId: string, artistId: string, album: string, artist: string, seconds: number) =>
  ({...media(id, 'song', title, 'item', {durationMs: seconds * 1000, artist: {id: artistId, name: artist}, album: {id: albumId, name: album},
    playback: {itemId: id}, available: true})}); // lint-strings-allow
const songs = [
  song('maxwells-silver-hammer', "Maxwell's Silver Hammer (2019 Mix)", 'abbey-road-2019-mix', 'the-beatles', 'Abbey Road (2019 Mix)', 'The Beatles', 207), // lint-strings-allow
  song('second-hand-news', 'Second Hand News', 'rumours', 'fleetwood-mac', 'Rumours', 'Fleetwood Mac', 176), // lint-strings-allow
  song('dreams', 'Dreams', 'rumours', 'fleetwood-mac', 'Rumours', 'Fleetwood Mac', 254), // lint-strings-allow
  song('never-going-back-again', 'Never Going Back Again', 'rumours', 'fleetwood-mac', 'Rumours', 'Fleetwood Mac', 134), // lint-strings-allow
];
const books = [
  media('the-martian-unabridged', 'book', 'The Martian (Unabridged)', 'book', {author: 'Andy Weir'}), // lint-strings-allow
  media('project-hail-mary-unabridged', 'book', 'Project Hail Mary (Unabridged)', 'book', {author: 'Andy Weir'}), // lint-strings-allow
];
const channel = (id: string, name: string, number: string) => ({id, kind: 'channel', title: name, subtitle: number + ' · Drama', // lint-strings-allow
  navigation: {view: 'channel', entityId: id, sourceId: 'demo-source', channelId: id, provenance: 'live-source'}});
const channels = [
  channel('ember-drama', 'Ember Drama', '802'), // lint-strings-allow
  channel('northwind-news', 'Northwind News', '101'), // lint-strings-allow
  channel('summit-sports', 'Summit Sports', '201'), // lint-strings-allow
];

type GroupFixture = Readonly<{id: string; entityKind: string; items: readonly unknown[]; totalCount: number; nextCursor?: string}>;
const fixtures: readonly GroupFixture[] = [
  {id: 'movies', entityKind: 'movie', items: movies, totalCount: 7},
  {id: 'shows', entityKind: 'show', items: shows, totalCount: 5},
  {id: 'episodes', entityKind: 'episode', items: episodes, totalCount: 10, nextCursor: 'episode-more'},
  {id: 'people', entityKind: 'person', items: people, totalCount: 3},
  {id: 'artists', entityKind: 'artist', items: artists, totalCount: 1},
  {id: 'albums', entityKind: 'album', items: albums, totalCount: 1},
  {id: 'songs', entityKind: 'song', items: songs, totalCount: 5, nextCursor: 'song-more'},
  {id: 'books', entityKind: 'book', items: books, totalCount: 2},
  {id: 'live-tv', entityKind: 'channel', items: channels, totalCount: 3},
];

function searchFixture(url: URL): unknown {
  const group = url.searchParams.get('group') ?? '';
  const groups = group ? [group] : (url.searchParams.get('groups') ?? '').split(',').filter(Boolean);
  return {scope: {serverId, libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: fence},
    revision: {catalog: 4, viewer: 2}, heading: {key: 'search.title', fallback: 'Search'},
    query: {q: url.searchParams.get('q') ?? '', sort: url.searchParams.get('sort') ?? 'relevance', direction: url.searchParams.get('direction') ?? 'desc',
      group, groups, libraryIds: [], limit: Number(url.searchParams.get('limit')), searchMode: 'normalized_prefix', recorded: false},
    groups: fixtures.filter(fixture => groups.includes(fixture.id)).map(fixture => ({id: fixture.id, entityKind: fixture.entityKind,
      title: fixture.id, status: 'success', errorCode: '', items: fixture.items, totalCount: fixture.totalCount, totalCountExact: true,
      hasMore: !!fixture.nextCursor, nextCursor: fixture.nextCursor ?? ''})),
    capabilities: {groups: fixtures.map(fixture => ({id: fixture.id, entityKind: fixture.entityKind, title: fixture.id, available: true, sorts: ['relevance', 'title']})),
      sorts: ['relevance', 'title'], directions: ['asc', 'desc'], maxLimit: 40, groupBudgetMs: 600}};
}

type ChannelFixture = Readonly<{id: string; name: string; number: string; programme: string; subtitle?: string; start: string; end: string; live?: boolean; mark: {text: string; seed: string}}>;
const guideChannels: readonly ChannelFixture[] = [
  {id: 'ember-drama', name: 'Ember Drama', number: '802', programme: 'The Night Shift', subtitle: 'Fault Lines', mark: {text: 'ED', seed: 'demo:ember-drama'}, // lint-strings-allow
    start: '2026-10-24T18:00:00Z', end: '2026-10-24T19:15:00Z'},
  {id: 'northwind-news', name: 'Northwind News', number: '101', programme: 'Market Watch', mark: {text: 'NN', seed: 'demo:northwind-news'}, // lint-strings-allow
    start: '2026-10-24T19:00:00Z', end: '2026-10-24T19:30:00Z'},
  {id: 'summit-sports', name: 'Summit Sports', number: '201', programme: 'Match Highlights', live: true, mark: {text: 'SS', seed: 'demo:summit-sports'}, // lint-strings-allow
    start: '2026-10-24T19:00:00Z', end: '2026-10-24T19:30:00Z'},
];
const source = {id: 'demo-source', name: 'Synthetic Live TV', generation: 'g1', publishedAt: '2026-10-24T12:00:00Z', // lint-strings-allow
  availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: 'ready'};
const guideChannel = (channel: ChannelFixture) => ({id: channel.id, sourceId: source.id, provenance: 'live-source', name: channel.name,
  number: channel.number, group: 'Drama', generation: source.generation, preferenceRevision: 0, tuneAvailable: true, recordAvailable: true, // lint-strings-allow
  favorite: false, hidden: false, mark: channel.mark, programmes: [{id: 'programme-' + channel.id, channelId: channel.id, title: channel.programme,
    ...(channel.subtitle ? {subtitle: channel.subtitle} : {}), start: channel.start, end: channel.end, ...(channel.live ? {flags: {live: true}} : {})}]});

function guideFixture(url: URL): unknown {
  const ids = (url.searchParams.get('channels') ?? '').split(',').filter(Boolean);
  return {protocolVersion: '1.0', serverId, guide: {state: 'ready', viewerFence: fence, start: url.searchParams.get('start'),
    end: url.searchParams.get('end'), timezone: url.searchParams.get('timezone'), observedAt: url.searchParams.get('start'), nextCursor: '',
    sources: [source], channels: guideChannels.filter(channel => ids.includes(channel.id)).map(guideChannel)}};
}

async function fixture(path: string): Promise<unknown> {
  const url = new URL(path, 'https://fixture.invalid');
  if (url.pathname === '/v1/search') return searchFixture(url);
  if (url.pathname === '/v1/guide') return guideFixture(url);
  if (url.pathname === '/v1/search/history') return {serverId, viewerFence: fence, maxEntries: 20, entries: []};
  throw new Error('No specimen fixture for ' + url.pathname);
}

const effective = {'search.rememberHistory': false, 'appearance.cardSizePercent': 100, 'appearance.showBackdrops': true};
const preferenceSnapshot = {effective} as never;
const preferences = {getSnapshot: () => preferenceSnapshot, subscribe: () => () => {}};
const api = {
  request: async <T,>(path: string) => await fixture(path) as T,
  requestRaw: async () => { throw new Error('No specimen transport.'); },
  getRouteSnapshot: () => ({phase: 'ready', origin: 'https://fixture.invalid', revision: 1, network: 'online', message: '', discovery: 'available'}),
  subscribeRoute: () => () => {},
};
const context = {
  api: api as unknown as HttpLocalApi, events: null, scope: {serverId, viewerId: '["local","account","profile"]'},
  isCurrent: () => true, identity: {liveAvailable: true} as never, preferences,
  // No fabricated paint: the kit's own empty art slot is the specimen's neutral picture.
  artwork: {foreground: () => undefined, ambient: () => undefined} as never,
  effects: {openEntry: () => {}, openSettings: () => {}, openBrowse: () => {}, play: async () => {}, reportError: () => {}},
} as unknown as ScreenContext;
const binding = createSearchBindings(context);

function Specimen() {
  return <><IconSprite /><FreshnessProvider api={api as unknown as HttpLocalApi} viewerId="profile" serverId={serverId} events={null}>
    <SearchScreen binding={binding} linkedQuery="ma" onConsumeLinkedQuery={() => {}} />
  </FreshnessProvider></>;
}
createRoot(document.getElementById('root')!).render(<Specimen />);
