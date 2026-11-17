import assert from 'node:assert/strict';
import test from 'node:test';
import {LyricsService} from '../src/lyrics.ts';
const page={items:[{id:'plain',format:'text',text:'First words',lines:[{text:'First words'}]},{id:'timed',format:'lrc',text:'[00:01]Second words',lines:[{atMs:1000,text:'Second words'}]}]};
const scope={serverId:'server',viewerId:'viewer'}, target={libraryId:'library',itemId:'song',sessionId:'playback',sessionGeneration:1,sourceId:'source'};
test('lyric choices stay in one playback without a selection write or follow-up read',async()=>{
 const calls:string[]=[];
 const service=new LyricsService({request:async<T>(_path:string,method?:string)=>{calls.push(method??'GET');return page as T;}},scope,target);
 await service.refresh();let shown=service.getSnapshot();const row=shown.data!.resources[1];
 await service.choose(row,{snapshot:shown,signal:new AbortController().signal,isCurrent:()=>true});
 assert.equal(service.getSnapshot().data!.selection.resource?.id,'timed');assert.deepEqual(calls,['GET']);
 await assert.rejects(service.choose(shown.data!.resources[0],{snapshot:shown,signal:new AbortController().signal,isCurrent:()=>true}));
 await service.refresh();assert.equal(service.getSnapshot().data!.selection.resource?.id,'timed');
 const next=new LyricsService({request:async<T>()=>page as T},scope,{...target,sessionId:'next-playback'});await next.refresh();
 assert.equal(next.getSnapshot().data!.selection.resource?.id,'plain');
 shown=service.getSnapshot();service.dispose();await assert.rejects(service.choose(shown.data!.resources[0],{snapshot:shown,signal:new AbortController().signal,isCurrent:()=>true}));next.dispose();
});
test('aborted, foreign, retired and reentered lyric openings cannot change the visible set',async()=>{
 const service=new LyricsService({request:async<T>()=>page as T},scope,target);await service.refresh();
 const shown=service.getSnapshot(), row=shown.data!.resources[1], aborted=new AbortController();aborted.abort();
 await assert.rejects(service.choose(row,{snapshot:shown,signal:aborted.signal,isCurrent:()=>true}));
 await assert.rejects(service.choose({...row},{snapshot:shown,signal:new AbortController().signal,isCurrent:()=>true}));
 await assert.rejects(service.choose(row,{snapshot:shown,signal:new AbortController().signal,isCurrent:()=>false}));
 await assert.rejects(service.choose(row,{snapshot:shown,signal:new AbortController().signal,isCurrent:()=>{service.cancel();return true;}}));
 assert.equal(service.getSnapshot().data,null);service.dispose();
});
