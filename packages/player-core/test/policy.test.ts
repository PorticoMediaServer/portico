import test from 'node:test';import assert from 'node:assert/strict';
import {PlaybackPolicy,parseSegmentMarkers,parseQueuePostPlay} from '../src/index.ts';
import {fence,channelFence,frame,intro,credits,post} from './fixtures.ts';
const player=(markers:unknown=[intro,credits])=>{const p=new PlaybackPolicy();p.begin(fence,markers,1400);return p;};

test('the end card exposes only the real policy remainder after confirmed completion',()=>{
 const p=player();
 assert.equal(p.update(frame())!.view.endCountdown,undefined);
 const ended={ended:true,playing:false,positionSeconds:1400};
 assert.deepEqual(p.update(frame({...ended,nowMs:1000}))!.view.endCountdown,{seconds:10,automatic:true});
 assert.deepEqual(p.update(frame({...ended,nowMs:3250}))!.view.endCountdown,{seconds:7.75,automatic:true});
 const last=p.update(frame({...ended,nowMs:11000}))!;
 assert.equal(last.view.endCountdown,undefined);
 assert.equal(last.commands[0]?.kind,'advance');
});

test('a docked end retains its policy clock without advertising a card or resetting on expansion',()=>{
 const p=player(),ended={ended:true,playing:false,positionSeconds:1400};
 assert.equal(p.update(frame({...ended,expanded:false}))!.view.endCountdown,undefined);
 assert.equal(p.update(frame({...ended,expanded:false,nowMs:4000}))!.view.endCountdown,undefined);
 assert.deepEqual(p.update(frame({...ended,nowMs:5000}))!.view.endCountdown,{seconds:5,automatic:true});
 p.cancelAutoplay(fence);
 assert.equal(p.update(frame({...ended,nowMs:6000}))!.view.endCountdown,undefined);
});

test('manual, unavailable and attention end states never publish an automatic countdown',()=>{
 for(const postPlay of [{...post,autoplay:false},{...post,available:false},{...post,passoutCheckDue:true}]){
  const p=player();assert.equal(p.update(frame({ended:true,playing:false,positionSeconds:1400,postPlay}))!.view.endCountdown,undefined);
 }
 const p=player();
 assert.equal(p.update(frame({ended:true,playing:false,positionSeconds:1400,attention:'playback-error'}))!.view.endCountdown,undefined);
 const replacement={...fence,sessionId:'replacement'};p.begin(replacement,[],1400);
 assert.equal(p.update(frame({ended:true})),null);
 assert.deepEqual(p.update(frame({fence:replacement,ended:true,playing:false,positionSeconds:1400}))!.view.endCountdown,{seconds:10,automatic:true});
});

test('a rendered marker mismatch cannot dismiss another overlapping active marker',()=>{
 const earlier={...credits,id:'earlier-outro',kind:'outro' as const,startSeconds:1290,endSeconds:1380};
 const p=player([earlier,credits]);
 p.update(frame({positionSeconds:1320}));
 assert.equal(p.skip(fence,credits.id),null);
 const unchanged=p.skip(fence,earlier.id);
 assert.equal(unchanged?.kind,'seek');assert.equal(unchanged?.kind==='seek'&&unchanged.markerId,earlier.id);
 const next=p.skip(fence,credits.id);
 assert.equal(next?.kind,'seek');assert.equal(next?.kind==='seek'&&next.markerId,credits.id);
 const legacy=player([earlier,credits]);legacy.update(frame({positionSeconds:1320}));
 assert.equal(legacy.skip(fence)?.kind,'seek');
});

