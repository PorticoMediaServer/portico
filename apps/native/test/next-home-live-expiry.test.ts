// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {LIVE_REREAD_JITTER_MS, LIVE_REREAD_MAX_MS, LIVE_REREAD_MIN_MS, liveRereadPlan, type LiveRereadMemory} from '../src-next/screens/home/live-expiry.ts';
import {createTheme} from '../src-next/kit/theme.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const liveRow = (observedAt: number, expiresAt: number) => ({id: 'live', live: {observedAt: new Date(observedAt).toISOString(), expiresAt: new Date(expiresAt).toISOString()}}) as never;

test('E-mobile-29: a server that always answers with an expiry 2 s ahead is read at most once per 30 s, never in a loop', () => {
  let now = Date.parse('2026-10-29T12:00:00Z'), memory: LiveRereadMemory = {stalls: 0};
  const reads: number[] = [];
  while (now < Date.parse('2026-10-29T12:10:00Z')) {
    const plan = liveRereadPlan([liveRow(now, now + 2_000)], memory, () => 0.5)!;
    assert.ok(plan.delayMs >= LIVE_REREAD_MIN_MS, `never sooner than 30 s (${plan.delayMs})`);
    memory = {expiry: plan.expiry, stalls: plan.stalls};
    now += plan.delayMs; reads.push(now);
  }
  assert.ok(reads.length <= 20, `${reads.length} reads in 10 minutes`);
  for (let i = 1; i < reads.length; i++) assert.ok(reads[i]! - reads[i - 1]! >= LIVE_REREAD_MIN_MS);
});

test('the same or an already-expired expiry backs off exponentially, up to 10 minutes', () => {
  const at = Date.parse('2026-10-29T12:00:00Z');
  let memory: LiveRereadMemory = {stalls: 0};
  const delays: number[] = [];
  for (let i = 0; i < 7; i++) {
    const plan = liveRereadPlan([liveRow(at, at + 45_000)], memory, () => 0)!;
    delays.push(plan.delayMs); memory = {expiry: plan.expiry, stalls: plan.stalls};
  }
  assert.deepEqual(delays, [45_000, 60_000, 120_000, 240_000, 480_000, LIVE_REREAD_MAX_MS, LIVE_REREAD_MAX_MS]);
  const expired = liveRereadPlan([liveRow(at, at - 5_000)], {stalls: 0}, () => 0)!;
  assert.equal(expired.delayMs, 60_000, 'an expiry already past never fires at once');
});

test('time left is measured on the server clock, and clients spread out with jitter', () => {
  // A device clock far ahead of the server changes nothing: only expiresAt − observedAt counts.
  const server = Date.parse('2026-10-29T12:00:00Z');
  assert.equal(liveRereadPlan([liveRow(server, server + 50_000)], {stalls: 0}, () => 0)!.delayMs, 50_000);
  const spread = liveRereadPlan([liveRow(server, server + 50_000)], {stalls: 0}, () => 0.999)!.delayMs;
  assert.ok(spread > 50_000 && spread < 50_000 + LIVE_REREAD_JITTER_MS);
  assert.equal(liveRereadPlan([{id: 'films'} as never], {stalls: 0}), undefined, 'no live row, no timer');
});

