import {canReturnToPage, holdsPageFocus} from '../src-next/shell/page-focus-return.ts';
import * as homeWindow from '../src-next/screens/home/home-window';
import * as homeStart from '../src-next/screens/home/home-start';
import {listeningCoverFixture, stackSlotFixture} from './helpers/screen-harness.mjs';
import {homeActionDiagnostics} from '../src-next/platform/home-action-diagnostics.ts';
import {focusTestID, createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {tvFocusIds, tvHomeRegionMap, sameDestination} from '../src-next/shell/tv-focus';
import {NativeHomePaging} from '../src-next/shell/home-paging';
import {tvHomeRows} from '../src-next/screens/home/tv-presentation';
import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import * as simpleReact from 'react';
import {heroSpec} from '@portico/design';
import {sameFence, type PlaybackFence} from '../../../packages/player-core/src/index.ts';
import * as heroModel from '../src-next/kit/layout/model.ts';
import * as shellModel from '../src-next/shell/model.ts';
import * as menuKey from '../src-next/platform/menu-key';
import * as homeModel from '../src-next/screens/home/model';
import * as errorPresentation from '../../../packages/client-core/src/presentation/errors';
import {createTheme} from '../src-next/kit/theme';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry';
import type {DockOccupant} from '../src-next/kit/dock/types.ts';
import type {NativeShellRuntime} from '../src-next/shell/runtime.ts';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import {act, createHookHost} from './hook-driver.ts';
import {surfacePaintFixture, screenModule} from './helpers/screen-harness.mjs';
import {currentMorphFence} from '../src-next/kit/dock/morph-fence.ts';
import {releaseDock, validRect, sameDockFence} from '../src-next/kit/dock/model.ts';
import {playerPostureCrossFadeMs} from '../src-next/platform/layout/player-presentation.ts';

type HostNode = {tag: number; type: unknown; props: Record<string, any>; children: HostNode[]};

// The lifecycle cases below run the actual owned hooks/components on the
// installed Fabric renderer, so React owns setter identity, effect ordering,
// cleanup on unmount or dependency change and keyed identity. Native hosts and
// animation delivery are controlled; this does not prove device motion.
// Only the effect-replay case at the end still uses the simple driver: the
// production renderer never re-invokes a committed effect, so that case has no
// real-React equivalent.
type EffectRecord = {setup: () => void | (() => void); cleanup: void | (() => void)};
/** The module stubs every case shares. `base` is the React instance the chosen
 * renderer uses: the Fabric helper's production copy, or the plain driver's. */
function lifecycle(base: any = React) {
  const effects: EffectRecord[] = [];
  let currentEffect: EffectRecord | undefined;
  const timings: {value: number; duration?: number; complete?: (finished: boolean) => void}[] = [];
  const cancelled: object[] = [], delays: number[] = [];
  let writes = 0;
  const motion = {reduce: false, transforms: true, timing: () => ({duration: 180})};
  const react = {...base, default: base,
    useState: (initial: any) => {
      const [value, set] = base.useState(initial);
      // The counted setter keeps React's stable identity; only the recorded
      // setter calls are counted, which is what "no write after unmount" means.
      const write = base.useCallback((next: any) => {writes++; set(next);}, [set]);
      return [value, write];
    },
    useEffect: (setup: () => void | (() => void), deps?: readonly unknown[]) => base.useEffect(() => {
      const effect: (typeof effects)[number] = {setup, cleanup: undefined}, previous = currentEffect;
      currentEffect = effect;
      try {effect.cleanup = setup(); effects.push(effect); return effect.cleanup;}
      finally {currentEffect = previous;}
    }, deps),
  };
  const animated = {
    default: {View: 'AnimatedView', ScrollView: 'AnimatedScrollView'},
    useSharedValue: (value: number) => base.useRef({value}).current,
    useAnimatedStyle: () => ({}), useAnimatedScrollHandler: (handler: unknown) => handler,
    withTiming: (value: number, config: {duration?: number}, complete?: (finished: boolean) => void) => {timings.push({value, ...config, complete}); return value;},
    withDelay: (delay: number, value: number) => {delays.push(delay); return value;},
    cancelAnimation: (value: object) => {cancelled.push(value);}, runOnJS: (fn: unknown) => fn,
  };
  const native = {View: 'View', Text: 'Text', Pressable: 'Pressable', RefreshControl: 'RefreshControl',
    AppState: {currentState: 'active', addEventListener: () => ({remove() {}})},
    useWindowDimensions: () => ({width: 390, height: 844}),
  };
  return {react, animated, native, motion, effects, timings, cancelled, delays, get currentEffect() {return currentEffect;}, get writes() {return writes;}};
}

/** A hook body mounted as the real component React renders. */
function hook(body: () => void) {
  return () => {body(); return null;};
}
/** The real component/hook on the installed Fabric renderer; each update is one React commit. */
async function mount(component: any, initial?: any) {
  const host = await fabricHost();
  let props = initial;
  const element = () => React.createElement(component, props);
  await host.render(element());
  return {host, get tree() {return host.root[0] as HostNode;},
    async update(next?: any) {if (next !== undefined) props = next; await host.render(element());},
    async act(work: () => void | Promise<void>) {await work(); await host.settle();},
    unmount: () => host.unmount()};
}
function nodes(list: HostNode[]): HostNode[] {return list.flatMap(node => [node, ...nodes(node.children)]);}
const nodeOf = (tree: HostNode, type: unknown) => nodes([tree]).find(node => node.type === type);

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {resolve = done;});
  return {promise, resolve};
}
function timers(t: TestContext, state?: ReturnType<typeof lifecycle>) {
  let now = 0;
  const tasks: {run: () => void; ms: number; cancelled: boolean; effect: EffectRecord | undefined}[] = [];
  t.mock.method(Date, 'now', () => now);
  t.mock.method(globalThis, 'setTimeout', (run: () => void, ms = 0) => {
    // The timer remembers the effect that queued it, so a case can replay that
    // exact effect's cleanup instead of guessing an array position.
    const task = {run, ms, cancelled: false, effect: state?.currentEffect}; tasks.push(task);
    return task as unknown as ReturnType<typeof setTimeout>;
  });
  t.mock.method(globalThis, 'clearTimeout', (token: unknown) => {
    const task = tasks.find(value => value === token); if (task) task.cancelled = true;
  });
  return {tasks, at(value: number) {now = value;}};
}

