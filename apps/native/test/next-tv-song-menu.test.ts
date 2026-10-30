import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';
async function fixture() {
  const host = hooks(), effects: (() => void)[] = [], calls: unknown[] = [], lease = {};
  const react = {...host.react, useLayoutEffect: (run: () => void) => effects.push(run)};
  const {useTVQueueSongMenu} = await screenModule(new URL('../src-next/screens/destinations/music/TVQueueSongMenu.tsx', import.meta.url), {
    react: {...react, default: react}, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/overlays': {Menu: 'Menu'},
  });
  const facts: any = {available: true, position: 1}, row = {entryId: 'served-song', title: 'Served song', current: false};
  const input: any = {enabled: true, queueId: 'served-queue', revision: '7', context: {scopeKey: 'original-run', fenceKey: 'original-track', lease},
    upcomingIds: new Set([row.entryId]), entry: () => facts, onPlay: (...args: unknown[]) => calls.push(['play', ...args]),
    edit: {enabled: true, fenceKey: 'original-track', lease, entry: () => facts, apply: (...args: unknown[]) => calls.push(['edit', ...args])}, testID: 'queue'};
  const render = (commit = true) => {const value = host.render(() => useTVQueueSongMenu(input)); if (commit) effects.splice(0).forEach(run => run()); return value;};
  const open = () => {render().open(row); return render().menu;};
  return {input, row, facts, calls, render, open};
}
test('the song menu offers Play now then Remove using the captured real queue/revision and default shared return owner', async () => {
  const f = await fixture(); let menu = f.open();
  assert.deepEqual(menu.props.items.map((item: any) => [item.id, item.disabled]), [['play-now', false], ['remove', false]]);
  assert.equal(menu.props.returnFocusRef, undefined); assert.equal(menu.props.initialFocusId, undefined);
  menu.props.items[0].onSelect(); assert.deepEqual(f.calls, [['play', 'served-queue', 'served-song', '7']]);
  menu.props.onOpenChange(false); menu = f.open(); menu.props.items[1].onSelect();
  assert.deepEqual(f.calls[1], ['edit', {action: 'remove', entryId: 'served-song'}, 'served-queue', '7']);
});
test('closing and reopening retires old callbacks even on the same row, revision and context', async () => {
  const f = await fixture(), first = f.open(); first.props.onOpenChange(false);
  f.open(); first.props.items.forEach((item: any) => item.onSelect()); assert.deepEqual(f.calls, []);
});
test('scope, fence, lease, revision, eligibility and missing facts invalidate callbacks before layout retirement', async () => {
  const mutations = [
    (f: any) => {f.input.context = {...f.input.context, scopeKey: 'another-run'};},
    (f: any) => {f.input.context = {...f.input.context, fenceKey: 'another-track'};},
    (f: any) => {f.input.context = {...f.input.context, lease: {}};},
    (f: any) => {f.input.revision = '8';}, (f: any) => {f.input.enabled = false;},
    (f: any) => {f.input.upcomingIds = new Set();}, (f: any) => {f.facts.available = false;},
    (f: any) => {delete f.facts.position;}, (f: any) => {f.input.edit = {...f.input.edit, enabled: false};},
  ];
  for (const mutate of mutations) {
    const f = await fixture(), menu = f.open(); mutate(f); assert.equal(f.render(false).menu.props.open, false);
    menu.props.items.forEach((item: any) => item.onSelect()); assert.deepEqual(f.calls, []);
  }
});
test('play-only and edit-only admission never upgrades missing facts or silently enables an unavailable action', async () => {
  const f = await fixture(), originalEdit = f.input.edit; f.input.edit = undefined; delete f.facts.position;
  let menu = f.open(); assert.deepEqual(menu.props.items.map((item: any) => item.disabled), [false, true]);
  menu.props.onOpenChange(false); f.input.onPlay = undefined;
  assert.equal(f.render().canOpen(f.row), false);
  f.input.edit = originalEdit; f.facts.position = 1; menu = f.open();
  assert.deepEqual(menu.props.items.map((item: any) => item.disabled), [true, false], 'edit-only capability leaves Play now disabled');
  menu.props.onOpenChange(false);
  f.input.entry = () => undefined; assert.equal(f.render().canOpen(f.row), false);
  f.input.entry = () => f.facts; f.input.context = undefined; assert.equal(f.render().canOpen(f.row), false);
});
