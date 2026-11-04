import assert from 'node:assert/strict';
import test from 'node:test';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {liveTabsFixture, ownerTagFixture} from './helpers/live-fixtures.mjs';
import {defaultI18n} from '../../../packages/i18n/src';
import * as dvr from '../../../packages/client-core/src/dvr';
import * as channels from '../../../packages/client-core/src/channel-guide';
import * as guide from '../../../packages/client-core/src/guide';
import * as directory from '../../../packages/client-core/src/guide/directory';
import * as kitModel from '../../../packages/client-core/src/presentation/kit-model';
import * as availability from '../../../packages/client-core/src/presentation/live-availability';
import * as guidePresentation from '../src-next/screens/settings-live/guide-presentation';
import * as guidePan from '../src-next/screens/settings-live/guide-pan-model';
import * as rules from '../src-next/screens/settings-live/recording-rule-presentation';
import * as shellModel from '../src-next/shell/model';
import {tvFocusIds} from '../src-next/shell/tv-focus';
import {createTheme} from '../src-next/kit/theme';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';
import {nativeServerAdmission} from '../src-next/screens/settings-live/server/tv-server-reader';
import * as tvSettingsFocus from '../src-next/screens/settings-live/tv-settings-focus';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree ? [tree, ...nodes(tree.children)] : [];
const textOf = (tree: any): string => nodes(tree).filter(node => node.type === 'RCTRawText').map(node => node.props.text).join('');
const noop = () => {}, subscribe = () => noop;
const jsx = (type: any, props: any, ...children: any[]) => React.createElement(type, props, ...children);

