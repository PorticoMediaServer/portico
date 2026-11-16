import test from 'node:test';import assert from 'node:assert/strict';import {PlayerChaptersService} from '../src/player-chapters.ts';
// W1 F4 (16 Nov walk): the player's Chapters list reads the title's item (the rebuilt server has no session chapters route).
const scope={serverId:'server',viewerId:'local-owner-profile'},target={libraryId:'library',itemId:'item',sessionId:'session',sessionGeneration:2,sourceId:'med_b'};
const item={id:'item',kind:'audiobook',durationMs:90000,versions:[
 {id:'med_a',durationMs:90000,parts:[{}],markers:[{kind:'chapter',startMs:0,endMs:90000,title:'Other version'}]},
 {id:'med_b',durationMs:90000,parts:[{}],markers:[{kind:'chapter',startMs:0,endMs:30000,title:'One'},{kind:'chapter',startMs:30000,endMs:90000,title:''}]}]};
function service(fn:(p:string)=>Promise<unknown>){return new PlayerChaptersService({scope,timeoutMs:1000,api:{request:<T>(p:string,m?:string,b?:unknown)=>{assert.equal(m,'GET');assert.equal(b,undefined);return fn(p) as Promise<T>;}}});}

test('the playing version\'s chapters, in seconds, each a seek to its start',async()=>{
 const asked:string[]=[];const s=service(async p=>{asked.push(p);return item;});await s.select(target);
 const snap=s.getSnapshot();assert.deepEqual(asked,['/v1/items/item']);assert.equal(snap.phase,'ready');
 assert.equal(snap.data!.status,'available');assert.equal(snap.data!.scope.sourceId,'med_b');assert.equal(snap.data!.nextCursor,'');
 assert.deepEqual(snap.data!.chapters.map(c=>[c.id,c.index,c.title,c.startSeconds,c.endSeconds,c.action.positionSeconds]),[['med_b:1',1,'One',0,30,0],['med_b:2',2,'Chapter 2',30,90,30]]);
 s.dispose();
});
test('a title without chapters says so; a refused read says who cannot read it',async()=>{
 const none=service(async()=>({...item,versions:[{id:'med_b',durationMs:90000,parts:[{}]}]}));await none.select(target);
 assert.equal(none.getSnapshot().data!.status,'none');assert.deepEqual(none.getSnapshot().data!.chapters,[]);none.dispose();
 const refused=service(async()=>{throw Object.assign(new Error('no'),{status:403});});await refused.select(target);
 assert.equal(refused.getSnapshot().phase,'error');assert.equal(refused.getSnapshot().error!.message,'This viewer cannot read these chapters.');refused.dispose();
});
