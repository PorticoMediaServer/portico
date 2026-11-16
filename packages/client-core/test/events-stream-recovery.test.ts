import test from 'node:test';
import assert from 'node:assert/strict';
import {EventsClient} from '../src/playback-v1/events.ts';
test('a recovered SSE connection is retried after repeated outages without using a JSON event route', async () => {
 const timers = new Map<number, () => void>();let n = 0, streams = 0, polls = 0, changed = 0;
 const events = new EventsClient({http: {send: async () => {polls++;throw new Error('server only serves SSE');}},
  openStream: async (_cursor, receive, signal) => {
   if (++streams <= 3) throw new Error('offline');
   receive({id: 'current', event: 'change', data: JSON.stringify({topic: 'preferences'})});
   await new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), {once: true}));
  }, setTimer: fn => {timers.set(++n, fn);return n;}, clearTimer: id => {timers.delete(id as number);}, random: () => 0});
 events.on('settings.changed', () => changed++);
 const settle = async () => {for (let i = 0; i < 20; i++) await Promise.resolve();};
 try {
  events.start();await settle();
  for (let i = 0; i < 3; i++) {const [id, timer] = timers.entries().next().value!;timers.delete(id);timer();await settle();}
  assert.equal(polls, 0);assert.equal(streams, 4);assert.equal(changed, 1);
 } finally {events.stop();}
});
