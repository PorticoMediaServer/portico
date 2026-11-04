import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {channelTint, programmeTimeRange} from '../../../packages/client-core/src/presentation/kit-model';
import {SEARCH_CHIPS, orderedSearchGroups, searchGroupLabel, topSearchResult, topResultKindLabel} from '../../../packages/client-core/src/presentation/search-model';
import {nowNext} from '../../../packages/client-core/src/guide/now-next';
import {serverNow} from '../../../packages/client-core/src/server-clock';
import {browseType} from '../src-next/kit/browse/presentation';
import {createTheme} from '../src-next/kit/theme';
import {homeCardText} from '../src-next/screens/home/model';
import {NativeSearchOnNow} from '../src-next/screens/search/on-now';
import {nativeSearchGenreTiles} from '../src-next/screens/search/genre-tiles';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const text = (node: any): string => Array.isArray(node) ? node.map(text).join('') : typeof node === 'string' ? node : node?.props ? text(node.props.children) : '';
const idle = {generation: 1, scope: {}, query: null, phase: 'idle', data: null, groups: [], pagination: {cursor: null, canPrevious: false}, error: null};

const now = serverNow();
const programme = (id: string, title: string) => ({id, channelId: 'ch1', title, start: now - 20 * 60_000, end: now + 40 * 60_000, subtitle: 'Evening edition', flags: {live: true}});
const card = (id: string, sourceId: string) => ({channel: {sourceId, id, name: `Channel ${id}`, number: '4', mark: {text: 'C', seed: `${sourceId}:${id}`}}, programmes: [programme(id, `News ${id}`)]});
const readyOnNow = {phase: 'ready', cards: [card('a', 'tuner'), card('b', 'tuner'), card('c', 'm3u')], at: now};

async function mountSearch({hasLiveTV, onNow, typed = false}: {hasLiveTV: boolean; onNow?: any; typed?: boolean}) {
  const host = hooks();
  (host.react as any).useId = () => 'search-p3m';
  const theme = createTheme({width: 390, isTV: false});
  const destinations: unknown[] = [];
  const view = await screenModule(new URL('../src-next/screens/search/SearchScreen.tsx', import.meta.url), {'../../../../../packages/client-core/src/timer': await import('../../../packages/client-core/src/timer.ts'), 
    react: host.react,
    'react-native': {FlatList: 'FlatList', Keyboard: {dismiss: () => {}}, ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34, left: 0, right: 0})},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/presentation/search-model': {orderedSearchGroups, SEARCH_CHIPS, searchGroupLabel, topSearchResult, topResultKindLabel},
    '../../../../../packages/client-core/src/guide/now-next': {nowNext}, '../../../../../packages/client-core/src/server-clock': {serverNow},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/cards': {GenreTile: 'GenreTile', LiveCard: 'LiveCard'}, '../../kit/cards/primitives': {CardFrame: 'CardFrame'},
    '../../kit/controls': {Chip: 'Chip', Focusable: 'Focusable', TextField: 'TextField'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'},
    '../../kit/overlays': {Banner: 'Banner'}, '../../kit/states': {GridSkeleton: 'GridSkeleton', StateView: 'StateView'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': {browseType}, '../../kit/theme': {useTheme: () => theme},
    '../home/model': {homeCardText, nativeSearchCardText: () => ({})}, './model': {NativeSearchDestination: class {}, nativeSearchEntryKey: (_group: string, entry: any) => entry.id},
    './genre-tiles': {nativeSearchGenreTiles}, './SearchGridCard': {SearchGridCard: 'SearchGridCard'}, './TVSearchScreen': {TVSearchScreen: 'TVSearchScreen'},
    '../../../../../packages/client-core/src/presentation/kit-model': {channelTint, programmeTimeRange},
  });
  const snapshot = {text: typed ? 'matrix' : '', group: 'all', search: idle, groups: [], clearing: false, recent: [], paging: {}, problem: undefined};
  const loads: string[] = [];
  const tree = host.render(() => view.SearchScreen({
    destination: {subscribe: () => () => {}, getSnapshot: () => snapshot, refreshHistory: async () => {}, clearHistory: async () => {}, setText: () => {},
      selectGroup: () => {}, commit: async () => {}, useRecent: () => {}, next: async () => {}, retry: async () => {}},
    runtime: {artwork: () => undefined, text: defaultI18n, reportError: () => {}, error: () => ({title: 'error'}), registerScrollToTop: () => () => {},
      subscribe: () => () => {}, getSnapshot: () => ({hasLiveTV}), onDestination: (destination: unknown) => destinations.push(destination)},
    explore: {subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready', genres: [], backdropGenres: [], initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'}), load: async () => {}, openGenre: () => {}},
    ...(onNow ? {onNow: {subscribe: () => () => {}, getSnapshot: () => onNow, load: async () => {loads.push('load');}}} : {}),
    actions: {open: () => {}, play: () => {}, menu: () => {}}, activation: {subscribe: () => () => {}, getSnapshot: () => ({visible: true, intent: 'enter', revision: 1})},
    tvCopy: {preferToTalk: 'next.tv.preferToTalk', voiceHint: 'next.tv.voiceHint'},
  }));
  const paint = (node: any) => nodes(host.render(() => node.type(node.props)));
  const section = nodes(tree).find(node => typeof node.type === 'function' && node.props.snapshot);
  return {tree, destinations, loads, paint, section};
}