const fence: PlaybackFence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1},
  sessionId: 'session', sourceId: 'source', itemId: 'item', entryId: 'entry', generation: 1};
function occupant(generation = 1, playing = true): DockOccupant {
  const current = {...fence, generation, sourceId: `source-${generation}`};
  return {mode: 'music', playback: {fence: current, kind: 'music', queue: null, positionSeconds: 30, playing, expanded: false}, presentation: {fence: current}} as DockOccupant;
}
async function dockModule(state: ReturnType<typeof lifecycle>) {
  return screenModule(new URL('../src-next/kit/dock/useDockPresence.ts', import.meta.url), {
    react: state.react, 'react-native-reanimated': state.animated,
    '../../../../../packages/player-core/src/index': {sameFence}, '../useMotion': {useMotion: () => state.motion},
    './model': {sameDockFence},
  });
}

test('idle PlayerMorph hides the Android surface host without removing its persistent child', async () => {
  const state = lifecycle();
  const {PlayerMorph} = await screenModule(new URL('../src-next/kit/dock/PlayerMorph.tsx', import.meta.url), {
    react: state.react, 'react-native': {...state.native, Platform: {OS: 'android'}},
    'react-native-gesture-handler': {Gesture: {}}, 'react-native-reanimated': {...state.animated, useAnimatedReaction() {}},
    '../../../../../packages/player-core/src/index': {}, '../Icon': {Icon: 'Icon'},
    '../theme': {useTheme: () => ({layers: {player: 30}, colors: {shadow: '#000'}})},
    '../useMotion': {useMotion: () => state.motion}, './morph-fence': {currentMorphFence},
    '../../platform/layout/player-presentation': {playerPostureCrossFadeMs},
    './model': {releaseDock, validRect}, './visual': {alpha: () => '#000', dockStyles: {fill: {position: 'absolute', inset: 0}}},
  });
  const frame = {x: 0, y: 0, width: 390, height: 844}, child = React.createElement('PersistentNativeSurface');
  const props = {controller: {progress: {value: 0}, velocity: {value: 0}, dragging: {value: false}},
    docked: false, fullFrame: frame, dockFrame: frame, children: child, fence: undefined as PlaybackFence | undefined};
  const morph = await mount(PlayerMorph, props);
  const childNode = () => nodeOf(morph.tree, 'PersistentNativeSurface')!, childTag = childNode().tag;
  try {
    assert.equal(morph.tree.props.style.at(-1).display, 'none');
    assert.equal(morph.tree.props.importantForAccessibility, 'no-hide-descendants');
    assert.equal(childNode().tag, childTag);
    await morph.update({...props, fence});
    assert.equal(morph.tree.props.style.at(-1).display, 'flex');
    assert.equal(childNode().tag, childTag, 'the same committed child host survives the visible state');
    await morph.update(props);
    assert.equal(morph.tree.props.style.at(-1).display, 'none');
    assert.equal(childNode().tag, childTag, 'the same committed child host survives hiding again');
  } finally {await morph.unmount();}
});

