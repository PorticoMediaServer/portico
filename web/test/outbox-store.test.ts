import test from 'node:test';
import assert from 'node:assert/strict';
import {IndexedDBOutboxStore} from '../src/platform/outbox-store.ts';
import type {OutboxEntry} from '../../packages/client-core/src/outbox.ts';

/** IndexedDB as the store uses it: one object store keyed by `id`, and a write that is visible only once its transaction completes. */
function indexedDB(options: {refuseWrites?: boolean} = {}) {
  const saved = new Map<string, unknown>();
  const database = {
    createObjectStore() {}, close() {},
    transaction(_name: string, mode: string) {
      const tx: any = {}, work: (() => void)[] = [];
      const request = (run: (r: any) => void) => { const r: any = {}; work.push(() => run(r)); return r; };
      tx.objectStore = () => ({
        getAll: () => request(r => { r.result = [...saved.values()].map(value => structuredClone(value)); }),
        put: (value: {id: string}) => request(() => { saved.set(value.id, structuredClone(value)); }),
        delete: (id: string) => request(() => { saved.delete(id); }),
      });
      queueMicrotask(() => {
        if (mode === 'readwrite' && options.refuseWrites) { tx.error = new Error('quota'); tx.onabort?.(); return; }
        for (const run of work) run();
        tx.oncomplete?.();
      });
      return tx;
    },
  };
  const open = () => { const request: any = {result: database}; queueMicrotask(() => { request.onupgradeneeded?.(); request.onsuccess?.(); }); return request; };
  return {open} as unknown as IDBFactory;
}
const scope = {serverId: 's', authority: 'local', accountId: 'a', profileId: 'p'};
const entry = (id: string): OutboxEntry => ({id, scope, method: 'PUT', path: '/v1/items/x/personal-state', body: {watched: true}, createdAt: 1, attempts: 0});

test('edits kept by two tabs both survive, a finished one is gone, and a reopened tab reads what is left', async () => {
  const factory = indexedDB(), one = new IndexedDBOutboxStore(factory), two = new IndexedDBOutboxStore(factory);
  await Promise.all([one.put(entry('first')), two.put(entry('second'))]);
  assert.deepEqual((await one.list()).map(each => each.id).sort(), ['first', 'second']);
  await two.delete('first');
  assert.deepEqual(await new IndexedDBOutboxStore(factory).list(), [entry('second')]);
});

test('storage that refuses a write rejects, so the edit is not reported as kept', async () => {
  await assert.rejects(new IndexedDBOutboxStore(indexedDB({refuseWrites: true})).put(entry('first')), /quota/);
});
