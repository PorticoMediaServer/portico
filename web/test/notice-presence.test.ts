import test from 'node:test';
import assert from 'node:assert/strict';
import {
  noticeClearOutgoing, noticeEntryFrom, noticeInitial, noticeReverse, noticeState, noticeTiming,
  type NoticeSlot, type NoticeState,
} from '../src/kit/media/notice-presence.ts';

type Value = Readonly<{id: string; text: string}>;
const keyOf = (value: Value) => value.id;
const a: Value = {id: 'a', text: 'A'}, b: Value = {id: 'b', text: 'B'}, c: Value = {id: 'c', text: 'C'};

test('a notice already live at mount is current with no duplicate outgoing', () => {
  assert.deepEqual(noticeInitial(a, keyOf), {current: {key: 'a', value: a, outgoing: false, retained: false}});
  assert.deepEqual(noticeInitial(undefined, keyOf), {});
});

test('reconciling the committed value is an identity no-op', () => {
  const state = noticeInitial(a, keyOf);
  assert.equal(noticeState(state, a, keyOf), state);
  const empty: NoticeState<Value> = {};
  assert.equal(noticeState(empty, undefined, keyOf), empty);
  const withdrawn = noticeState(state, undefined, keyOf);
  assert.equal(noticeState(withdrawn, undefined, keyOf), withdrawn);
});

test('withdrawal keeps the same key so the painted node and facts remain', () => {
  const withdrawn = noticeState(noticeInitial(a, keyOf), undefined, keyOf);
  assert.equal(withdrawn.current, undefined);
  assert.equal(withdrawn.outgoing?.key, 'a');
  assert.equal(withdrawn.outgoing?.value, a);
});

test('replacement retires the older outgoing and moves the current to outgoing', () => {
  let state: NoticeState<Value> = noticeState(noticeInitial(a, keyOf), b, keyOf);
  state = noticeState(state, c, keyOf);
  assert.equal(state.current?.key, 'c');
  assert.equal(state.outgoing?.key, 'b');
  assert.equal(state.outgoing?.value, b);
});

test('a same-key replacement keeps the retained slot and the running outgoing', () => {
  const first = noticeState(noticeInitial(a, keyOf), b, keyOf);
  const outgoing = first.outgoing;
  const next: Value = {id: 'b', text: 'B revised'};
  const replaced = noticeState(first, next, keyOf);
  assert.notEqual(replaced, first);
  assert.equal(replaced.current?.key, 'b');
  assert.equal(replaced.current?.value, next);
  assert.equal(replaced.outgoing, outgoing);
});

test('reviving the outgoing key is retained paint, not a fresh entry', () => {
  const withdrawn = noticeState(noticeInitial(a, keyOf), undefined, keyOf);
  const resumed = noticeState(withdrawn, a, keyOf);
  assert.equal(resumed.current?.key, 'a');
  assert.equal(resumed.current?.retained, true);
  assert.equal(resumed.outgoing, undefined);
  assert.equal(noticeInitial(a, keyOf).current?.retained, false);
});

test('a finished exit retires only its captured slot, never a same-key successor', () => {
  const captured: NoticeSlot<Value> = {key: 'listening', value: a, outgoing: true, retained: false};
  const successor: NoticeSlot<Value> = {key: 'listening', value: b, outgoing: true, retained: false};
  const current: NoticeSlot<Value> = {key: 'listening', value: c, outgoing: false, retained: false};
  const state: NoticeState<Value> = {current, outgoing: successor};
  assert.equal(noticeClearOutgoing(state, captured), state);
  assert.equal(noticeClearOutgoing({current, outgoing: captured}, captured).outgoing, undefined);
  assert.deepEqual(noticeClearOutgoing(state, captured), {current, outgoing: successor});
});

test('a fully painted or partial retained paint is not reset; fresh entry is separate', () => {
  assert.equal(noticeReverse(1), 1);
  assert.equal(noticeReverse(0.42), 0.42);
  assert.equal(noticeReverse(-1), 0);
  assert.equal(noticeReverse(Number.NaN), 1);
  const fresh = noticeInitial(a, keyOf).current;
  assert.equal(fresh?.retained, false);
  assert.equal(noticeEntryFrom(false, 1), 0);
  assert.equal(noticeEntryFrom(true, 1), 1);
  assert.equal(noticeEntryFrom(true, 0.42), 0.42);
  assert.equal(noticeEntryFrom(true, Number.NaN), 1);
});

test('authored notice timings are 180 in and 120 out, 160 reduced, immediate loss exit', () => {
  assert.deepEqual(noticeTiming(false, 'fade'), {entryMs: 180, exitMs: 120});
  assert.deepEqual(noticeTiming(true, 'fade'), {entryMs: 160, exitMs: 160});
  assert.deepEqual(noticeTiming(true, 'immediate'), {entryMs: 160, exitMs: 0});
  assert.deepEqual(noticeTiming(false, 'immediate'), {entryMs: 180, exitMs: 120});
});
