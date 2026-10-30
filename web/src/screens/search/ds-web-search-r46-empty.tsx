import {createRoot} from 'react-dom/client';
import type {HttpLocalApi} from '@core/index.ts';
import {FreshnessProvider} from '../../platform/freshness';
import {SearchScreen} from './Search';
import {createSearchBindings} from './bindings';
import {IconSprite} from '../../kit/Icon';
import type {ScreenContext} from '../detail/context';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// Drawn demo facts exist only in this DEV specimen, never in runtime adapters.
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

const serverId = 'specimen-server';
const artPath = (id: string) => `/v1/metadata/item/${id}/art/backdrop?v=${'a'.repeat(64)}&w=1920`;
const genre = (value: string, label: string, count: number) => ({value, label, count,
  backdrop: {kind: 'item', id: value, path: artPath(value)}, covers: []});
const genres = [
  genre('animation', 'Animation', 14), genre('science-fiction', 'Science fiction', 16), genre('drama', 'Drama', 31), // lint-strings-allow
  genre('crime', 'Crime', 9), genre('fantasy', 'Fantasy', 8), genre('horror', 'Horror', 6), // lint-strings-allow
  genre('comedy', 'Comedy', 12), genre('adventure', 'Adventure', 18), // lint-strings-allow
];
const history = ['the last of us', 'spirited away', 'pedro pascal', 'fleetwood mac', 'hail mary', 'toy story', 'severance', 'dune']; // lint-strings-allow
type ChannelFixture = {id: string; name: string; number: string; title: string; start: string; end: string; subtitle?: string; live?: boolean; mark: {text: string; seed: string}};
const channels: readonly ChannelFixture[] = [
  {id: 'northwind', name: 'Northwind News', number: '101', title: 'World Tonight', mark: {text: 'NN', seed: 'northwind'}, start: '2026-10-24T18:00:00Z', end: '2026-10-24T19:30:00Z'}, // lint-strings-allow
  {id: 'summit', name: 'Summit Sports', number: '201', title: 'Cycling: Mountain Stage', live: true, mark: {text: 'SS', seed: 'summit'}, start: '2026-10-24T18:00:00Z', end: '2026-10-24T21:30:00Z'}, // lint-strings-allow
  {id: 'pebble', name: 'Pebble Kids', number: '401', title: 'Story Time Island', subtitle: 'Homecoming', mark: {text: 'PK', seed: 'pebble'}, start: '2026-10-24T18:00:00Z', end: '2026-10-24T19:30:00Z'}, // lint-strings-allow
  {id: 'atlas', name: 'Atlas Documentaries', number: '501', title: 'How Bridges Stand', subtitle: 'Fault Lines', mark: {text: 'AD', seed: 'atlas'}, start: '2026-10-24T18:00:00Z', end: '2026-10-24T19:15:00Z'}, // lint-strings-allow
];
const source = {id: 'synthetic', name: 'Synthetic Live TV', generation: 'g1', publishedAt: '2026-10-24T12:00:00Z', // lint-strings-allow
  availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: 'ready'};
const guideChannel = (channel: ChannelFixture, programmes: readonly unknown[]) => ({id: channel.id, sourceId: source.id,
  provenance: 'live-source', name: channel.name, number: channel.number, group: 'Synthetic', generation: source.generation, // lint-strings-allow
  preferenceRevision: 0, programmes, tuneAvailable: true, recordAvailable: true, favorite: false, hidden: false, mark: channel.mark});
const programme = (channel: ChannelFixture) => ({id: 'programme-' + channel.id, channelId: channel.id, title: channel.title,
  subtitle: channel.subtitle, start: channel.start, end: channel.end, flags: channel.live ? {live: true} : undefined});

async function fixture(path: string): Promise<unknown> {
  const url = new URL(path, 'https://fixture.invalid');
  if (url.pathname === '/v1/search/history') return {serverId, viewerFence: 'fence', maxEntries: 20,
    entries: history.map((q, index) => ({q, updatedAt: new Date(Date.parse('2026-10-24T12:00:00Z') - index * 60000).toISOString(), uses: 1}))};
  if (url.pathname === '/v1/search/genres') return {revision: 'genre-revision', genres};
  if (url.pathname === '/v1/guide/channels') return {protocolVersion: '1.0', serverId,
    directory: {viewerFence: 'fence', revision: 'directory-revision', total: channels.length, offset: 0,
      limit: Number(url.searchParams.get('limit')), sources: [source], channels: channels.map(channel => guideChannel(channel, []))}};
  if (url.pathname === '/v1/guide/sources') return {protocolVersion: '1.0', serverId, viewerFence: 'fence', revision: 'sources-revision',
    sources: [{...source, position: 0, recordAvailable: false, guideDays: 7, channelCount: channels.length, favoriteCount: 0, groups: [], groupCounts: [], unavailableReasons: {}}]};
  if (url.pathname === '/v1/guide') {
    const start = url.searchParams.get('start')!, end = url.searchParams.get('end')!, ids = (url.searchParams.get('channels') ?? '').split(',');
    return {protocolVersion: '1.0', serverId, guide: {state: 'ready', viewerFence: 'fence', revision: 'guide-revision', start, end,
      timezone: url.searchParams.get('timezone'), observedAt: start, nextCursor: '', sources: [source],
      channels: channels.filter(channel => ids.includes(channel.id)).map(channel => guideChannel(channel, [programme(channel)]))}};
  }
  throw new Error('No specimen fixture for ' + url.pathname);
}

const effective = {'search.rememberHistory': true, 'appearance.cardSizePercent': 100, 'appearance.showBackdrops': true};
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
    <SearchScreen binding={binding} onOpenLive={() => {}} />
  </FreshnessProvider></>;
}
createRoot(document.getElementById('root')!).render(<Specimen />);
