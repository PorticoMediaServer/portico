import test from 'node:test';
import assert from 'node:assert/strict';
import type {SavedApi} from '../../../packages/client-core/src/saved';
import {NativeLibrarySaveView} from '../src-next/screens/library/save-view';
import {createDestinationEntryFocus} from '../src-next/screens/destinations/focus';
import {createFocusRegistry} from '../src-next/kit/focus/registry';
import {createNativeTVNameInput} from '../src-next/tv/library/keyboard-state';

const scope = {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])};
const definition = {libraryId: 'movies', pivot: 'movies', query: {field: 'favorite', operator: 'equals', value: true},
  sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'} as const;
const operationId = '12345678-1234-4123-8123-123456789abc';
const directory = {serverId: 'server', viewerFence: 'actual-viewer', revision: 1, resources: [], nextCursor: ''};

test('every explicit TV name-keyboard opening gets first-key focus once, after its real host attaches', () => {
  let requests = 0;
  const controller = new NativeLibrarySaveView({api: {request: async () => {throw new Error('unexpected_name_entry_io');}}, scope, definition,
    current: () => true, requestId: async () => operationId, saved: () => {throw new Error('unexpected_save');}});
  controller.setName('Actual suggested conditions');
  const input = createNativeTVNameInput(controller), registry = createFocusRegistry(), request = createDestinationEntryFocus(registry);
  const first = {regionId: 'actual-name-keyboard', childId: 'a'};
  registry.configure({id: first.regionId, entry: first.childId});
  try {
    assert.equal(input.openKeyboard(), true);
    assert.equal(request(input.getSnapshot().keyboard, first), false, 'unmounted keys do not consume entry');
    registry.attach(first, {requestTVFocus() {requests++;}});
    assert.equal(request(input.getSnapshot().keyboard, first), true);
    const revision = input.getSnapshot().keyboard.revision;
    assert.equal(input.press('b'), true);
    assert.equal(input.openKeyboard(), true);
    assert.equal(input.getSnapshot().keyboard.revision, revision);
    assert.equal(request(input.getSnapshot().keyboard, first), false, 'typing and repeated OK cannot steal focus');
    assert.equal(input.back(), 'keyboard');
    assert.equal(request(input.getSnapshot().keyboard, first), false);
    assert.equal(input.openKeyboard(), true);
    assert.equal(input.getSnapshot().keyboard.revision, revision + 1);
    assert.equal(request(input.getSnapshot().keyboard, first), true, 'a second opening cannot reuse the completed first ticket');
    assert.equal(requests, 2);
    assert.equal(input.back(), 'keyboard'); assert.equal(input.back(), 'close');
    assert.equal(controller.getSnapshot().open, true, 'only the actual form owner handles the returned close intent');
    assert.equal(input.getSnapshot(), input.getSnapshot());
  } finally {input.dispose(); controller.dispose(); registry.attach(first, null);}
});

test('TV name editing keeps Unicode bounds and the exact uncertain Save operation owned by the real controller', async () => {
  let fail = true;
  const bodies: unknown[] = [], saved: string[] = [];
  const api: SavedApi = {request: async <T>(_path: string, method?: string, body?: unknown) => {
    if (method !== 'POST') return directory as T;
    bodies.push(body);
    if (fail) throw Object.assign(new Error('offline'), {code: 'network_error', retryable: true});
    return {serverId: 'server', viewerFence: 'actual-viewer', receipt: {operationId, revision: 2, resourceId: 'created-view', deleted: false}} as T;
  }};
  const controller = new NativeLibrarySaveView({api, scope, definition, current: () => true, requestId: async () => operationId, saved: id => {saved.push(id);}});
  const input = createNativeTVNameInput(controller);
  try {
    controller.setName('🎬'.repeat(200)); input.openKeyboard();
    assert.equal(input.press('a'), false); assert.equal(Array.from(controller.getSnapshot().name).length, 200);
    assert.equal(input.press('delete'), true); assert.equal(Array.from(controller.getSnapshot().name).length, 199);
    assert.equal(input.press('a'), true);
    assert.throws(() => input.press('@'), /not_drawn/);
    controller.setName('Keep exact pending name');
    await controller.save();
    assert.equal(controller.getSnapshot().nameLocked, true); assert.equal(controller.getSnapshot().open, true);
    assert.equal(input.getSnapshot().keyboard.visible, false);
    assert.equal(input.openKeyboard(), false); assert.equal(input.press('clear'), false);
    assert.equal(controller.getSnapshot().name, 'Keep exact pending name');
    input.dispose();
    assert.equal(controller.getSnapshot().open, true, 'retiring the borrowed keyboard cannot discard an unresolved Save receipt');
    assert.equal(input.openKeyboard(), false); assert.equal(input.back(), 'blocked');
    fail = false; await controller.save();
    assert.deepEqual(bodies[1], bodies[0]); assert.deepEqual(saved, ['created-view']);
    assert.equal(input.getSnapshot().form.open, false); assert.equal(input.getSnapshot().form.name, '');
  } finally {input.dispose(); controller.dispose();}
});

test('busy and retired form lifetimes reject TV keyboard callbacks without issuing another mutation', async () => {
  let current = true, finish!: (value: unknown) => void, entered!: () => void, posts = 0, saved = 0;
  const started = new Promise<void>(resolve => {entered = resolve;});
  const api: SavedApi = {request: async <T>(_path: string, method?: string) => {
    if (method === 'POST') {posts++; throw new Error('unexpected_retired_mutation');}
    entered(); return new Promise<T>(resolve => {finish = value => resolve(value as T);});
  }};
  const controller = new NativeLibrarySaveView({api, scope, definition, current: () => current, requestId: async () => operationId, saved: () => {saved++;}});
  const input = createNativeTVNameInput(controller);
  try {
    controller.setName('Current name'); input.openKeyboard();
    const pending = controller.save(); await started;
    assert.equal(input.back(), 'blocked'); assert.equal(input.press('a'), false); assert.equal(input.openKeyboard(), false);
    current = false;
    assert.equal(input.getSnapshot().form.open, false);
    assert.equal(input.getSnapshot().form.name, '');
    assert.equal(input.getSnapshot().keyboard.visible, false);
    assert.equal(input.openKeyboard(), false); assert.equal(input.back(), 'blocked');
    finish(directory); await pending;
    assert.equal(posts, 0); assert.equal(saved, 0);
  } finally {input.dispose(); controller.dispose();}
});