test('empty Search on a server with Live TV draws On now, a Guide link and the live cards that opens Live TV', async () => {
  const page = await mountSearch({hasLiveTV: true, onNow: readyOnNow});
  assert.ok(page.section, 'the On now section is drawn under the empty page');
  const body = page.paint(page.section);
  assert.ok(body.some(node => node.props.testID === 'search-on-now'));
  const heading = body.find(node => typeof node.type === 'function' && node.props.title === defaultI18n.t('next.home.onNow'));
  assert.ok(heading, 'heading "On now"');
  assert.equal(heading.props.action, defaultI18n.t('next.controls.guide'), 'Guide action');
  heading.props.onAction();
  assert.deepEqual(page.destinations, [{kind: 'live'}], 'Guide opens Live TV');
  assert.equal(body.some(node => node.type === 'Text'), false, 'phone-app #8 draws no sources line under the heading');
  const cards = body.filter(node => typeof node.type === 'function' && node.props.card);
  assert.equal(cards.length, 3, 'one card per channel');
  const shown = page.paint(cards[0]!).find(node => node.type === 'LiveCard');
  assert.equal(shown.props.testID, 'search-on-now-a');
  assert.equal(shown.props.title, 'News a', 'the programme on now');
  assert.equal(shown.props.channel, 'Channel a');
  assert.ok(shown.props.progress > .3 && shown.props.progress < .4, 'twenty of sixty minutes in');
  assert.deepEqual(shown.props.flags.map((flag: any) => flag.id), ['live'], 'the Live tag');
  assert.match(shown.props.range, /^Ends (?:[A-Za-z]+ \d{1,2}, \d{4}, )?\d{1,2}:\d{2}/, 'Ends h:mm, with the date when the programme crosses midnight');
  assert.equal(shown.props.number, undefined, 'no channel number');
  assert.equal(shown.props.caption, undefined, 'no remaining-time line');
  shown.props.onNavigate();
  assert.deepEqual(page.destinations.at(-1), {kind: 'live'}, 'a card opens Live TV');
});