test('an interrupted dock exit cannot remove a replacement source or invalidate its later exit', async () => {
  const state = lifecycle(), {useDockPresence} = await dockModule(state);
  const first = occupant(), replacement = occupant(2);
  let wanted: DockOccupant | null = first, shown: DockOccupant | null;
  const session = await mount(hook(() => {shown = useDockPresence(wanted, false).shown;}));
  try {
    wanted = null; await session.update();
    const staleExit = state.timings.at(-1)!.complete!, staleCleanup = state.effects.at(-1)!.cleanup!;
    wanted = replacement; await session.update();
    await session.act(() => staleExit(true));
    assert.equal(shown!, replacement);
    wanted = null; await session.update();
    const exit = state.timings.at(-1)!.complete!, cancellations = state.cancelled.length;
    staleCleanup();
    assert.equal(state.cancelled.length, cancellations, 'old cleanup must not cancel the current animation');
    await session.act(() => exit(true));
    assert.equal(shown!, null, 'the current primitive epoch must still be valid');
  } finally {await session.unmount();}
});

test('dock exit completion is ignored after unmount and an unfinished exit retains its chrome', async () => {
  const state = lifecycle(), {useDockPresence} = await dockModule(state), first = occupant();
  let wanted: DockOccupant | null = first, shown: DockOccupant | null;
  const session = await mount(hook(() => {shown = useDockPresence(wanted, false).shown;}));
  try {
    wanted = null; await session.update();
    const exit = state.timings.at(-1)!.complete!;
    await session.act(() => exit(false)); assert.equal(shown!, first);
    await session.unmount(); const writes = state.writes;
    exit(true); assert.equal(state.writes, writes);
    assert.ok(state.cancelled.length > 0);
  } finally {await session.unmount();}
});

test('returning to the same dock source retargets its interrupted fade toward visible', async () => {
  const state = lifecycle(), {useDockPresence} = await dockModule(state), first = occupant();
  let wanted: DockOccupant | null = first, shown: DockOccupant | null;
  const session = await mount(hook(() => {shown = useDockPresence(wanted, false).shown;}));
  try {
    wanted = null; await session.update(); const exit = state.timings.at(-1)!.complete!;
    wanted = {...first}; await session.update();
    assert.equal(state.timings.at(-1)!.value, 1);
    await session.act(() => exit(true)); assert.equal(shown!, wanted);
  } finally {await session.unmount();}
});

