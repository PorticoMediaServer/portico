import test from 'node:test';
import assert from 'node:assert/strict';
import {SubtitleService} from '../src/subtitles.ts';

test('PERF-24: the subtitle catalogue is re-read every 25 s only while polling is on', async () => {
  const api = {request: async () => { throw Object.assign(new Error('offline'), {code: 'network'}); }};
  const service = new SubtitleService(api as never, {itemId: 'i', sessionId: 's', generation: 1}, () => 'op');
  service.start();
  assert.ok((service as unknown as {interval?: unknown}).interval, 'on by default (Apple keeps its behaviour)');
  service.setPolling(false);
  assert.equal((service as unknown as {interval?: unknown}).interval, undefined, 'off: no 25 s re-read');
  service.setPolling(true);
  assert.ok((service as unknown as {interval?: unknown}).interval, 'back on: re-read again');
  service.stop();
});