const now = Date.now();
const iso = (offsetMinutes: number) => new Date(now + offsetMinutes * 60_000).toISOString();
const rawChannel = (over: Record<string, unknown> = {}): any => ({id: 'c1', sourceId: 'src-a', provenance: 'live-source', name: 'Summit Sports', number: '12', group: 'Sports', generation: 'g1', programmes: [],
  tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', guide: 'full', favorite: false, hidden: false, preferenceRevision: 1, ...over});
const rawProgramme = (id: string, startMin: number, endMin: number, over: Record<string, unknown> = {}): any => ({id, channelId: 'c1', title: `Programme ${id}`, start: iso(startMin), end: iso(endMin),
  lineage: 'provider-id', seriesId: '', episodeId: '', newEvidence: 'unknown', description: '', ...over});
const airing = (programme: any) => ({channel: {id: 'c1', sourceId: 'src-a', provenance: 'live-source', generation: 'g1', name: 'Summit Sports', number: '12', group: 'Sports'}, programme});
const source = (id: string, over: Record<string, unknown> = {}) => ({id, name: `Source ${id}`, generation: 'one', publishedAt: '2026-10-26T00:00:00Z', availableStart: '2026-10-26T00:00:00Z', availableEnd: '2026-10-28T00:00:00Z',
  provenance: 'live-source', refreshState: 'ready', position: 0, recordAvailable: true, guideDays: 2, channelCount: 0, favoriteCount: 0, groups: [], groupCounts: [], unavailableReasons: {}, ...over});
const twoSources = [
  source('src-a', {channelCount: 10, favoriteCount: 2, groups: ['News', 'Sports'], groupCounts: [{name: 'News', count: 2}, {name: 'Sports', count: 8}]}),
  source('src-b', {position: 1, channelCount: 4, favoriteCount: 1, groups: ['News'], groupCounts: [{name: 'News', count: 1}]}),
];

type Options = {isTV?: boolean; width?: number; height?: number; role?: 'owner' | 'member'; sources?: any[]; filteredTotal?: number; hangChannels?: boolean;
  later?: any[]; scheduleFails?: boolean};
async function fixture({isTV = false, width, height, role = 'owner', sources = twoSources, filteredTotal = 0, hangChannels = false, later = [], scheduleFails = false}: Options = {}) {
  const host = await fabricHost(), listeners = new Set<() => void>(), editors: string[] = [], scheduled: any[] = [];
  const retirement = new AbortController();
  const theme = createTheme({isTV, width: width ?? (isTV ? 1920 : 400)});
  const authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: 'one', viewerFence: 'fence'};
  const route: any = {revision: 1, route: {kind: 'main'}, destination: {kind: 'live'}};
  const shell: any = {authority, destination: route.destination, contentVisible: true, mediaExpanded: false, serverName: 'Portico demo'};
  const listen = (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};};
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: listen});
  const api = {request: async <T,>(path: string) => {
    if (hangChannels && path.includes('channels')) return new Promise<T>(() => {});
    const filtered = path.includes('group=') || path.includes('favorites=true');
    const query = new URLSearchParams(path.split('?')[1] ?? ''), total = filtered ? filteredTotal : 14;
    return {protocolVersion: '1.0', serverId: 'server', viewerFence: 'guide', revision: 'one', sources,
      directory: {viewerFence: 'guide', revision: 'one', total, offset: Number(query.get('offset') ?? 0), limit: Number(query.get('limit') ?? 50), channels: [], sources}} as T;
  }};
  const controller = new NativeLiveController(lifetime, api, 'America/Halifax'); await controller.loadSources();
  const account: DirectSnapshot = {authority: 'local', serverId: 'server', canManage: role === 'owner',
    capabilities: {manageAccount: role === 'owner', managePeople: role === 'owner', manageServer: role === 'owner', manageSignIn: role === 'owner'},
    account: {id: 'owner', username: 'Owner', role, primaryProfileId: 'main', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Main', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 0, pinRequired: false}]};
  const context: any = {api: {baseUrl: 'https://server.example/'}, account, authority: () => authority, current: () => true,
    navigation: {subscribe: listen, getSnapshot: () => route, isCurrent: () => true}};
  const runtime: any = {retirementSignal: retirement.signal, subscribe: listen, getSnapshot: () => shell, text: {t: defaultI18n.t, locale: 'en-US'},
    error: (_: unknown, retry: () => void) => ({title: 'Unavailable', primaryAction: {id: 'retry', label: 'Retry', onPress: retry}}), reportError: (error: unknown) => {throw error;}, onDestination: noop};
  const native = {View: 'View', Text: 'Text', ScrollView: 'ScrollView', VirtualizedList: 'VirtualizedList', Platform: {OS: 'android'},
    FlatList: ({data, renderItem, ListHeaderComponent}: any) => jsx('FlatList', null, ListHeaderComponent, ...(data ?? []).map((item: unknown, index: number) => jsx(React.Fragment, {key: index}, renderItem({item})))),
    useWindowDimensions: () => ({width: theme.width, height: height ?? 1080})};
  const controls = {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable', Segmented: 'Segmented', Select: 'Select'};
  const focus = {FocusRegion: 'Region', FocusItem: 'FocusItem', useCovered: () => false, useFocusRegistry: () => ({subscribe, cellPosition: () => undefined, attachedFocus: () => undefined, focus: () => true})};
  class FakeFeatures {
    dispose() {}
    async reminders() {return {items: []};}
    async airings() {return {groups: [{airings: later}]};}
    async remind() {return undefined;}
    async dismiss() {return undefined;}
  }
  const recordings: any = {getSnapshot: () => ({error: new Error('record_failed')}), lifetime,
    schedule: async (draft: unknown, options: unknown) => {scheduled.push({draft, options}); return scheduleFails ? undefined : {value: {id: `rec-${scheduled.length}`, state: 'scheduled'}};}};
  const {NativeLiveScreen} = await screenModule(new URL('../src-next/screens/settings-live/LiveScreen.tsx', import.meta.url), {...liveTabsFixture(React), ...ownerTagFixture,
    react: {...React, default: React}, 'react-native': native,
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Pattern: 'Pattern', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/dvr': dvr, '../../../../../packages/client-core/src/presentation/kit-model': kitModel,
    '../../kit/controls': controls, '../../kit/focus': focus, '../../kit/overlays': {Banner: 'Banner', Dialog: 'Dialog', Menu: 'Menu', ToastHost: 'ToastHost'}, '../../kit/artwork': {Artwork: 'Artwork', ChannelMark: 'ChannelMark'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, '../../kit/states': {StateView: (props: any) => jsx('StateView', props)},
    '../../kit/shell/visual': {alpha: (value: string) => value, shellType: () => ({})}, '../../kit/theme': {useTheme: () => theme, fontFamilies: {}},
    '../../shell/tv-focus': {tvFocusIds}, './Screen': {Screen: 'Screen', ScreenText: 'ScreenText', useScreenBack: noop, useScreenEntry: () => noop}, './recording-rule-presentation': rules,
    './tv-settings-focus': tvSettingsFocus,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, 'react-native-gesture-handler': {GestureDetector: 'GestureDetector'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn()}, '../../../../../packages/client-core/src/channel-guide': channels,
    '../../../../../packages/client-core/src/guide': guide, '../../../../../packages/client-core/src/guide/directory': {...directory, GuideFeatureClient: FakeFeatures}, './GuideLoadingMarker': {GuideLoadingMarker: 'Loading'},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})}, '../../platform/tv-keys': {useTVRemoteActivity: noop}, '../../shell/model': shellModel, './RecordingsScreen': {NativeRecordingsScreen: 'Recordings'}, '../../tv/layout/guide-focus': {},
    '../destinations/useTVPageBack': {useTVPageBack: noop}, '../destinations/useEntryFocus': {useDestinationEntryFocus: noop}, './guide-presentation': guidePresentation,
    '../../kit/useScreenReader': {useScreenReader: () => false},
    './useGuideTimePan': {useGuideTimePan: () => ({active: false, position: {value: 0}, adjust: noop})}, '../../../../../packages/client-core/src/presentation/live-availability': availability, './guide-pan-model': guidePan, './LivePhonePicker': {NativeLivePhonePicker: 'PhonePicker'},
    './LiveOwnerScreen': {NativeLiveOwnerScreen: (props: any) => {editors.push(`owner:${props.initialAction}`); return jsx('OwnerEditor', props);}},
    './LiveSeriesRule': {NativeSeriesRuleSheet: 'SeriesRule'}, './server/tv-server-reader': {nativeServerAdmission}, '../../tv/auth': {TVManagementHandoff: 'Handoff'},
    '../../tv/auth/TVHandoff': {publicTVHandoffAddress: (address: string) => ({url: address})},
  });
  const ports = {context};
  await host.render(jsx(NativeLiveScreen, {controller, recordings, owner: {}, ports, runtime}));
  const all = () => nodes(host.root), find = (id: string) => all().find(node => node.props?.testID === id);
  const until = async (done: () => boolean) => {for (let n = 0; n < 20 && !done(); n++) await host.settle(); assert.ok(done(), 'condition not reached');};
  const ids = tvFocusIds(authority);
  const open = async (channel: any, program: any) => {
    const row = find('VirtualizedList') ?? all().find(node => node.type === 'VirtualizedList');
    const element = row.props.renderItem({item: 0});
    element.props.onOpen(guide.legacyChannelRow(channel), program && guide.legacyProgram(program)); await host.settle();
    await host.settle();
  };
  const inSheet = (id: string) => nodes(find('live-programme')).find(node => node.props?.testID === id);
  return {host, all, find, inSheet, until, ids, scheduled, editors, controller, open, theme,
    sheet: () => find('live-programme'),
    list: () => all().find(node => node.type === 'VirtualizedList'),
    async press(id: string) {const node = find(id); assert.ok(node, id); node.props.onPress(); await host.settle();},
    async close() {await host.unmount(); controller.dispose(); lifetime.dispose();},
  };
}
const order = (tree: any, wanted: string[]) => nodes(tree).map(node => node.props?.testID).filter((id): id is string => wanted.includes(id));