// Pure calculation: which restoration delay the hook chooses. No lifecycle or
// ordering claim, so this case stays on the simple callback driver.
test('paused audio keeps the phone and TV restoration delays', async () => {
  for (const tv of [false, true]) {
    const state = lifecycle(simpleReact), {useDockPresence} = await dockModule(state);
    let wanted: DockOccupant | null = null;
    const host = createHookHost(() => {useDockPresence(wanted, tv);});
    host.mount();
    try {
      wanted = occupant(2, false); await act(() => host.rerender());
      assert.equal(state.delays.at(-1), tv ? 300 : 200);
    } finally {host.unmount();}
  }
});

async function heroModule(state: ReturnType<typeof lifecycle>) {
  return screenModule(new URL('../src-next/kit/layout/Hero.tsx', import.meta.url), {
    react: state.react, 'react-native': state.native, 'react-native-reanimated': state.animated, '@portico/design': {heroSpec},
    '../theme': {useTheme: () => ({isTV: false, layoutMode: 'phone', platform: 'phone', colors: {text: '#edf5fa'}})}, '../useMotion': {useMotion: () => state.motion},
    './Lockup': {Lockup: 'Lockup'}, './model': heroModel,
    '../controls/Focusable': {Focusable: 'Focusable'}, '../focus/registry': {focusTestID},
  });
}
const heroProps = () => ({candidates: [{id: 'a', title: 'A'}, {id: 'b', title: 'B'}], variant: 'phone-app',
  renderArtwork: () => null, renderActions: () => null, onIndexChange: (_index: number) => {}});

test('hero interruption preserves remaining dwell and rejects a previously queued timeout', async t => {
  const state = lifecycle(), timer = timers(t, state), {Hero} = await heroModule(state), changes: number[] = [];
  const props = {...heroProps(), onIndexChange: (index: number) => changes.push(index)};
  const hero = await mount(Hero, props);
  try {
    assert.equal(timer.tasks.at(-1)!.ms, heroSpec.rotation.intervalMs);
    const old = timer.tasks.at(-1)!, oldCleanup = old.effect!.cleanup!;
    timer.at(3000); await hero.update({...props, active: false});
    assert.equal(old.cancelled, true);
    old.run(); assert.deepEqual(changes, []);
    timer.at(20_000); await hero.update(props);
    assert.equal(timer.tasks.at(-1)!.ms, heroSpec.rotation.intervalMs - 3000);
    const cancellations = state.cancelled.length;
    oldCleanup(); assert.equal(state.cancelled.length, cancellations);
    await hero.act(() => timer.tasks.at(-1)!.run());
    assert.deepEqual(changes, [1]);
    assert.equal(timer.tasks.at(-1)!.ms, heroSpec.rotation.intervalMs);
  } finally {await hero.unmount();}
});

test('a new hero source at the same index owns a fresh clock and unmount fences every timeout', async t => {
  const state = lifecycle(), timer = timers(t, state), {Hero} = await heroModule(state), changes: number[] = [];
  const props = {...heroProps(), onIndexChange: (index: number) => changes.push(index)};
  const hero = await mount(Hero, props);
  try {
    const old = timer.tasks.at(-1)!, oldCleanup = old.effect!.cleanup!;
    timer.at(3000);
    const replacement = {...props, candidates: [{id: 'c', title: 'C'}, {id: 'd', title: 'D'}]};
    await hero.update(replacement);
    assert.equal(old.cancelled, true);
    assert.equal(timer.tasks.at(-1)!.ms, heroSpec.rotation.intervalMs);
    timer.at(4000); oldCleanup(); old.run(); assert.deepEqual(changes, []);
    timer.at(5000); await hero.update({...replacement, active: false});
    timer.at(20_000); await hero.update(replacement);
    assert.equal(timer.tasks.at(-1)!.ms, heroSpec.rotation.intervalMs - 2000);
    await hero.unmount(); const writes = state.writes;
    for (const task of timer.tasks) {assert.equal(task.cancelled, true); task.run();}
    assert.deepEqual(changes, []); assert.equal(state.writes, writes);
  } finally {await hero.unmount();}
});

