import test from 'node:test';import assert from 'node:assert/strict';import {PlaybackService,type PlaybackApi} from '../src/index.ts';
async function setup(){let creates=0;const api:PlaybackApi={createPlayback:async()=>({id:'s'+(++creates),generation:1,mode:'hls',streamUrl:'/media',duration:96,resumeSeconds:10}),progressPlayback:async()=>{},stopPlayback:async()=>{}};const s=new PlaybackService(api,()=>String(creates));await s.play('item');s.ready(s.getSnapshot().intentId);return s;}

test('running out of media is a status, not an error: playback stays alive while it recovers',async()=>{
 const s=await setup(),intent=s.getSnapshot().intentId;
 s.recovering(intent,true);
 assert.equal(s.getSnapshot().recovering,true);assert.notEqual(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().error,undefined);assert.ok(s.getSnapshot().session,'the session is kept');
 s.recovering(intent,false);assert.equal(s.getSnapshot().recovering,undefined);assert.notEqual(s.getSnapshot().phase,'error');
});

test('a stale intent cannot mark a newer playback as recovering, and ending playback clears it',async()=>{
 const s=await setup(),intent=s.getSnapshot().intentId;
 s.recovering(intent-1,true);assert.equal(s.getSnapshot().recovering,undefined);
 s.recovering(intent,true);s.fail(intent,'This stream is no longer available.');
 assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().recovering,undefined);
 s.recovering(intent,true);assert.equal(s.getSnapshot().recovering,undefined,'an ended playback is not recovering');
});
