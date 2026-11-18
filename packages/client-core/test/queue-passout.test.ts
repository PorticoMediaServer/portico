import test from 'node:test';
import assert from 'node:assert/strict';
import {parseQueue} from '../src/playback-v1/types.ts';

const queue = (over: Record<string, unknown> = {}) => ({id: 'que_1', revision: '3', medium: 'video', total: 2, current: {entryId: 'e1', position: 0},
  repeat: 'off', nextEntryId: 'e2', automaticAdvances: 3, ...over});

test('the queue says when Still watching? is due, as the server works it out', () => {
  assert.equal(parseQueue(queue({passoutCheckDue: true})).postPlay?.passoutCheckDue, true);
  assert.equal(parseQueue(queue()).postPlay?.passoutCheckDue, false);
  assert.equal(parseQueue(queue({passoutCheckDue: 'yes'})).postPlay?.passoutCheckDue, false);
  assert.equal(parseQueue(queue({passoutCheckDue: true})).postPlay?.automaticAdvances, 3);
});
