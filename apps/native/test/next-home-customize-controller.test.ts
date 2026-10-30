import assert from 'node:assert/strict';
import test from 'node:test';
import type {PersonalResource} from '../../../packages/client-core/src/personal-saved';
import type {HomeLayoutView} from '../../../packages/client-core/src/home';
import {NativeHomeCustomizeController} from '../src-next/screens/home/customize-controller';

const layout = (revision = 7): HomeLayoutView => ({revision, rowOrder: ['continue', 'recommended', 'recent'], hiddenRowIds: [], rows: [
  {id: 'continue', title: 'Continue Watching', kind: 'continue', artworkShape: 'landscape', required: true, hideable: false, reorderable: true, hidden: false},
  {id: 'recommended', title: 'Recommended for you', kind: 'recommended', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false},
  {id: 'recent', title: 'Recently added', kind: 'recent', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false},
]});
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};

test('pick-up cancellation restores exact server row order; required rows move but cannot be hidden', async () => {
  let writes = 0;
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request', home: {
    loadLayout: async () => layout(), saveLayout: async () => {writes++; return layout();}, resetLayout: async () => {writes++;},
  }});
  await controller.load();
  assert.equal(controller.getSnapshot().rows[0].id, 'continue');
  assert.equal(controller.toggle('continue'), false);
  assert.equal(controller.pick('continue'), true);
  assert.equal(controller.move(1), true);
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['recommended', 'continue', 'recent']);
  assert.equal(controller.cancelPickup(), true);
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), layout().rowOrder);
  assert.equal(controller.getSnapshot().dirty, false);
  assert.equal(writes, 0, 'moving/cancelling is a transient draft only');
});

test('Save sends the captured revision and actual IDs once, and a conflict never overwrites or retries', async () => {
  const calls: unknown[] = []; let conflict = true, revision = 7;
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request', home: {
    loadLayout: async () => layout(revision), resetLayout: async () => {},
    saveLayout: async (ids, hidden, request, expected) => {calls.push({ids, hidden, id: request(), expected}); if (conflict) throw {status: 409, code: 'revision_conflict'}; return layout(++revision);},
  }});
  await controller.load(); controller.toggle('recent');
  assert.equal(await controller.save(), false);
  assert.deepEqual(calls, [{ids: ['continue', 'recommended', 'recent'], hidden: ['recent'], id: 'request', expected: 7}]);
  assert.equal(controller.getSnapshot().showConflict, true);
  controller.keepEditing();
  assert.equal(controller.getSnapshot().showConflict, false);
  assert.equal(controller.getSnapshot().conflict, true);
  assert.equal(await controller.save(), false);
  assert.equal(calls.length, 1);
  conflict = false; revision = 8;
  await controller.load();
  assert.equal(controller.getSnapshot().dirty, false);
  assert.equal(controller.getSnapshot().conflict, false);
  controller.toggle('recent');
  assert.equal(await controller.save(), true);
  assert.equal((calls[1] as any).expected, 8);
});

test('retirement before request identity resolves prevents the write, and duplicate Save is refused', async () => {
  let current = true, writes = 0;
  const request = deferred<string>();
  const controller = new NativeHomeCustomizeController({current: () => current, requestId: () => request.promise, home: {
    loadLayout: async () => layout(), saveLayout: async () => {writes++; return layout();}, resetLayout: async () => {writes++;},
  }});
  await controller.load(); controller.toggle('recent');
  const pending = controller.save();
  assert.equal(await controller.save(), false);
  assert.equal(controller.toggle('recommended'), false);
  current = false; request.resolve('request');
  assert.equal(await pending, false);
  assert.equal(writes, 0);
});

test('late layout reads cannot publish over a newer read or a disposed screen', async () => {
  const first = deferred<HomeLayoutView>(), second = deferred<HomeLayoutView>(); let reads = 0;
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request', home: {
    loadLayout: async () => ++reads === 1 ? first.promise : second.promise, saveLayout: async () => layout(), resetLayout: async () => {},
  }});
  const a = controller.load(), b = controller.load();
  second.resolve(layout(9)); await b;
  first.resolve(layout(7)); await a;
  assert.equal(controller.getSnapshot().revision, 9);
  controller.dispose();
  assert.equal(controller.pick('continue'), false);
});


