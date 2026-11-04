// #689 review: on TV, pressing Show all in Live TV's filtered-empty state unmounts the focused button and swaps the
// grid's focus region for the guide's own, so nothing focused is left. The guide must then ask for its own entry
// (tv-focus 24: the programme on now on the first channel), never leave focus to native recovery. The ask is one
// shot: bound to the revision Show all produces, cancelled by anything the remote or Back does after the press (raw TV
// events, swipes and pans included; Android's Back through BackHandler), by leaving Live or opening one of its pages
// (Recordings), and by focus moving to somewhere other than where native recovery left it; and made only while the
// page can take focus (not covered, still the Live destination, visible, no media expanded, home not obscured).
//
// The fake native focus engine with the real focus kit, Focusable, the real `useScreenEntry` (settings-live
// Screen.tsx), the real `useTVRemoteActivity` (platform/tv-keys) and the rules LiveScreen's rows use (`liveGuideEntry`,
// `guideReentryNext`, `livePageTakesFocus`, `guideFocusMoved`). The scene mirrors LiveScreen's TV structure: the page layer (an autoFocus
// CoveredLayer), the filter chips region, then the grid region holding either the filtered-empty state's Show all or
// the guide rows (keyed by the guide revision, the first row asking for entry once its channel has arrived).
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';
import {GUIDE_RECOVERY_SETTLE_MS, guideFocusMoved, guideReentryNext, liveGuideEntry, livePageTakesFocus, type GuideReentry, type GuideReentryEvent} from '../src-next/screens/settings-live/tv-settings-focus.ts';

const g = globalThis as any;
g.requestAnimationFrame ??= (run: () => void) => setTimeout(run, 0);
g.cancelAnimationFrame ??= (id: unknown) => clearTimeout(id as ReturnType<typeof setTimeout>);
const frames = () => new Promise(resolve => setTimeout(resolve, 5));
// Native recovery's landing is taken as the baseline this long after the target revision (the backstop's reference).
const recovered = () => new Promise(resolve => setTimeout(resolve, GUIDE_RECOVERY_SETTLE_MS + 50));

