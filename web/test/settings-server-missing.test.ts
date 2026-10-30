import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsMissingSource} from '../src/screens/settings/server/missing-source.ts';
const page = {observedAt: '2026-10-22T20:00:00Z', items: [{id: 'title', libraryId: 'library', kind: 'movie', title: 'A title', missingSince: '2026-10-22T19:00:00Z'}], total: 1, rule: 'never', days: 30};

test('newest page wins even when the transport ignores abort', async () => {
  const releases: ((value: unknown) => void)[] = [], paths: string[] = [];
  const source = new SettingsMissingSource({request: async <T>(path: string) => {paths.push(path);return new Promise<T>(resolve => releases.push(value => resolve(value as T)));}}, () => true, () => {});
  const first = source.load(), next = source.load('cursor/next');releases[1]({...page, total: 2});await next;releases[0](page);await first;
  assert.equal(source.getSnapshot().page?.total, 2);assert.ok(paths[1].includes('cursor=cursor%2Fnext'));source.dispose();
});

test('removal requires the current captured page and exact typed count; more never loops writes', async () => {
  let posts = 0; const bodies: unknown[] = [];
  const source = new SettingsMissingSource({request: async <T>(_path: string, method?: string, body?: unknown) => {if (method === 'POST') {posts++;bodies.push(body);return {titles: 1, files: 2, more: true} as T;}return page as T;}}, () => true, () => {});
  await source.load();const captured = source.getSnapshot().page!;
  await source.remove(captured, '01');assert.equal(posts, 0);
  await source.refresh();await source.remove(captured, '1');assert.equal(posts, 0);
  await source.remove(source.getSnapshot().page!, '1');assert.equal(posts, 1);assert.deepEqual(bodies, [{before: page.observedAt}]);assert.equal(source.getSnapshot().removed?.more, true);source.dispose();
});

test('lost removal response rereads and never retries the destructive call', async () => {
  let posts = 0, reads = 0; const bodies: unknown[] = [];
  const errors: unknown[] = [];
  const source = new SettingsMissingSource({request: async <T>(_path: string, method?: string, body?: unknown) => {if (method === 'POST') {posts++;bodies.push(body);throw new Error('Response lost');}reads++;return page as T;}}, () => true, error => errors.push(error));
  await source.load();await source.remove(source.getSnapshot().page!, '1');assert.equal(posts, 1);assert.deepEqual(bodies, [{before: page.observedAt}]);assert.equal(reads, 2);assert.equal(errors.length, 0);assert.equal(source.getSnapshot().error, 'remove');source.dispose();
});

test('a legacy page without an observation never grants a removal cutoff', async () => {
  let posts = 0;
  const source = new SettingsMissingSource({request: async <T>(_path: string, method?: string) => {if (method === 'POST') posts++;return {...page, observedAt: undefined} as T;}}, () => true, () => {});
  await source.load();await source.remove(source.getSnapshot().page!, '1');assert.equal(posts, 0);source.dispose();
});

test('terminal read failure retires the source and cannot issue a removal or another read', async () => {
  let calls = 0;
  const source = new SettingsMissingSource({request: async () => {calls++;throw Object.assign(new Error('Permission ended'), {status: 403});}}, () => true, () => {});
  await source.load();await source.load();await source.remove(page as never, '1');assert.equal(calls, 1);assert.equal(source.getSnapshot().phase, 'idle');source.dispose();
});