const authority: shellModel.ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: '1', viewerFence: 'viewer'};
function shellRuntime(onMemoryRead?: () => void) {
  let snapshot: any = {authority, destination: {kind: 'home'}, libraries: [{id: 'movies', kind: 'movie'}], hasLiveTV: false, profile: {}, account: {actions: []}};
  const reads: ReturnType<typeof deferred<string | undefined>>[] = [], destinations: unknown[] = [], listeners = new Set<() => void>();
  const notify = () => {for (const listener of [...listeners]) listener();};
  const runtime = {subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}, getSnapshot: () => snapshot,
    memory: {read: () => {onMemoryRead?.(); const read = deferred<string | undefined>(); reads.push(read); return read.promise;}, remember: async () => {}},
    onDestination: (destination: unknown) => {destinations.push(destination); snapshot = {...snapshot, destination}; notify();},
    renderRoutes: () => null, renderSelection: () => null, renderDestination: () => null, reportError: (error: unknown) => {throw error;},
  } as unknown as NativeShellRuntime;
  return {runtime, reads, destinations, listeners,
    /** A same-value publication with a fresh snapshot identity, as the server would send. */
    publish() {snapshot = {...snapshot}; notify();},
    authority(next: shellModel.ShellAuthority) {snapshot = {...snapshot, authority: next}; notify();}};
}
async function shellSession(state: ReturnType<typeof lifecycle>, runtime: NativeShellRuntime, registry = createFocusRegistry()) {
  const theme = createTheme({isTV: false, width: 390});
  const {NativeShell} = await screenModule(new URL('../src-next/shell/NativeShell.tsx', import.meta.url), {...stackSlotFixture, 
    './page-focus-return': {canReturnToPage, holdsPageFocus},
    '../screens/home/home-start': homeStart,
    '../kit/overlays/SurfacePaintHost': surfacePaintFixture,
    '../kit/dock/DockClearance': {DockClearanceProvider: 'DockClearanceProvider'},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined, usePlayerMorphPublication() {}},
    '../platform/tv-menu': {useTVMenuCapture: () => {}}, '../platform/menu-key': menuKey,
    react: state.react, 'react-native': state.native, 'react-native-reanimated': state.animated,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '../kit/theme': {useTheme: () => theme}, '../kit/useMotion': {useMotion: () => state.motion}, './model': shellModel,
    '../kit/focus': {useFocusRegistry: () => registry}, './tv-focus': {tvFocusIds, sameDestination},
    '../kit/overlays': {OverlaysProvider: 'OverlaysProvider'}, '../kit/shell': {AccountMenu: 'AccountMenu'}, '../kit/browse': {},
    '../screens/home/HomeScreen': {HomeScreen: 'HomeScreen'}, './PhoneTabBar': {PhoneTabBar: 'PhoneTabBar'}, './MediumRail': {MediumRail: 'MediumRail'}, './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './PhoneHomeHeader': {PhoneHomeHeader: 'PhoneHomeHeader'}, './TVRail': {}, './TVProfileMenu': {}, './NativePlaybackLayer': {}, './DesktopHeader': {},
  });
  return mount(NativeShell, {runtime});
}
/** Selecting a tab goes through the exact onSelect the shell hands its tab bar. */
const tab = (session: Awaited<ReturnType<typeof shellSession>>, id: string) => nodeOf(session.tree, 'PhoneTabBar')!.props.onSelect(id);

test('shell library-opening epochs reject navigation interrupted by a later destination or unmount', async () => {
  const state = lifecycle(), port = shellRuntime(), session = await shellSession(state, port.runtime);
  try {
    await session.act(() => tab(session, 'libraries')); assert.equal(port.reads.length, 2);
    await session.act(() => tab(session, 'search'));
    await session.act(() => port.reads[1]!.resolve('movies'));
    assert.deepEqual(port.destinations, [{kind: 'search'}]);
    await session.act(() => port.publish());
    await session.act(() => tab(session, 'libraries')); assert.equal(port.reads.length, 3);
    await session.unmount(); const writes = state.writes;
    await session.act(() => {port.reads[0]!.resolve('movies'); port.reads[2]!.resolve('movies');});
    assert.deepEqual(port.destinations, [{kind: 'search'}]); assert.equal(state.writes, writes);
  } finally {await session.unmount();}
});

