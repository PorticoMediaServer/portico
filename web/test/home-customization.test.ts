import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeLayoutView} from '@core/home.ts';
import {HomeCustomization, type HomeCustomizationBindings} from '../src/screens/home/customization.ts';
const layout: HomeLayoutView = {revision: 4, rowOrder: ['continue', 'new'], hiddenRowIds: [], rows: [
  {id: 'continue', title: 'Continue Watching', kind: 'continue', artworkShape: 'landscape', required: true, hideable: false, reorderable: true, hidden: false},
  {id: 'new', title: 'New on your server', kind: 'recent', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false},
]};
const bindings: HomeCustomizationBindings = {createSavedViews: () => {throw Error('picker not opened');}, requestId: () => 'actual-idempotency-key', isCurrent: () => true};
test('a conflict keeps the original draft/revision and refuses another save until an explicit fresh read', async () => {
  let revision = 4, writes = 0;const requests: unknown[] = [];
  const controller = new HomeCustomization({loadLayout: async () => ({...layout, revision}), saveLayout: async (order, hidden, key, expectedRevision) => {
    requests.push({order, hidden, key: key(), expectedRevision});writes++;
    if (expectedRevision !== revision) throw Object.assign(Error('changed'), {code: 'home_layout_conflict'});
    return {...layout, revision: ++revision};
  }, resetLayout: async () => {}}, bindings);
  try {
    await controller.load();controller.show('new', false);revision = 5;
    assert.equal(await controller.save(), false);assert.equal(controller.getSnapshot().conflict, true);
    assert.equal(controller.getSnapshot().draft?.view.revision, 4);assert.equal(controller.getSnapshot().draft?.hidden.has('new'), true);
    controller.move('new', 0);assert.equal(await controller.save(), false);assert.equal(writes, 1);
    await controller.load();assert.equal(controller.getSnapshot().conflict, false);assert.equal(controller.getSnapshot().draft?.view.revision, 5);
    assert.equal(await controller.save(), true);assert.equal(writes, 2);
    assert.deepEqual(requests[0], {order: ['continue', 'new'], hidden: ['new'], key: 'actual-idempotency-key', expectedRevision: 4});
  } finally {controller.dispose();}
});
test('disposed or changed viewer reads cannot publish or send a layout mutation', async () => {
  let current = true, signal: AbortSignal | undefined, finish!: (layout: HomeLayoutView) => void;
  const controller = new HomeCustomization({loadLayout: async provided => {signal = provided;return new Promise(resolve => {finish = resolve;});},
    saveLayout: async () => {throw Error('must not send');}, resetLayout: async () => {throw Error('must not send');}}, {...bindings, isCurrent: () => current});
  let publications = 0;controller.subscribe(() => publications++);
  const read = controller.load();current = false;controller.dispose();assert.equal(signal?.aborted, true);finish(layout);await read;
  assert.equal(publications, 1);assert.equal(controller.getSnapshot().draft, undefined);assert.equal(await controller.save(), false);assert.equal(await controller.reset(), false);
});
test('failed layout reads leave Save and Reset unavailable', async () => {
  const controller = new HomeCustomization({loadLayout: async () => {throw Error('network');}, saveLayout: async () => {throw Error('must not send');}, resetLayout: async () => {throw Error('must not send');}}, bindings);
  try {await controller.load();assert.equal(controller.getSnapshot().phase, 'load-error');assert.equal(await controller.save(), false);assert.equal(await controller.reset(), false);}
  finally {controller.dispose();}
});
test('reset sends the captured layout revision and keeps a stale editor conflicted until reload', async () => {
  let revision = 4;
  const requests: {key: string; expectedRevision?: number}[] = [];
  const controller = new HomeCustomization({loadLayout: async () => ({...layout, revision}), saveLayout: async () => {throw Error('must not save');},
    resetLayout: async (key, expectedRevision) => {
      requests.push({key: key(), expectedRevision});
      if (expectedRevision !== revision) throw Object.assign(Error('changed'), {code: 'home_layout_conflict'});
      revision++;
    }}, bindings);
  try {
    await controller.load();controller.show('new', false);revision = 5;
    assert.equal(await controller.reset(), false);
    assert.deepEqual(requests, [{key: 'actual-idempotency-key', expectedRevision: 4}]);
    assert.equal(controller.getSnapshot().conflict, true);
    assert.equal(controller.getSnapshot().draft?.hidden.has('new'), true);
    assert.equal(await controller.reset(), false);
    assert.equal(requests.length, 1, 'a conflict cannot be bypassed by another reset');
    await controller.load();
    assert.equal(await controller.reset(), true);
    assert.equal(requests[1]?.expectedRevision, 5);
  } finally {controller.dispose();}
});
