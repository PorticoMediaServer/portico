import test from 'node:test';
import assert from 'node:assert/strict';
import {LibraryScanTracker, parseLibraryScanEvent} from '../src/library-inventory.ts';
import type {ServerEvent} from '../src/playback-v1/types.ts';

const event = (over: Record<string, unknown> = {}): ServerEvent => ({
  id: '20931',
  type: 'library.scan.updated',
  at: '2026-09-24T12:00:00.000Z',
  resource: {kind: 'library', id: 'lib_1'},
  revision: '1758715200000',
  data: {state: 'progress', found: 1240},
  ...over,
}) as ServerEvent;

const flush = async () => {
  for (let i = 0; i < 100; i++) {
    await Promise.resolve();
    await new Promise<void>(done => setImmediate(done));
  }
};

test('parses a scan event and skips malformed or unknown ones', () => {
  assert.deepEqual(parseLibraryScanEvent(event()), {libraryId: 'lib_1', revision: 1758715200000, state: 'progress', found: 1240});
  assert.equal(parseLibraryScanEvent(event({type: 'session.updated'})), undefined);
  assert.equal(parseLibraryScanEvent(event({resource: {kind: 'job', id: 'lib_1'}})), undefined);
  assert.equal(parseLibraryScanEvent(event({resource: {kind: 'library', id: ''}})), undefined);
  assert.equal(parseLibraryScanEvent(event({resource: undefined})), undefined);
  assert.equal(parseLibraryScanEvent(event({revision: '12.5'})), undefined);
  assert.equal(parseLibraryScanEvent(event({revision: 'abc'})), undefined);
  assert.equal(parseLibraryScanEvent(event({revision: '-3'})), undefined);
  assert.equal(parseLibraryScanEvent(event({revision: 42})), undefined);
  assert.equal(parseLibraryScanEvent(event({revision: '9007199254740993'})), undefined);
  assert.equal(parseLibraryScanEvent(event({data: {state: 'paused', found: 3}})), undefined);
  assert.equal(parseLibraryScanEvent(event({data: {state: 'progress', found: -1}})), undefined);
  assert.equal(parseLibraryScanEvent(event({data: {state: 'progress', found: 1.5}})), undefined);
  assert.equal(parseLibraryScanEvent(event({data: undefined})), undefined);
  assert.equal(parseLibraryScanEvent(event({data: []})), undefined);
  // Extra data fields are ignored.
  assert.deepEqual(parseLibraryScanEvent(event({data: {state: 'started', found: 0, extra: true}})), {libraryId: 'lib_1', revision: 1758715200000, state: 'started', found: 0});
});

test('started, progress and finished drive the state; finished counts', async () => {
  const tracker = new LibraryScanTracker({read: async () => ({scanning: false, found: 0})});
  tracker.watch(['lib_1']);
  await flush();
  tracker.apply({libraryId: 'lib_1', revision: 1, state: 'started', found: 3});
  assert.deepEqual(tracker.get('lib_1'), {scanning: true, found: 3});
  assert.equal(tracker.finished('lib_1'), 0);
  tracker.apply({libraryId: 'lib_1', revision: 2, state: 'progress', found: 9});
  assert.deepEqual(tracker.get('lib_1'), {scanning: true, found: 9});
  tracker.apply({libraryId: 'lib_1', revision: 3, state: 'finished', found: 9});
  assert.deepEqual(tracker.get('lib_1'), {scanning: false, found: 0});
  assert.equal(tracker.finished('lib_1'), 1);
  tracker.apply({libraryId: 'lib_1', revision: 4, state: 'finished', found: 9});
  assert.equal(tracker.finished('lib_1'), 2);
  assert.deepEqual(tracker.get('unknown'), {scanning: false, found: 0});
  tracker.dispose();
});

test('an older revision is ignored; an equal one applies', async () => {
  const tracker = new LibraryScanTracker({read: async () => ({scanning: false, found: 0})});
  tracker.watch(['lib_1']);
  await flush();
  tracker.apply({libraryId: 'lib_1', revision: 5, state: 'progress', found: 50});
  assert.deepEqual(tracker.get('lib_1'), {scanning: true, found: 50});
  tracker.apply({libraryId: 'lib_1', revision: 4, state: 'progress', found: 1});
  assert.deepEqual(tracker.get('lib_1'), {scanning: true, found: 50});
  tracker.apply({libraryId: 'lib_1', revision: 5, state: 'progress', found: 7});
  assert.deepEqual(tracker.get('lib_1'), {scanning: true, found: 7});
  tracker.dispose();
});

