import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackUserMessage,playbackFailurePermanent} from '../src/playback-messages.ts';
test('playback messages preserve actionable categories without exposing arbitrary server text',()=>{
 assert.match(playbackUserMessage({code:'owner_server_cap',message:'secret session 123'}),/simultaneous stream limit/);
 assert.match(playbackUserMessage({status:403}),/does not have access/);
 assert.match(playbackUserMessage({code:'receipt_expired'}),/could not be confirmed/);
 assert.equal(playbackUserMessage('http://server/private?token=secret'), 'Playback could not continue. Please try again.');
 assert.equal(playbackUserMessage({message:JSON.stringify({account:'secret'})}), 'Playback could not continue. Please try again.');
 assert.match(playbackUserMessage(new TypeError('Failed to fetch')),/Check your connection/);
});

test('trusted control outage message describes the automatic recovery',()=>{
 assert.equal(playbackUserMessage('Playback control disconnected. Retrying…'),'Reconnecting playback controls…');
});

test('a missing conversion route explains device support without suggesting permission or a retry',()=>{
 assert.equal(playbackUserMessage({code:'no_admissible_delivery_route',message:'private converter detail'}),'This server cannot prepare a version of this title that this device can play.');
 assert.equal(playbackFailurePermanent('no_admissible_delivery_route'),true);
});

test('conversion capacity is actionable and retryable without exposing converter details',()=>{
 assert.match(playbackUserMessage({code:'conversion_capacity',message:'private session path'}),/Wait a moment or stop another stream/);
 assert.equal(playbackFailurePermanent({code:'conversion_capacity'}),false);
});

test('an offline storage source is named plainly and stays worth retrying',()=>{
 assert.equal(playbackUserMessage({code:'storage_offline',message:'private mount /mnt/media is offline'}),'This title’s storage is offline right now.');
 // Storage coming back needs no new request shape or sign-in: offer Try again.
 assert.equal(playbackFailurePermanent({code:'storage_offline'}),false);
});
