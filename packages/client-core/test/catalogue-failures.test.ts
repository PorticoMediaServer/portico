import assert from 'node:assert/strict';
import test from 'node:test';
import {parseCatalogueFailures, readCatalogueFailures} from '../src/catalogue-failures.ts';

const row = {domain: 3, entityId: 'item-1', title: 'Ran', reason: 'artwork: decode failed', attempts: 4, failedAt: '2025-10-09T08:53:20Z'};

test('titles set aside are read row by row, and one unreadable row is left out alone', () => {
  const out = parseCatalogueFailures({failures: [row, {domain: 'x'}, {...row, entityId: undefined, title: undefined, attempts: 1}], more: true});
  assert.equal(out.failures.length, 2);
  assert.deepEqual(out.failures[0], {...row, failedAt: 1760000000000});
  assert.equal(out.failures[1]!.title, undefined);
  assert.equal(out.more, true);
  assert.equal(parseCatalogueFailures({failures: []}).more, false);
});

test('an answer that is not a list of failures is refused', () => {
  assert.throws(() => parseCatalogueFailures({failures: 'none'}));
  assert.throws(() => parseCatalogueFailures(null));
});

test('the list is read from the owner route', async () => {
  const calls: string[] = [];
  const out = await readCatalogueFailures({request: async <T,>(path: string, method = 'GET') => { calls.push(`${method} ${path}`); return {failures: [row], more: false} as T; }});
  assert.deepEqual(calls, ['GET /v1/admin/catalogue/failures']);
  assert.equal(out.failures[0]!.attempts, 4);
});

test('the general RFC 3339 instant is converted to the console date model', () => {
  const out = parseCatalogueFailures({failures: [{...row, failedAt: '2025-10-09T08:53:20.123Z'}], more: false});
  assert.equal(out.failures[0]?.failedAt, 1760000000123);
});
