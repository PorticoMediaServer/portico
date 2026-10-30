import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {test} from 'node:test';
import {HttpLocalApi, parsePreferenceSnapshot, type ReadingLocation, type ReadingPreferences} from '@portico/client-core';
import {routePath} from '../../../packages/contracts/src/index.ts';
import {createReadingController, type ReaderAdapter, type ReaderMount} from '@portico/reader-core';
import {bindReadingProgress} from '../../src/screens/read/progress.ts';

const revision = 'a'.repeat(64), source = {id: 'unit-source', revision, size: 1};
const preferences = parsePreferenceSnapshot(JSON.parse(readFileSync(new URL('../../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences);
async function fixture(saved = true, conflict = false) {
  let serverRevision = 0, mount!: ReaderMount<ReadingPreferences>;
  const writes: {expectedRevision: number; location: ReadingLocation}[] = [], errors: unknown[] = [];
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'unit-token', async (input, init = {}) => {
    const path = new URL(String(input)).pathname;
    if (path === routePath('post_reading_sessions')) return new Response(JSON.stringify({itemId: 'unit-book', sourceId: source.id, sessionId: 'unit-session', revision,
      format: 'pdf', size: 1, metadata: {}}), {headers: {'Content-Type': 'application/json', ETag: '"' + revision + '"'}});
    if (init.method === 'PUT') {
      const request = JSON.parse(String(init.body)); writes.push(request);
      return new Response(JSON.stringify(conflict ? {} : {saved, progress: saved ? {location: request.location, observedAt: 1, revision: ++serverRevision,
        sourceId: source.id, sourceRevision: revision, stale: false} : null}), {status: conflict ? 412 : 200, headers: {'Content-Type': 'application/json', ETag: '"' + revision + '"'}});
    }
    if (init.method === 'DELETE') return new Response(null, {status: 204});
    throw new Error('unexpected unit operation');
  });
  const client = api.reading({viewer: {accountId: 'unit-account', profileId: 'unit-profile', serverId: 'unit-server', authority: 'local', role: 'member'}, isCurrent: () => true});
  const session = await client.open({itemId: 'unit-book', sourceId: source.id});
  const adapter: ReaderAdapter<ReadingPreferences> = {format: 'pdf', async open() {return {id: source.id, revision, format: 'pdf', metadata: {}, toc: [], async *search() {}, dispose() {},
    async mount(options) {mount = options; return {async navigate(target) {if (target.kind === 'location') options.onLocation(target.location);}, async resize() {}, async applyPreferences() {}, getLocation: () => null, dispose() {}};}};}};
  const controller = createReadingController({adapters: [adapter], preferenceSnapshot: preferences});
  await controller.open({source, format: 'pdf', container: {} as HTMLElement, viewport: {width: 390, height: 844}});
  const bound = bindReadingProgress({controller, session, initial: {progress: null}, authority: {isCurrent: () => true, subscribe: () => () => {}}, reportError: error => errors.push(error)});
  return {bound, controller, session, client, writes, errors, location(page: number) {mount.onLocation({format: 'pdf', page, progression: page / 10});},
    async dispose() {await bound.stop(false); await controller.close(); await session.close(); client.dispose();}};
}

test('observed core pages map to real canonical CAS locations and use returned revisions', async t => {
  const f = await fixture(); t.after(() => f.dispose());
  f.location(3); await f.bound.flush(); f.location(4); await f.bound.flush();
  assert.deepEqual(f.writes.map(write => write.expectedRevision), [0, 1]);
  assert.deepEqual(f.writes[0].location, {format: 'pdf', pageIndex: 3, progression: .3});
  assert.deepEqual(f.errors, []);
});

test('saved:false and null progress never manufacture a new revision or progress value', async t => {
  const f = await fixture(false); t.after(() => f.dispose());
  f.location(3); await f.bound.flush(); f.location(4); await f.bound.flush();
  assert.deepEqual(f.writes.map(write => write.expectedRevision), [0, 0]); assert.deepEqual(f.errors, []);
});

test('a CAS conflict stops automatic writes and session retirement cannot persist another observation', async t => {
  const f = await fixture(true, true); t.after(() => f.dispose());
  f.location(3); await f.bound.flush(); f.location(4); await f.bound.flush();
  assert.equal(f.writes.length, 1); assert.equal(f.errors.length, 1);
  await f.session.close(); f.location(5); await f.bound.flush(); assert.equal(f.writes.length, 1);
});
