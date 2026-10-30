import test from 'node:test';import assert from 'node:assert/strict';import {PlaybackService,type PlaybackApi} from '../src/index.ts';import {validateAudioPlan} from '../src/audio-selection.ts';
const plan=(sessionId='s1')=>validateAudioPlan({policyVersion:1,revision:'plan1',sessionId,generation:1,sourceId:'source',factsRevision:1,defaultRenditionId:'b',renditions:['a','b'].map((id,i)=>({id,sourceStreamIndex:i+1,manifestIdentity:{groupId:'audio',name:'track_'+id,playlistFile:`rendition-${i}.m3u8`},label:'English',language:'en',codec:'aac',channels:2}))});
async function setup(mode:'direct'|'hls'='hls',audioTimeoutMs=1000){let creates=0;const progress:any[]=[];const api:PlaybackApi={createPlayback:async()=>({id:'s'+(++creates),generation:1,mode,streamUrl:'/media',duration:96,resumeSeconds:10}),progressPlayback:async(_id,p)=>{progress.push(p);},stopPlayback:async()=>{}};const s=new PlaybackService(api,()=>String(creates),{audioTimeoutMs});await s.play('item');const t=s.getSnapshot();s.ready(t.intentId);s.seekApplied(t.intentId,t.pendingSeek!.revision,10,'playing');return {s,progress,creates:()=>creates};}
function mapped(s:PlaybackService){assert(s.installAudioPlan(s.getSnapshot().intentId,plan()));const b=s.getSnapshot().audio.binding!,e=s.attachAudioEngine(b)!;assert(s.audioMapped(b,e,['a','b']));return {b,e};}
test('server default is not observation; switching preserves session intent time and progress',async()=>{const {s,progress,creates}=await setup();const {b,e}=mapped(s);assert.equal(s.getSnapshot().audio.observedRenditionId,null);s.audioObserved(b,e,1,'b');const before=progress.length;s.pause();s.selectAudio('a');const t=s.getSnapshot();assert.equal(t.positionSeconds,10);assert.equal(t.intent,'paused');assert.equal(t.audio.observedRenditionId,'b');assert.equal(s.audioApplied(b,e,t.audio.pending!.revision,'a'),false);s.audioObserved(b,e,2,'a');assert(s.audioApplied(b,e,t.audio.pending!.revision,'a'));assert.equal(creates(),1);assert.equal(s.getSnapshot().session?.id,'s1');assert(progress.slice(before).every(p=>p.positionSeconds===10));s.leave();});
test('duplicate click deduplicates; rapid opposite request rejects stale completion and observation',async()=>{const {s}=await setup();const {b,e}=mapped(s);s.selectAudio('b');const first=s.getSnapshot().audio.pending!.revision;s.selectAudio('b');assert.equal(s.getSnapshot().audio.pending!.revision,first);s.selectAudio('a');const second=s.getSnapshot().audio.pending!.revision;s.audioObserved(b,e,2,'b');assert(!s.audioApplied(b,e,first,'b'));assert(!s.audioObserved(b,e,1,'a'));assert(!s.audioFailed(b,e,first,'old'));assert.equal(s.getSnapshot().audio.pending!.revision,second);s.audioObserved(b,e,3,'a');assert(s.audioApplied(b,e,second,'a'));s.leave();});
test('old plans and null offers cannot reset selection; changed immutable plan rejected',async()=>{const {s}=await setup();const {b,e}=mapped(s);s.audioObserved(b,e,1,'a');s.selectAudio('b');const p=s.getSnapshot().audio.pending;assert(s.installAudioPlan(b.intentId,plan()));s.audioUnavailable(b.intentId,'old response');assert.equal(s.getSnapshot().audio.pending,p);assert(!s.installAudioPlan(b.intentId,{...plan(),revision:'new'}));assert(!s.installAudioPlan(b.intentId,{...plan(),generation:2}));assert.equal(s.getSnapshot().audio.observedRenditionId,'a');s.leave();});
test('incomplete mapping and direct sessions never authorize choices',async()=>{const {s}=await setup();assert(s.installAudioPlan(s.getSnapshot().intentId,plan()));const b=s.getSnapshot().audio.binding!,e=s.attachAudioEngine(b)!;assert(!s.audioMapped(b,e,['a','a']));assert(!s.selectAudio('a'));s.leave();const d=await setup('direct');assert(!d.s.installAudioPlan(d.s.getSnapshot().intentId,plan()));assert(!d.s.selectAudio('b'));d.s.leave();});
test('engine replacement fences callbacks/timer; timeout is nonfatal and late factual observation truthful',async()=>{const {s}=await setup('hls',5);const {b,e}=mapped(s);s.audioObserved(b,e,1,'a');s.selectAudio('b');const r=s.getSnapshot().audio.pending!.revision;const fresh=s.attachAudioEngine(b)!;assert(!s.audioObserved(b,e,10,'b'));assert(!s.audioApplied(b,e,r,'b'));s.audioMapped(b,fresh,['a','b']);s.audioObserved(b,fresh,1,'a');s.selectAudio('b');await new Promise(r=>setTimeout(r,12));assert.equal(s.getSnapshot().phase,'ready');assert.equal(s.getSnapshot().intent,'playing');assert.equal(s.getSnapshot().audio.observedRenditionId,'a');assert(s.getSnapshot().audio.failed);s.audioObserved(b,fresh,2,'b');assert.equal(s.getSnapshot().audio.observedRenditionId,'b');assert(s.getSnapshot().audio.failed);s.leave();});
test('leave/new intent and terminal state fence all audio facts; seek remains independent',async()=>{const {s}=await setup();const {b,e}=mapped(s);s.selectAudio('b');s.seek(20);const revision=s.getSnapshot().pendingSeek!.revision;s.audioObserved(b,e,1,'b');s.audioApplied(b,e,s.getSnapshot().audio.pending!.revision,'b');assert.equal(s.getSnapshot().pendingSeek!.revision,revision);assert.equal(s.getSnapshot().positionSeconds,10);s.leave();await s.play('new');assert(!s.audioObserved(b,e,2,'a'));assert(!s.installAudioPlan(b.intentId,plan()));s.fail(s.getSnapshot().intentId,'Failure');assert.equal(s.getSnapshot().audio.plan,null);s.leave();});
test('parser rejects duplicate source/manifest identities, paths and invented output facts',()=>{for(const mutate of [(p:any)=>p.policyVersion=99,(p:any)=>p.renditions[1].sourceStreamIndex=1,(p:any)=>p.renditions[1].manifestIdentity=p.renditions[0].manifestIdentity,(p:any)=>p.renditions[0].manifestIdentity.playlistFile='../private',(p:any)=>p.renditions[0].channels=6,(p:any)=>p.defaultRenditionId='unknown']){const p=JSON.parse(JSON.stringify(plan()));mutate(p);assert.throws(()=>validateAudioPlan(p));}});

