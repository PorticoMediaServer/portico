import test from 'node:test';
import assert from 'node:assert/strict';
import {receiptTime, serverReceiptFloor} from '../platform/receipt-clock.ts';

test('a clock turned back does not move a receipt\'s expiry: the floor holds the latest time seen', () => {
  const first = receiptTime({floorMs: 0}, Date.parse('2026-10-15T12:00:00Z'));
  assert.equal(first.floorMs, Date.parse('2026-10-15T12:00:00Z'));
  const turnedBack = receiptTime({floorMs: first.floorMs}, Date.parse('2026-09-01T00:00:00Z'));
  assert.equal(turnedBack.nowMs, first.floorMs, 'the earlier wall clock is not believed');
  const later = receiptTime({floorMs: first.floorMs}, Date.parse('2026-10-16T00:00:00Z'));
  assert.equal(later.nowMs, Date.parse('2026-10-16T00:00:00Z'));
});

test('the server\'s answer resets the floor, so a device clock once far ahead recovers when online', () => {
  const ahead = receiptTime({floorMs: 0}, Date.parse('2030-01-01T00:00:00Z')).floorMs;
  const floor = serverReceiptFloor({floorMs: ahead}, ['2026-10-15T12:00:00Z', '2026-10-15T12:00:05Z', 'not a time']);
  assert.equal(floor, Date.parse('2026-10-15T12:00:05Z'));
  assert.equal(serverReceiptFloor({floorMs: ahead}, []), ahead, 'no answer leaves it alone');
});
