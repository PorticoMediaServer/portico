import test from 'node:test';import assert from 'node:assert/strict';import {PlaybackService,type PlaybackApi,type PlaybackSession} from '../src/index.ts';
const tick=()=>new Promise(r=>setImmediate(r));
// The server re-plans the same session another way: same id, a new generation and URL.
function setup(escalates:(n:number)=>boolean|null,protocol:'v1'|null='v1'){
 let created=0;const reports:any[]=[];
 const session=(generation:number):PlaybackSession=>({id:'s1',generation,streamUrl:'/media/'+generation,mode:generation===1?'direct':'hls',duration:120,resumeSeconds:12,...(protocol?{protocol}:{})} as PlaybackSession);
 const api:PlaybackApi={createPlayback:async()=>{created++;return session(1);},stopPlayback:async()=>{},progressPlayback:async()=>{},
  reportPlayerFailure:async(sessionId,code,detail,positionSeconds)=>{reports.push({sessionId,code,detail,positionSeconds});const e=escalates(reports.length);return e===null?null:e?{escalates:true,session:session(reports.length+1)}:{escalates:false};}};
 return {service:new PlaybackService(api,()=>String(created),{seekTimeoutMs:1000}),reports,created:()=>created};
}
async function playing(x:ReturnType<typeof setup>,at=40){await x.service.play('movie');const s=x.service.getSnapshot();x.service.ready(s.intentId);x.service.seekApplied(s.intentId,s.pendingSeek!.revision,12,'playing');x.service.fact(x.service.getSnapshot().intentId,at,'playing');}

test('an engine failure re-plans the same session from the same position and shows recovery, not an error',async()=>{
 const x=setup(()=>true);await playing(x);
 const before=x.service.getSnapshot();
 x.service.engineFailed(before.intentId,'decode_error','MEDIA_ERR_DECODE','This media could not be played.');
 assert.equal(x.service.getSnapshot().recovering,true);assert.notEqual(x.service.getSnapshot().phase,'error');
 await tick();await tick();
 const after=x.service.getSnapshot();
 assert.deepEqual(x.reports,[{sessionId:'s1',code:'decode_error',detail:'MEDIA_ERR_DECODE',positionSeconds:40}]);
 assert.equal(x.created(),1,'no new session is started: the queue and its place are kept');
 assert.equal(after.session!.id,'s1');assert.equal(after.session!.generation,2);assert.equal(after.session!.streamUrl,'/media/2');assert.notEqual(after.phase,'error');
 assert.equal(after.pendingSeek!.positionSeconds,40,'the new presentation starts where the viewer was');
});

test('the last resort failing, or a server that cannot escalate, is a real failure',async()=>{
 const x=setup(()=>false);await playing(x);
 x.service.engineFailed(x.service.getSnapshot().intentId,'decode_error','','This media could not be played.');await tick();
 assert.equal(x.service.getSnapshot().phase,'error');assert.equal(x.service.getSnapshot().error,'This media could not be played.');assert.equal(x.created(),1);
 const y=setup(()=>null);await playing(y);
 y.service.engineFailed(y.service.getSnapshot().intentId,'source_not_supported','','Nope.');await tick();
 assert.equal(y.service.getSnapshot().phase,'error');
});

test('escalation is bounded per session and a stale engine cannot trigger it',async()=>{
 const x=setup(()=>true);await playing(x);
 const stale=x.service.getSnapshot().intentId;
 for(let i=0;i<5;i++){const s=x.service.getSnapshot();if(s.phase==='error')break;x.service.ready(s.intentId);x.service.engineFailed(s.intentId,'decode_error','','Failed.');await tick();await tick();}
 assert.equal(x.reports.length,3);assert.equal(x.service.getSnapshot().phase,'error');
 x.service.engineFailed(stale,'decode_error','','Failed.');assert.equal(x.reports.length,3);
});

test('a session the server cannot re-plan (a download) fails without asking',async()=>{
 const x=setup(()=>true,null);await playing(x);
 x.service.engineFailed(x.service.getSnapshot().intentId,'decode_error','','Failed.');await tick();
 assert.equal(x.reports.length,0);assert.equal(x.service.getSnapshot().phase,'error');
});