// Item 5
for (const [name, width, height, expected] of [['portrait phone', 390, 844, 'list'], ['landscape phone', 844, 390, 'guide'], ['upright tablet', 820, 1180, 'guide']] as const) {
  test(`Live TV ${name} with no remembered choice opens on ${expected}`, async () => {
    const f = await fixture({width, height}); try {
      assert.equal(f.find('live-view').props.value, expected);
    } finally {await f.close();}
  });
}
test('a remembered List choice still wins on a landscape phone, and a remembered Guide choice on a portrait phone', async () => {
  const landscape = await fixture({width: 844, height: 390}); try {
    landscape.controller.chooseView('list'); await landscape.host.settle();
    assert.equal(landscape.find('live-view').props.value, 'list');
  } finally {await landscape.close();}
  const portrait = await fixture({width: 390, height: 844}); try {
    portrait.controller.chooseView('guide'); await portrait.host.settle();
    assert.equal(portrait.find('live-view').props.value, 'guide');
  } finally {await portrait.close();}
});

// Item 2
const chip = (f: any, id: string) => {const node = f.find(id); assert.ok(node, id); return node;};
for (const [name, options] of [['phone', {width: 390, height: 844}], ['tablet', {width: 1180, height: 820}], ['TV', {isTV: true}]] as const) {
  test(`${name} filter chips draw the counts the store knows`, async () => {
    const f = await fixture(options); try {
      await f.until(() => chip(f, 'live-filter-all').props.count === 14);
      assert.equal(chip(f, 'live-filter-all').props.count, 14, 'the loaded guide total');
      assert.equal(chip(f, 'live-filter-favorites').props.count, 3, 'favourites summed over the shown sources');
      assert.equal(chip(f, 'live-filter-News').props.count, 3, 'a group is summed across sources');
      assert.equal(chip(f, 'live-filter-Sports').props.count, 8);
    } finally {await f.close();}
  });
}
test('All keeps the whole count while a filter is active, and no count is drawn for what is not known yet', async () => {
  const f = await fixture({filteredTotal: 3}); try {
    await f.press('live-filter-favorites'); await f.until(() => f.controller.guide.getSnapshot().total === 3);
    assert.equal(chip(f, 'live-filter-all').props.count, 14, 'the filtered page total is not the All count');
  } finally {await f.close();}
  const pending = await fixture({hangChannels: true}); try {
    assert.equal(chip(pending, 'live-filter-all').props.count, undefined, 'the guide total has not arrived: no fake zero');
    assert.equal(chip(pending, 'live-filter-favorites').props.count, 3);
  } finally {await pending.close();}
});