test('a read that started before an event is discarded', async () => {
  let release!: (v: {scanning: boolean; found: number}) => void;
  const read = () => new Promise<{scanning: boolean; found: number}>(resolve => { release = resolve; });
  const tracker = new LibraryScanTracker({read});
  tracker.watch(['lib_1']);
  await Promise.resolve();
  await new Promise<void>(done => setImmediate(done));
  tracker.apply({libraryId: 'lib_1', revision: 2, state: 'progress', found: 40});
  release({scanning: false, found: 0});
  await flush();
  assert.deepEqual(tracker.get('lib_1'), {scanning: true, found: 40});
  tracker.dispose();
});

test('watch reads each new library once, sequentially; the same set reads nothing; resync reads each watched library once', async () => {
  const started: string[] = [];
  const counts = new Map<string, number>();
  let releases: Array<(v: {scanning: boolean; found: number}) => void> = [];
  const read = (libraryId: string) => {
    started.push(libraryId);
    counts.set(libraryId, (counts.get(libraryId) ?? 0) + 1);
    return new Promise<{scanning: boolean; found: number}>(resolve => { releases.push(resolve); });
  };
  const tracker = new LibraryScanTracker({read});
  tracker.watch(['a', 'b']);
  await Promise.resolve();
  await new Promise<void>(done => setImmediate(done));
  // Sequential: only the first read has started while it is pending.
  assert.deepEqual(started, ['a']);
  releases.shift()!({scanning: false, found: 0});
  await Promise.resolve();
  await new Promise<void>(done => setImmediate(done));
  await Promise.resolve();
  assert.deepEqual(started, ['a', 'b']);
  releases.shift()!({scanning: false, found: 0});
  await flush();
  assert.equal(counts.get('a'), 1);
  assert.equal(counts.get('b'), 1);
  // The same set (even reordered) reads nothing.
  tracker.watch(['b', 'a']);
  await flush();
  assert.equal(counts.get('a'), 1);
  assert.equal(counts.get('b'), 1);
  // A new library reads once; forgotten ones are not re-read.
  releases = [];
  tracker.watch(['b', 'c']);
  await flush();
  for (const r of releases.splice(0)) r({scanning: false, found: 0});
  await flush();
  assert.equal(counts.get('a'), 1);
  assert.equal(counts.get('b'), 1);
  assert.equal(counts.get('c'), 1);
  // Resync reads each watched library once.
  tracker.resync();
  await flush();
  for (const r of releases.splice(0)) r({scanning: false, found: 0});
  await flush();
  assert.equal(counts.get('b'), 2);
  assert.equal(counts.get('c'), 2);
  assert.equal(counts.get('a'), 1);
  tracker.dispose();
});

test('a failed read leaves the state; unwatched libraries ignore events', async () => {
  const tracker = new LibraryScanTracker({read: async () => { throw new Error('denied'); }});
  tracker.watch(['lib_1']);
  await flush();
  assert.deepEqual(tracker.get('lib_1'), {scanning: false, found: 0});
  tracker.apply({libraryId: 'elsewhere', revision: 1, state: 'started', found: 5});
  assert.deepEqual(tracker.get('elsewhere'), {scanning: false, found: 0});
  assert.equal(tracker.finished('elsewhere'), 0);
  tracker.dispose();
});

test('listeners fire only on real changes', async () => {
  let calls = 0;
  const tracker = new LibraryScanTracker({read: async () => ({scanning: true, found: 8})});
  const off = tracker.subscribe(() => { calls++; });
  const before = tracker.getSnapshot();
  tracker.watch(['lib_1']);
  await flush();
  assert.equal(calls, 1);
  assert.notEqual(tracker.getSnapshot(), before);
  // The same event state again: no notification.
  tracker.apply({libraryId: 'lib_1', revision: 2, state: 'progress', found: 8});
  assert.equal(calls, 1);
  // A real change notifies.
  tracker.apply({libraryId: 'lib_1', revision: 3, state: 'progress', found: 9});
  assert.equal(calls, 2);
  // A resync read that differs notifies once, then a matching one stays quiet.
  tracker.resync();
  await flush();
  assert.equal(calls, 3);
  tracker.resync();
  await flush();
  assert.equal(calls, 3);
  off();
  tracker.apply({libraryId: 'lib_1', revision: 4, state: 'progress', found: 10});
  assert.equal(calls, 3);
  tracker.dispose();
});
