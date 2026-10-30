import assert from 'node:assert/strict';
import test from 'node:test';
import {HomeService, homeRowRetryDelaysMs} from '../src/home-service.ts';
import type {HomeRow} from '../src/home.ts';

const entry = (id: string) => ({id, libraryId: 'movies', kind: 'movie' as const, title: id, available: true});
const row = (id: string, entries: ReturnType<typeof entry>[], deferred = false): HomeRow => ({id, title: id, kind: 'recent', artworkShape: 'poster', entries, start: 0,
  viewerFence: 'fence', endpoint: '', privacySensitivity: '', policyState: '', priority: 1, required: false, hideable: true, reorderable: true,
  critical: false, cursorCapable: true, total: entries.length, limit: 2, hasMore: false, revision: {catalog: 1, viewer: 1}, ...(deferred ? {deferred: true} : {})});
const document = (rows: HomeRow[]) => ({serverId: 'server', viewerFence: 'fence', rows, spotlight: [], layout: {revision: 1, rowOrder: rows.map(value => value.id), hiddenRowIds: []},
  revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T20:00:00Z'});
const settle = async () => {for (let index = 0; index < 20; index++) await Promise.resolve();};

test('a deferred shelf whose first read fails is retried, so it never stays a skeleton (E-tv-02, E-mobile-08)', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let reads = 0;
  const home = new HomeService({request: async path => {
    if (!path.includes('/rows/')) return JSON.parse(JSON.stringify(document([row('later', [], true)])));
    if (++reads === 1) throw Object.assign(new Error('slow'), {code: 'request_timeout', retryable: true});
    return JSON.parse(JSON.stringify(row('later', [entry('a')])));
  }});
  await home.load(); await settle();
  assert.equal(home.getSnapshot().document!.rows[0].entries.length, 0, 'first read failed; shelf still a skeleton');
  t.mock.timers.tick(homeRowRetryDelaysMs[0]); await settle();
  assert.equal(reads, 2);
  assert.deepEqual(home.getSnapshot().document!.rows[0].entries.map(item => item.id), ['a']);
  home.dispose();
});

test('a shelf that keeps failing is dropped after the last retry instead of staying a skeleton forever', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let reads = 0;
  const home = new HomeService({request: async path => {
    if (!path.includes('/rows/')) return JSON.parse(JSON.stringify(document([row('later', [], true)])));
    reads++; throw Object.assign(new Error('down'), {code: 'request_timeout', retryable: true});
  }});
  await home.load(); await settle();
  for (const delay of homeRowRetryDelaysMs) {t.mock.timers.tick(delay); await settle();}
  assert.equal(reads, homeRowRetryDelaysMs.length + 1);
  assert.equal(home.getSnapshot().document!.rows.length, 0);
  home.dispose();
});

test('a dropped shelf is read again on the next return to Home, and nothing is read when none was dropped', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let documents = 0, reads = 0, failing = true;
  const home = new HomeService({request: async path => {
    if (!path.includes('/rows/')) {documents++; return JSON.parse(JSON.stringify(document([row('later', [], true)])));}
    reads++; if (failing) throw Object.assign(new Error('down'), {code: 'request_timeout', retryable: true});
    return JSON.parse(JSON.stringify(row('later', [entry('a')])));
  }});
  await home.load(); await settle();
  for (const delay of homeRowRetryDelaysMs) {t.mock.timers.tick(delay); await settle();}
  assert.equal(home.getSnapshot().document!.rows.length, 0, 'dropped after the last retry');
  failing = false;
  const before = documents;
  await home.recoverDroppedRows(); await settle();
  assert.equal(documents, before + 1, 'the document is read again');
  assert.deepEqual(home.getSnapshot().document!.rows.map(value => value.id), ['later']);
  assert.deepEqual(home.getSnapshot().document!.rows[0]!.entries.map(item => item.id), ['a'], 'the shelf is back with its cards');
  await home.recoverDroppedRows(); await settle();
  assert.equal(documents, before + 1, 'no read when nothing was dropped');
  home.dispose();
});

test('a deferred shelf outside the declared demand waits, and loads once it is demanded', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let reads = 0;
  const home = new HomeService({request: async path => {
    if (!path.includes('/rows/')) return JSON.parse(JSON.stringify(document([row('later', [], true)])));
    reads++; return JSON.parse(JSON.stringify(row('later', [entry('a')])));
  }});
  home.setRowDemand([]);
  await home.load(); await settle();
  assert.equal(reads, 0);
  home.setRowDemand(['later']); await settle();
  assert.equal(reads, 1);
  home.dispose();
});

test('quiet re-reads neither restart a failing shelf\'s retries nor bring a dropped shelf back as a skeleton', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let reads = 0, failing = true;
  const home = new HomeService({request: async path => {
    if (!path.includes('/rows/')) return JSON.parse(JSON.stringify(document([row('later', [], true)])));
    reads++; if (failing) throw Object.assign(new Error('down'), {code: 'request_timeout', retryable: true});
    return JSON.parse(JSON.stringify(row('later', [entry('a')])));
  }});
  await home.load(); await settle();
  // A background re-read lands between every failure, as the live app's freshness reads do.
  for (let index = 0; index < homeRowRetryDelaysMs.length + 2; index++) { await home.revalidate(); await settle(); }
  assert.equal(reads, homeRowRetryDelaysMs.length + 1, 'four failed reads in all, then the shelf is dropped');
  assert.equal(home.getSnapshot().document!.rows.length, 0);
  await home.revalidate(); await settle();
  assert.equal(home.getSnapshot().document!.rows.length, 0, 'a later quiet re-read does not bring it back as a skeleton');
  assert.equal(reads, homeRowRetryDelaysMs.length + 1, 'and does not read it again');
  failing = false;
  await home.recoverDroppedRows(); await settle();
  assert.deepEqual(home.getSnapshot().document!.rows[0]!.entries.map(item => item.id), ['a'], 'the return to Home brings it back');
  home.dispose();
});