test('detaching cancels pending deadline, clears capability and cannot detach newer engine',async()=>{const {s}=await setup('hls',5);const {b,e}=mapped(s);s.selectAudio('b');s.detachAudioEngine(b,e);assert(!s.selectAudio('a'));await new Promise(r=>setTimeout(r,12));assert.equal(s.getSnapshot().audio.failed,undefined);const fresh=s.attachAudioEngine(b)!;s.audioMapped(b,fresh,['a','b']);s.detachAudioEngine(b,e);assert(s.selectAudio('a'));s.leave();});

test('explicit current authority invalidation clears choices and old engine facts without stopping media',async()=>{const {s}=await setup();const {b,e}=mapped(s);s.selectAudio('a');s.invalidateAudioPlan(b.intentId,'Permission changed');assert.equal(s.getSnapshot().audio.plan,null);assert(!s.audioObserved(b,e,1,'a'));assert.equal(s.getSnapshot().phase,'ready');assert.equal(s.getSnapshot().positionSeconds,10);s.leave();});

test('manifest policy2 requires structurally valid server language; v1 remains exact and unknown policy rejects',()=>{for(const language of ['und','en-US','zh-Hant-TW','de-CH-1901','en-u-ca-gregory','x-portico']){const raw=JSON.parse(JSON.stringify(plan()));raw.policyVersion=2;raw.renditions.forEach((r:any)=>{r.manifestLanguage=language;r.sampleRate=48000;});assert.equal(validateAudioPlan(raw).renditions[0].manifestLanguage,language);}for(const language of [undefined,'en_Us','en--US','en-a','en-x','en-a-foo-a-bar','en-12345-12345','en-"bad','éé','x','a','en-'+ 'a'.repeat(60)]){const raw=JSON.parse(JSON.stringify(plan()));raw.policyVersion=2;raw.renditions.forEach((r:any)=>{r.manifestLanguage=language;r.sampleRate=48000;});assert.throws(()=>validateAudioPlan(raw));}const v1=JSON.parse(JSON.stringify(plan()));v1.renditions[0].manifestLanguage='en';assert.throws(()=>validateAudioPlan(v1));assert.equal(validateAudioPlan(plan()).policyVersion,1);});

