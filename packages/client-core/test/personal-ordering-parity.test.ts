import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import {compileFunction} from 'node:vm';
import * as group from '../src/reading-saved-group.ts';
import {removeSavedReadingGroup} from '../src/saved-reading-reference.ts';
import {clearPersonalHistory} from '../src/personal-history-actions.ts';
import {readCollectionSuccessor} from '../src/query/collection-successor.ts';
import {encodedRoutePath} from '../../contracts/src/routes.ts';

// Verbatim source slices from untouched main. The full-file SHA-256 digests and
// source commit are kept with the reference; both sides execute the same ports.
const original = JSON.parse(readFileSync(new URL('./fixtures/cc2-personal-main-ordering.json', import.meta.url), 'utf8'));
const saved = readFileSync(new URL('../../../apps/native/src-next/screens/destinations/saved/model.ts', import.meta.url), 'utf8');
const currentHistory = saved.slice(saved.indexOf('  clearHistory = async'), saved.indexOf('  dispose =', saved.indexOf('  clearHistory = async')));
const collection = readFileSync(new URL('../../../web/src/screens/watch/collection-next.ts', import.meta.url), 'utf8');
const runtime = readFileSync(new URL('../../../web/src/screens/saved/runtime.ts', import.meta.url), 'utf8');
const start = runtime.indexOf('    removeReadingGroup(');
const currentRemoval = runtime.slice(start, runtime.indexOf('\n    },', start) + 7);
function compile(code: string, ports: Record<string, unknown>, result: string) {
  const source = stripTypeScriptTypes('function fixtureBody() {\n' + code + '\nreturn ' + result + ';\n}', {mode: 'transform'});
  return compileFunction(source + '\nreturn fixtureBody();', Object.keys(ports))(...Object.values(ports));
}
const deferred = () => {let resolve!: (value: unknown) => void, reject!: (error: unknown) => void;const promise = new Promise<unknown>((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};};
const flush = async () => {for (let n = 0; n < 12; n++) await Promise.resolve();};
const after = (depth: number, action: () => void) => {if (!depth) queueMicrotask(action); else queueMicrotask(() => after(depth - 1, action));};
test('main parity: history refresh runs before a retirement queued behind the request answer', async () => {
  const exercise = async (code: string) => {
    const response = deferred(), trace: string[] = []; let live = true;
    const owner = {options: {requestId: async () => 'held', api: {request: () => response.promise}}, live: () => live, historyRevision: () => 9,
      state: {tab: 'history'}, select: async () => {trace.push('refresh');}};
    // Arrow methods capture their class instance; this fixture supplies that exact receiver.
    const source = code.replace('  clearHistory = async', '  const clearHistory = async');
    const fn = compileFunction(stripTypeScriptTypes('function create() {\n' + source + '\nreturn clearHistory;\n}', {mode: 'transform'}) + '\nreturn create.call(owner);', ['owner', 'clearPersonalHistory', 'routePath'])(owner, clearPersonalHistory, () => '/v1/personal-history/actions');
    const task = fn({revision: 9, signal: new AbortController().signal, isCurrent: () => live}); await flush();
    response.resolve({}); queueMicrotask(() => {live = false; trace.push('retire');}); await task;
    return trace;
  };
  const expected = await exercise(original.history); assert.deepEqual(expected, ['refresh', 'retire']);
  assert.deepEqual(await exercise(currentHistory), expected);
});
function collectionReader(code: string) {
  return compile(code.replace(/^import .*;\n/gm, '').replace('export async function', 'async function').replace('export function', 'function').replace(/export type[\s\S]*?\n\}>;\n/, ''), {encodedRoutePath, readCollectionSuccessor}, 'readCollectionNext');
}
test('main parity: collection projection happens in the original request continuation, before queued retirement', async () => {
  const exercise = async (code: string) => {
    const response = deferred(), trace: string[] = []; let current = true;
    const next = {id: 'next', available: true, missing: false, title: 'Next', get year() {trace.push('project');if (!current) throw new Error('late projection');return 2026;}};
    const fn = collectionReader(code), task = fn({request: () => response.promise}, {id: 'collection', name: 'Collection'}, 'current', async () => {}, new AbortController().signal);
    response.resolve({items: [{id: 'current'}, next], nextCursor: ''}); queueMicrotask(() => {current = false; trace.push('retire');}); await task; return trace;
  };
  const expected = await exercise(original.collection); assert.ok(expected.indexOf('project') < expected.indexOf('retire'));
  assert.deepEqual(await exercise(collection), expected);
});
for (const depth of [0, 1, 2, 3]) {
  for (const outcome of ['transport rejection', 'stale malformed reply', 'current malformed reply'] as const) {
    test(`main parity: saved reference ${outcome}, competing checkpoint at microtask ${depth}`, async () => {
      const exercise = async (code: string) => {
        const response = deferred(), trace: string[] = []; let current = true;
        const operations = {get current() {trace.push('guard'); return current;}};
        const fn = compile('const owner = {\n' + code + '\n};', {...group, removeSavedReadingGroup, api: {request: () => response.promise}, scope: {serverId: 'server'}, operations,
          abort: () => new DOMException('Saved authority is no longer current.', 'AbortError')}, 'owner.removeReadingGroup');
        const task = fn('reference', {operationId: 'held', expectedRevision: 2}, new AbortController().signal).then(() => trace.push('settled'), (e: Error) => trace.push('error:' + e.message));
        if (outcome === 'transport rejection') response.reject(new Error('transport'));
        else {
          current = outcome !== 'stale malformed reply';
          response.resolve(Object.defineProperty({}, 'serverId', {enumerable: true, get() {trace.push('parse'); throw new Error('dto');}}));
        }
        after(depth, () => trace.push('checkpoint')); await task; await flush(); return trace;
      };
      const expected = await exercise(original.removal); assert.deepEqual(await exercise(currentRemoval), expected);
      if (outcome === 'stale malformed reply') assert.ok(!expected.includes('parse'));
    });
  }
}