type Shell = {destination: {kind: string}; contentVisible?: boolean; mediaExpanded?: boolean; homeObscured?: boolean};
// `noTVEvents`: Android's Back never reaches TVEventHandler, so only BackHandler can see it.
async function scene(platform: 'android' | 'tvos', {showAllAsks = true, rowsHeld = false, noTVEvents = false}: {showAllAsks?: boolean; rowsHeld?: boolean; noTVEvents?: boolean} = {}) {
  const kit = await tvFocusKit({platform}), {e, engine, focus, Stop, React} = kit, n = engine.native;
  const {useScreenEntry} = await screenModule(new URL('../src-next/screens/settings-live/Screen.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': n, '../../kit/theme': {useTheme: () => ({isTV: true, colors: {}})},
    '../../kit/shell/visual': {shellType: () => ({})}, '../../kit/focus': focus, '../../kit/artwork': {StandardBackdrop: () => null}});
  const {useTVRemoteActivity} = await screenModule(new URL('../src-next/platform/tv-keys.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': noTVEvents ? {...n, useTVEventHandler: () => {}} : n, './tv-key-adapter': {}, './tv-key-source': {}});
  const registry = focus.createFocusRegistry();
  const filters = 'live.filters', grid = 'live.grid';
  const shell: Shell = {destination: {kind: 'live'}, contentVisible: true, mediaExpanded: false, homeObscured: false};
  const ctl = {arrive: () => {}, bump: () => {}, cover: (_covered: boolean) => {}};
  // One guide row: the first asks for the programme on now when the rule says so, once its channel has arrived and
  // while the page can take it; layout stands in as a layout effect (the fake engine fires no onLayout).
  const Row = ({index, ready, entryEnabled, entryOpen, onCell}: {index: number; ready: boolean; entryEnabled: boolean; entryOpen: () => boolean; onCell: () => void}) => {
    const covered = focus.useCovered(), coveredRef = React.useRef(covered), openRef = React.useRef(entryOpen); coveredRef.current = covered; openRef.current = entryOpen;
    const current = React.useCallback(() => livePageTakesFocus({covered: coveredRef.current, destination: shell.destination.kind, contentVisible: shell.contentVisible,
      mediaExpanded: shell.mediaExpanded, homeObscured: shell.homeObscured}) && openRef.current(), []);
    const entry = useScreenEntry(entryEnabled && ready && index === 0 ? {regionId: grid, childId: `row-${index}-now`} : undefined, current);
    React.useLayoutEffect(() => {entry();}, [entry]);
    if (!ready) return e(n.View, {style: {height: 100}});
    return e(n.View, {style: {flexDirection: 'row', gap: 12, height: 100}},
      e(Stop, {id: `row-${index}-channel`, focusId: `row-${index}-channel`, w: 160, h: 90, onFocus: onCell}),
      e(Stop, {id: `row-${index}-now`, focusId: `row-${index}-now`, w: 400, h: 90, onFocus: onCell}),
      e(Stop, {id: `row-${index}-next`, focusId: `row-${index}-next`, w: 400, h: 90, onFocus: onCell}));
  };
  const Live = () => {
    const [filter, setFilter] = React.useState('news') as ['all' | 'news', (value: 'all' | 'news') => void];
    const [revision, setRevision] = React.useState(3) as [number, (update: (value: number) => number) => void];
    const [ready, setReady] = React.useState(!rowsHeld) as [boolean, (value: boolean) => void];
    const [covered, setCovered] = React.useState(false) as [boolean, (value: boolean) => void];
    const [reentry, setReentry] = React.useState(undefined) as [GuideReentry | undefined, (value: GuideReentry | undefined) => void];
    const [recordingsOpen, setRecordingsOpen] = React.useState(false) as [boolean, (value: boolean) => void];
    const [recordingsReturn, setRecordingsReturn] = React.useState(0) as [number, (update: (value: number) => number) => void];
    const reentryRef = React.useRef(undefined as GuideReentry | undefined), baseline = React.useRef(undefined as {at?: {regionId: string; childId: string}} | undefined);
    ctl.arrive = () => setReady(true); ctl.bump = () => setRevision((value: number) => value + 1); ctl.cover = setCovered;
    const step = (event: GuideReentryEvent) => {
      const next = guideReentryNext(reentryRef.current, event);
      if (event.kind === 'press' || next === undefined) baseline.current = undefined;
      if (next !== reentryRef.current) {reentryRef.current = next; setReentry(next);}
    };
    useTVRemoteActivity(reentry !== undefined, (raw: {eventType?: string}) => step({kind: 'remote', eventType: raw.eventType}));
    React.useLayoutEffect(() => {
      step({kind: 'revision', revision});
      const armed = reentryRef.current;
      if (armed?.target !== revision || baseline.current) return;
      const settle = setTimeout(() => {if (reentryRef.current === armed && !baseline.current) baseline.current = {at: registry.attachedFocus()};}, GUIDE_RECOVERY_SETTLE_MS);
      return () => clearTimeout(settle);
    }, [revision]);
    React.useLayoutEffect(() => {if (recordingsOpen) step({kind: 'leave'});}, [recordingsOpen]);
    // The restore a return from Recordings makes (useDestinationEntryFocus): its own control, not the guide's entry.
    React.useEffect(() => {if (recordingsReturn > 0) registry.focus({regionId: 'live.controls', childId: 'live-recordings'});}, [recordingsReturn]);
    const empty = filter === 'news';
    const showAll = () => {if (showAllAsks) step({kind: 'press', revision}); setFilter('all'); setRevision((value: number) => value + 1);};
    if (recordingsOpen) return e(focus.FocusProvider, {registry}, e(focus.CoveredLayer, {covered, autoFocus: true, testID: 'native-shell-base', style: {position: 'absolute', inset: 0}},
      e(focus.FocusRegion, {id: 'recordings.page', style: {flex: 1}},
        e(Stop, {id: 'recordings-guide', focusId: 'recordings-guide', w: 200, h: 60, onPress: () => {setRecordingsReturn((value: number) => value + 1); setRecordingsOpen(false);}}))));
    return e(focus.FocusProvider, {registry}, e(focus.CoveredLayer, {covered, autoFocus: true, testID: 'native-shell-base', style: {position: 'absolute', inset: 0}},
      e(focus.FocusRegion, {id: 'live.controls', orientation: 'horizontal', edges: {down: filters}, style: {flexDirection: 'row', height: 60}},
        e(Stop, {id: 'live-recordings', focusId: 'live-recordings', w: 160, h: 50, onPress: () => setRecordingsOpen(true)})),
      e(focus.FocusRegion, {id: filters, orientation: 'horizontal', entry: filter, edges: {up: 'live.controls', down: grid}, style: {flexDirection: 'row', gap: 12, height: 80}},
        e(Stop, {id: 'chip-all', focusId: 'all', w: 120, h: 60, onPress: () => {setFilter('all'); setRevision((value: number) => value + 1);}}),
        e(Stop, {id: 'chip-news', focusId: 'news', w: 120, h: 60})),
      empty
        ? e(focus.FocusRegion, {id: grid, edges: {up: filters}, style: {flex: 1}}, e(Stop, {id: 'show-all', focusId: 'show-all', w: 300, h: 80, onPress: showAll}))
        : e(focus.FocusRegion, {id: grid, edges: {up: filters}, style: {flex: 1}},
          [0, 1].map(index => e(Row, {key: `${revision}:${index}`, index, ready, onCell: () => step({kind: 'focus'}),
            entryEnabled: liveGuideEntry(revision, 0, 0, reentry), entryOpen: () => reentry === undefined || reentryRef.current !== undefined && !guideFocusMoved(baseline.current, registry.attachedFocus())})))));
  };
  return {...kit, Live, ctl, shell};
}
const requests = (engine: any) => engine.log.filter((step: any) => step.why === 'request').length;

