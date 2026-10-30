import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {createStableFocusOrder} from '../src-next/kit/focus/stable-order.ts';
import {createTheme} from '../src-next/kit/theme.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text';
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const key = (row: {id: string}) => row.id;

test('stable native order uses only the last committed projection, current source membership and the same scope', () => {
  const owner = createStableFocusOrder(key), rows = ['b', 'c', 'd'].map(id => ({id, title: id})), scope = {};
  const input = {scope, held: false, items: rows, allItems: rows, memberIds: new Set(rows.map(key))};
  owner.commit(scope, owner.project(input));
  assert.deepEqual(owner.project({...input, held: true, items: rows.slice(1)}), rows);
  assert.deepEqual(owner.project({...input, held: true, allItems: [rows[0], rows[2]]}), rows, 'retired facts do not invent removal of a still-owned source member');
  assert.deepEqual(owner.project({...input, held: true, memberIds: new Set(['b', 'd'])}), [rows[0], rows[2]], 'a genuine removal remains authoritative');
  owner.project({...input, scope: {}, items: [], held: true});
  assert.deepEqual(owner.project({...input, held: true}), rows, 'an abandoned new-scope render cannot mutate the committed order');
  assert.deepEqual(owner.project({...input, scope: {}, items: [], held: true}), [], 'a new playback/viewer scope cannot borrow old UI rows');
  assert.deepEqual(owner.project({...input, held: false, items: rows.slice(2)}), rows.slice(2), 'leaving ownership admits the actual current Up next order');
});

async function fixture() {
  const host = hooks(), registry = createFocusRegistry(), effects: (() => void)[] = [], calls: unknown[] = [];
  const react = {...host.react, useLayoutEffect: (run: () => void) => effects.push(run)};
  const modules = {react: {...react, default: react}};
  const {useStableFocusOrder} = await screenModule(new URL('../src-next/kit/focus/useStableFocusOrder.ts', import.meta.url), {
    ...modules, './FocusRegion': {useOptionalFocusRegistry: () => registry}, './stable-order': {createStableFocusOrder},
  });
  const {useTVQueueSongMenu} = await screenModule(new URL('../src-next/screens/destinations/music/TVQueueSongMenu.tsx', import.meta.url), {
    ...modules, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/overlays': {Menu: 'Menu'},
  });
  const {NowPlayingQueue, nowPlayingQueueFocusScope, nowPlayingQueueEditPort} = await screenModule(new URL('../src-next/screens/destinations/music/NowPlayingQueue.tsx', import.meta.url), {
    ...modules, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/presentation/kit-model/text': {durationText},
    '../../../kit/artwork': {Artwork: 'Artwork'}, '../../../kit/controls': {Focusable: 'Focusable'},
    '../../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../../kit/focus/useStableFocusOrder': {useStableFocusOrder},
    './TVQueueSongMenu': {useTVQueueSongMenu}, '../../../kit/overlays': {Menu: 'Menu'}, '../../../kit/theme': {useTheme: () => createTheme({isTV: true, width: 1920})},
    '../../../kit/browse/presentation': {browseType: () => ({})},
    '../../../kit/overlays/CommittedPaint': {committedPaintScope: () => ({}), useCommittedPaintPublication() {}},
    './ListeningPanePaint': {ListeningQueuePaint: 'ListeningQueuePaint'},
  });
  const entries = ['a', 'b', 'c', 'd'].map(id => ({id, hidden: false, removed: false}));
  const props: any = {testID: 'queue', regionId: 'queue-region', focusScopeKey: 'actual-fence-A', onPlay: (...args: unknown[]) => calls.push(args),
    view: {queue: {id: 'real-queue', revision: '1', currentEntryId: 'a', entries}, items: entries.map(entry => ({entryId: entry.id, itemId: entry.id, title: entry.id, durationMs: 100000}))}};
  let requests = 0;
  const render = () => {const tree = host.render(() => NowPlayingQueue(props)); effects.splice(0).forEach(run => run()); return tree;};
  const bind = (tree: any) => {
    const region = nodes(tree).find(node => node.type === 'FocusRegion'); registry.configure(region.props);
    for (const row of nodes(tree).filter(node => node.type === 'Focusable')) registry.attach({regionId: 'queue-region', childId: row.props.focusId}, {requestTVFocus() {requests++;}});
  };
  return {props, calls, registry, render, bind, nowPlayingQueueFocusScope, nowPlayingQueueEditPort, requests: () => requests};
}
const rows = (tree: any) => nodes(tree).filter(node => node.type === 'Focusable');
const ids = (tree: any) => rows(tree).map(row => row.props.focusId);

