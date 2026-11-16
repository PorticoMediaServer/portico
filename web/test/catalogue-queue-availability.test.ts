import test from 'node:test';
import assert from 'node:assert/strict';
import {WebShellPlaybackRuntime} from '../src/platform/playback/runtime.ts';

function owner() {
  let active = false;
  const runtime = {assertCurrent() {}, stack: {mode: 'server', queue: {getSnapshot: () => ({})}},
    group: {active: () => active ? {id: 'room'} : undefined}, dock: {getSnapshot: () => ({})}};
  const queue = () => WebShellPlaybackRuntime.prototype.catalogueQueue.call(runtime as never);
  return {queue, join: () => {active = true;}};
}

test('an idle group-service binding leaves catalogue Queue available', () => {
  const idle = owner();
  assert.ok(idle.queue(), 'a profile outside a room can queue its selection');
  idle.join(); assert.equal(idle.queue(), undefined, 'an active group retains its own queue');
});

test('a captured catalogue Queue port refuses a room joined before the command', async () => {
  const idle = owner(), queue = idle.queue()!;
  assert.ok(queue); idle.join();
  await assert.rejects(queue.enqueueSegments([{source: {query: {libraryId: 'library', pivot: 'shows'}}}], 'end', {
    signal: new AbortController().signal, isCurrent: () => true,
  }), /queue_control_unavailable/);
});
