import assert from 'node:assert/strict';
import {test} from 'node:test';
import {capacityChangedEventType} from '../src/account-capacity.ts';
import {parseEvent} from '../src/playback-v1/types.ts';
import {parseRemotePlaybackControls} from '../src/playback-controls.ts';

const hint = {variant: 'capacity', id: '123', type: capacityChangedEventType, at: '2026-10-25T20:00:00Z'};
test('capacity hints admit sequencing only, before generic event projection', () => {
 assert.deepEqual(parseEvent(hint), hint);
 for (const extra of [{resource: {kind: 'session', id: 'private'}}, {revision: '3'}, {data: {}}, {accountId: 'private'}, {playing: 0}]) {
  assert.equal(parseEvent({...hint, ...extra}), undefined);
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
test('controls retain actual account counts independently of visible rows', () => {
 const document = parseRemotePlaybackControls({items: [], nextCursor: '', streamLimit: {scope: 'account', limit: 2, playing: 3}});
 assert.deepEqual(document.streamLimit, {scope: 'account', limit: 2, playing: 3});
 assert.equal(parseRemotePlaybackControls({items: [], nextCursor: ''}).streamLimit, undefined);
 for (const streamLimit of [null, {scope: 'account', limit: 0, playing: 0}, {scope: 'account', limit: 2, playing: -1}, {scope: 'server', limit: 2, playing: 0}]) {
  assert.throws(() => parseRemotePlaybackControls({items: [], nextCursor: '', streamLimit}));
 }
});
