// @fabric-react
import * as homeWindow from '../src-next/screens/home/home-window';
import {homeActionDiagnostics} from '../src-next/platform/home-action-diagnostics.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {pathToFileURL} from 'node:url';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {heroSpec} from '@portico/design';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry';
import {createTheme, nativeType} from '../src-next/kit/theme';
import * as registryModule from '../src-next/kit/focus/registry';
import * as hostRefModule from '../src-next/kit/focus/useHostRef';
import * as tvFocus from '../src-next/shell/tv-focus';
import * as homePresentation from '../../../packages/client-core/src/presentation/home-spotlight';
import * as homeCapabilities from '../../../packages/client-core/src/home-selection';
import * as serverText from '../../../packages/client-core/src/presentation/server-text';
import {defaultI18n} from '../../../packages/i18n/src';
import * as homeModel from '../src-next/screens/home/model';
import * as shellModel from '../src-next/shell/model';
import {NativeHomePaging} from '../src-next/shell/home-paging';
import {tvHomeRows} from '../src-next/screens/home/tv-presentation';
import * as homeStart from '../src-next/screens/home/home-start';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
const entry = {id: 'movie', libraryId: 'movies', kind: 'movie', title: 'Actual title', overview: 'Server supplied overview.', available: true,
  homeActions: {play: true, myList: true, details: true, state: {watchlisted: false}}, playback: {itemId: 'movie'}, navigation: {view: 'item', entityId: 'movie'}, watchlisted: false};
const document = () => ({serverId: 'server', viewerFence: 'fence', spotlight: [{entry, reason: {text: {code: 'recently_added', fallback: 'New in Movies'}}}], rows: []});