// Item 1
for (const [name, options] of [['phone', {width: 390, height: 844}], ['tablet', {width: 1180, height: 820}], ['TV', {isTV: true}]] as const) {
  for (const [filter, chipId, label] of [['group', 'live-filter-News', 'News'], ['Favorites', 'live-filter-favorites', 'Favorites']] as const) {
    test(`${name} ${filter} with no matching channels says so, and Show all returns to All${name === 'TV' ? ' with the guide asking for its entry' : ''}`, async () => {
      const f = await fixture({...options, filteredTotal: 0}); try {
        await f.until(() => f.controller.guide.getSnapshot().total === 14);
        assert.equal(f.find('live-filter-empty'), undefined);
        await f.press(chipId);
        await f.until(() => f.find('live-filter-empty') !== undefined);
        const state = f.find('live-filter-empty').props;
        assert.equal(state.title, defaultI18n.t('guide.filterEmpty', {filter: label}));
        assert.equal(state.title, `Nothing in ${label}. Try all channels.`);
        assert.equal(state.primaryAction.label, defaultI18n.t('channels.showAll'));
        assert.equal(f.list(), undefined, 'no empty virtual list is left behind');
        if (name === 'TV') {
          const region = f.all().find(node => node.type === 'Region' && node.props.id === f.ids.surface('live.grid'));
          assert.ok(region && nodes(region).includes(f.find('live-filter-empty')), 'the action sits in the focus region the filter chips step down into');
        }
        state.primaryAction.onPress(); await f.host.settle();
        await f.until(() => f.find('live-filter-empty') === undefined);
        assert.equal(f.controller.getSnapshot().options.group, undefined);
        assert.equal(f.controller.getSnapshot().options.favorites, false);
        await f.until(() => f.list() !== undefined);
        // tv-focus 24: the guide starts on the programme on now on the first channel. Show all unmounted the focused
        // button, so on TV the new first row asks for that entry (not native recovery); touch has no focus to place.
        const firstRow = f.list().props.renderItem({item: 0});
        assert.equal(firstRow.props.entryEnabled, name === 'TV', name === 'TV' ? 'TV: the first row asks for the guide entry' : 'touch: no entry ask');
        if (name === 'TV') {
          assert.equal(firstRow.props.entryOpen(), true, 'TV: the ask is still armed');
          // One shot: a later revision (here a sort change) is not the one Show all produced, so the row never asks again.
          f.controller.select({sort: 'name'}); await f.host.settle();
          await f.until(() => f.list() !== undefined);
          assert.equal(f.list().props.renderItem({item: 0}).props.entryEnabled, false, 'TV: a later revision never asks');
        }
      } finally {await f.close();}
    });
  }
}
test('a filter that matches channels draws no filtered-empty state', async () => {
  const f = await fixture({filteredTotal: 2}); try {
    await f.press('live-filter-News'); await f.until(() => f.controller.guide.getSnapshot().total === 2);
    assert.equal(f.find('live-filter-empty'), undefined); assert.ok(f.list());
  } finally {await f.close();}
});

