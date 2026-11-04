// #689 review: on TV, pressing Show all in Live TV's filtered-empty state unmounts the focused button and swaps the
// grid's focus region for the guide's own, so nothing focused is left. The guide must then ask for its own entry
// (tv-focus 24: the programme on now on the first channel), never leave focus to native recovery. The ask is one
// shot: bound to the revision Show all produces, cancelled by any key from the remote after the press, and made only
// while the page can take focus (not covered, still the Live destination, visible, no media expanded).
//
// The fake native focus engine with the real focus kit, Focusable, the real `useScreenEntry` (settings-live
// Screen.tsx), the real `useTVKeyEvent` (platform/tv-keys) and the rules LiveScreen's rows use (`liveGuideEntry`,
// `guideReentryNext`, `livePageTakesFocus`). The scene mirrors LiveScreen's TV structure: the page layer (an autoFocus
// CoveredLayer), the filter chips region, then the grid region holding either the filtered-empty state's Show all or
// the guide rows (keyed by the guide revision, the first row asking for entry once its channel has arrived).
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';
import * as keyAdapter from '../src-next/platform/tv-key-adapter.ts';
import * as keySource from '../src-next/platform/tv-key-source.ts';
import {guideReentryNext, liveGuideEntry, livePageTakesFocus, type GuideReentry, type GuideReentryEvent} from '../src-next/screens/settings-live/tv-settings-focus.ts';

const g = globalThis as any;
g.requestAnimationFrame ??= (run: () => void) => setTimeout(run, 0);
g.cancelAnimationFrame ??= (id: unknown) => clearTimeout(id as ReturnType<typeof setTimeout>);
const frames = () => new Promise(resolve => setTimeout(resolve, 5));

