import assert from 'node:assert/strict';
import {test} from 'node:test';
import {capacityChangedEventType} from '../src/account-capacity.ts';
import {parseEvent} from '../src/playback-v1/types.ts';

const hint = {variant: 'capacity', id: '123', type: capacityChangedEventType, at: '2026-10-25T20:00:00Z'};
test('capacity hints admit sequencing only, before generic event projection', () => {
 assert.deepEqual(parseEvent(hint), hint);
 for (const extra of [{resource: {kind: 'session', id: 'private'}}, {revision: '3'}, {data: {}}, {accountId: 'private'}, {playing: 0}]) {
  // A field this app does not know is ignored, and never carried into what the app keeps.
  assert.deepEqual(parseEvent({...hint, ...extra}), hint);
 }
 assert.equal(parseEvent({...hint, at: 'private'}), undefined);
 assert.equal(parseEvent({variant: 'capacity', id: '123', type: capacityChangedEventType}), undefined);
 assert.equal(parseEvent({id: '123', type: capacityChangedEventType, at: hint.at}), undefined, 'an untagged event is not read');
});
test('an event variant this app does not know is skipped, not thrown', () => {
 assert.doesNotThrow(() => parseEvent({variant: 'heartbeat', id: '9', type: 'x'}));
 assert.equal(parseEvent({variant: 'heartbeat', id: '9', type: 'x'}), undefined);
 assert.equal(parseEvent({variant: 'change', id: '9', type: 'session.updated'})?.type, 'session.updated');
});
test('an account stream limit with a field this app does not know is still read', async () => {
 const {parseAccountStreamLimit} = await import('../src/playback-controls.ts');
 assert.deepEqual(parseAccountStreamLimit({scope: 'account', limit: 3, playing: 1, perDevice: 2}), {scope: 'account', limit: 3, playing: 1});
 assert.throws(() => parseAccountStreamLimit({scope: 'account', limit: 0, playing: 1}));
});