// Item 3
for (const [name, options] of [['phone', {width: 390, height: 844}], ['TV', {isTV: true}]] as const) {
  test(`${name} programme sheet says it has no description when the listing has none, and prints the description when it has one`, async () => {
    const f = await fixture(options); try {
      await f.open(rawChannel(), rawProgramme('p1', -10, 20));
      assert.match(textOf(f.sheet()), new RegExp(defaultI18n.t('guide.programNoDescription')));
      await f.open(rawChannel(), rawProgramme('p2', -10, 20, {description: 'A real description.'}));
      assert.match(textOf(f.sheet()), /A real description\./);
      assert.doesNotMatch(textOf(f.sheet()), new RegExp(defaultI18n.t('guide.programNoDescription')));
    } finally {await f.close();}
  });
}

// Item 4
const stale = [source('src-a', {channelCount: 10, refreshState: 'degraded'})];
for (const [name, options] of [['phone', {width: 390, height: 844}], ['tablet', {width: 1180, height: 820}], ['TV', {isTV: true}]] as const) {
  test(`${name} owner's stale banner is the quiet line the frames draw, with no action`, async () => {
    const f = await fixture({...options, sources: stale}); try {
      assert.ok(f.find('live-guide-stale')); assert.equal(f.find('live-guide-stale-source'), undefined);
      assert.equal(f.all().some(node => node.type === 'Region' && node.props.id === f.ids.surface('live.notices')), false, 'no focus stop on TV');
    } finally {await f.close();}
  });
  test(`${name} member's stale banner stays a quiet line with no action`, async () => {
    const f = await fixture({...options, sources: stale, role: 'member'}); try {
      assert.ok(f.find('live-guide-stale')); assert.equal(f.find('live-guide-stale-source'), undefined);
    } finally {await f.close();}
  });
}

// Item 6
const laterA = rawProgramme('l1', 60, 90), laterB = rawProgramme('l2', 90, 120, {recordingId: 'rec-existing', recordingState: 'scheduled'});
for (const [name, options] of [['phone', {width: 390, height: 844}], ['tablet', {width: 1180, height: 820}]] as const) {
  test(`${name} later airings get a Record button that schedules exactly as the main Record does`, async () => {
    const f = await fixture({...options, later: [airing(laterA), airing(laterB)]}); try {
      const main = rawProgramme('p1', -10, 20);
      await f.open(rawChannel(), main);
      await f.until(() => f.find('live-later-record-l1') !== undefined);
      const button = f.find('live-later-record-l1');
      assert.match(button.props.accessibilityLabel, /^Record Programme l1 at .+/);
      assert.ok(!button.props.disabled);
      button.props.onPress(); await f.host.settle();
      await f.press('live-record');
      assert.equal(f.scheduled.length, 2);
      assert.deepEqual(f.scheduled[0].options, f.scheduled[1].options, 'the same recording options');
      assert.deepEqual(f.scheduled[0].draft.channel, f.scheduled[1].draft.channel);
      assert.equal(f.scheduled[0].draft.series, false); assert.equal(f.scheduled[0].draft.programme.id, 'l1');
      assert.equal(f.scheduled[1].draft.programme.id, 'p1');
      await f.until(() => f.find('live-later-record-l1').props.disabled === true);
      assert.equal(f.find('live-later-record-l1').props.accessibilityLabel, defaultI18n.t('guide.recordingScheduled'), 'a scheduled airing says its state');
      // An airing the server already shows as scheduled is not offered again.
      assert.equal(f.find('live-later-record-l2').props.disabled, true);
      assert.equal(f.find('live-later-record-l2').props.accessibilityLabel, defaultI18n.t('guide.recordingScheduled'));
    } finally {await f.close();}
  });
}
test('later airings offer no Record where the source cannot record, and a failed schedule says so and stays retryable', async () => {
  const cannot = await fixture({width: 390, height: 844, later: [airing(laterA)]}); try {
    await cannot.open(rawChannel({recordAvailable: false}), rawProgramme('p1', -10, 20));
    await cannot.until(() => cannot.find('live-later-remind-l1') !== undefined);
    assert.equal(cannot.find('live-later-record-l1'), undefined);
  } finally {await cannot.close();}
  const failing = await fixture({width: 390, height: 844, later: [airing(laterA)], scheduleFails: true}); try {
    await failing.open(rawChannel(), rawProgramme('p1', -10, 20));
    await failing.until(() => failing.find('live-later-record-l1') !== undefined);
    await failing.press('live-later-record-l1');
    await failing.until(() => failing.find('live-record-error') !== undefined);
    assert.ok(!failing.find('live-later-record-l1').props.disabled);
  } finally {await failing.close();}
});
test('TV later rows keep Remind and details only', async () => {
  const f = await fixture({isTV: true, later: [airing(laterA)]}); try {
    await f.open(rawChannel(), rawProgramme('p1', -10, 20));
    await f.until(() => f.find('live-later-remind-l1') !== undefined);
    assert.equal(f.find('live-later-record-l1'), undefined);
  } finally {await f.close();}
});

