import assert from 'node:assert/strict';
import test from 'node:test';
import {refreshAvailableCatalogueMetadata} from '../src/query/catalogue-menu.ts';

test('menu refresh preserves the kind route, escaped ID, empty body and captured signal', async () => {
  const signal = new AbortController().signal, calls: unknown[][] = [];
  const api = {async request(...args: unknown[]) {calls.push(args); return {};}};
  await refreshAvailableCatalogueMetadata(api, 'movie', 'title /?', signal);
  await refreshAvailableCatalogueMetadata(api, 'show', 'show /?', signal);
  assert.deepEqual(calls, [
    ['/v1/items/title%20%2F%3F/metadata/refresh', 'POST', {}, signal],
    ['/v1/shows/show%20%2F%3F/metadata/refresh', 'POST', {}, signal],
  ]);
  assert.equal(refreshAvailableCatalogueMetadata(api, 'person', 'person', signal), undefined);
  assert.equal(refreshAvailableCatalogueMetadata(api, undefined, 'missing-kind', signal), undefined);
  assert.equal(calls.length, 2, 'unsupported kinds dispatch nothing');
});

test('menu refresh does not retry a lost or refused response', async () => {
  const failure = new Error('transport-outcome-unknown'); let calls = 0;
  const api = {async request<T>(): Promise<T> {calls++; throw failure;}};
  await assert.rejects(refreshAvailableCatalogueMetadata(api, 'movie', 'title')!, error => error === failure);
  assert.equal(calls, 1);
});