for (const platform of ['android', 'tvos'] as const) {
  test(`${platform}: Show all lands on the programme on now on the first channel, by the guide's own request`, async () => {
    const s = await scene(platform), {engine, e, Live} = s;
    await engine.render(e(Live, {}));
    await engine.focus('show-all');
    await engine.press('select');
    await frames(); await engine.settle();
    assert.equal(engine.focused, 'row-0-now', `${platform}: the guide's entry (tv-focus 24)`);
    assert.equal(engine.log.filter((step: any) => step.to !== undefined).at(-1)?.why, 'request', `${platform}: a request, not the engine's recovery`);
    // Once in the guide, a later revision asks nothing: entry never pulls focus back.
    await engine.press('right');
    assert.equal(engine.focused, 'row-0-next');
    await engine.unmount();
  });

  test(`${platform}: without the ask, Show all leaves focus to native recovery, which does not land on the guide entry`, async () => {
    // The control for the test above: the same scene with Show all not asking (the bug the review found).
    const s = await scene(platform, {showAllAsks: false}), {engine, e, Live} = s;
    await engine.render(e(Live, {}));
    await engine.focus('show-all');
    await engine.press('select');
    await frames(); await engine.settle();
    assert.notEqual(engine.focused, 'row-0-now', `${platform}: recovery picked ${engine.focused}`);
    await engine.unmount();
  });

  test(`${platform}: rows that arrive after Show all, with nothing pressed since, still get the entry ask`, async () => {
    // The control for the cancel test: the held-rows scene itself lands on the entry when the person has not moved.
    const s = await scene(platform, {rowsHeld: true}), {engine, e, Live, ctl} = s;
    await engine.render(e(Live, {}));
    await engine.focus('show-all');
    await engine.press('select');
    await frames(); await engine.settle();
    assert.notEqual(engine.focused, 'row-0-now', 'the rows are not there yet');
    ctl.arrive(); await frames(); await engine.settle();
    assert.equal(engine.focused, 'row-0-now');
    await engine.unmount();
  });

  test(`${platform}: rows that arrive after recovery has settled, with nothing pressed since, still get the ask: recovery's landing is not the person's`, async () => {
    // Recovery lands on the grid region's host on Android (no registered target) and on a control on tvOS (the registry
    // learns it late); either way it is the baseline the backstop compares against, not a move.
    const s = await scene(platform, {rowsHeld: true}), {engine, e, Live, ctl} = s;
    await engine.render(e(Live, {}));
    await engine.focus('show-all');
    await engine.press('select');
    await frames(); await engine.settle();
    const landing = engine.focused;
    await recovered();
    assert.equal(engine.focused, landing, `${platform}: recovery landed on ${landing}`);
    ctl.arrive(); await frames(); await engine.settle();
    assert.equal(engine.focused, 'row-0-now');
    await engine.unmount();
  });

  test(`${platform}: Up pressed while the guide loads cancels the ask, and focus stays where Up put it once the rows arrive`, async () => {
    const s = await scene(platform, {rowsHeld: true}), {engine, e, Live, ctl} = s;
    await engine.render(e(Live, {}));
    await engine.focus('show-all');
    await engine.press('select');
    await frames(); await engine.settle();
    await engine.press('up');
    const placed = engine.focused, before = requests(engine);
    assert.ok(placed !== undefined && placed !== 'row-0-now');
    ctl.arrive(); await frames(); await engine.settle();
    assert.equal(engine.focused, placed, `${platform}: the rows arriving did not move focus`);
    assert.equal(requests(engine), before, 'no entry request was made');
    await engine.unmount();
  });

  test(`${platform}: a revision after the one Show all produced never asks again, after landing or after a cancel`, async () => {
    // After landing: a later revision (a sort, source or view change, or a return from Recordings) remounts the rows.
    const landed = await scene(platform), {engine, e, Live, ctl} = landed;
    await engine.render(e(Live, {}));
    await engine.focus('show-all');
    await engine.press('select');
    await frames(); await engine.settle();
    assert.equal(engine.focused, 'row-0-now');
    await engine.press('right');
    await engine.press('down');
    const settled = engine.focused, before = requests(engine);
    ctl.bump(); await frames(); await engine.settle();
    ctl.bump(); await frames(); await engine.settle();
    assert.equal(requests(engine), before, `${platform}: no entry request after a later revision`);
    assert.notEqual(engine.focused, 'row-0-now', `${platform}: focus left at ${settled}, recovered to ${engine.focused}`);
    await engine.unmount();

    // After a cancel: the revision bump while the rows are still held, then they arrive on a later revision.
    const cancelled = await scene(platform, {rowsHeld: true});
    await cancelled.engine.render(cancelled.e(cancelled.Live, {}));
    await cancelled.engine.focus('show-all');
    await cancelled.engine.press('select');
    await frames(); await cancelled.engine.settle();
    cancelled.ctl.bump(); await frames(); await cancelled.engine.settle();
    cancelled.ctl.arrive(); await frames(); await cancelled.engine.settle();
    assert.equal(requests(cancelled.engine), 0, `${platform}: rows that arrive on a revision after the target are not asked`);
    assert.notEqual(cancelled.engine.focused, 'row-0-now');
    await cancelled.engine.unmount();
  });

  // Show all, with the rows still to arrive, and recovery settled.
  const showAllLoading = async (options: {noTVEvents?: boolean} = {}) => {
    const s = await scene(platform, {rowsHeld: true, ...options});
    await s.engine.render(s.e(s.Live, {}));
    await s.engine.focus('show-all');
    await s.engine.press('select');
    await frames(); await s.engine.settle();
    return s;
  };

  test(`${platform}: Back pressed while the guide loads disarms the ask`, async () => {
    // Android: Back never reaches TVEventHandler, so only BackHandler sees it (the scene drops the raw events).
    // tvOS: Back is Menu, which reaches both; the BackHandler listener must not consume it.
    const s = await showAllLoading({noTVEvents: platform === 'android'}), {engine, ctl} = s;
    const placed = engine.focused, before = requests(engine);
    await engine.press(platform === 'android' ? 'back' : 'menu');
    assert.equal(engine.log.filter((step: any) => step.why === 'back').at(-1)?.handled, false, 'the listener did not consume Back');
    ctl.arrive(); await frames(); await engine.settle();
    assert.equal(requests(engine), before, `${platform}: no entry request after Back`);
    assert.notEqual(engine.focused, 'row-0-now');
    assert.equal(engine.focused, placed);
    await engine.unmount();
  });

  for (const gesture of ['swipeLeft', 'swipeDown', 'pan', 'channelUp', 'pageDown']) {
    test(`${platform}: a ${gesture} event while the guide loads disarms the ask (the adapter drops it; the raw listener sees it)`, async () => {
      // Swipes and pans are tvOS-only on a real device; the raw TV event path is the same on both, so both run it.
      const s = await showAllLoading(), {engine, ctl} = s;
      const before = requests(engine);
      await engine.press(gesture);
      ctl.arrive(); await frames(); await engine.settle();
      assert.equal(requests(engine), before, `${platform}: no entry request after ${gesture}`);
      assert.notEqual(engine.focused, 'row-0-now');
      await engine.unmount();
    });
  }

  test(`${platform}: Select into Recordings and back leaves the restore to live-recordings alone; the ask does not fire on the same revision`, async () => {
    const s = await showAllLoading(), {engine, ctl} = s;
    await engine.focus('live-recordings');
    await engine.press('select');
    await frames(); await engine.settle();
    assert.ok(engine.has('recordings-guide'), 'the Recordings page is open');
    await engine.focus('recordings-guide');
    await engine.press('select');
    await frames(); await engine.settle();
    assert.equal(engine.focused, 'live-recordings', 'the restore landed');
    const before = requests(engine);
    ctl.arrive(); await frames(); await engine.settle();
    assert.equal(requests(engine), before, `${platform}: no entry request after the return`);
    assert.equal(engine.focused, 'live-recordings', `${platform}: the restore won`);
    await engine.unmount();
  });

  test(`${platform}: focus moved mid-load by input the adapter does not report is the person's: the rows arriving do not take it`, async () => {
    const s = await showAllLoading(), {engine, ctl} = s;
    await recovered();
    await engine.focus('chip-all');
    const before = requests(engine);
    ctl.arrive(); await frames(); await engine.settle();
    assert.equal(requests(engine), before, `${platform}: no entry request`);
    assert.equal(engine.focused, 'chip-all');
    await engine.unmount();
  });

  for (const [name, hold] of [
    ['the page is covered', (s: any) => s.ctl.cover(true)],
    ['the destination is not Live', (s: any) => {s.shell.destination = {kind: 'home'};}],
    ['media is expanded', (s: any) => {s.shell.mediaExpanded = true;}],
    ['the content is not visible', (s: any) => {s.shell.contentVisible = false;}],
    ['home is obscured', (s: any) => {s.shell.homeObscured = true;}],
  ] as const) {
    test(`${platform}: no entry ask when the rows arrive while ${name}`, async () => {
      const s = await scene(platform, {rowsHeld: true}), {engine, e, Live, ctl} = s;
      await engine.render(e(Live, {}));
      await engine.focus('show-all');
      await engine.press('select');
      await frames(); await engine.settle();
      hold(s); await frames(); await engine.settle();
      const before = requests(engine);
      ctl.arrive(); await frames(); await engine.settle();
      assert.equal(requests(engine), before, `${platform}: ${name}: no request`);
      assert.notEqual(engine.focused, 'row-0-now');
      await engine.unmount();
    });
  }
}