// Item 7
const actions = ['live-watch', 'live-record', 'live-record-series', 'live-channel-favorite', 'live-remind'];
test('TV sheet reads Later on before its actions, and focus still enters on Watch live', async () => {
  const f = await fixture({isTV: true, later: [airing(laterA)]}); try {
    await f.open(rawChannel(), rawProgramme('p1', -10, 20, {description: 'Words.'}));
    await f.until(() => f.inSheet('live-later') !== undefined);
    const seen = order(f.sheet(), ['live-later', ...actions]);
    assert.deepEqual(seen, ['live-later', 'live-watch', 'live-record', 'live-record-series', 'live-channel-favorite']);
    const text = textOf(f.sheet());
    assert.ok(text.indexOf('Words.') < text.indexOf(defaultI18n.t('guide.laterOn', {channel: 'Summit Sports'})), 'description, then Later on');
    const region = f.all().find(node => node.type === 'Region' && node.props.id === f.ids.surface('live.programme'));
    assert.equal(region.props.entryRegion, `${f.ids.surface('live.programme')}.watch`, 'entry stays on Watch live, not a Later-on row');
  } finally {await f.close();}
});
test('TV sheet for a later programme reads Later on, then Record, Record series, Favorites and Remind', async () => {
  const f = await fixture({isTV: true, later: [airing(laterA)]}); try {
    await f.open(rawChannel(), rawProgramme('p9', 30, 60));
    await f.until(() => f.inSheet('live-later') !== undefined);
    assert.deepEqual(order(f.sheet(), ['live-later', ...actions]), ['live-later', 'live-record', 'live-record-series', 'live-channel-favorite', 'live-remind']);
    const region = f.all().find(node => node.type === 'Region' && node.props.id === f.ids.surface('live.programme'));
    assert.equal(region.props.entryRegion, `${f.ids.surface('live.programme')}.record`);
  } finally {await f.close();}
});
test('TV directional order follows the drawn order: Later on rows lead into the actions', async () => {
  const f = await fixture({isTV: true, later: [airing(laterA)]}); try {
    await f.open(rawChannel(), rawProgramme('p1', -10, 20));
    await f.until(() => f.inSheet('live-later') !== undefined);
    const group = (id: string) => f.all().find(node => node.type === 'Region' && node.props.id === `${f.ids.surface('live.programme')}.${id}`);
    assert.equal(group('later:l1').props.edges.up, undefined);
    assert.equal(group('later:l1').props.edges.down, `${f.ids.surface('live.programme')}.watch`);
    assert.equal(group('watch').props.edges.up, `${f.ids.surface('live.programme')}.later:l1`);
  } finally {await f.close();}
});
for (const [name, options] of [['phone', {width: 390, height: 844}], ['tablet', {width: 1180, height: 820}]] as const) {
  test(`${name} sheet order is unchanged: actions first, Later on last`, async () => {
    const f = await fixture({...options, later: [airing(laterA)]}); try {
      await f.open(rawChannel(), rawProgramme('p1', -10, 20));
      await f.until(() => f.inSheet('live-later') !== undefined);
      assert.deepEqual(order(f.sheet(), ['live-later', ...actions]), ['live-watch', 'live-record', 'live-record-series', 'live-later']);
      await f.open(rawChannel(), rawProgramme('p9', 30, 60));
      await f.until(() => f.inSheet('live-later') !== undefined);
      assert.deepEqual(order(f.sheet(), ['live-later', ...actions]), ['live-record', 'live-record-series', 'live-remind', 'live-watch', 'live-later']);
    } finally {await f.close();}
  });
}

