import test from 'node:test';
import assert from 'node:assert/strict';
import {emptyLibraryTrash, readLibraryTrash, removedAtNextScan, saveTrashRule} from '../src/admin/library-trash.ts';

// A library's missing titles on the rebuilt server (03-scanning rules 59 to 62).
const options = {depth: 'basic', trash: {mode: 'days', days: 14}, allowDelete: false, watch: false, rescanMinutes: 0};
const library = {id: 'lib_1', name: 'Films', kind: 'movies', folders: [], options, scanning: false, revision: '4'};
const missing = {items: [{id: 'itm_1', kind: 'movie', title: 'Arrival', year: 2016, missingSinceAt: '2026-11-01T10:00:00Z', removedAt: '2026-11-15T10:00:00Z'}], total: 3};

test('the trash reads the library\'s own rule and its missing titles, with when each goes', async () => {
  const paths: string[] = [];
  const api = {request: async <T,>(path: string) => {paths.push(path); return (path.endsWith('/missing') ? missing : library) as T;}};
  const trash = await readLibraryTrash(api, 'lib_1');
  assert.deepEqual(paths.sort(), ['/v1/admin/libraries/lib_1', '/v1/admin/libraries/lib_1/missing']);
  assert.deepEqual([trash.name, trash.rule, trash.total], ['Films', {mode: 'days', days: 14}, 3]);
  assert.deepEqual(trash.items[0], {id: 'itm_1', title: 'Arrival', year: 2016, missingSince: '2026-11-01T10:00:00Z', removedAt: '2026-11-15T10:00:00Z'});
});

test('a new rule is saved over the library\'s options as they are now, changing only the rule; Empty trash sends the count typed', async () => {
  const calls: [string, string, unknown][] = [];
  // Someone else turned deleting files on since the row was read: the save keeps it.
  const now = {...options, allowDelete: true};
  const api = {request: async <T,>(path: string, method = 'GET', body?: unknown) => {calls.push([method, path, body]);
    return (method === 'GET' ? {...library, options: now} : method === 'PATCH' ? {...library, options: (body as {options: unknown}).options} : {removed: 3}) as T;}};
  assert.deepEqual(await saveTrashRule(api, 'lib_1', {mode: 'scan', days: 14}), {mode: 'scan', days: 30});
  assert.equal(await emptyLibraryTrash(api, 'lib_1', ' 3 '.trim()), 3);
  assert.deepEqual(calls, [
    ['GET', '/v1/admin/libraries/lib_1', undefined],
    ['PATCH', '/v1/admin/libraries/lib_1', {options: {...now, trash: {mode: 'scan'}}}],
    ['DELETE', '/v1/admin/libraries/lib_1/missing?confirm=3', undefined],
  ]);
});

test('a rule says how many missing titles it removes at the next scan', () => {
  const day = 86_400_000, at = Date.parse('2026-11-20T00:00:00Z');
  const trash = {total: 3, items: [{id: 'a', title: 'A', missingSince: new Date(at - 10 * day).toISOString()}, {id: 'b', title: 'B', missingSince: new Date(at - 2 * day).toISOString()}]};
  assert.equal(removedAtNextScan(trash, {mode: 'scan', days: 30}, at), 3, 'every missing title after every scan');
  assert.equal(removedAtNextScan(trash, {mode: 'days', days: 7}, at), 1, 'those missing for at least the days');
  assert.equal(removedAtNextScan(trash, {mode: 'days', days: 30}, at), 0);
  assert.equal(removedAtNextScan(trash, {mode: 'manual', days: 30}, at), 0);
});