test('Home keeps one timer per live expiry, only for live rows in view, so neither hydration nor an off-screen row drives re-reads', async () => {
  const timers = new Map<number, {fn: () => void; delay: number}>();
  let nextTimerId = 1, createdCount = 0;
  const originalSetTimeout = globalThis.setTimeout;
  const originalClearTimeout = globalThis.clearTimeout;
  (globalThis as any).setTimeout = (fn: () => void, delay = 0) => {
    const id = nextTimerId++;
    createdCount++;
    timers.set(id, {fn, delay});
    return id;
  };
  (globalThis as any).clearTimeout = (id: any) => {timers.delete(Number(id));};

  const authority = {serverId: 'srv', accountId: 'acc', profileId: 'prof', epoch: '["fam",false]', viewerFence: 'opened', privacyFence: 'opened'};
  const theme = createTheme({isTV: false, width: 390});
  let mountedSet = new Set<number>([0]);
  const {useHome} = await screenModule(new URL('../src-next/pages/shared/home/useHome.ts', import.meta.url), {
    react: {...React, default: React},
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, useWindowDimensions: () => ({width: 390, height: 844, fontScale: 1})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 34, left: 0, right: 0})},
    'react-native-reanimated': {runOnJS: (fn: any) => fn, useAnimatedScrollHandler: (fn: any) => fn, useSharedValue: (v: any) => React.useRef({value: v}).current},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/home-spotlight': {
      homeRowPresentation: (row: any) => ({title: {fallback: row.title}, subtitle: undefined, cards: []}),
      homeSpotlightModels: () => [],
      homeTitleFactParts: () => [],
    },
    '../../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({category: 'other'})},
    '../../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_l: unknown, text: any, fb?: string) => text?.fallback ?? fb ?? ''},
    '../../../kit/layout/FocusAnchor': {useFocusAnchor: () => React.useMemo(() => ({offset: {value: 0}, scrollRef: {current: null}, scrollStyle: {}, move() {}, stop() {}}), [])},
    '../../../kit/home': {homeRowMetrics: () => ({estimate: 220, gap: 16, cardWidth: 120})},
    '../../../kit/frames/metrics': {useFrameMetrics: () => ({width: 390, form: 'compact'})},
    '../../../kit/states': {skeletonTiming: {delayMs: 300, slowMs: 2500}},
    '../../../kit/theme': {useTheme: () => theme},
    '../../../shell/home-read-log': {noteHomeRead() {}},
    '../../../shell/boot-timing': {logBootMilestone() {}},
    '../../../shell/model': {phoneHeaderGeometry: () => ({top: 54, height: 104}), sameAuthority: () => true},
    '../../../shell/tv-focus': {tvFocusIds: () => ({entry: (e: any) => e.id})},
    '../../../screens/home/home-start': {homeSpotlightFocusActions: () => ({offers: {}})},
    '../../../screens/home/home-window': {homeMountedRows: () => mountedSet, homeVisibleRange: () => [0, 0]},
    '../../../screens/home/live-expiry': {liveRereadPlan},
    '../../../screens/home/model': {authorizedHome: (doc: any) => doc, homeRowHasContent: () => true, pageAtVisibleEnd: () => false, platformRows: (doc: any) => doc.rows},
  });

  const observed = Date.parse('2026-10-29T12:00:00Z');
  const expiry = new Date(observed + 45_000).toISOString();
  const makeDoc = (rows: any[]) => ({spotlight: [], rows});
  const onNow = {id: 'live', title: 'On now', kind: 'live', artworkShape: 'landscape', entries: [], live: {observedAt: new Date(observed).toISOString(), expiresAt: expiry, channels: []}};
  const movies = {id: 'movies', title: 'Movies', kind: 'recent', artworkShape: 'poster', entries: [{id: 'm1', kind: 'movie', title: 'Film'}]};

  let homeSnapshot = {phase: 'ready', document: makeDoc([movies, onNow])};
  const shellSnapshot = {authority, libraries: [], selectionActive: false, librariesUnavailable: false};
  let revalidates = 0;
  const listeners = new Set<() => void>();
  const runtime: any = {
    text: defaultI18n,
    home: {
      subscribe: (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};},
      getSnapshot: () => homeSnapshot,
      demand() {},
      load: async () => {},
      revalidate: async () => {revalidates++;},
    },
    getSnapshot: () => shellSnapshot,
    registerScrollToTop: () => () => {},
    setHomeWindow() {},
    actions: {seeAll() {}, customize() {}},
    reportError() {},
  };

  const host = createFabricHookHost(() => {useHome({runtime, authority});});
  try {
    // 1) Live row is off-screen (only row index 0 = `movies` is mounted/demanded) -> zero timers scheduled.
    mountedSet = new Set([0]);
    await act(() => host.mount());
    assert.equal(timers.size, 0, 'an off-screen live row never schedules a reread timer');

    // 2) Scroll brings live row (index 1) into demanded rows -> exactly one timer scheduled using server clock.
    mountedSet = new Set([0, 1]);
    await act(() => host.rerender());
    assert.equal(timers.size, 1, 'demanded live row schedules one timer');
    const [firstTimer] = [...timers.values()];
    assert.ok(firstTimer.delay >= 45_000 && firstTimer.delay <= 45_000 + LIVE_REREAD_JITTER_MS, `server-clock delay (${firstTimer.delay} ms)`);
    const createdAfterFirst = createdCount;

    // 3) Re-render with a fresh document object carrying the SAME live expiry (e.g. row hydration)
    //    keeps the exact existing timer without creating a duplicate or resetting it.
    homeSnapshot = {phase: 'ready', document: makeDoc([movies, {...onNow}])};
    await act(() => {for (const fn of [...listeners]) fn();});
    assert.equal(createdCount, createdAfterFirst, 'same expiry across hydration creates no new timer');
    assert.equal(timers.size, 1);

    // 4) When the timer fires, Home revalidates once.
    firstTimer.fn();
    assert.equal(revalidates, 1, 'timer expiry triggers Home revalidation');
  } finally {
    await host.unmount();
    globalThis.setTimeout = originalSetTimeout;
    globalThis.clearTimeout = originalClearTimeout;
  }
});