test('TV Up next retains the genuine playing row and declared row key while focus or its original-return lease owns the queue', async () => {
  const f = await fixture(); let tree = f.render(); f.bind(tree);
  assert.deepEqual(ids(tree), ['queue-b', 'queue-c', 'queue-d']);
  f.registry.didFocus({regionId: 'queue-region', childId: 'queue-b'});
  f.props.view = {...f.props.view, queue: {...f.props.view.queue, currentEntryId: 'b', revision: '2'}};
  tree = f.render(); assert.deepEqual(ids(tree), ['queue-b', 'queue-c', 'queue-d']);
  assert.equal(rows(tree)[0].props.key, 'b');
  assert.ok(nodes(rows(tree)[0]).some(node => node.type === 'Text' && node.props.children.includes(defaultI18n.t('player.queueNowPlaying'))));
  const release = f.registry.retain(f.registry.capture()!); f.registry.didFocus({regionId: 'song-menu', childId: 'play-now'});
  f.props.view = {...f.props.view, queue: {...f.props.view.queue, currentEntryId: 'c', revision: '3'}};
  tree = f.render(); assert.deepEqual(ids(tree), ['queue-b', 'queue-c', 'queue-d'], 'a retained original opener keeps the list steady through menu paint exit');
  release(); f.registry.didFocus({regionId: 'transport', childId: 'pause'});
  assert.deepEqual(ids(f.render()), ['queue-d']); assert.equal(f.requests(), 0, 'source arrivals do not request any focus move');
});

test('genuine queue removal retains the surviving native order and current commands use the actual revision', async () => {
  const f = await fixture(); let tree = f.render(); f.bind(tree); f.registry.didFocus({regionId: 'queue-region', childId: 'queue-c'});
  f.props.view = {...f.props.view, queue: {...f.props.view.queue, revision: '4', entries: f.props.view.queue.entries.filter((entry: any) => entry.id !== 'c')}};
  tree = f.render(); assert.deepEqual(ids(tree), ['queue-b', 'queue-d']); rows(tree)[1].props.onPress();
  assert.deepEqual(f.calls, [['real-queue', 'd', '4']]);
  f.props.focusScopeKey = 'actual-fence-B'; f.props.view = {...f.props.view, queue: {...f.props.view.queue, currentEntryId: 'd'}};
  assert.deepEqual(ids(f.render()), [], 'the still-focused old region cannot retain rows across a new actual fence');
});

test('a TV queue without a play port still exposes read-only native stops without a press callback', async () => {
  const f = await fixture(); f.props.onPlay = undefined;
  const tree = f.render(); assert.deepEqual(ids(tree), ['queue-b', 'queue-c', 'queue-d']);
  for (const row of rows(tree)) {assert.equal(row.props.disabled, true); assert.equal(row.props.focusableWhenDisabled, true); assert.equal(row.props.onPress, undefined);}
});


