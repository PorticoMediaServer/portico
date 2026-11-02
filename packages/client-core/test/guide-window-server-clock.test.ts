import test from 'node:test';
import assert from 'node:assert/strict';
import {guideWindow} from '../src/channel-guide.ts';
import {observeServerTime, resetServerClocks, serverNow} from '../src/server-clock.ts';

test('the "on now" guide window contains the server\'s now when the device clock is hours off (LT-9)', () => {
  resetServerClocks();
  try {
    // The device is three hours behind the server.
    const device = Date.now(), server = device + 3 * 3_600_000;
    observeServerTime('', server, device, device);
    const window = guideWindow(), start = Date.parse(window.start), end = Date.parse(window.end);
    assert.ok(start <= serverNow() && serverNow() < end, `${window.start}–${window.end} misses the server's now`);
    assert.ok(!(start <= device && device < end), 'the window is not the device clock\'s');
    assert.equal(end - start, 3 * 3_600_000);
    assert.equal(guideWindow(Date.parse('2026-11-02T10:42:00Z')).start, '2026-11-02T10:00:00Z');
  } finally {resetServerClocks();}
});