test('adding an authorized saved view is a draft until Save and refuses retired resource grants', async () => {
  const view: PersonalResource = {serverId: 'server', viewerFence: 'saved-domain-fence', id: 'view/resource', kind: 'view', name: 'Actual saved name', summary: 'Actual stored summary',
    visibility: 'private', revision: 5, role: 'owner', entryCount: 4, actions: ['pin'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 2, shares: [],
    definition: {libraryId: 'library', pivot: 'movies', query: null, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'}};
  let resourceCurrent = true; const writes: unknown[] = [];
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'save-request', savedViews: {serverId: 'server', isCurrent: candidate => resourceCurrent && candidate === view}, home: {
    loadLayout: async () => layout(), resetLayout: async () => {}, saveLayout: async (order, hidden, _id, revision) => {writes.push({order, hidden, revision}); return layout();},
  }});
  await controller.load();
  assert.equal(controller.addSavedView({...view, serverId: 'other-server'}), false);
  assert.equal(controller.addSavedView(view), true);
  assert.equal(controller.addSavedView(view), false);
  assert.equal(controller.getSnapshot().rows.at(-1)?.title, 'Actual saved name');
  assert.equal(writes.length, 0, 'choosing a view does not pin it or mutate the server');
  resourceCurrent = false;
  assert.equal(await controller.save(), false, 'retired resource authority cannot be saved from an old sheet');
  assert.equal(writes.length, 0);
  resourceCurrent = true;
  assert.equal(await controller.save(), true);
  assert.deepEqual(writes, [{order: ['continue', 'recommended', 'recent', 'view:view/resource'], hidden: [], revision: 7}]);
  controller.dispose();
});

test('the TV add-saved-view panel returns with the new row held so it moves at once', async () => {
  const view: PersonalResource = {serverId: 'server', viewerFence: 'saved-domain-fence', id: 'view/resource', kind: 'view', name: 'Actual saved name', summary: 'Actual stored summary',
    visibility: 'private', revision: 5, role: 'owner', entryCount: 4, actions: ['pin'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 2, shares: [],
    definition: {libraryId: 'library', pivot: 'movies', query: null, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'}};
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request', savedViews: {serverId: 'server', isCurrent: candidate => candidate === view}, home: {
    loadLayout: async () => layout(), resetLayout: async () => {}, saveLayout: async () => layout(),
  }});
  await controller.load();
  assert.equal(controller.addSavedView(view, true), true);
  assert.equal(controller.getSnapshot().rows.at(-1)?.id, 'view:view/resource');
  assert.equal(controller.getSnapshot().pickedId, 'view:view/resource', 'frame 29 holds the new row');
  assert.equal(controller.move(-1), true, 'the held row moves without a second pick-up');
  assert.deepEqual(controller.getSnapshot().rows.map(row => row.id), ['continue', 'recommended', 'view:view/resource', 'recent']);
  assert.equal(controller.cancelPickup(), true);
  assert.equal(controller.getSnapshot().pickedId, undefined);
  assert.equal(controller.getSnapshot().rows.at(-1)?.id, 'view:view/resource', 'Back restores the added row at the end');
  controller.dispose();
});

test('the shared draft preserves unpublished hidden IDs and excludes fixed rows from the write order', async () => {
  const original = layout(), fixed = {...original.rows[1], id: 'fixed', reorderable: false};
  const published: HomeLayoutView = {...original, rows: [original.rows[0], fixed, ...original.rows.slice(1)], hiddenRowIds: ['temporarily-unavailable']};
  const writes: unknown[] = [];
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request', home: {
    loadLayout: async () => published, resetLayout: async () => {},
    saveLayout: async (order, hidden, _request, revision) => {writes.push({order, hidden, revision}); return published;},
  }});
  try {
    await controller.load();
    assert.equal(controller.getSnapshot().dirty, false);
    controller.pick('continue');
    assert.equal(controller.move(1), false, 'fixed rows retain their drawn slot');
    controller.drop();controller.toggle('recent');
    assert.equal(await controller.save(), true);
    assert.deepEqual(writes, [{order: ['continue', 'recommended', 'recent'], hidden: ['temporarily-unavailable', 'recent'], revision: 7}]);
  } finally {controller.dispose();}
});