test('the rule: the first draw enters, a return from Recordings or the source page does not, and Show all enters once, on the revision it produced', () => {
  assert.equal(liveGuideEntry(0, 0, 0), true);
  assert.equal(liveGuideEntry(0, 1, 0), false);
  assert.equal(liveGuideEntry(0, 0, 1), false);
  assert.equal(liveGuideEntry(4, 0, 0), false, 'a later revision on its own never enters');
  assert.equal(liveGuideEntry(4, 0, 0, {from: 4}), false, 'not on the revision Show all was pressed at');
  assert.equal(liveGuideEntry(5, 0, 0, {from: 4}), true, 'on the next one, before it is recorded as the target');
  assert.equal(liveGuideEntry(5, 0, 0, {from: 4, target: 5}), true, 'on the target');
  assert.equal(liveGuideEntry(6, 0, 0, {from: 4, target: 5}), false, 'never on a later one');
});

test('the one-shot reducer: armed at the press, the first revision is the target, and a key, a cell focus, a filter or a later revision disarms it for good', () => {
  const run = (...events: GuideReentryEvent[]) => events.reduce<GuideReentry | undefined>((state, event) => guideReentryNext(state, event), undefined);
  const press = {kind: 'press', revision: 4} as const, rev = (revision: number) => ({kind: 'revision', revision}) as const;
  assert.deepEqual(run(press), {from: 4});
  assert.deepEqual(run(press, rev(4)), {from: 4}, 'the same revision records nothing');
  assert.deepEqual(run(press, rev(5)), {from: 4, target: 5}, 'the first change is the target');
  assert.deepEqual(run(press, rev(5), rev(5)), {from: 4, target: 5});
  assert.equal(run(press, rev(5), rev(6)), undefined, 'a later revision disarms');
  assert.equal(run(press, rev(5), rev(6), rev(5)), undefined, 'and never re-arms, even back at the target');
  assert.equal(run(press, rev(5), rev(6), rev(7)), undefined);
  assert.equal(run(rev(5)), undefined, 'a revision with nothing armed arms nothing');
  for (const key of ['up', 'down', 'left', 'right', 'menu', 'back', 'playPause', 'swipeLeft', 'swipeUp', 'pan', 'channelUp', 'pageDown']) {
    assert.equal(run(press, {kind: 'remote', eventType: key}), undefined, `${key} after the press cancels`);
    assert.equal(run(press, rev(5), {kind: 'remote', eventType: key}), undefined, `${key} after the target cancels`);
    assert.equal(run(press, {kind: 'remote', eventType: key}, rev(5)), undefined, `${key} cancelled it for good`);
  }
  assert.deepEqual(run(press, {kind: 'remote', eventType: 'select'}), {from: 4}, 'the select that pressed Show all is not the person moving');
  assert.equal(run(press, {kind: 'focus'}), undefined, 'a guide cell taking focus');
  assert.equal(run(press, rev(5), {kind: 'focus'}), undefined);
  assert.equal(run(press, {kind: 'filter'}), undefined, 'a filter chosen again');
  assert.equal(run({kind: 'remote', eventType: 'up'}), undefined, 'a key with nothing armed arms nothing');
  assert.deepEqual(run({kind: 'press', revision: 2}, rev(3), {kind: 'remote', eventType: 'up'}, press), {from: 4}, 'a new press arms afresh');
});

