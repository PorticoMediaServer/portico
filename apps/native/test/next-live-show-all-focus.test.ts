// #689 review: on TV, pressing Show all in Live TV's filtered-empty state unmounts the focused button and swaps the
// grid's focus region for the guide's own, so nothing focused is left. The guide must then ask for its own entry
// (tv-focus 24: the programme on now on the first channel), never leave focus to native recovery.
//
// The fake native focus engine with the real focus kit, Focusable and the real `useScreenEntry` (settings-live
// Screen.tsx) and `liveGuideEntry` (the rule LiveScreen's rows use). The scene mirrors LiveScreen's TV structure: the
// page layer (an autoFocus CoveredLayer), the filter chips region, then the grid region holding either the
// filtered-empty state's Show all or the guide rows (keyed by the guide revision, the first row asking for entry).
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';
import {liveGuideEntry} from '../src-next/screens/settings-live/tv-settings-focus.ts';

const g = globalThis as any;
g.requestAnimationFrame ??= (run: () => void) => setTimeout(run, 0);
g.cancelAnimationFrame ??= (id: unknown) => clearTimeout(id as ReturnType<typeof setTimeout>);
const frames = () => new Promise(resolve => setTimeout(resolve, 5));

async function scene(platform: 'android' | 'tvos', showAllAsks: boolean) {
  const kit = await tvFocusKit({platform}), {e, engine, focus, Stop, React} = kit, n = engine.native;
  const {useScreenEntry} = await screenModule(new URL('../src-next/screens/settings-live/Screen.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': n, '../../kit/theme': {useTheme: () => ({isTV: true, colors: {}})},
    '../../kit/shell/visual': {shellType: () => ({})}, '../../kit/focus': focus, '../../kit/artwork': {StandardBackdrop: () => null}});
  const registry = focus.createFocusRegistry();
  const filters = 'live.filters', grid = 'live.grid';
  // One guide row: the first asks for the programme on now when the rule says so; layout stands in as a layout effect
  // (the fake engine fires no onLayout).
  const Row = ({index, entryEnabled}: {index: number; entryEnabled: boolean}) => {
    const entry = useScreenEntry(entryEnabled && index === 0 ? {regionId: grid, childId: `row-${index}-now`} : undefined, () => true);
    React.useLayoutEffect(() => {entry();}, [entry]);
    return e(n.View, {style: {flexDirection: 'row', gap: 12, height: 100}},
      e(Stop, {id: `row-${index}-channel`, focusId: `row-${index}-channel`, w: 160, h: 90}),
      e(Stop, {id: `row-${index}-now`, focusId: `row-${index}-now`, w: 400, h: 90}),
      e(Stop, {id: `row-${index}-next`, focusId: `row-${index}-next`, w: 400, h: 90}));
  };
  const Live = () => {
    const [filter, setFilter] = React.useState('news') as ['all' | 'news', (value: 'all' | 'news') => void];
    const [revision, setRevision] = React.useState(3) as [number, (update: (value: number) => number) => void];
    const [reentry, setReentry] = React.useState(undefined) as [number | undefined, (value: number | undefined) => void];
    const empty = filter === 'news';
    const showAll = () => {if (showAllAsks) setReentry(revision); setFilter('all'); setRevision((value: number) => value + 1);};
    return e(focus.FocusProvider, {registry}, e(focus.CoveredLayer, {covered: false, autoFocus: true, testID: 'native-shell-base', style: {position: 'absolute', inset: 0}},
      e(focus.FocusRegion, {id: filters, orientation: 'horizontal', entry: filter, edges: {down: grid}, style: {flexDirection: 'row', gap: 12, height: 80}},
        e(Stop, {id: 'chip-all', focusId: 'all', w: 120, h: 60, onPress: () => {setFilter('all'); setRevision((value: number) => value + 1);}}),
        e(Stop, {id: 'chip-news', focusId: 'news', w: 120, h: 60})),
      empty
        ? e(focus.FocusRegion, {id: grid, edges: {up: filters}, style: {flex: 1}}, e(Stop, {id: 'show-all', focusId: 'show-all', w: 300, h: 80, onPress: showAll}))
        : e(focus.FocusRegion, {id: grid, edges: {up: filters}, style: {flex: 1}},
          [0, 1].map(index => e(Row, {key: `${revision}:${index}`, index, entryEnabled: liveGuideEntry(revision, 0, 0, reentry)})))));
  };
  return {...kit, Live};
}

for (const platform of ['android', 'tvos'] as const) {
  test(`${platform}: Show all lands on the programme on now on the first channel, by the guide's own request`, async () => {
    const s = await scene(platform, true), {engine, e, Live} = s;
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
    const s = await scene(platform, false), {engine, e, Live} = s;
    await engine.render(e(Live, {}));
    await engine.focus('show-all');
    await engine.press('select');
    await frames(); await engine.settle();
    assert.notEqual(engine.focused, 'row-0-now', `${platform}: recovery picked ${engine.focused}`);
    await engine.unmount();
  });
}

test('the rule: the first draw enters, a return from Recordings or the source page does not, and Show all enters once on the next revision', () => {
  assert.equal(liveGuideEntry(0, 0, 0), true);
  assert.equal(liveGuideEntry(0, 1, 0), false);
  assert.equal(liveGuideEntry(0, 0, 1), false);
  assert.equal(liveGuideEntry(4, 0, 0), false, 'a later revision on its own never enters');
  assert.equal(liveGuideEntry(4, 0, 0, 4), false, 'not on the revision Show all was pressed at');
  assert.equal(liveGuideEntry(5, 0, 0, 4), true, 'on the next one');
});