test('removing and readding a published saved view uses the new admitted resource and its current name', async () => {
  const view: PersonalResource = {serverId: 'server', viewerFence: 'saved-fence', id: 'saved', kind: 'view', name: 'Old name', summary: '', visibility: 'private',
    revision: 1, role: 'owner', entryCount: 1, actions: ['pin'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 1, shares: [],
    definition: {libraryId: 'library', pivot: 'movies', query: null, sort: [], presentation: 'grid'}};
  const renamed = {...view, revision: 2, name: 'Current name'};
  const original = layout(), published: HomeLayoutView = {...original, rows: [...original.rows,
    {id: 'view:saved', title: view.name, kind: 'saved_view', libraryId: 'library', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false}]};
  let admitted = view;
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request', savedViews: {serverId: 'server', isCurrent: resource => resource === admitted}, home: {
    loadLayout: async () => published, resetLayout: async () => {}, saveLayout: async () => published,
  }});
  try {
    await controller.load();controller.bindSavedViews([view]);
    assert.equal(controller.removeSavedView('view:saved'), true);
    admitted = renamed;
    assert.equal(controller.addSavedView(renamed), true);
    const row = controller.getSnapshot().rows.at(-1)!;
    assert.equal(row.title, 'Current name');assert.strictEqual(row.savedView, renamed);
    assert.equal(controller.toggle('view:saved'), true);
    assert.equal(await controller.save(), true);
  } finally {controller.dispose();}
});

test('retry retains the command ID and captured revision, and an accepted write retries only its failed reload', async () => {
  let identities = 0, reads = 0, writes = 0;
  const calls: unknown[] = [];
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => `request-${++identities}`, home: {
    loadLayout: async () => {if (++reads === 2) throw new TypeError('reload unavailable'); return layout(reads === 1 ? 7 : 8);}, resetLayout: async () => {},
    saveLayout: async (order, hidden, id, revision, lifetime) => {
      calls.push({order, hidden, id: id(), revision});
      assert.equal(lifetime?.isCurrent(), true);assert.equal(lifetime?.signal?.aborted, false);
      if (++writes === 1) throw new TypeError('reply lost'); return layout(8);
    },
  }});
  try {
    await controller.load();controller.toggle('recent');
    assert.equal(await controller.save(), false);
    assert.equal(controller.getSnapshot().retry, 'save');
    assert.equal(controller.toggle('recommended'), false, 'a retry owns the exact captured draft');
    assert.equal(await controller.retry(), false);
    assert.equal(controller.getSnapshot().phase, 'ready');
    assert.equal(controller.getSnapshot().revision, 7);
    assert.equal(controller.getSnapshot().rows.at(-1)?.hidden, true);
    assert.equal(await controller.save(), true);
    assert.equal(writes, 2, 'the acknowledged write is not sent again after its reload fails');
    assert.equal(identities, 1);
    assert.deepEqual(calls, Array(2).fill({order: ['continue', 'recommended', 'recent'], hidden: ['recent'], id: 'request-1', revision: 7}));
    assert.equal(controller.getSnapshot().revision, 8);
    assert.equal(controller.getSnapshot().dirty, false);
  } finally {controller.dispose();}
});