test('the page takes a focus request only when it is uncovered, the Live destination, visible and not under expanded media', () => {
  const open = {covered: false, destination: 'live', contentVisible: true, mediaExpanded: false};
  assert.equal(livePageTakesFocus(open), true);
  assert.equal(livePageTakesFocus({...open, contentVisible: undefined}), true, 'an unset visibility is visible, as revealTVCell reads it');
  assert.equal(livePageTakesFocus({...open, covered: true}), false);
  assert.equal(livePageTakesFocus({...open, destination: 'home'}), false);
  assert.equal(livePageTakesFocus({...open, contentVisible: false}), false);
  assert.equal(livePageTakesFocus({...open, mediaExpanded: true}), false);
  assert.equal(livePageTakesFocus({...open, homeObscured: true}), false, 'the page Back handler requires home not obscured; so does the ask');
});

test('leaving Live or opening one of its pages disarms for good; the focus engine reports are not the person moving', () => {
  const run = (...events: GuideReentryEvent[]) => events.reduce<GuideReentry | undefined>((state, event) => guideReentryNext(state, event), undefined);
  const press = {kind: 'press', revision: 4} as const, rev = (revision: number) => ({kind: 'revision', revision}) as const;
  assert.equal(run(press, {kind: 'leave'}), undefined);
  assert.equal(run(press, rev(5), {kind: 'leave'}, rev(5)), undefined, 'coming back at the target revision never re-arms');
  for (const eventType of ['select', 'longSelect', 'focus', 'blur']) assert.deepEqual(run(press, {kind: 'remote', eventType}), {from: 4}, `${eventType} is not the person moving`);
  assert.equal(run(press, {kind: 'remote'}), undefined, 'an event with no type still counts as input');
});

test('the backstop: focus is moved only when it is somewhere other than where native recovery left it', () => {
  const chip = {regionId: 'live.filters', childId: 'all'}, other = {regionId: 'live.filters', childId: 'news'};
  assert.equal(guideFocusMoved(undefined, chip), false, 'recovery has not settled: nothing to compare');
  assert.equal(guideFocusMoved({at: undefined}, undefined), false, 'recovery left focus on the page host and it is still there');
  assert.equal(guideFocusMoved({at: undefined}, chip), true, 'focus went to a real target from the host');
  assert.equal(guideFocusMoved({at: chip}, chip), false, 'recovery landed on a chip and it is still there');
  assert.equal(guideFocusMoved({at: chip}, other), true);
  assert.equal(guideFocusMoved({at: chip}, undefined), true);
});