test('synchronous adapter callbacks retain exact latest command through nested snapshot publication',async()=>{const {s}=await setup();const {b,e}=mapped(s);let seen=0;s.attachAdapter({apply(snapshot){const p=snapshot.audio.pending;if(p&&p.revision!==seen){seen=p.revision;s.audioFailed(b,e,p.revision,'Unavailable audio');}}});s.selectAudio('b');assert.equal(s.getSnapshot().audio.failed?.renditionId,'b');assert.equal(s.getSnapshot().audio.pending,undefined);s.attachAdapter({apply(snapshot){const p=snapshot.audio.pending;if(p&&p.revision!==seen){seen=p.revision;s.audioObserved(b,e,seen,p.renditionId);s.audioApplied(b,e,p.revision,p.renditionId);}}});s.selectAudio('a');assert.equal(s.getSnapshot().audio.observedRenditionId,'a');assert.equal(s.getSnapshot().audio.pending,undefined);assert.equal(s.getSnapshot().audio.failed,undefined);assert.equal(s.getSnapshot().positionSeconds,10);s.leave();});

test('v2 fixed48k output facts survive parsing and installation; missing/other sample rates reject',async()=>{const raw:any=JSON.parse(JSON.stringify(plan()));raw.policyVersion=2;raw.renditions.forEach((r:any)=>{r.manifestLanguage='en';r.sampleRate=48000;});const parsed=validateAudioPlan(raw);assert.equal(parsed.renditions[0].sampleRate,48000);const {s}=await setup();assert(s.installAudioPlan(s.getSnapshot().intentId,parsed));s.leave();for(const value of [undefined,44100,'48000']){raw.renditions[0].sampleRate=value;assert.throws(()=>validateAudioPlan(raw));}});

test('same-option retry after timeout requires a new command and preserves paused playback', async () => {
  const {s, creates} = await setup('hls', 5);
  const {b, e} = mapped(s);
  s.pause();
  s.audioObserved(b, e, 1, 'a');
  s.selectAudio('b');
  const expired = s.getSnapshot().audio.pending!.revision;
  await new Promise(resolve => setTimeout(resolve, 12));
  assert.equal(s.getSnapshot().audio.failed?.revision, expired);
  // Native may report a real option change after the command deadline.
  assert(s.audioObserved(b, e, 2, 'b'));
  assert(!s.audioApplied(b, e, expired, 'b'));
  assert(s.getSnapshot().audio.failed);
  assert(s.selectAudio('b'));
  const retry = s.getSnapshot().audio.pending!.revision;
  assert(retry > expired);
  assert(!s.audioFailed(b, e, expired, 'Late old failure'));
  // Adapter re-reads currentMediaSelection for this explicit retry, even if
  // selecting an already-selected AV option emits no new native notification.
  assert(s.audioObserved(b, e, 3, 'b'));
  assert(s.audioApplied(b, e, retry, 'b'));
  assert.equal(s.getSnapshot().audio.failed, undefined);
  assert.equal(s.getSnapshot().intent, 'paused');
  assert.equal(s.getSnapshot().positionSeconds, 10);
  assert.equal(creates(), 1);
  s.leave();
});

test('native envelope identity mismatches cannot observe, complete or fail current command', async () => {
  const {s} = await setup();
  const {b, e} = mapped(s);
  s.audioObserved(b, e, 1, 'a');
  s.selectAudio('b');
  const revision = s.getSnapshot().audio.pending!.revision;
  for (const patch of [
    {intentId:b.intentId + 1}, {sessionId:'other'}, {generation:b.generation + 1},
    {sourceId:'other'}, {planRevision:'other'},
  ]) {
    const stale = {...b, ...patch};
    assert(!s.audioObserved(stale, e, 99, 'b'));
    assert(!s.audioApplied(stale, e, revision, 'b'));
    assert(!s.audioFailed(stale, e, revision, 'Stale native event'));
  }
  // Invalid high sequence did not consume the current engine's sequence.
  assert(s.audioObserved(b, e, 2, 'b'));
  assert(s.audioApplied(b, e, revision, 'b'));
  s.leave();
});

 test('a confirmed rendition failure disables only that track until the presentation changes',async()=>{const {s}=await setup();const {b,e}=mapped(s);s.selectAudio('b');const revision=s.getSnapshot().audio.pending!.revision;assert(s.audioFailed(b,e,revision,'Unavailable',true));assert(!s.selectAudio('b'));assert(s.selectAudio('a'));assert.equal(s.getSnapshot().phase,'ready');assert(!s.audioRenditionUnavailable({...b,generation:b.generation+1},e,'a'));s.leave();});