test('the actual queue-run scope survives an entry fence change but rejects another authority or run; presentation admission dominates readonly focus', async () => {
  const f = await fixture();
  const fence: any = {kind: 'queue', playback: {scope: {serverId: 'served-server', accountId: 'account', profileId: 'viewer', queueId: 'queue'}, run: 4}, sessionId: 'session-a', entryId: 'a', generation: 1};
  assert.equal(f.nowPlayingQueueFocusScope(fence), f.nowPlayingQueueFocusScope({...fence, sessionId: 'session-b', entryId: 'b', generation: 2}));
  assert.notEqual(f.nowPlayingQueueFocusScope(fence), f.nowPlayingQueueFocusScope({...fence, playback: {...fence.playback, run: 5}}));
  assert.notEqual(f.nowPlayingQueueFocusScope(fence), f.nowPlayingQueueFocusScope({...fence, playback: {...fence.playback, scope: {...fence.playback.scope, profileId: 'another'}}}));
  f.props.interactive = false; const tree = f.render();
  assert.equal(nodes(tree).find(node => node.type === 'FocusRegion').props.enabled, false);
  for (const row of rows(tree)) {assert.equal(row.props.disabled, true); assert.equal(row.props.focusableWhenDisabled, false); assert.equal(row.props.onPress, undefined);}
});


test('the TV song binding needs an accepted exact queue window and forwards the original fence and revision to its existing guard', async () => {
  const f = await fixture(), calls: unknown[] = [], fence: any = {kind: 'queue', playback: {scope: {}, run: 1}};
  const lease = {}, edit = {enabled: true, lease, apply: (...args: unknown[]) => calls.push(args)};
  assert.equal(f.nowPlayingQueueEditPort(f.props.view, fence, edit), undefined);
  const view = {...f.props.view, window: {queueId: 'real-queue', revision: '1', segments: [], entries: [{entryId: 'b', position: 1, available: true}]}};
  const port = f.nowPlayingQueueEditPort(view, fence, edit)!;
  assert.equal(port.lease, lease); assert.deepEqual(port.entry('b'), view.window.entries[0]);
  port.apply({action: 'remove', entryId: 'b'}, 'real-queue', '1');
  assert.deepEqual(calls, [[fence, {action: 'remove', entryId: 'b'}, 'real-queue', '1']]);
  for (const window of [{...view.window, revision: 'retired'}, {...view.window, queueId: 'another'}, {...view.window, entries: [...view.window.entries, ...view.window.entries]}]) {
    assert.equal(f.nowPlayingQueueEditPort({...view, window}, fence, edit), undefined);
  }
  assert.equal(f.nowPlayingQueueEditPort(view, fence, {...edit, enabled: false}), undefined);
});

test('the actual TV row supplies Hold OK only from its current accepted source and keeps the shared menu mounted for exit', async () => {
  const f = await fixture(), lease = {}, fenceKey = 'original-track-fence', edits: unknown[] = [];
  f.props.menuScope = {lease, fenceKey};
  f.props.view = {...f.props.view, window: {queueId: 'real-queue', revision: '1', segments: [], entries: [{entryId: 'b', available: true, position: 1}]}};
  f.props.queueEdit = {enabled: true, lease, fenceKey, entry: () => f.props.view.window.entries[0], apply: (...args: unknown[]) => edits.push(args)};
  let tree = f.render(), row = rows(tree)[0]; assert.equal(typeof row.props.onLongPressAction, 'function');
  assert.equal(rows(tree)[1].props.onLongPressAction, undefined, 'an unknown row has no invented admission');
  row.props.onLongPressAction(); tree = f.render();
  let menu = nodes(tree).find(node => node.type === 'Menu'); assert.equal(menu.props.open, true); assert.equal(menu.props.presentation, 'sideSheet');
  assert.deepEqual(menu.props.items.map((item: any) => item.id), ['play-now', 'remove']);
  const old = menu.props.items[1].onSelect; menu.props.onOpenChange(false); tree = f.render(); menu = nodes(tree).find(node => node.type === 'Menu');
  assert.equal(menu.props.open, false, 'logical close preserves the shared menu host for actual-exit restoration');
  old(); assert.deepEqual(edits, [], 'a retired outgoing paint callback has no command');
});
