import * as React from 'react';
import * as homeWindow from '../src-next/screens/home/home-window';
import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {heroSpec} from '../../../packages/design/src';
import {HomeService} from '../../../packages/client-core/src/home-service';
import * as homePresentation from '../../../packages/client-core/src/presentation/home-spotlight';
import * as homeCapabilities from '../../../packages/client-core/src/home-selection';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import * as serverText from '../../../packages/client-core/src/presentation/server-text';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {createFocusRegistry} from '../src-next/kit/focus/registry';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry';
import {activateCard, holdCard} from '../src-next/kit/cards/model';
import {itemSelection} from '../src-next/kit/layout/types';
import * as shellModel from '../src-next/shell/model';
import * as tvFocus from '../src-next/shell/tv-focus';
import {NativeHomePaging} from '../src-next/shell/home-paging';
import {tvHomeRows} from '../src-next/screens/home/tv-presentation';
import * as homeModel from '../src-next/screens/home/model';
import * as homeStart from '../src-next/screens/home/home-start';
import {homeActionDiagnostics} from '../src-next/platform/home-action-diagnostics';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

for (const tv of [false, true]) test(`actual ${tv ? 'TV' : 'phone'} Continue card retains its own menu anchor and primary action`, async () => {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
  let currentAuthority = authority, menuOpen = false;
  const entry = {id: 'episode', libraryId: 'television', kind: 'episode', title: 'Actual episode', available: true,
    homeActions: {play: true, state: {watchlisted: false, favorite: false, watched: false, rating: null, personalRevision: 1, containerRevision: 1}, selection: {play: true}},
    navigation: {view: 'item', entityId: 'episode'}, playback: {itemId: 'episode'}, duration: 3600, progressSeconds: 300};
  const row = {id: 'continue', viewerFence: 'fence', title: 'Continue', kind: 'continue', artworkShape: 'landscape', entries: [entry],
    privacySensitivity: '', policyState: '', priority: 1, required: false, hideable: true, reorderable: true, critical: false, cursorCapable: false,
    total: 1, start: 0, limit: 20, hasMore: false, nextCursor: '', endpoint: '', revision: {catalog: 1, viewer: 1}};
  const raw = {serverId: 'server', viewerFence: 'fence', rows: [row], spotlight: [],
    layout: {revision: 1, rowOrder: ['continue'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-23T12:00:00Z'};
  const home = new HomeService({request: async () => JSON.parse(JSON.stringify(raw))});
  await home.load();
  const h = hooks(), child = hooks(), registry = createFocusRegistry(), theme = createTheme({isTV: tv, width: tv ? 1920 : 390});
  let hookOwner = h;
  const react = {...h.react, useRef: (value: unknown) => hookOwner.react.useRef(value), useLayoutEffect: h.react.useEffect};
  const calls: any[] = [], selection = {active: false, isSelected: () => false, onToggle: () => calls.push('toggle'), onEnter() {}, isMenuOpen: () => menuOpen};
  const runtime = {home, text: {t: defaultI18n.t, locale: 'en-US'}, getSnapshot: () => ({authority: currentAuthority, libraries: []}),
    actions: {navigate: (...args: any[]) => calls.push(['navigate', ...args]), play: (...args: any[]) => calls.push(['play', ...args]), more: (...args: any[]) => calls.push(['menu', ...args])},
    selection: () => selection, playback: {subscribe: () => () => {}, getSnapshot: () => ({})}, artwork() {}, ambient() {}};
  const native = {View: 'View', Text: 'Text', RefreshControl: 'RefreshControl',
    AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, useWindowDimensions: () => ({width: tv ? 1920 : 390, height: tv ? 1080 : 844})};
  // The recovery hook is a real Home entry/retry lifetime guard, so it runs its own source
  // on this screen's hook host as HomeScreen does instead of being forced to invisible.
  const recovery = await screenModule(new URL('../src-next/screens/home/useTvHomeRecovery.ts', import.meta.url), {
    react, 'react-native': native, '../../../../../packages/client-core/src/presentation/errors': {presentError}, '../../shell/model': shellModel,
  });
  const {HomeScreen} = await screenModule(new URL('../src-next/screens/home/HomeScreen.tsx', import.meta.url), {'./live-expiry': await import('../src-next/screens/home/live-expiry.ts'), '../../shell/boot-timing': {logBootMilestone: () => {}}, '../../shell/home-read-log': {noteHomeRead: () => {}, createHomeReadLog: () => () => 'home-service'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, '../../kit/overlays': {Banner: 'Banner'}, 
    '../../kit/useMotion': {useMotion: () => ({transforms: true, reduce: false, timing: () => ({})})}, 
    react, 'react-native': native,
    'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (fn: unknown) => fn, useAnimatedScrollHandler: (fn: unknown) => fn, useSharedValue: (value: unknown) => react.useRef({value}).current}, '@portico/design': {heroSpec},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/home-spotlight': homePresentation,
    '../../../../../packages/client-core/src/home-selection': homeCapabilities, '../../../../../packages/client-core/src/presentation/server-text': serverText,
    '../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../packages/client-core/src/presentation/kit-model': {}, '../../platform/ArtworkPipeline': {ArtworkPlacementContext: {Provider: 'ArtworkPlacement'}},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})}, '../destinations/useTVPageBack': {useTVPageBack() {}},
    '../../kit/focus/initial-entry': {createInitialRegionEntry},
    '../../kit/artwork': {ArtworkArrivalContext: React.createContext(undefined)}, '../../kit/controls': {}, '../../kit/cards': {WideCard: 'WideCard'},
    '../../kit/focus': {useFocusRegistry: () => registry}, '../../shell/tv-focus': tvFocus, '../../shell/home-paging': {NativeHomePaging}, './tv-presentation': {tvHomeRows},
    '../../kit/layout': {Row: 'Row', Hero: 'Hero'}, '../../kit/layout/types': {itemSelection},
    '../../kit/layout/FocusAnchor': {useFocusAnchor: () => ({offset: {value: 0}, scrollRef: {current: null}})},
    '../../kit/states': {LoadingPaint: 'LoadingPaint', }, '../../kit/theme': {nativeType, useTheme: () => theme}, '../../platform/home-action-diagnostics': {homeActionDiagnostics}, '../../shell/model': shellModel,
    './home-window': homeWindow, './home-start': homeStart, '../destinations/music/MusicMixesRow': {MusicMixesRow: 'MusicMixesRow'},
    './TvHomeRecovery': {TvHomeRecovery: 'TvHomeRecovery'}, './useTvHomeRecovery': recovery,
    './HomeHeroFacts': {}, './model': homeModel,
  });
  try {
    const tree = h.render(() => HomeScreen({runtime, authority}));
    const rowElement = nodes(tree).find(node => node.type === 'Row');
    const actual = home.getSnapshot().document!.rows[0].entries[0];
    const content = rowElement.props.renderItem({entry: actual}, {index: 0, selection: itemSelection(selection, actual.id)});
    const element = nodes(content).find(node => node.type?.name === 'HomeCard');
    // The parent and real private card have separate retained hook hosts.
    const renderCard = () => {hookOwner = child; return child.render(() => element.type(element.props));};
    const card = nodes(renderCard()).find(node => node.type === 'WideCard');
    activateCard(card.props);
    assert.equal(calls[0][0], 'play', 'the current Continue primary action remains genuine Play on both platforms');
    const nativeHost = {actual: 'native-card'};
    card.props.menuAnchorRef.current = nativeHost;
    holdCard(card.props, tv);
    assert.deepEqual(calls.at(-1), ['menu', actual, 'continue', card.props.menuAnchorRef]);
    menuOpen = true;
    const reopened = nodes(renderCard()).find(node => node.type === 'WideCard');
    assert.equal(reopened.props.menuAnchorRef, card.props.menuAnchorRef);
    assert.equal(reopened.props.moreExpanded, true);
    assert.equal(reopened.props.accessibilityState.expanded, true);
    assert.equal(reopened.props.menuAnchorRef.current, nativeHost);
    selection.active = true;
    element.props.context = {...element.props.context, selection: itemSelection(selection, actual.id)};
    const selecting = nodes(renderCard()).find(node => node.type === 'WideCard');
    holdCard(selecting.props, false);
    assert.equal(calls.at(-1), 'toggle', 'active touch selection keeps its real action');
    currentAuthority = {...authority, epoch: 'retired'};
    const count = calls.length;
    reopened.props.onMore();
    reopened.props.onNavigate();
    assert.equal(calls.length, count, 'retired Home callbacks cannot open a menu or title');
  } finally {home.dispose();}
});