test('an explicit reload abandons a failed command identity even when the revision and next draft match', async () => {
  let identities = 0; const keys: string[] = [];
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => `request-${++identities}`, home: {
    loadLayout: async () => layout(), resetLayout: async () => {},
    saveLayout: async (_order, _hidden, id) => {keys.push(id());throw new TypeError('unavailable');},
  }});
  try {
    await controller.load();controller.toggle('recent');await controller.save();
    await controller.load();controller.toggle('recent');await controller.save();
    assert.deepEqual(keys, ['request-1', 'request-2']);
  } finally {controller.dispose();}
});

test('Reset confirms the exact opening and retries its own ID without accepting a stale document', async () => {
  let identities = 0; const calls: unknown[] = [];
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => `request-${++identities}`, home: {
    loadLayout: async () => layout(), saveLayout: async () => layout(),
    resetLayout: async (id, revision, lifetime) => {
      calls.push({id: id(), revision});assert.equal(lifetime?.isCurrent(), true);
      if (calls.length === 1) throw new TypeError('reply lost');
    },
  }});
  try {
    await controller.load();const stale = controller.captureReset(controller.getSnapshot().layout)!;
    await controller.load();
    assert.equal(await controller.resetConfirmed(stale), false);
    const opening = controller.captureReset(controller.getSnapshot().layout)!;
    assert.equal(await controller.resetConfirmed(opening), false);
    assert.equal(controller.getSnapshot().retry, 'reset');
    assert.equal(await controller.save(), false, 'Save cannot replace the pending reset command');
    assert.equal(await controller.retry(), true);
    assert.deepEqual(calls, [{id: 'request-1', revision: 7}, {id: 'request-1', revision: 7}]);
    assert.equal(identities, 1);
  } finally {controller.dispose();}
});

for (const code of ['home_layout_conflict', 'revision_conflict', 'revision_mismatch']) {
  test(`Reset ${code} remains blocked after dismissing the notice until a successful reload`, async () => {
    let writes = 0, failedRead = false;
    const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request', home: {
      loadLayout: async () => {if (failedRead) throw new TypeError('offline'); return layout();}, saveLayout: async () => layout(),
      resetLayout: async () => {writes++;throw {code};},
    }});
    try {
      await controller.load();const opening = controller.captureReset(controller.getSnapshot().layout)!;
      assert.equal(await controller.resetConfirmed(opening), false);
      controller.keepEditing();controller.toggle('recent');
      assert.equal(controller.getSnapshot().conflict, true);
      assert.equal(controller.getSnapshot().showConflict, false);
      assert.equal(await controller.save(), false);assert.equal(await controller.retry(), false);
      assert.equal(await controller.resetConfirmed(opening), false);
      failedRead = true;await controller.load();
      assert.equal(await controller.save(), false);
      failedRead = false;await controller.load();
      assert.equal(controller.getSnapshot().conflict, false);
      assert.equal(controller.getSnapshot().dirty, false);assert.equal(writes, 1);
    } finally {controller.dispose();}
  });
}

for (const kind of ['save', 'reset'] as const) {
  test(`disposing a pending ${kind} aborts its captured write lifetime and prevents the reload`, async () => {
    const done = deferred<void>(); let reads = 0;
    let lifetime: {signal?: AbortSignal; isCurrent: () => boolean} | undefined;
    const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request', home: {
      loadLayout: async () => {reads++;return layout();},
      saveLayout: async (_order, _hidden, _id, _revision, captured) => {lifetime = captured;await done.promise;return layout();},
      resetLayout: async (_id, _revision, captured) => {lifetime = captured;await done.promise;},
    }});
    await controller.load();controller.toggle('recent');
    const writing = kind === 'save' ? controller.save() : controller.resetConfirmed(controller.captureReset(controller.getSnapshot().layout)!);
    await Promise.resolve();
    assert.equal(lifetime?.isCurrent(), true);
    assert.equal(await controller.save(), false);assert.equal(await controller.retry(), false);
    controller.dispose();
    assert.equal(lifetime?.signal?.aborted, true);assert.equal(lifetime?.isCurrent(), false);
    done.resolve();assert.equal(await writing, false);assert.equal(reads, 1);
  });
}
