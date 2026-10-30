import test from 'node:test';import assert from 'node:assert/strict';
import {PlayerQueue,episodePlayback,PlayerSessionChoices} from '../src/index.ts';import {view,scope,identity,fence} from './fixtures.ts';
test('queue controls use stable occurrence IDs and current server revision for any kind of queue',()=>{
 const p=new PlayerQueue(scope),raw=view();raw.items.reverse();p.adopt(raw);assert.equal(p.previous(),null);assert.deepEqual(p.next(),{kind:'next',queueId:'queue',expectedRevision:'1'});assert.deepEqual(p.upcoming().map(e=>e.entryId),['second']);assert.deepEqual(p.select('second'),{kind:'entry',entryId:'second',queueId:'queue',expectedRevision:'1'});
 assert.equal(p.select('missing'),null);p.adopt({...view(),queue:{...view().queue,revision:'2',currentEntryId:'second'}});assert.equal(p.previous()?.kind,'previous');assert.equal(p.next()?.expectedRevision,'2');
});
test('Previous offers a revision-fenced intent at the first occurrence only with Repeat all',()=>{
 const p=new PlayerQueue(scope),raw=view();raw.queue.repeat='all';assert.equal(p.adopt(raw),true);
 const before=p.getSnapshot();assert.deepEqual(p.previous(),{kind:'previous',queueId:'queue',expectedRevision:'1'});assert.equal(p.getSnapshot(),before);assert.deepEqual(p.getSnapshot()?.queue.entries.map(e=>e.id),['first','second']);
 for(const repeat of ['off','one']){assert.equal(p.adopt({...raw,queue:{...raw.queue,repeat,revision:'2'}}),true);assert.equal(p.previous(),null);}
});
test('Previous stays unavailable without a current occurrence, including Repeat all',()=>{
 for(const repeat of ['off','one','all']){
  const p=new PlayerQueue(scope);assert.equal(p.previous(),null);assert.equal(p.adopt({...view(),queue:{...view().queue,repeat,currentEntryId:null,currentPlaybackId:null}}),true);assert.equal(p.previous(),null);
 }
});
test('out-of-order queue updates, wrong identity, leaked hidden display data and unbounded windows are rejected',()=>{
 const p=new PlayerQueue(scope);p.adopt({...view(),queue:{...view().queue,revision:'9',highestSeenSequence:'8'}});assert.equal(p.adopt(view()),false);assert.equal(p.getSnapshot()?.queue.revision,'9');
 for(const key of Object.keys(scope))assert.throws(()=>p.adopt({...view(),queue:{...view().queue,scope:{...scope,[key]:'other'}}}));
 assert.throws(()=>p.adopt({...view(),items:Array.from({length:201},()=>view().items[0])}));assert.throws(()=>p.adopt({...view(),queue:{...view().queue,entries:[{id:'first',hidden:true,removed:false},view().queue.entries[1]]}}));
 assert.throws(()=>p.adopt({...view(),next:{entryId:'second',available:true,reason:'end'}}));
});
test('queue edits copy and bound every selected input; queue ordering is never changed optimistically',()=>{
 const p=new PlayerQueue(scope);p.adopt(view());const selected=[{itemId:'song',editionId:null,partId:null,sourceContext:{kind:'item',id:'song',revision:null,entryId:null}}];const action=p.edit({action:'insert-next',entries:selected} as never);selected[0].itemId='changed';assert.equal(action?.kind,'edit');if(action?.kind==='edit'&&action.intent.action==='insert-next')assert.equal(action.intent.entries[0].itemId,'song');assert.equal(p.getSnapshot()?.queue.entries[0].id,'first');assert.throws(()=>p.edit({action:'append',entries:Array.from({length:1001},()=>selected[0])} as never));
});
test('the whole-show selector and drawer picks across seasons retain playback choices, outside starts reset them',()=>{
 const c=new PlayerSessionChoices();c.begin(identity);c.bindSource(fence);c.set('subtitleLanguage',identity,'en',fence);c.set('speed',identity,1.5);const launch=episodePlayback(identity,'show','season2episode1');assert.deepEqual(launch.selector,{container:{kind:'show',id:'show'}});assert.deepEqual(launch.options,{anchor:{itemId:'season2episode1'}});c.begin(launch.playback);assert.equal(c.get('speed',launch.playback,1),1.5);assert.equal(c.get('subtitleLanguage',launch.playback,null),'en');c.begin({...identity,run:2});assert.equal(c.get('speed',{...identity,run:2},1),1);
});