type Shell = {destination: {kind: string}; contentVisible?: boolean; mediaExpanded?: boolean};
async function scene(platform: 'android' | 'tvos', {showAllAsks = true, rowsHeld = false}: {showAllAsks?: boolean; rowsHeld?: boolean} = {}) {
  const kit = await tvFocusKit({platform}), {e, engine, focus, Stop, React} = kit, n = engine.native;
  const {useScreenEntry} = await screenModule(new URL('../src-next/screens/settings-live/Screen.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': n, '../../kit/theme': {useTheme: () => ({isTV: true, colors: {}})},
    '../../kit/shell/visual': {shellType: () => ({})}, '../../kit/focus': focus, '../../kit/artwork': {StandardBackdrop: () => null}});
  const {useTVKeyEvent} = await screenModule(new URL('../src-next/platform/tv-keys.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': n, './tv-key-adapter': keyAdapter, './tv-key-source': keySource});
  const registry = focus.createFocusRegistry();
  const filters = 'live.filters', grid = 'live.grid';
  const shell: Shell = {destination: {kind: 'live'}, contentVisible: true, mediaExpanded: false};
  const ctl = {arrive: () => {}, bump: () => {}, cover: (_covered: boolean) => {}};
  // One guide row: the first asks for the programme on now when the rule says so, once its channel has arrived and
  // while the page can take it; layout stands in as a layout effect (the fake engine fires no onLayout).
  const Row = ({index, ready, entryEnabled, entryOpen, onCell}: {index: number; ready: boolean; entryEnabled: boolean; entryOpen: () => boolean; onCell: () => void}) => {
    const covered = focus.useCovered(), coveredRef = React.useRef(covered), openRef = React.useRef(entryOpen); coveredRef.current = covered; openRef.current = entryOpen;
    const current = React.useCallback(() => livePageTakesFocus({covered: coveredRef.current, destination: shell.destination.kind, contentVisible: shell.contentVisible,
      mediaExpanded: shell.mediaExpanded}) && openRef.current(), []);
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
    const reentryRef = React.useRef(undefined as GuideReentry | undefined);
    ctl.arrive = () => setReady(true); ctl.bump = () => setRevision((value: number) => value + 1); ctl.cover = setCovered;
    const step = (event: GuideReentryEvent) => {
      const next = guideReentryNext(reentryRef.current, event);
      if (next !== reentryRef.current) {reentryRef.current = next; setReentry(next);}
    };
    React.useLayoutEffect(() => {step({kind: 'revision', revision});}, [revision]);
    useTVKeyEvent((event: {key: string}) => step({kind: 'key', key: event.key}));
    const empty = filter === 'news';
    const showAll = () => {if (showAllAsks) step({kind: 'press', revision}); setFilter('all'); setRevision((value: number) => value + 1);};
    return e(focus.FocusProvider, {registry}, e(focus.CoveredLayer, {covered, autoFocus: true, testID: 'native-shell-base', style: {position: 'absolute', inset: 0}},
      e(focus.FocusRegion, {id: filters, orientation: 'horizontal', entry: filter, edges: {down: grid}, style: {flexDirection: 'row', gap: 12, height: 80}},
        e(Stop, {id: 'chip-all', focusId: 'all', w: 120, h: 60, onPress: () => {setFilter('all'); setRevision((value: number) => value + 1);}}),
        e(Stop, {id: 'chip-news', focusId: 'news', w: 120, h: 60})),
      empty
        ? e(focus.FocusRegion, {id: grid, edges: {up: filters}, style: {flex: 1}}, e(Stop, {id: 'show-all', focusId: 'show-all', w: 300, h: 80, onPress: showAll}))
        : e(focus.FocusRegion, {id: grid, edges: {up: filters}, style: {flex: 1}},
          [0, 1].map(index => e(Row, {key: `${revision}:${index}`, index, ready, onCell: () => step({kind: 'focus'}),
            entryEnabled: liveGuideEntry(revision, 0, 0, reentry), entryOpen: () => reentry === undefined || reentryRef.current !== undefined})))));
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

  for (const [name, hold] of [
    ['the page is covered', (s: any) => s.ctl.cover(true)],
    ['the destination is not Live', (s: any) => {s.shell.destination = {kind: 'home'};}],
    ['media is expanded', (s: any) => {s.shell.mediaExpanded = true;}],
    ['the content is not visible', (s: any) => {s.shell.contentVisible = false;}],
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
  for (const key of ['up', 'down', 'left', 'right', 'menu', 'playPause']) {
    assert.equal(run(press, {kind: 'key', key}), undefined, `${key} after the press cancels`);
    assert.equal(run(press, rev(5), {kind: 'key', key}), undefined, `${key} after the target cancels`);
    assert.equal(run(press, {kind: 'key', key}, rev(5)), undefined, `${key} cancelled it for good`);
  }
  assert.deepEqual(run(press, {kind: 'key', key: 'select'}), {from: 4}, 'the select that pressed Show all is not the person moving');
  assert.equal(run(press, {kind: 'focus'}), undefined, 'a guide cell taking focus');
  assert.equal(run(press, rev(5), {kind: 'focus'}), undefined);
  assert.equal(run(press, {kind: 'filter'}), undefined, 'a filter chosen again');
  assert.equal(run({kind: 'key', key: 'up'}), undefined, 'a key with nothing armed arms nothing');
  assert.deepEqual(run({kind: 'press', revision: 2}, rev(3), {kind: 'key', key: 'up'}, press), {from: 4}, 'a new press arms afresh');
});

test('the page takes a focus request only when it is uncovered, the Live destination, visible and not under expanded media', () => {
  const open = {covered: false, destination: 'live', contentVisible: true, mediaExpanded: false};
  assert.equal(livePageTakesFocus(open), true);
  assert.equal(livePageTakesFocus({...open, contentVisible: undefined}), true, 'an unset visibility is visible, as revealTVCell reads it');
  assert.equal(livePageTakesFocus({...open, covered: true}), false);
  assert.equal(livePageTakesFocus({...open, destination: 'home'}), false);
  assert.equal(livePageTakesFocus({...open, contentVisible: false}), false);
  assert.equal(livePageTakesFocus({...open, mediaExpanded: true}), false);
});
