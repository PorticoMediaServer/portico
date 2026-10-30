import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError} from '@portico/client-core';
import {resolveItemRead} from '../src/screens/detail/read-resolution.ts';
import {isReadingLibrary} from '../src/screens/browse/reading-library.ts';

const versions = {chosenSourceId: 'actual-source', versions: [{sourceId: 'actual-source', format: 'epub', size: 500, inventoryAvailable: false}]};
function setup(answer: () => Promise<Response> | Response) {
  const paths: string[] = [];
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'unit-token', async input => {
    paths.push(new URL(String(input)).pathname);return answer();
  });
  const client = api.reading({viewer: {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'member'}, isCurrent: () => true});
  return {client, paths};
}
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}});
test('primary Item dispatch uses actual versions, including unavailable publications, without probing ordinary detail', async t => {
  const f = setup(() => json(versions));t.after(() => f.client.dispose());
  const result = await resolveItemRead(f.client, 'item', new AbortController().signal, () => true);
  assert.equal(result?.itemId, 'item');
  assert.equal(result?.versions[0].inventoryAvailable, false);
  assert.deepEqual(f.paths, ['/v1/items/item/reading/versions']);
  assert.equal(isReadingLibrary('book'), true);assert.equal(isReadingLibrary('comic'), true);
  for (const kind of ['audiobook', 'movie', 'music', 'Book', 'book-file']) assert.equal(isReadingLibrary(kind), false);
});
test('only a successful empty/404 reading lookup chooses ordinary Item; service and permission errors propagate', async t => {
  for (const status of [200, 404, 403, 503]) {
    const f = setup(() => json(status === 200 ? {versions: []} : {}, status));t.after(() => f.client.dispose());
    const result = resolveItemRead(f.client, 'item', new AbortController().signal, () => true);
    if (status === 200 || status === 404) assert.equal(await result, null);
    else await assert.rejects(result, error => error instanceof ReadingError && error.status === status);
    assert.equal(f.paths.length, 1);
  }
});
test('late reading answers cannot select either Item branch after navigation or authority retirement', async t => {
  for (const abort of [false, true]) {
    let release!: (response: Response) => void, current = true;
    const f = setup(() => new Promise<Response>(resolve => {release = resolve;}));t.after(() => f.client.dispose());
    const request = new AbortController(), result = resolveItemRead(f.client, 'item', request.signal, () => current);
    if (abort) request.abort();else current = false;
    release(json({versions: []}));
    await assert.rejects(result, error => error instanceof ReadingError && error.code === 'cancelled' || error instanceof DOMException && error.name === 'AbortError');
    assert.equal(f.paths.length, 1);
  }
});