// Actual Home hooks + guide/target hooks, with native refs and focus events
// delivered at separate commits. This catches the async mount gap that a
// render-only preferred-focus assertion missed. Native D-pad/layout still needs
// the disposable TV proof owned by the integration lane.
async function fixture() {
  const registry = registryModule.createFocusRegistry(), theme = createTheme({isTV: true, width: 1920}), ids = tvFocus.tvFocusIds(authority);
  const contexts: {current: unknown}[] = [];
  const react = {...React, default: React, createContext: (current: unknown) => {const context = {current}; contexts.push(context); return context;},
    useContext: (context: {current: unknown}) => context.current};
  const native = {View: 'View', Text: 'Text', RefreshControl: 'RefreshControl', TVFocusGuideView: 'TVFocusGuideView',
    findNodeHandle: (host: any) => host.nativeTag,
    AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, useWindowDimensions: () => ({width: 1920, height: 1080})};
  const focus = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react, 'react-native': native, '../theme': {useTheme: () => theme}, './registry': registryModule, './useHostRef': hostRefModule,
  });
  // The guide Region must not inherit the injected RegionContext as its own
  // parent; only the standalone Play target needs the spotlight region identity.
  const targetContexts: {current: unknown}[] = [];
  const targetReact = {...React, default: React, createContext: (current: unknown) => {const context = {current}; targetContexts.push(context); return context;},
    useContext: (context: {current: unknown}) => context.current};
  const targetFocus = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react: targetReact, 'react-native': native, '../theme': {useTheme: () => theme}, './registry': registryModule, './useHostRef': hostRefModule,
  });
  contexts[0].current = registry;
  targetContexts[0].current = registry; targetContexts[1].current = ids.spotlight;
  const listeners = new Set<() => void>();
  let snapshot: any = {phase: 'loading'}, currentAuthority = authority, selectionActive = false;
  const home = {subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}, getSnapshot: () => snapshot,
    load: async () => {}, recoverDroppedRows: async () => {}, setRowDemand() {}, registerWindow() {}, subscribeRowPages: () => () => {}, rowPageReceipt: () => undefined};
  const runtime = {home, getSnapshot: () => ({authority: currentAuthority, libraries: [], selectionActive}), text: {t: defaultI18n.t, locale: 'en-US'},
    registerScrollToTop: () => () => {}, registerTVPageBack: () => () => {}, reportError(error: unknown) {throw error;}, actions: {}, playback: {}};
  const useFocusAnchor = () => React.useMemo(() => ({scrollRef: {current: null}, offset: {value: 0}, repeating: false, stop() {}, move() {}, anchor() {}}), []);
  // HomeScreen's TV entry/retry lifetime guard is real source, run on this fixture's hook host
  // with the screen's own runtime, authority, error, active and resolved inputs.
  const recovery = await screenModule(new URL('../src-next/screens/home/useTvHomeRecovery.ts', import.meta.url), {
    react, 'react-native': native, '../../../../../packages/client-core/src/presentation/errors': {presentError}, '../../shell/model': shellModel,
  });
  const source = process.env.PORTICO_HOME_ENTRY_SOURCE ? pathToFileURL(process.env.PORTICO_HOME_ENTRY_SOURCE) : new URL('../src-next/screens/home/HomeScreen.tsx', import.meta.url);
  const {HomeScreen} = await screenModule(source, {'./live-expiry': await import('../src-next/screens/home/live-expiry.ts'), '../../shell/boot-timing': {logBootMilestone: () => {}}, '../../shell/home-read-log': {noteHomeRead: () => {}, createHomeReadLog: () => () => 'home-service'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, 
    '../../kit/useMotion': {useMotion: () => ({transforms: true, reduce: false, timing: () => ({})})}, 
    react, 'react-native': native, 'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (fn: unknown) => fn, useAnimatedScrollHandler: (fn: unknown) => fn, useSharedValue: (value: unknown) => react.useRef({value}).current},
    '@portico/design': {heroSpec},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/home-spotlight': homePresentation,
    '../../../../../packages/client-core/src/home-selection': homeCapabilities, '../../../../../packages/client-core/src/presentation/server-text': serverText,
    '../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../packages/client-core/src/presentation/kit-model': {}, '../../platform/ArtworkPipeline': {}, '../../kit/artwork': {ArtworkArrivalContext: React.createContext(undefined)},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})}, '../destinations/useTVPageBack': {useTVPageBack() {}},
    '../../kit/focus/initial-entry': {createInitialRegionEntry},
    '../../kit/controls': {Button: 'Button'}, '../../kit/cards': {}, '../../kit/focus': {...focus, useFocusRegistry: () => registry},
    '../../shell/tv-focus': tvFocus, '../../shell/home-paging': {NativeHomePaging}, './tv-presentation': {tvHomeRows},
    '../../kit/layout': {Hero: 'Hero'}, '../../kit/layout/FocusAnchor': {useFocusAnchor}, '../../kit/layout/types': {},
    '../../kit/overlays': {Banner: 'Banner'}, '../../kit/states': {skeletonTiming: {delayMs: 300, slowMs: 8000}, LoadingPaint: 'LoadingPaint', HomeSkeleton: 'HomeSkeleton'}, '../../platform/home-action-diagnostics': {homeActionDiagnostics}, '../../kit/theme': {nativeType, useTheme: () => theme}, '../../shell/model': shellModel,
    './home-window': homeWindow, './home-start': homeStart, '../destinations/music/MusicMixesRow': {MusicMixesRow: 'MusicMixesRow'},
    './TvHomeRecovery': {TvHomeRecovery: 'TvHomeRecovery'}, './useTvHomeRecovery': recovery,
    './HomeHeroFacts': {}, './model': homeModel,
  });
  let props: any = {}, tree: any, guide: any, target: any, requests = 0;
  const host = createFabricHookHost(() => {tree = HomeScreen({runtime, authority, ...props});}); await host.mount();
  const childHosts: Awaited<ReturnType<typeof createFabricHookHost>>[] = [], cleanups: (() => void)[] = [], destinations: number[][] = [];
  return {
    registry, ids, get tree() {return tree;}, get guide() {return guide;}, get requests() {return requests;}, destinations,
    async publish(value = document()) {await act(async () => {await Promise.resolve(); snapshot = {phase: 'ready', document: value}; for (const listener of listeners) listener();});},
    async update(value: any) {await act(async () => {props = {...props, ...value}; await host.rerender();});},
    async foreign() {await act(async () => {currentAuthority = {...authority, epoch: 'different'}; await host.rerender();});},
    async selection(value: boolean) {await act(async () => {selectionActive = value; await host.rerender();});},
    async mountGuide() {
      const hero = nodes(tree).find(node => node.type === 'Hero');
      const candidate = hero.props.candidates[0];
      assert.equal(candidate.overview, entry.overview, 'genuine server overview reaches Hero unchanged');
      assert.notEqual(hero.props.showOverview, false, 'Home never suppresses the available overview');
      const wrapper = hero.props.renderActions(candidate), region = wrapper.type(wrapper.props);
      let outer: any;
      const outerHost = createFabricHookHost(() => {outer = focus.FocusRegion.render(region.props, null);}); await outerHost.mount(); childHosts.push(outerHost);
      const guideHost = createFabricHookHost(() => {guide = outer.type.render(outer.props, null);}); await guideHost.mount(); childHosts.push(guideHost);
      const cleanup = guide.props.ref({nativeTag: 101, requestTVFocus() {}, setDestinations: (values: number[]) => destinations.push(values)});
      cleanups.push(cleanup);
      return region;
    },
    async mountPlay() {
      const targetHost = createFabricHookHost(() => {target = targetFocus.useFocusTarget({focusId: 'play'});}); await targetHost.mount(); childHosts.push(targetHost);
      cleanups.push(target.ref({nativeTag: 102, requestTVFocus() {requests++; target.onFocus();}}));
    },
    async dispose() {await host.unmount(); cleanups.reverse().forEach(cleanup => cleanup?.()); for (const child of childHosts.reverse()) await child.unmount();},
  };
}