test('shell authority replacement rejects a pending library read', async () => {
  const state = lifecycle(), port = shellRuntime(), session = await shellSession(state, port.runtime);
  try {
    await session.act(() => tab(session, 'libraries'));
    await session.act(() => port.authority({...authority, epoch: '2'}));
    assert.ok(port.reads.length > 2, 'the replacement authority owns a fresh opening');
    await session.act(() => {port.reads[0]!.resolve('movies'); port.reads[1]!.resolve('movies');});
    assert.deepEqual(port.destinations, []);
  } finally {await session.unmount();}
});

test('replacing a shell runtime in the same authority retires its pending opening and memory read', async () => {
  const state = lifecycle(), first = shellRuntime(), second = shellRuntime(), session = await shellSession(state, first.runtime);
  try {
    await session.act(() => tab(session, 'libraries'));
    await session.update({runtime: second.runtime});
    const writes = state.writes;
    await session.act(() => {first.reads[0]!.resolve('removed'); first.reads[1]!.resolve('movies');});
    assert.deepEqual(first.destinations, []); assert.deepEqual(second.destinations, []);
    assert.equal(state.writes, writes);
    await session.act(() => tab(session, 'libraries'));
    await session.act(() => second.reads.at(-1)!.resolve('movies'));
    assert.deepEqual(second.destinations, [{kind: 'library', libraryId: 'movies'}]);
  } finally {await session.unmount();}
});