// states 17c: the owner's no-source state names the server under the Owner only tag, and a phone stacks its actions full width.
for (const [name, options, tag, stacked] of [['phone', {width: 390, height: 844}, true, true], ['tablet', {width: 1180, height: 820}, true, false], ['TV', {isTV: true}, false, false]] as const) {
  test(`${name} owner with no Live TV source: ${tag ? 'the server\'s Owner only tag over the heading' : 'no tag'}, actions ${stacked ? 'stacked' : 'side by side'}`, async () => {
    const f = await fixture({...options, sources: []}); try {
      const state = f.find('live-no-sources'); assert.ok(state);
      assert.equal(state.props.title, defaultI18n.t('web.live.empty.noSourcesOwnerTitle'));
      if (tag) {assert.equal(state.props.kicker?.type, 'OwnerTag'); assert.equal(state.props.kicker.props.identity, 'Portico demo');}
      else assert.equal(state.props.kicker, undefined);
      assert.equal(state.props.stackActions, stacked);
      assert.equal(state.props.variant, stacked ? 'centered' : 'inline', 'a phone draws the page state at its 30 pt heading');
      assert.ok(state.props.primaryAction && state.props.secondaryAction);
    } finally {await f.close();}
  });
}
test('a member with no Live TV source gets no owner tag, and a phone still stacks its one action', async () => {
  const f = await fixture({width: 390, height: 844, sources: [], role: 'member'}); try {
    const state = f.find('live-no-sources'); assert.ok(state);
    assert.equal(state.props.kicker, undefined); assert.equal(state.props.stackActions, true);
  } finally {await f.close();}
});

// tablet-play7: an upright tablet has no 2-hour stepper; Now docks at the guide's edge once now scrolls out of view.
test('an upright tablet\'s guide docks Now at its edges, as the phone does, and draws no stepper', async () => {
  const f = await fixture({width: 820, height: 1180}); try {
    assert.equal(f.find('live-view').props.value, 'guide');
    assert.ok(f.find('live-docked-now-earlier')); assert.ok(f.find('live-docked-now-later'));
    assert.equal(f.find('live-earlier'), undefined); assert.equal(f.find('live-later'), undefined);
  } finally {await f.close();}
});

// tablet-play6: at expanded touch widths the title row carries the Guide / Recordings pair and the view switch; the day,
// the 2-hour steps with Now between them, and Sort sit under the chips. TV keeps its one controls row.
test('an expanded tablet draws Live TV with the Guide / Recordings pair over the chips, then the day, the steps and Sort', async () => {
  const f = await fixture({width: 1180, height: 820}); try {
    const header = f.find('live-header'); assert.ok(header); assert.equal(header.props.value, 'guide');
    const inHeader = nodes(header).map(node => node.props?.testID);
    for (const id of ['live-recordings', 'live-view', 'live-owner-open']) assert.ok(inHeader.includes(id), id);
    assert.deepEqual(order(f.host.root, ['live-header', 'live-filter-all', 'live-time']), ['live-header', 'live-filter-all', 'live-time']);
    assert.deepEqual(order(f.find('live-time'), ['live-day', 'live-earlier', 'live-now', 'live-later', 'live-sort']), ['live-day', 'live-earlier', 'live-now', 'live-later', 'live-sort']);
    assert.equal(f.all().filter(node => node.props?.testID === 'live-now').length, 1, 'one Now');
    assert.equal(f.find('live-docked-now-later'), undefined, 'the expanded guide keeps its stepper instead');
    await f.press('live-recordings');
    assert.ok(f.all().some(node => node.type === 'Recordings'), 'the Recordings tab opens Recordings');
  } finally {await f.close();}
  const tv = await fixture({isTV: true}); try {
    assert.equal(tv.find('live-header'), undefined); assert.equal(tv.find('live-time'), undefined);
    assert.deepEqual(order(tv.host.root, ['live-now', 'live-recordings', 'live-owner-open', 'live-filter-all']), ['live-now', 'live-recordings', 'live-owner-open', 'live-filter-all'], 'TV keeps its row above the chips');
  } finally {await tv.close();}
});

