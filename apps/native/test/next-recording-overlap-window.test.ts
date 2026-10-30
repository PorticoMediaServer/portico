import test from 'node:test';
import assert from 'node:assert/strict';
import type {Recording, RecordingDetail, RecordingOverlap} from '../../../packages/client-core/src/dvr';
import type {NativeRecordingsController} from '../src-next/screens/settings-live/recordings-controller';
import {RecordingOverlapWindow, type OverlapSnapshot} from '../src-next/screens/settings-live/recording-overlap-window';

const recording = {id: 'recording', revision: 7} as Recording;
const overlaps = (start: number, count: number) => Array.from({length: count}, (_, i) => ({id: `overlap${start + i}`}) as RecordingOverlap);
const detail = (start: number, count: number, nextOverlap: string): RecordingDetail => ({recording, capacity: {known: true, effective: 1}, overlaps: overlaps(start, count), nextOverlap});

test('one original detail opening follows offered overlap cursors in order', async () => {
  const calls: string[] = []; let state!: OverlapSnapshot;
  const controller = {detail: async (id: string, cursor: string) => {assert.equal(id, recording.id); calls.push(cursor); return cursor === 'p2' ? detail(20, 20, 'p3') : detail(40, 1, '');}} as unknown as NativeRecordingsController;
  const window = new RecordingOverlapWindow(recording, detail(0, 20, 'p2'), controller, () => true, value => {state = value;});
  window.seed(detail(0, 20, 'p2')); await window.next(state); await window.next(state);
  assert.deepEqual(state.items.map(row => row.id), overlaps(0, 41).map(row => row.id));
  assert.deepEqual(calls, ['p2', 'p3']); assert.equal(state.nextCursor, ''); window.dispose();
});

test('closing a detail opening refuses a late overlap response', async () => {
  let live = true, finish!: (page: RecordingDetail) => void, state!: OverlapSnapshot;
  const controller = {detail: () => new Promise<RecordingDetail>(resolve => {finish = resolve;})} as unknown as NativeRecordingsController;
  const window = new RecordingOverlapWindow(recording, detail(0, 20, 'p2'), controller, () => live, value => {state = value;});
  window.seed(detail(0, 20, 'p2')); const pending = window.next(state); live = false; window.dispose();
  finish(detail(20, 1, '')); await pending;
  assert.equal(state.items.length, 20); assert.equal(state.items.some(row => row.id === 'overlap20'), false);
});

test('overlap failure retries the offered cursor and revision replacement requires refresh', async () => {
  let state!: OverlapSnapshot, fail = true; const calls: string[] = [];
  const controller = {detail: async (_id: string, cursor: string) => {
    calls.push(cursor); if (fail) {fail = false; throw new Error('offline');}
    return {...detail(20, 1, ''), recording: {...recording, revision: 8}};
  }} as unknown as NativeRecordingsController;
  const window = new RecordingOverlapWindow(recording, detail(0, 20, 'p2'), controller, () => true, value => {state = value;});
  window.seed(detail(0, 20, 'p2')); await window.next(state);
  assert.equal(state.items.length, 20); assert.ok(state.error); await window.retry(state);
  assert.equal(state.items.length, 20); assert.equal(state.refreshRequired, true); assert.deepEqual(calls, ['p2', 'p2']); window.dispose();
});