test('Home scroll registration follows the move function and runtime rather than the transient anchor object', async () => {
  const registry = createFocusRegistry();
  const state = lifecycle(), moves: string[] = [], events: string[] = [];
  const a = (offset: number) => moves.push(`a:${offset}`), b = (offset: number) => moves.push(`b:${offset}`);
  let anchor = {move: a, offset: {value: 0}};
  function port(id: string) {
    let callback: (() => void) | undefined;
    // A real external store returns a cached snapshot; a fresh object per call
    // would re-render forever under React's identity check.
    const homeSnapshot = {phase: 'ready'};
    const runtime = {getSnapshot: () => ({authority, libraries: []}), text: {t: (key: string) => key, locale: 'en-US'},
      home: {subscribe: () => () => {}, getSnapshot: () => homeSnapshot, load: async () => {}, recoverDroppedRows: async () => {}, setRowDemand: () => {}},
      registerScrollToTop: (_destination: unknown, run: () => void) => {
        events.push(`register:${id}`); callback = run;
        return () => {events.push(`remove:${id}`); if (callback === run) callback = undefined;};
      },
    };
    return {runtime, top() {callback?.();}, get registered() {return !!callback;}};
  }
  const recovery = await screenModule(new URL('../src-next/screens/home/useTvHomeRecovery.ts', import.meta.url), {
    react: state.react, 'react-native': state.native,
    '../../../../../packages/client-core/src/presentation/errors': errorPresentation, '../../shell/model': shellModel,
  });
  const {HomeScreen} = await screenModule(new URL('../src-next/screens/home/HomeScreen.tsx', import.meta.url), {'./live-expiry': await import('../src-next/screens/home/live-expiry.ts'), '../../shell/boot-timing': {logBootMilestone: () => {}}, '../../shell/home-read-log': {noteHomeRead: () => {}, createHomeReadLog: () => () => 'home-service'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, '../../kit/overlays': {Banner: 'Banner'}, 
    '../../kit/useMotion': {useMotion: () => ({transforms: true, reduce: false, timing: () => ({})})}, 
    react: state.react, 'react-native': state.native, 'react-native-reanimated': state.animated,
    '@portico/design': {heroSpec}, '../../../../../packages/client-core/src/presentation/errors': errorPresentation,
    // Physical posture and page Back are host bridges outside this registration fixture;
    // the sibling Home registration fixture keeps the same inert seams.
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable', folds: []})},
    '../destinations/useTVPageBack': {useTVPageBack() {}}, '../../kit/focus/initial-entry': {createInitialRegionEntry},
    './home-window': homeWindow, './home-start': homeStart, '../destinations/music/MusicMixesRow': {MusicMixesRow: 'MusicMixesRow'},
    './TvHomeRecovery': {TvHomeRecovery: 'TvHomeRecovery'}, './useTvHomeRecovery': recovery,
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../packages/client-core/src/presentation/home-spotlight': {}, '../../../../../packages/client-core/src/home-selection': {},
    '../../../../../packages/client-core/src/presentation/server-text': {}, '../../../../../packages/client-core/src/presentation/kit-model': {},
    '../../kit/focus': {useFocusRegistry: () => registry}, '../../shell/tv-focus': {tvFocusIds, tvHomeRegionMap}, './tv-presentation': {tvHomeRows}, '../../shell/home-paging': {NativeHomePaging},
    '../../platform/ArtworkPipeline': {}, '../../kit/artwork': {}, '../../kit/controls': {}, '../../kit/cards': {}, '../../kit/layout': {}, '../../kit/layout/types': {},
    '../../kit/layout/FocusAnchor': {useFocusAnchor: () => anchor}, '../../kit/states': {LoadingPaint: 'LoadingPaint', HomeSkeleton: 'HomeSkeleton'},
    '../../platform/home-action-diagnostics': {homeActionDiagnostics}, '../../kit/theme': {useTheme: () => ({isTV: false, layoutMode: 'phone', platform: 'phone', spec: {hero: {homeHeight: {phone: 560}}}})},
    '../../shell/model': shellModel, './HomeHeroFacts': {HomeHeroFacts: 'HomeHeroFacts', HomeRemoteHint: 'HomeRemoteHint'},
    './model': homeModel,
  });
  const first = port('first'), second = port('second'), home = await mount(HomeScreen, {runtime: first.runtime, authority});
  try {
    first.top(); anchor = {move: a, offset: {value: 0}}; await home.update({runtime: first.runtime, authority});
    assert.deepEqual(events, ['register:first']);
    anchor = {move: b, offset: {value: 0}}; await home.update({runtime: first.runtime, authority}); first.top();
    await home.update({runtime: second.runtime, authority}); first.top(); second.top();
    assert.deepEqual(events, ['register:first', 'remove:first', 'register:first', 'remove:first', 'register:second']);
    assert.deepEqual(moves, ['a:0', 'b:0', 'b:0']); assert.equal(first.registered, false);
  } finally {await home.unmount();}
  assert.equal(second.registered, false);
});

test('original shell retirement detaches focus memory before region teardown without relaxing the accessor', async () => {
  const state = lifecycle(), port = shellRuntime(), registry = createFocusRegistry(), life = new AbortController();
  let reads = 0;
  const runtime = {...port.runtime, retirementSignal: life.signal, getSnapshot: () => {
    if (life.signal.aborted) throw Object.assign(new Error('native_shell_cancelled'), {name: 'AbortError'});
    reads++;return port.runtime.getSnapshot();
  }};
  const session = await shellSession(state, runtime, registry);
  try {
    const before = reads;
    registry.configure({id: 'original-page', order: []});assert.ok(reads > before, 'live focus publications still read the original runtime');
    life.abort();const retiredReads = reads;
    // Region's actual registry configure/release publications can precede React cleanup.
    assert.doesNotThrow(() => {registry.configure({id: 'outgoing-page', order: []});registry.mountRegion('outgoing-page', {})();});
    assert.equal(reads, retiredReads, 'no retired getSnapshot read or successor lookup');
    assert.throws(() => runtime.getSnapshot(), /native_shell_cancelled/, 'business accessor remains guarded');
  } finally {await session.unmount();}
});

// Effect replay only: this case retires one lifetime and re-invokes the very
// same committed effects, which the production renderer never does. The simple
// driver is the only harness that can replay a captured setup/cleanup pair, so
// it stays here and does not claim React StrictMode semantics.
function elements(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  if (!React.isValidElement(node)) return [];
  return [node, ...elements(node.props.children)];
}
function driverMount(component: any, initial: any) {
  let props = initial, tree: any;
  const host = createHookHost(() => {tree = component(props);});
  host.mount();
  return {host, get tree() {return tree;}, update(next: any) {props = next; host.rerender();}};
}
async function driverShellSession(state: ReturnType<typeof lifecycle>, runtime: NativeShellRuntime, registry = createFocusRegistry()) {
  const theme = createTheme({isTV: false, width: 390});
  const {NativeShell} = await screenModule(new URL('../src-next/shell/NativeShell.tsx', import.meta.url), {...stackSlotFixture, 
    './page-focus-return': {canReturnToPage, holdsPageFocus},
    '../screens/home/home-start': homeStart,
    '../kit/overlays/SurfacePaintHost': surfacePaintFixture,
    '../kit/dock/DockClearance': {DockClearanceProvider: 'DockClearanceProvider'},
    '../kit/dock/ListeningCoverBridge': listeningCoverFixture,
    '../kit/dock/PlayerMorphBridge': {PlayerMorphProvider: 'PlayerMorphProvider', useInitialPlayerPagePaint: () => undefined, usePlayerMorphPublication() {}},
    '../platform/tv-menu': {useTVMenuCapture: () => {}}, '../platform/menu-key': menuKey,
    react: state.react, 'react-native': state.native, 'react-native-reanimated': state.animated,
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '../kit/theme': {useTheme: () => theme}, '../kit/useMotion': {useMotion: () => state.motion}, './model': shellModel,
    '../kit/focus': {useFocusRegistry: () => registry}, './tv-focus': {tvFocusIds, sameDestination},
    '../kit/overlays': {OverlaysProvider: 'OverlaysProvider'}, '../kit/shell': {AccountMenu: 'AccountMenu'}, '../kit/browse': {},
    '../screens/home/HomeScreen': {HomeScreen: 'HomeScreen'}, './PhoneTabBar': {PhoneTabBar: 'PhoneTabBar'}, './MediumRail': {MediumRail: 'MediumRail'}, './chrome': {nativeScreenChromeHidden: () => false, subscribeNativeScreenChrome: () => () => {}},
    './PhoneHomeHeader': {PhoneHomeHeader: 'PhoneHomeHeader'}, './TVRail': {}, './TVProfileMenu': {}, './NativePlaybackLayer': {}, './DesktopHeader': {},
  });
  const shell = driverMount(NativeShell, {runtime});
  try {
    const session = elements(shell.tree).find(element => element.type?.name === 'ShellSession')!;
    return driverMount(session.type, session.props);
  } finally {shell.host.unmount();}
}

test('shell lifetime replay keeps the newer opening alive while retiring its previous memory read', async () => {
  const state = lifecycle(simpleReact), memoryOwner: {current?: (typeof state.effects)[number]} = {};
  const port = shellRuntime(() => {memoryOwner.current ??= state.currentEffect;}), session = await driverShellSession(state, port.runtime);
  const lifetime = state.effects[2]!, memory = memoryOwner.current;
  // Select the actual read owner, not the sidebar effect now preceding it.
  assert.ok(memory, 'the memory read must belong to a mounted effect');
  assert.equal(typeof lifetime.cleanup, 'function');
  assert.equal(typeof memory.cleanup, 'function');
  let newLifetime: void | (() => void) = undefined, newMemory: void | (() => void) = undefined;
  try {
    lifetime.cleanup!(); memory.cleanup!();
    newLifetime = lifetime.setup(); newMemory = memory.setup();
    const writes = state.writes;
    await act(() => port.reads[0]!.resolve('removed'));
    assert.equal(state.writes, writes, 'a retired read cannot mark the new session memory-ready');
    elements(session.tree).find(element => element.type === 'PhoneTabBar')!.props.onSelect('libraries');
    lifetime.cleanup!();
    await act(() => port.reads.at(-1)!.resolve('movies'));
    assert.deepEqual(port.destinations, [{kind: 'library', libraryId: 'movies'}]);
  } finally {if (typeof newMemory === 'function') newMemory(); if (typeof newLifetime === 'function') newLifetime(); session.host.unmount();}
});