test('catalogue segment and Up next policy rejects live domains without replacing its queue binding',()=>{
 const p=player();assert.throws(()=>p.begin(channelFence as never,[intro],1400));
 assert.equal(p.update(frame({fence:channelFence as never})),null);assert.equal(p.skip(channelFence as never),null);
 assert.equal(p.update(frame())?.view.prompt?.kind,'skip');
});
test('Ask waits in the collapsed dock and appears immediately when expanded during the segment',()=>{
 const p=player();const quiet=p.update(frame({expanded:false}))!;
 assert.equal(quiet.view.prompt,null);assert.equal(quiet.view.undo,null);assert.deepEqual(quiet.view.markers,[]);assert.deepEqual(quiet.commands,[]);
 assert.equal(p.update(frame({nowMs:1000}))!.view.prompt?.kind,'skip');
});
test('automatic safe intro and recap skips are silent in the dock and cannot re-skip after expansion',()=>{
 for(const kind of ['intro','recap'] as const){const p=player([{...intro,kind}]);
 const result=p.update(frame({expanded:false,modes:{[kind]:'auto'}}))!;
 assert.equal(result.commands[0]?.kind,'seek');assert.deepEqual(result.view,{prompt:null,undo:null,markers:[],finished:false,replay:false,attention:null});
 assert.equal(p.update(frame({nowMs:1000,modes:{[kind]:'auto'}}))!.view.undo,null);
 assert.deepEqual(p.update(frame({nowMs:2000,modes:{[kind]:'auto'}}))!.commands,[]);
 }
});
test('Never is quiet and unsafe Automatic asks in the full player',()=>{
 const p=player();assert.equal(p.update(frame({modes:{intro:'off'}}))!.view.prompt,null);
 const q=player([{...intro,automaticSafe:false}]);const out=q.update(frame({modes:{intro:'auto'}}))!;assert.equal(out.view.prompt?.kind,'skip');assert.deepEqual(out.commands,[]);
});
test('Undo returns to the actual skip position for six seconds and never skips that marker again',()=>{
 const p=player();const result=p.update(frame({modes:{intro:'auto'}}))!;
 assert.equal(result.view.undo?.remainingSeconds,6);assert.equal(result.commands[0]?.kind,'seek');
 const undo=p.undoSkip(fence,5000);assert.equal(undo?.kind,'seek');if(undo?.kind==='seek')assert.equal(undo.positionSeconds,40);
 assert.deepEqual(p.update(frame({nowMs:5000,modes:{intro:'auto'}}))!.commands,[]);
 const q=player();q.update(frame({modes:{intro:'auto'}}));assert.equal(q.undoSkip(fence,6000),null);
});
test('manual skip and Back dismissal are segment-scoped and do not affect another segment',()=>{
 const p=player();p.update(frame());assert.equal(p.dismiss(fence),true);assert.equal(p.update(frame({nowMs:1000}))!.view.prompt,null);
 p.update(frame({nowMs:2000,positionSeconds:1320,postPlay:null}));const skip=p.skip(fence);assert.equal(skip?.kind,'seek');if(skip?.kind==='seek')assert.equal(skip.positionSeconds,1400);
});
test('credits never start the next title early by default or across a scene, even in a quiet dock',()=>{
 for(const expanded of [true,false]){
  const p=player();p.update(frame({positionSeconds:1310,expanded}));assert.deepEqual(p.update(frame({nowMs:20000,positionSeconds:1330,expanded}))!.commands,[]);
  const q=player([{...credits,endSeconds:1360}]);q.update(frame({positionSeconds:1310,expanded,postPlay:{...post,startDuringCredits:true}}));assert.deepEqual(q.update(frame({nowMs:20000,positionSeconds:1330,expanded,postPlay:{...post,startDuringCredits:true}}))!.commands,[]);
 }
});
test('explicit during-credits autoplay acts once, invisibly in a collapsed dock',()=>{
 const p=player();p.update(frame({positionSeconds:1310,expanded:false,postPlay:{...post,startDuringCredits:true}}));
 const due=p.update(frame({nowMs:10000,positionSeconds:1320,expanded:false,postPlay:{...post,startDuringCredits:true}}))!;
 assert.equal(due.view.prompt,null);assert.deepEqual(due.view.markers,[]);assert.equal(due.commands[0]?.kind,'advance');
 assert.deepEqual(p.update(frame({nowMs:11000,positionSeconds:1321,expanded:false,postPlay:{...post,startDuringCredits:true}}))!.commands,[]);
});
test('Watch credits dismisses the early countdown; autoplay still waits for the actual ending',()=>{
 const p=player();p.update(frame({positionSeconds:1310,postPlay:{...post,startDuringCredits:true}}));p.dismiss(fence);
 assert.deepEqual(p.update(frame({nowMs:20000,positionSeconds:1330,postPlay:{...post,startDuringCredits:true}}))!.commands,[]);
 assert.deepEqual(p.update(frame({nowMs:30000,positionSeconds:1400,ended:true}))!.commands,[]);
 assert.equal(p.update(frame({nowMs:40000,positionSeconds:1400,ended:true}))!.commands[0]?.kind,'advance');
});
test('end-card wait carries on silently only with autoplay and a ready next entry; Cancel is respected',()=>{
 for(const autoplay of [true,false]){const p=player();p.update(frame({ended:true,expanded:false,postPlay:{...post,autoplay}}));const result=p.update(frame({nowMs:10000,ended:true,expanded:false,postPlay:{...post,autoplay}}))!;assert.equal(result.commands.some(c=>c.kind==='advance'),autoplay);assert.equal(result.view.replay,!autoplay);assert.equal(result.view.prompt,null);}
 const p=player();p.update(frame({ended:true}));p.cancelAutoplay(fence);assert.deepEqual(p.update(frame({nowMs:20000,ended:true}))!.commands,[]);
 const q=player();assert.equal(q.update(frame({ended:true,postPlay:null}))!.view.replay,true);
});
test('Still watching requires its explicit response and all four urgent states expand a quiet dock',()=>{
 for(const attention of ['still-watching','playback-error','owner-stopped','host-only'] as const){const p=player();const out=p.update(frame({expanded:false,attention}))!;assert.equal(out.commands[0]?.kind,'expand');assert.equal(out.view.prompt,null);}
 const p=player();const policy={...post,passoutCheckDue:true};const out=p.update(frame({expanded:false,ended:true,postPlay:policy}))!;assert.equal(out.view.attention,'still-watching');assert.equal(p.advance(fence,'manual'),null);
 p.update(frame({nowMs:10000,ended:true,postPlay:policy}));assert.equal(p.advance(fence,'manual'),null);assert.equal(p.advance(fence,'still-watching')?.kind,'advance');
});
test('stale session, source, generation, entry and backward clocks cannot seek or advance',()=>{
 const p=player();p.update(frame({nowMs:1000}));assert.equal(p.update(frame({nowMs:999})),null);
 for(const key of ['sessionId','sourceId','itemId','entryId','generation'] as const){const other={...fence,[key]:key==='generation'?2:'other'};assert.equal(p.update(frame({fence:other})),null);assert.equal(p.skip(other),null);}
 p.stop(fence);assert.equal(p.update(frame()),null);
});
test('marker parser refuses malformed, excessive, unordered or source-clock-disagreeing data',()=>{
 for(const markers of [[{...intro,automaticSafe:'yes'}],[{...intro,startSeconds:NaN}],[{...intro,endSeconds:Infinity}],[credits,intro],[intro,intro],Array.from({length:513},(_,i)=>({...intro,id:'m'+i})),[{...intro,endSeconds:1401}]])assert.throws(()=>parseSegmentMarkers(markers,1400));
 assert.throws(()=>parseQueuePostPlay({nextEntryId:'second',available:true,reason:'end',countdownMs:10000,autoplay:true,passoutCheckDue:false,automaticAdvances:0}));
});
test('owner-stopped and host-only states cannot be bypassed by a manual skip, Undo or advance',()=>{
 for(const attention of ['owner-stopped','host-only','playback-error'] as const){const p=player();p.update(frame({modes:{intro:'auto'}}));p.update(frame({nowMs:1000,attention}));assert.equal(p.skip(fence),null);assert.equal(p.undoSkip(fence,2000),null);assert.equal(p.advance(fence,'manual'),null);}
});