test('the On now row is absent without Live TV, while typing, and when the read left nothing', async () => {
  assert.equal((await mountSearch({hasLiveTV: false, onNow: readyOnNow})).section, undefined, 'no Live TV on the server');
  assert.equal((await mountSearch({hasLiveTV: true, onNow: readyOnNow, typed: true})).section, undefined, 'only the empty page');
  assert.equal((await mountSearch({hasLiveTV: true, onNow: {...readyOnNow, cards: []}})).section, undefined, 'no channels');
  assert.equal((await mountSearch({hasLiveTV: true, onNow: {...readyOnNow, phase: 'hidden', cards: []}})).section, undefined, 'a refused read shows nothing');
  assert.equal((await mountSearch({hasLiveTV: true})).section, undefined, 'a screen without the port (TV) draws none');
});

test('the On now read is bounded to one favourites page and hides on failure', async () => {
  const requests: string[] = [];
  const failing = new NativeSearchOnNow({serverId: 's', timezone: 'UTC', current: () => true,
    api: {request: async (path: string) => {requests.push(path); throw Object.assign(new Error('refused'), {code: 'access_refused'});}} as any});
  const phases: string[] = [];
  failing.subscribe(() => phases.push(failing.getSnapshot().phase));
  await failing.load();
  assert.deepEqual(phases, ['loading', 'hidden']);
  assert.equal(failing.getSnapshot().cards.length, 0);
  assert.equal(requests.length, 1, 'one directory read, no continuation');
  const retired = new NativeSearchOnNow({serverId: 's', timezone: 'UTC', current: () => false, api: {request: async () => {throw new Error('never read');}} as any});
  await retired.load();
  assert.equal(retired.getSnapshot().phase, 'idle', 'a stale screen reads nothing');
  failing.dispose();
});

// #728 review: the programmes are read for one moment, so a card would show a finished programme (then "No information")
// once it ends. The row is read again once the clock passes the first shown programme's end, and not before.
test('the On now row is read again once a programme it shows has ended, keeping its cards meanwhile', async () => {
  const reads: number[] = [];
  let ends = serverNow() + 80;
  const source = {
    channels: async () => ({items: [{id: 'ch1', sourceId: 'tuner', name: 'Channel 1'}], total: 1, revision: 'r'}),
    programs: async (_ids: readonly string[], start: number) => {reads.push(start); return {ch1: [{id: `p${reads.length}`, channelId: 'ch1', title: `Show ${reads.length}`, start: start - 60_000, end: ends}]};},
  } as any;
  const row = new NativeSearchOnNow({serverId: 's', timezone: 'UTC', current: () => true, api: {} as any, source});
  await row.load();
  assert.equal(row.getSnapshot().until, ends, 'until the programme on now ends');
  await row.load();
  assert.equal(reads.length, 1, 'still current: not read again');
  const before = row.getSnapshot().cards, phases: string[] = [];
  row.subscribe(() => phases.push(row.getSnapshot().phase));
  await new Promise(resolve => setTimeout(resolve, 120));
  ends = serverNow() + 60_000;
  await row.load();
  assert.equal(reads.length, 2, 'past its end: read again');
  assert.deepEqual(phases, ['ready'], 'no loading state in between: the old cards stay until the answer');
  assert.notEqual(row.getSnapshot().cards, before);
  assert.equal(row.getSnapshot().cards[0]!.programmes[0]!.title, 'Show 2');
  assert.equal(row.getSnapshot().until, ends);
  row.dispose();
});

test('until the re-read answers, a card whose programme has ended keeps that programme, at its end, never "No information"', async () => {
  const ended = {id: 'old', channelId: 'a', title: 'Late news', start: now - 60 * 60_000, end: now - 1_000, flags: {live: true}};
  const stale = {phase: 'ready', cards: [{...card('a', 'tuner'), programmes: [ended]}], at: now - 30 * 60_000, until: ended.end};
  const page = await mountSearch({hasLiveTV: true, onNow: stale});
  const body = page.paint(page.section);
  const shown = page.paint(body.find(node => typeof node.type === 'function' && node.props.card)!).find(node => node.type === 'LiveCard');
  assert.equal(shown.props.title, 'Late news');
  assert.ok(shown.props.progress > .99, 'at its end');
});