test('async empty Home acquires Play exactly once only after both real guide and real target mount', async () => {
  const f = await fixture();
  try {
    assert.equal(f.tree.type, 'LoadingPaint'); assert.equal(f.tree.props.skeleton.type, 'HomeSkeleton'); assert.equal(f.requests, 0);
    await f.publish(); assert.equal(f.requests, 0, 'document arrival is not a focus request');
    const region = await f.mountGuide(); await act(() => {});
    assert.equal(f.requests, 0, 'guide alone cannot focus an unmounted target');
    assert.equal(f.guide.type, 'TVFocusGuideView'); assert.equal(f.guide.props.autoFocus, true);
    assert.equal(f.guide.props.trapFocusRight, true); assert.equal(f.guide.props.trapFocusLeft ?? false, false);
    assert.equal(f.guide.props.style.flexDirection, 'row'); assert.equal(f.guide.props.style.gap, 18);
    assert.equal(f.guide.props.testID, `portico.region.${encodeURIComponent(f.ids.spotlight)}`);
    assert.deepEqual(region.props.order, ['play', 'my-list', 'details']);
    await f.mountPlay(); await act(() => {});
    assert.deepEqual(f.destinations.at(-1), [102], 'platform guide targets the actual native Play handle');
    assert.equal(f.requests, 1); assert.equal(f.registry.capture()?.childId, 'play');
    const other = {regionId: f.ids.row('continue'), childId: 'actual-card'};
    await act(() => f.registry.didFocus(other)); await f.publish();
    await act(() => f.registry.didBlur(other)); await f.publish();
    assert.equal(f.requests, 1, 'revalidation and a later blur never reacquire entry');
  } finally {await f.dispose();}
});

test('a rail visit that still holds attached focus when async content lands owns initial entry, and a later rail blur never re-acquires it', async () => {
  const f = await fixture();
  try {
    const rail = {regionId: f.ids.rail, childId: 'home'};
    f.registry.attach(rail, {requestTVFocus() {}});
    await act(() => f.registry.didFocus(rail));
    await f.publish();
    await f.mountGuide(); await f.mountPlay(); await act(() => {});
    await act(() => f.registry.didBlur(rail));
    await act(() => {});
    assert.equal(f.requests, 0, 'content arriving after user navigation cannot steal focus');
  } finally {await f.dispose();}
});

test('a rail focus already blurred before async content lands leaves no attached ownership, so arrival requests entry', async () => {
  const f = await fixture();
  try {
    const rail = {regionId: f.ids.rail, childId: 'home'};
    f.registry.attach(rail, {requestTVFocus() {}});
    await act(() => f.registry.didFocus(rail));
    await act(() => f.registry.didBlur(rail));
    await act(() => {});
    assert.equal(f.registry.attachedFocus(), undefined, 'the earlier blur leaves no current focus ownership');
    await f.publish();
    await f.mountGuide(); await f.mountPlay(); await act(() => {});
    assert.equal(f.requests, 1, 'initial entry consumes only currently attached focus, so a blur before arrival leaves entry available');
  } finally {await f.dispose();}
});

for (const obstruction of [{active: false}, {heroObscured: true}]) test(`Home waits through ${JSON.stringify(obstruction)} before first host entry`, async () => {
  const f = await fixture();
  try {
    await f.update(obstruction); await f.publish(); await f.mountGuide(); await f.mountPlay(); await act(() => {});
    assert.equal(f.requests, 0);
    await f.update({active: true, heroObscured: false}); assert.equal(f.requests, 1);
    await f.update(obstruction); await f.update({active: true, heroObscured: false}); assert.equal(f.requests, 1);
  } finally {await f.dispose();}
});

test('expanded rail, active selection and stale authority cannot acquire Home entry', async () => {
  for (const blocked of ['rail', 'selection', 'authority']) {
    const f = await fixture();
    try {
      await f.publish(); await f.mountGuide();
      if (blocked === 'rail') await f.update({railExpanded: true});
      if (blocked === 'selection') await f.selection(true);
      if (blocked === 'authority') await f.foreign();
      await f.mountPlay(); await act(() => {}); assert.equal(f.requests, 0, blocked);
    } finally {await f.dispose();}
  }
});
