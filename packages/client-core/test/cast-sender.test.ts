import test from 'node:test';
import {captureCastChoices} from '../src/cast/protocol.ts';
import {castProofKey,type CastControllerContext} from '../src/cast/controller-proof.ts';
import assert from 'node:assert/strict';
import {CastController, castChoiceFields, parseCastEvent, type CastCommand, type CastTransport} from '../src/cast/index.ts';

const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(setImmediate); };

/** A fake receiver: records what the sender sends and lets the test answer as the TV would. */
function receiver(deviceName = 'Living Room TV', deviceId?: string) {
  const sent: CastCommand[] = [];
  const onMsg = new Set<(raw: unknown) => void>();
  const onEnd = new Set<() => void>();
  let ended: boolean | undefined;
  let autoAck = true;
  const transport: CastTransport = {
    deviceName,
    ...(deviceId ? {deviceId} : {}),
    send: async m => { sent.push(m); if (m.type === 'load' && autoAck) queueMicrotask(() => { for (const fn of onMsg) fn({type:'loaded',requestId:m.requestId,itemId:m.itemId}); }); },
    onMessage: fn => { onMsg.add(fn); return () => onMsg.delete(fn); },
    onSessionEnd: fn => { onEnd.add(fn); return () => onEnd.delete(fn); },
    end: stop => { ended = stop; },
  };
  return {
    transport, sent,
    set autoAck(value: boolean) { autoAck = value; },
    reply: (m: unknown) => { for (const fn of [...onMsg]) fn(typeof m === 'string' ? m : JSON.stringify(m)); },
    endSession: () => { for (const fn of [...onEnd]) fn(); },
    get ended() { return ended; },
    get listeners() { return onMsg.size; },
  };
}

function harness(code: string | null = 'ABC123') {
  const requests: {path: string; method?: string; body?: unknown}[] = [];
  const timers: {fn: () => void; ms: number; id: number}[] = [];
  let next = 0;
  const controller = new CastController({
    api: {request: async <T,>(path: string, method?: string, body?: unknown) => { requests.push({path, method, body}); return {bootstrap: code ? {code} : {}} as T; }},
    origin: 'https://demo.getportico.tv/some/path',
    displayName: 'Chrome on Mac',
    setTimer: (fn, ms) => { const id = ++next; timers.push({fn, ms, id}); return id; },
    clearTimer: id => { const i = timers.findIndex(t => t.id === id); if (i >= 0) timers.splice(i, 1); },
  });
  return {controller, requests, fire: (ms: number) => { for (const t of timers.filter(x => x.ms === ms)) { timers.splice(timers.indexOf(t), 1); t.fn(); } }};
}

const meta = {title: 'Pilot', seriesTitle: 'The Show', season: 1, episode: 1, kind: 'episode', artworkPath: '/v1/artwork/x'};

test('the subtitle chosen on the sender travels with load: a track id, or off; nothing when unknown', async () => {
  assert.deepEqual(castChoiceFields({subtitleTrackId: 's3'}), {subtitleTrackId: 's3'});
  assert.deepEqual(castChoiceFields({subtitleTrackId: null}), {subtitleTrackId: 'off'});
  assert.deepEqual(castChoiceFields({}), {});
  assert.deepEqual(castChoiceFields(undefined), {});
  assert.deepEqual(castChoiceFields({subtitleTrackId: 'a b'}), {});
  const h = harness(), r = receiver();
  const started = h.controller.start(r.transport, 'item-1', {title: 'Pilot'}, 0, {subtitleTrackId: 's3'});
  await settle();
  r.reply({type: 'paired'});
  assert.equal(await started, true);
  assert.deepEqual(r.sent[1], {type: 'load', requestId: 'load-1', itemId: 'item-1', startSeconds: 0, title: 'Pilot', subtitleTrackId: 's3'});
});

test('pairs with a server-issued code, then loads the title with its display fields', async () => {
  const h = harness(), r = receiver();
  const started = h.controller.start(r.transport, 'item-1', meta, 42);
  await settle();
  assert.deepEqual(h.requests[0], {path: '/v1/cast/bootstrap', method: 'POST', body: {protocolVersion: '1.0', displayName: 'Living Room TV'}});
  assert.deepEqual(r.sent[0], {type: 'pair', code: 'ABC123', origin: 'https://demo.getportico.tv', displayName: 'Chrome on Mac'});
  assert.equal(h.controller.getSnapshot().phase, 'pairing');
  r.reply({type: 'pair-pending'});
  assert.equal(h.controller.getSnapshot().phase, 'waiting', 'the TV is asking the person watching');
  r.reply({type: 'paired'});
  assert.equal(await started, true);
  // The receiver is told the one artwork slot its cover box needs.
  assert.deepEqual(r.sent[1], {type: 'load', requestId: 'load-1', itemId: 'item-1', startSeconds: 42, title: 'Pilot', seriesTitle: 'The Show', season: 1, episode: 1, kind: 'episode', artworkPath: '/v1/artwork/x?size=small'});
  const s = h.controller.getSnapshot();
  assert.equal(s.phase, 'casting');
  assert.equal(s.deviceName, 'Living Room TV');
  assert.equal(s.positionSeconds, 42);
});

test('status updates position, pause and tracks; commands go to the TV', async () => {
  const h = harness(), r = receiver();
  const started = h.controller.start(r.transport, 'item-1', meta, 0);
  await settle(); r.reply({type: 'paired'}); await started;
  r.reply({type: 'status', itemId: 'item-1', positionSeconds: 60, durationSeconds: 1800, paused: true, audioTracks: [{id: 1, language: 'en', name: 'English'}], textTracks: [{id: 3, language: 'fr', name: 'Français'}, {id: 'bad'}]});
  const s = h.controller.getSnapshot();
  assert.deepEqual([s.positionSeconds, s.durationSeconds, s.paused], [60, 1800, true]);
  assert.deepEqual(s.audioTracks, [{id: 1, language: 'en', name: 'English'}]);
  assert.deepEqual(s.textTracks.map(t => t.id), [3], 'malformed tracks are dropped');
  h.controller.toggle(); h.controller.seek(-5); h.controller.setAudioTrack(1); h.controller.setSubtitleTrack(3); h.controller.setSubtitleTrack(null);
  await settle();
  assert.deepEqual(r.sent.slice(2), [{type: 'status'}, {type: 'play'}, {type: 'seek', positionSeconds: 0}, {type: 'audio', trackId: 1}, {type: 'subtitles', trackIds: [3]}, {type: 'subtitles', trackIds: []}]);
  h.controller.stop(); await settle();
  assert.deepEqual(r.sent.at(-1), {type: 'stop'});
  assert.equal(r.ended, true, 'stop closes Portico on the TV');
  assert.equal(h.controller.getSnapshot().phase, 'off');
  assert.equal(r.listeners, 0);
});

test('a busy TV refuses with catalogue copy, never the receiver’s text', async () => {
  const h = harness(), r = receiver();
  const started = h.controller.start(r.transport, 'item-1', meta, 0);
  await settle();
  r.reply({type: 'pair-failed', code: 'tv_busy', message: 'Sam is watching'});
  assert.equal(await started, false);
  const s = h.controller.getSnapshot();
  assert.equal(s.phase, 'error');
  assert.equal(s.errorId, 'cast.error.tvBusy');
  assert.ok(!/Sam/.test(s.error!));
  assert.equal(r.ended, false, 'a refused takeover leaves the current film playing');
});

test('no answer within the pairing window, and no code from the server, are errors', async () => {
  const h = harness(), r = receiver();
  const started = h.controller.start(r.transport, 'item-1', meta, 0);
  await settle(); h.fire(35_000);
  assert.equal(await started, false);
  assert.equal(h.controller.getSnapshot().errorId, 'cast.error.noAnswer');
  const noCode = harness(null), r2 = receiver();
  assert.equal(await noCode.controller.start(r2.transport, 'item-1', meta, 0), false);
  assert.equal(noCode.controller.getSnapshot().errorId, 'cast.error.noCode');
  assert.equal(r2.sent.length, 0, 'nothing is sent to the TV without a code');
});

test('takeover: the controller is asked, answers, and the prompt expires after 30 s', async () => {
  const h = harness(), r = receiver();
  const started = h.controller.start(r.transport, 'item-1', meta, 0);
  await settle(); r.reply({type: 'paired'}); await started;
  r.reply({type: 'takeover-requested', requester: '  Sam’s iPhone  '});
  assert.deepEqual(h.controller.getSnapshot().takeover, {requester: 'Sam’s iPhone', controller: undefined, requestId: undefined});
  h.controller.answerTakeover(false); await settle();
  assert.deepEqual(r.sent.at(-1), {type: 'takeover', allow: false});
  assert.equal(h.controller.getSnapshot().takeover, undefined);
  r.reply({type: 'takeover-requested'});
  assert.equal(h.controller.getSnapshot().takeover?.requester, 'Someone');
  h.fire(30_000);
  assert.equal(h.controller.getSnapshot().takeover, undefined);
});

test('replaced by another sender, or the session ending, releases the TV without stopping it', async () => {
  const h = harness(), r = receiver();
  const started = h.controller.start(r.transport, 'item-1', meta, 0);
  await settle(); r.reply({type: 'paired'}); await started;
  r.reply({type: 'replaced'});
  assert.equal(h.controller.getSnapshot().phase, 'replaced');
  assert.equal(r.ended, false, 'the new sender keeps the TV');
  const h2 = harness(), r2 = receiver();
  const s2 = h2.controller.start(r2.transport, 'item-1', meta, 0);
  await settle(); r2.reply({type: 'paired'}); await s2;
  r2.endSession();
  assert.equal(h2.controller.getSnapshot().phase, 'off');
  assert.equal(r2.listeners, 0);
});

test('receiver messages are validated; HTTP origins are refused', () => {
  assert.equal(parseCastEvent('not json'), undefined);
  assert.equal(parseCastEvent({type: 'surprise'}), undefined);
  assert.deepEqual(parseCastEvent({type: 'pair-failed'}), {type: 'pair-failed', code: 'pair_failed'});
  assert.throws(() => new CastController({api: {request: async () => ({}) as never}, origin: 'http://192.168.1.2:32500', displayName: 'x'}));
});

const scope={serverId:'server',authority:'local',accountId:'account',profileId:'profile',senderDeviceId:'device',senderFamilyId:'family'};
const context:CastControllerContext={...scope,origin:'https://server.test',publicKey:'a'.repeat(43)};
const controllerStamp={bindingId:'cct_test',controllerGeneration:2,connectionGeneration:3};
const occurrence={sessionId:'playback-original',generation:7,itemId:'movie'};
const binding={id:'cct_test',bootstrapId:'cbs_test',receiverId:'tv-a',targetId:'cdv_target',targetFamilyId:'target-family',targetGeneration:'1',scope};
function returning(options:{context?:CastControllerContext;legacy?:boolean;validate?:()=>Promise<void>;describe?:()=>Promise<string|undefined>;setTimer?:(fn:()=>void,ms:number)=>unknown;clearTimer?:(timer:unknown)=>void}={}){
 const saved=new Map([[castProofKey(context,'tv-a'),options.legacy?'a'.repeat(48):JSON.stringify({version:2,context,binding,controlToken:'a'.repeat(48)})]]);
 let current=true,first=true;const requests:string[]=[];
 const controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:options.context??context,isCurrent:()=>current,
 controllerStorage:{get:k=>saved.get(k),set:(k,v)=>{saved.set(k,v);}},describe:options.describe??(async()=> 'Film'),setTimer:options.setTimer,clearTimer:options.clearTimer,
 api:{request:async<T,>(path:string,_method?:string,body?:unknown)=>{requests.push(path);if(first){first=false;await options.validate?.();}return {binding,attemptId:(body as {attemptId:string}).attemptId,ticket:'b'.repeat(43)} as T;}}});
 return {controller,requests,retire:()=>{current=false;controller.dispose();}};
}
test('Root50: legacy/mismatched original scope and unstable receivers disclose no proof/status and leave the receiver alone',async()=>{
 for(const opts of [{legacy:true},{context:{...context,profileId:'other'}},{context:{...context,senderFamilyId:'new-family'}},{context:{...context,publicKey:'b'.repeat(43)}}]){
  const h=returning(opts),tv=receiver('TV','tv-a');assert.equal(await h.controller.adopt(tv.transport),false);assert.deepEqual(tv.sent,[]);assert.equal(tv.ended,undefined);assert.equal(h.requests.length,0);h.controller.dispose();
 }
 const h=returning(),tv=receiver('TV');assert.equal(await h.controller.adopt(tv.transport),false);assert.deepEqual(tv.sent,[]);h.controller.dispose();
});
test('Root50: fresh sender validation precedes proof disclosure, correlated receiver admission and content read precede publication',async()=>{
 let release!:()=>void,describe!:()=>void;
 const h=returning({validate:()=>new Promise<void>(r=>{release=r;}),describe:()=>new Promise<string>(r=>{describe=()=>r('Film');})}),tv=receiver('TV','tv-a');
 const emptyCommands:CastCommand[]=[];
 const adopted=h.controller.adopt(tv.transport);assert.equal(h.requests.length,1);assert.deepEqual(tv.sent,emptyCommands);release();await settle();
 const command=tv.sent[0];assert.equal(command.type,'adopt');if(command.type!=='adopt')throw new Error('no adoption');
 const status={type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:80,durationSeconds:600,paused:false,audioTracks:[],textTracks:[]};
 tv.reply({...status,attemptId:'other-attempt'});assert.equal(h.controller.getSnapshot().phase,'off');
 tv.reply({...status,attemptId:command.attemptId});assert.equal(h.controller.getSnapshot().itemId,undefined);describe();assert.equal(await adopted,true);
 assert.equal(h.controller.getSnapshot().title,'Film');assert.equal(h.controller.getSnapshot().acceptedSource,undefined);assert.equal(tv.sent.some(m=>m.type==='load'||m.type==='pair'),false);h.controller.dispose();
});
test('Root50: validation refusal, caller replacement and late redacted descriptions never stop or populate another caller',async()=>{
 const refused=returning({validate:async()=>{throw new Error('revoked');}}),tv=receiver('TV','tv-a');assert.equal(await refused.controller.adopt(tv.transport),false);assert.deepEqual(tv.sent,[]);assert.equal(tv.ended,undefined);
 let release!:()=>void;const h=returning({validate:()=>new Promise<void>(r=>{release=r;})}),other=receiver('TV','tv-a');const pending=h.controller.adopt(other.transport);h.retire();release();assert.equal(await pending,false);await settle();assert.deepEqual(other.sent,[]);assert.equal(other.ended,undefined);
 const hidden=returning({describe:async()=>undefined}),target=receiver('TV','tv-a'),adopting=hidden.controller.adopt(target.transport);await settle();const command=target.sent[0];if(command.type!=='adopt')throw new Error('no adoption');target.reply({type:'status',controller:controllerStamp,occurrence:{...occurrence,itemId:'hidden'},attemptId:command.attemptId,itemId:'hidden',positionSeconds:9});assert.equal(await adopting,false);assert.equal(hidden.controller.getSnapshot().itemId,undefined);assert.equal(target.ended,undefined);
});

test('load acknowledgement is correlated and a missing/rejected load never claims playback', async () => {
  const h = harness(), r = receiver(); r.autoAck = false;
  const started = h.controller.start(r.transport, 'item-1', meta, 0);
  await settle(); r.reply({type:'paired'}); await settle();
  assert.equal(h.controller.getSnapshot().phase, 'pairing');
  r.reply({type:'loaded',requestId:'load-other',itemId:'item-1'});
  assert.equal(h.controller.getSnapshot().phase, 'pairing');
  r.reply({type:'load-failed',requestId:'load-1'});
  assert.equal(await started, false);
  assert.equal(h.controller.getSnapshot().phase, 'error');
  const second=h.controller.start(r.transport, 'item-1', meta, 0);
  await settle();r.reply({type:'paired'});await settle();h.fire(150_000);
  assert.equal(await second,false);
});
test('a queue load carries only bounded server queue and entry identities', async () => {
  assert.deepEqual(castChoiceFields({queue:{queueId:'q-1',entryId:'entry-2'}}),{queue:{queueId:'q-1',entryId:'entry-2'}});
  assert.deepEqual(castChoiceFields({queue:{queueId:'https://evil.test',entryId:'entry-2'}}),{});
  const h=harness(),r=receiver();const started=h.controller.start(r.transport,'song',meta,0,{queue:{queueId:'q-1',entryId:'entry-2'}});
  await settle();r.reply({type:'paired'});assert.equal(await started,true);
  const load=r.sent.find(m=>m.type==='load');assert.deepEqual(load?.queue,{queueId:'q-1',entryId:'entry-2'});
});


test('returning dock uses real occurrence facts; stale commands and Stop receipts cannot cross occurrences',async()=>{
 const h=returning(),tv=receiver('TV','tv-a');const adopted=h.controller.adopt(tv.transport);await settle();
 const command=tv.sent[0];if(command.type!=='adopt')throw new Error('no adoption');
 const status={type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:80,durationSeconds:600,paused:false};
 tv.reply({...status,attemptId:command.attemptId});assert.equal(await adopted,true);
 const original=h.controller.getSnapshot().remoteIdentity;assert.ok(original);
 h.controller.stop(original);const stop=tv.sent.at(-1);assert.equal(stop?.type,'stop');if(stop?.type!=='stop')throw new Error('no Stop');
 assert.equal(h.controller.getSnapshot().controlPending,true);assert.equal(tv.ended,undefined);
 tv.reply({...status,itemId:'',occurrence:undefined});assert.equal(h.controller.getSnapshot().remoteStopped,false);
 const successor={...occurrence,sessionId:'playback-successor',generation:8};
 tv.reply({...status,occurrence:successor});await settle();
 tv.reply({type:'stopped',requestId:stop.requestId,target:original,positionSeconds:81,durationSeconds:600});
 assert.equal(h.controller.getSnapshot().remoteIdentity?.occurrence.sessionId,'playback-successor');assert.equal(h.controller.getSnapshot().remoteStopped,false);
 const count=tv.sent.length;h.controller.pause(original);h.controller.seek(40,original);h.controller.stop(original);assert.equal(tv.sent.length,count);
 h.controller.stop(h.controller.getSnapshot().remoteIdentity);const next=tv.sent.at(-1);if(next?.type!=='stop')throw new Error('no successor Stop');
 tv.reply({type:'stopped',requestId:next.requestId,target:next.target,positionSeconds:82,durationSeconds:600});
 assert.equal(h.controller.getSnapshot().remoteStopped,true);assert.equal(h.controller.getSnapshot().reported?.positionSeconds,82);assert.equal(tv.ended,undefined);h.controller.dispose();
});
test('private occurrence parser refuses malformed stamps instead of downgrading to unscoped status',()=>{
 assert.equal(parseCastEvent({type:'status',controller:controllerStamp,occurrence:{...occurrence,generation:0},itemId:'movie'}),undefined);
 assert.equal(parseCastEvent({type:'status',controller:controllerStamp,occurrence,itemId:'other'}),undefined);
 assert.equal(parseCastEvent({type:'status',controller:{...controllerStamp,connectionGeneration:NaN},itemId:'movie'}),undefined);
});


test('scoped loadACK before first status never downgrades controls or Stop to latest receiver authority',async()=>{
 const tv=receiver('TV','tv-a'),saved=new Map<string,string>();
 const controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,describe:async()=> 'Film',
 controllerStorage:{get:k=>saved.get(k),set:(k,v)=>{saved.set(k,v);}},
 api:{request:async<T,>(path:string,_method?:string,body?:unknown)=> (path==='/v1/cast/bootstrap'?{bootstrap:{id:binding.bootstrapId,code:'ABC123'}}:{binding,attemptId:(body as {attemptId:string}).attemptId,ticket:'b'.repeat(43)}) as T}});
 const started=controller.start(tv.transport,'movie',{title:'Film'},0);await settle();
 tv.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await started,true);
 assert.equal(controller.getSnapshot().phase,'casting');assert.equal(controller.getSnapshot().remoteIdentity,undefined);
 const before=tv.sent.length;
 controller.pause();controller.play();controller.seek(20);controller.setAudioTrack(1);controller.setSubtitleTrack(null);
 assert.equal(tv.sent.length,before,'held first status supplies no occurrence-scoped control authority');
 controller.stop();assert.equal(tv.sent.length,before);assert.equal(tv.ended,undefined,'no SDK end can stop a successor receiver');
 tv.reply({type:'status',controller:controllerStamp,occurrence:{sessionId:'successor',generation:9,itemId:'later'},itemId:'later',positionSeconds:3,durationSeconds:60});
 controller.stop();controller.pause();assert.equal(tv.sent.length,before);assert.equal(tv.ended,undefined);assert.equal(controller.getSnapshot().remoteIdentity,undefined);controller.dispose();
});

test('late scoped transport refusal cannot detach a reported successor occurrence',async()=>{
 const h=returning(),tv=receiver('TV','tv-a');let refuse!:(e:Error)=>void;
 const held=new Promise<void>((_resolve,reject)=>{refuse=reject;});
 const transport={...tv.transport,send:(message:CastCommand)=>message.type==='pause'?held:tv.transport.send(message)};
 const adopted=h.controller.adopt(transport);await settle();const command=tv.sent[0];if(command.type!=='adopt')throw new Error('no adoption');
 const status={type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:8,durationSeconds:60,paused:false};
 tv.reply({...status,attemptId:command.attemptId});assert.equal(await adopted,true);
 h.controller.pause(h.controller.getSnapshot().remoteIdentity);
 tv.reply({...status,occurrence:{...occurrence,sessionId:'successor',generation:8}});await settle();refuse(new Error('attachment retired'));await settle();
 assert.equal(h.controller.getSnapshot().remoteIdentity?.occurrence.sessionId,'successor');assert.equal(h.controller.getSnapshot().phase,'casting');assert.equal(tv.ended,undefined);h.controller.dispose();
});

test('renewal requires original sender validation and correlated receiver completion before controls resume',async()=>{
 const tv=receiver('TV','tv-a'), saved=new Map<string,string>();
 const next={...binding,targetFamilyId:'target-successor',targetGeneration:'2'};
 const controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,describe:async()=> 'Film',
  controllerStorage:{get:k=>saved.get(k),set:(k,v)=>{saved.set(k,v);}},
  api:{request:async<T,>(path:string,_method?:string,body?:unknown)=>{
   if(path==='/v1/cast/bootstrap')return {bootstrap:{id:binding.bootstrapId,code:'ABC123'}} as T;
   const request=body as {binding:typeof binding;attemptId:string};return {binding:request.binding,attemptId:request.attemptId,ticket:'b'.repeat(43)} as T;
  }}});
 const started=controller.start(tv.transport,'movie',{title:'Film'},0);await settle();
 tv.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await started,true);
 tv.reply({type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:2,durationSeconds:20});await settle();
 const renewedStamp={...controllerStamp,controllerGeneration:controllerStamp.controllerGeneration+1};
 tv.reply({type:'renewal-offer',attemptId:'renew-1',binding:next,previousController:controllerStamp,controller:renewedStamp});await settle();
 const ack=tv.sent.find(m=>m.type==='renewal-ack');assert.ok(ack);assert.equal(controller.getSnapshot().controlPending,true);
 const before=tv.sent.length;controller.seek(3);assert.equal(tv.sent.length,before);
 tv.reply({type:'renewal-complete',attemptId:'other',controller:controllerStamp});assert.equal(controller.getSnapshot().controlPending,true);
 tv.reply({type:'renewal-complete',attemptId:'renew-1',controller:renewedStamp});await settle();
 assert.equal(controller.getSnapshot().controlPending,false);
 assert.equal(JSON.parse(saved.get(castProofKey(context,'tv-a'))!).binding.targetFamilyId,'target-successor');
 controller.seek(3);assert.equal(tv.sent.at(-1)?.type,'seek');controller.dispose();
});

test('renewal reentrancy cannot validate or publish into a successor',async()=>{
 for(const retirement of ['pending','storage','complete'] as const){
  const tv=receiver('TV','tv-a'),saved=new Map<string,string>();let validations=0,armed=false,storageWrites=0;
  const next={...binding,targetFamilyId:'target-successor',targetGeneration:'2'},nextStamp={...controllerStamp,controllerGeneration:controllerStamp.controllerGeneration+1};
  let controller:CastController;
  controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,describe:async()=> 'Film',
   controllerStorage:{get:k=>saved.get(k),set:(k,v)=>{storageWrites++;if(armed&&retirement==='storage')controller.dismiss();saved.set(k,v);}},
   api:{request:async<T,>(path:string,_method?:string,body?:unknown)=>{
    if(path==='/v1/cast/bootstrap')return {bootstrap:{id:binding.bootstrapId,code:'ABC123'}} as T;
    validations++;const request=body as {binding:typeof binding;attemptId:string};return {binding:request.binding,attemptId:request.attemptId,ticket:'b'.repeat(43)} as T;
   }}});
  const started=controller.start(tv.transport,'movie',{title:'Film'},0);await settle();tv.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await started,true);
  tv.reply({type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:2,durationSeconds:20});await settle();
  const before=validations,baseWrites=storageWrites;armed=true;
  const off=controller.subscribe(()=>{const state=controller.getSnapshot();if(retirement==='pending'&&state.controlPending||retirement==='complete'&&!state.controlPending&&state.remoteIdentity?.controller.controllerGeneration===nextStamp.controllerGeneration)controller.dismiss();});
  tv.reply({type:'renewal-offer',attemptId:'renew-1',binding:next,previousController:controllerStamp,controller:nextStamp});await settle();
  if(retirement==='pending'){assert.equal(validations,before);assert.equal(storageWrites,baseWrites);assert(!tv.sent.some(m=>m.type==='renewal-ack'));}
  else {assert(tv.sent.some(m=>m.type==='renewal-ack'));const beforeComplete=tv.sent.length;tv.reply({type:'renewal-complete',attemptId:'renew-1',controller:nextStamp});await settle();assert.equal(controller.getSnapshot().phase,'off');assert.equal(tv.sent.length,beforeComplete);}
  off();controller.dispose();
 }
});

test('renewal proof GET and validation current ports cannot lend admission to a same-device successor',async()=>{
 for(const seam of ['get','validation-current'] as const){
  const old=receiver('Old TV','tv-a'),nextTV=receiver('Next TV','tv-a'),saved=new Map<string,string>();
  const nextBinding={...binding,targetFamilyId:'target-successor',targetGeneration:'2'},nextStamp={...controllerStamp,controllerGeneration:controllerStamp.controllerGeneration+1};
  let controller:CastController,armed=false,validating=false,currentCalls=0,successor:Promise<boolean>|undefined,renewalValidations=0,writes=0,timers=0;
  let baseline:{writes:number;timers:number}|undefined;
  const replace=()=>{armed=false;successor=controller.start(nextTV.transport,'successor',{title:'Next'},0);baseline={writes,timers};};
  controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,describe:async()=> 'Film',
   isCurrent:()=>{if(armed&&seam==='validation-current'&&validating&&++currentCalls===2)replace();return true;},
   controllerStorage:{get:k=>{const original=saved.get(k);if(armed&&seam==='get')replace();return original;},set:(k,v)=>{writes++;saved.set(k,v);}},
   setTimer:(fn,ms)=>{if(ms===15000)timers++;return setTimeout(fn,ms)},clearTimer:t=>clearTimeout(t as ReturnType<typeof setTimeout>),
   api:{request:async<T,>(path:string,_method?:string,body?:unknown)=>{
    if(path==='/v1/cast/bootstrap')return {bootstrap:{id:binding.bootstrapId,code:'ABC123'}} as T;
    const request=body as {binding:typeof binding;attemptId:string};if(request.binding.targetFamilyId===nextBinding.targetFamilyId)renewalValidations++;
    return {binding:request.binding,attemptId:request.attemptId,ticket:'b'.repeat(43)} as T;
   }}});
  const started=controller.start(old.transport,'movie',{title:'Film'},0);await settle();old.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await started,true);
  const validate=(controller as any).validate.bind(controller);
  (controller as any).validate=(...args:any[])=>{validating=true;try{return validate(...args)}finally{validating=false}};
  armed=true;
  old.reply({type:'renewal-offer',attemptId:'renew-old',binding:nextBinding,previousController:controllerStamp,controller:nextStamp});await settle();
  assert.ok(successor);assert.equal(renewalValidations,0);assert.equal(old.sent.some(m=>m.type==='renewal-ack'),false);
  assert.equal(controller.getSnapshot().controlPending,false);assert.equal(controller.getSnapshot().phase,'pairing');
  assert.equal(writes,baseline!.writes);assert.equal(timers,baseline!.timers,'no old timer/status/pending work follows replacement');
  if(seam==='get')assert.equal(timers,0,'storage GET replacement occurs before pending/timer mutation');
  nextTV.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await successor,true);
  assert.equal(controller.getSnapshot().itemId,'successor');
  nextTV.reply({type:'renewal-offer',attemptId:'renew-next',binding:nextBinding,previousController:controllerStamp,controller:nextStamp});await settle();
  assert.ok(nextTV.sent.some(m=>m.type==='renewal-ack'&&m.attemptId==='renew-next'),'successor renewal is independently usable');
  controller.dispose();
 }
});

test('release detaches renewal before clearTimer can install a successor',async()=>{
 const old=receiver('Old','tv-a'),loser=receiver('Intermediate','tv-a'),nextTV=receiver('Successor','tv-a'),saved=new Map<string,string>();
 let controller:CastController,armed=false,next:Promise<boolean>|undefined;
 const timers=new Map<unknown,number>();
 controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,describe:async()=> 'Film',
  controllerStorage:{get:k=>saved.get(k),set:(k,v)=>saved.set(k,v)},
  setTimer:(fn,ms)=>{const handle=setTimeout(fn,ms);timers.set(handle,ms);return handle},
  clearTimer:handle=>{const ms=timers.get(handle);timers.delete(handle);clearTimeout(handle as ReturnType<typeof setTimeout>);if(armed&&ms===15000){armed=false;next=controller.start(nextTV.transport,'next',{title:'Next'},0)}},
  api:{request:async<T,>(path:string,_method?:string,body?:unknown)=>{
   if(path==='/v1/cast/bootstrap')return {bootstrap:{id:binding.bootstrapId,code:'ABC123'}} as T;
   const request=body as {binding:typeof binding;attemptId:string};return {binding:request.binding,attemptId:request.attemptId,ticket:'b'.repeat(43)} as T;
  }}});
 const started=controller.start(old.transport,'movie',{title:'Film'},0);await settle();old.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await started,true);
 const renewed={...binding,targetFamilyId:'target-successor',targetGeneration:'2'},stamp={...controllerStamp,controllerGeneration:controllerStamp.controllerGeneration+1};
 old.reply({type:'renewal-offer',attemptId:'old-renew',binding:renewed,previousController:controllerStamp,controller:stamp});await settle();
 assert.equal(controller.getSnapshot().controlPending,true);
 armed=true;const intermediate=controller.start(loser.transport,'loser',{title:'Lost'},0);assert.equal(await intermediate,false);await settle();
 assert.ok(next);assert.equal(old.ended,undefined,'old release cannot end an SDK connection after reentrant successor admission');assert.equal(loser.sent.length,0);assert.equal(controller.getSnapshot().phase,'pairing');assert.equal(controller.getSnapshot().controlPending,false);
 nextTV.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await next,true);
 nextTV.reply({type:'renewal-offer',attemptId:'next-renew',binding:renewed,previousController:controllerStamp,controller:stamp});await settle();
 assert.ok(nextTV.sent.some(m=>m.type==='renewal-ack'&&m.attemptId==='next-renew'));
 nextTV.reply({type:'renewal-complete',attemptId:'next-renew',controller:stamp});await settle();assert.equal(controller.getSnapshot().controlPending,false);assert.equal(controller.getSnapshot().itemId,'next');
 controller.dispose();
});

test('only new original-correlated pending evidence re-arms the finite LOAD deadline',async()=>{
 const tv=receiver('TV','tv-a');tv.autoAck=false;
 const timers=new Map<number,{fn:()=>void;ms:number}>();let counter=0;
 const controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,
  controllerStorage:{get:()=>undefined,set:()=>{}},setTimer:(fn,ms)=>{timers.set(++counter,{fn,ms});return counter},clearTimer:id=>{timers.delete(id as number)},
  api:{request:async<T,>(path:string,_method?:string,body?:unknown)=>path==='/v1/cast/bootstrap'?{bootstrap:{id:binding.bootstrapId,code:'ABC123'}} as T:{binding,attemptId:(body as {attemptId:string}).attemptId,ticket:'b'.repeat(43)} as T}});
 const started=controller.start(tv.transport,'movie',{title:'Film'},0);await settle();tv.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});await settle();
 const command=tv.sent.find(m=>m.type==='load');assert.ok(command);
 const originalTimer=[...timers].find(([,timer])=>timer.ms===150000)!;
 const pending={type:'load-pending',requestId:command.requestId,itemId:'movie',startId:'original-start',sequence:1,controller:controllerStamp};
 tv.reply(pending);const currentTimer=[...timers].find(([,timer])=>timer.ms===150000)!;assert.notEqual(currentTimer[0],originalTimer[0]);
 for(const wrong of [pending,{...pending,sequence:2,startId:'other-start'},{...pending,sequence:2,requestId:'other-load'},{...pending,sequence:2,itemId:'other-item'},{...pending,sequence:2,controller:{...controllerStamp,controllerGeneration:controllerStamp.controllerGeneration+1}},{...pending,sequence:0}]){
  tv.reply(wrong);assert.equal([...timers].find(([,timer])=>timer.ms===150000)![0],currentTimer[0]);
 }
 originalTimer[1].fn();await settle();assert.notEqual(controller.getSnapshot().phase,'error','a cancelled old deadline cannot terminate the continued original load');
 currentTimer[1].fn();assert.equal(await started,false,'absence of further valid progress still has a finite no-answer outcome');
 assert.equal(controller.getSnapshot().remoteIdentity,undefined);controller.dispose();
});

test('renewal-complete publish and final status predicates cannot borrow a same-device successor',async()=>{
 for(const seam of ['publish','status'] as const){
  const old=receiver('Old','tv-a'),nextTV=receiver('Next','tv-a'),saved=new Map<string,string>();
  const renewed={...binding,targetFamilyId:'target-successor',targetGeneration:'2'},stamp={...controllerStamp,controllerGeneration:controllerStamp.controllerGeneration+1};
  let controller:CastController,armed=false,next:Promise<boolean>|undefined;
  controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,describe:async()=> 'Film',
   isCurrent:()=>{const accepted=(controller as any)?.acceptedController,state=controller?.getSnapshot();if(armed&&accepted?.controllerGeneration===stamp.controllerGeneration&&(seam==='publish'?state.controlPending:!state.controlPending)){armed=false;next=controller.start(nextTV.transport,'next',{title:'Next'},0)}return true;},
   controllerStorage:{get:k=>saved.get(k),set:(k,v)=>saved.set(k,v)},api:{request:async<T,>(path:string,_method?:string,body?:unknown)=>path==='/v1/cast/bootstrap'?{bootstrap:{id:binding.bootstrapId,code:'ABC123'}} as T:{binding:(body as any).binding,attemptId:(body as any).attemptId,ticket:'b'.repeat(43)} as T}});
  const first=controller.start(old.transport,'movie',{title:'Film'},0);await settle();old.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await first,true);
  old.reply({type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:2,durationSeconds:60});await settle();
  old.reply({type:'renewal-offer',attemptId:'renew-tail',binding:renewed,previousController:controllerStamp,controller:stamp});await settle();
  const before=old.sent.length;armed=true;
  old.reply({type:'renewal-complete',attemptId:'renew-tail',controller:stamp});await settle();
  assert.ok(next,seam);assert.equal(old.sent.length,before,'no old final status dispatch');
  assert.equal(controller.getSnapshot().phase,'pairing');assert.equal(controller.getSnapshot().remoteIdentity,undefined);assert.equal(controller.getSnapshot().controlPending,false);
  assert.equal(nextTV.sent.some(m=>m.type==='status'),false);
  nextTV.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await next,true);
  nextTV.reply({type:'status',controller:controllerStamp,occurrence:{...occurrence,itemId:'next',sessionId:'next'},itemId:'next',positionSeconds:1,durationSeconds:60});await settle();
  controller.pause(controller.getSnapshot().remoteIdentity);assert.equal(nextTV.sent.at(-1)?.type,'pause','successor controls remain independently usable');controller.dispose();
 }
});

const originalTransferSource={sessionId:'source-original',generation:7,queueId:'queue-original',entryId:'entry-original',queueRevision:'"3"'} as const;
const originalTransferChoices={source:originalTransferSource,queue:{queueId:originalTransferSource.queueId,entryId:originalTransferSource.entryId}};

test('still-owned direct and transfer LOAD settle locally on false currentness without any IO',async()=>{
 for(const transfer of [false,true])for(const seam of ['deadline','clear'] as const){
  const tv=receiver('TV','tv-a');tv.autoAck=false;
  const timers=new Map<number,{fn:()=>void;ms:number}>(),saved=new Map<string,string>();let counter=0,current=true,flip=false;
  const requests:{path:string;method?:string;body?:unknown}[]=[];
  const controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,isCurrent:()=>current,
   controllerStorage:{get:k=>saved.get(k),set:(k,v)=>saved.set(k,v)},setTimer:(fn,ms)=>{timers.set(++counter,{fn,ms});return counter},clearTimer:id=>{const timer=timers.get(id as number);timers.delete(id as number);if(flip&&timer?.ms===150000){flip=false;current=false}},
   api:{request:async<T,>(path:string,method?:string,body?:unknown)=>{
    requests.push({path,method,body});
    if(path==='/v1/cast/bootstrap')return {bootstrap:{id:binding.bootstrapId,code:'ABC123',...(transfer?{transfer:{id:binding.bootstrapId,state:'pending'}}:{})}} as T;
    if(path.includes('/v1/cast/bootstrap/'))return {bootstrap:{transfer:{id:binding.bootstrapId,state:method==='DELETE'?'cancelled':'bound'}}} as T;
    return {binding,attemptId:(body as any).attemptId,ticket:'b'.repeat(43)} as T;
   }}});
  let settlements=0;
  const started=controller.start(tv.transport,'movie',{title:'Film'},0,transfer?originalTransferChoices:undefined).then(result=>{settlements++;return result});
  await settle();tv.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});await settle();
  const load=tv.sent.find(m=>m.type==='load');assert.ok(load);
  const flight=(controller as any).transferFlight,oldTimers=[...timers.values()];
  if(transfer){assert.equal(flight.id,binding.bootstrapId);assert.deepEqual((load.queue as any).transferId,binding.bootstrapId);assert.deepEqual((requests[0].body as any).source,originalTransferSource);}
  const before=tv.sent.length,apiBefore=requests.length;
  if(seam==='deadline'){current=false;oldTimers.find(timer=>timer.ms===150000)!.fn()}
  else {flip=true;tv.reply({type:'load-pending',requestId:load.requestId,itemId:'movie',startId:'original',sequence:1,controller:controllerStamp})}
  assert.equal(await started,false);assert.equal(settlements,1);assert.equal((controller as any).loads.size,0);assert.equal(timers.size,0);
  assert.equal(tv.sent.length,before,'false currentness permits no Stop/status/replay');assert.equal(requests.length,apiBefore,'including no transfer GET/DELETE/retry');assert.equal(tv.ended,undefined);
  assert.equal((controller as any).transport,undefined);assert.equal((controller as any).transferFlight,undefined);
  if(transfer){assert.equal((controller as any).recoveryTransfer,flight,'retain exact uncertain original receipt');assert.equal(flight.id,binding.bootstrapId);assert.deepEqual(flight.source,originalTransferSource);assert.equal(flight.cancellation,undefined);}
  assert.equal(controller.getSnapshot().acceptedSource,undefined,'no inferred transfer acceptance');
  // A fresh admitted sender owns this same SDK transport independently. Late
  // original timer/retry callbacks must not mutate or dispatch through it.
  tv.autoAck=true;
  const successor=new CastController({origin:context.origin,displayName:'Next',controllerContext:context,controllerStorage:{get:k=>saved.get(k),set:(k,v)=>saved.set(k,v)},
   api:{request:async<T,>(path:string,_method?:string,body?:unknown)=>path==='/v1/cast/bootstrap'?{bootstrap:{id:binding.bootstrapId,code:'ABC123'}} as T:{binding,attemptId:(body as any).attemptId,ticket:'b'.repeat(43)} as T}});
  const next=successor.start(tv.transport,'next',{title:'Next'},0);await settle();tv.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await next,true);
  const snapshot=successor.getSnapshot(),sent=tv.sent.length;
  for(const timer of oldTimers)timer.fn();await settle();
  assert.equal(successor.getSnapshot(),snapshot);assert.equal(tv.sent.length,sent);assert.equal(tv.listeners,1);assert.equal(tv.ended,undefined);assert.equal(requests.length,apiBefore);assert.equal(settlements,1);
  if(transfer)assert.equal((controller as any).recoveryTransfer,flight);
  controller.dispose();successor.dispose();
 }
});

test('explicit admitted transfer cancellation still uses only its original issued receipt',async()=>{
 const tv=receiver('TV','tv-a');tv.autoAck=false;const requests:{path:string;method?:string}[]=[],saved=new Map<string,string>();
 const controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,controllerStorage:{get:k=>saved.get(k),set:(k,v)=>saved.set(k,v)},
  api:{request:async<T,>(path:string,method?:string,body?:unknown)=>{
   requests.push({path,method});
   if(path==='/v1/cast/bootstrap')return {bootstrap:{id:binding.bootstrapId,code:'ABC123',transfer:{id:binding.bootstrapId,state:'pending'}}} as T;
   if(path.includes('/v1/cast/bootstrap/'))return {bootstrap:{transfer:{id:binding.bootstrapId,state:'cancelled'}}} as T;
   return {binding,attemptId:(body as any).attemptId,ticket:'b'.repeat(43)} as T;
  }}});
 const start=controller.start(tv.transport,'movie',{title:'Film'},0,originalTransferChoices);await settle();tv.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});await settle();
 controller.stop();await settle();assert.equal(await start,false);
 assert.deepEqual(requests.filter(request=>request.method==='DELETE'),[{path:'/v1/cast/bootstrap/'+binding.bootstrapId,method:'DELETE'}]);
 assert.equal(requests.some(request=>request.method==='GET'),false);assert.equal((controller as any).recoveryTransfer,undefined);assert.equal(controller.getSnapshot().transferPending,false);controller.dispose();
});

test('late pair send rejection and timeout cannot clear a waiter installed by release cleanup',async()=>{
 const old=receiver('Old'),middle=receiver('Middle'),nextTV=receiver('Next');
 let rejectOld!:(error:Error)=>void,next:Promise<boolean>|undefined,controller:CastController,armed=false,counter=0;
 const blocked=new Promise<void>((_resolve,reject)=>{rejectOld=reject}),timers=new Map<number,{fn:()=>void;ms:number}>();
 const oldTransport={...old.transport,send:(message:CastCommand)=>message.type==='pair'?blocked:old.transport.send(message)};
 controller=new CastController({origin:'https://server.test',displayName:'Phone',
  setTimer:(fn,ms)=>{timers.set(++counter,{fn,ms});return counter},clearTimer:id=>{const timer=timers.get(id as number);timers.delete(id as number);if(armed&&timer?.ms===35000){armed=false;next=controller.start(nextTV.transport,'next',{title:'Next'},0)}},
  api:{request:async<T,>()=>({bootstrap:{code:'ABC123'}}) as T}});
 const first=controller.start(oldTransport,'movie',{title:'Film'},0);await settle();
 const oldTimeout=[...timers.values()].find(timer=>timer.ms===35000)!;assert.ok(oldTimeout);
 armed=true;assert.equal(await controller.start(middle.transport,'middle',{title:'Middle'},0),false);await settle();assert.ok(next);
 rejectOld(new Error('late old pair send'));oldTimeout.fn();await settle();assert.equal(await first,false);
 assert.equal(controller.getSnapshot().phase,'pairing');assert.equal(nextTV.listeners,1);
 nextTV.reply({type:'paired'});assert.equal(await next,true);assert.equal(controller.getSnapshot().itemId,'next');controller.dispose();
});

test('adoption refusal cannot release a successor started by its captured timer cleanup',async()=>{
 const old=receiver('Old','tv-a'),nextTV=receiver('Next','tv-a');
 const saved=new Map([[castProofKey(context,'tv-a'),JSON.stringify({version:2,context,binding,controlToken:'a'.repeat(48)})]]),timers=new Map<number,{fn:()=>void;ms:number}>();
 let controller:CastController,counter=0,armed=false,next:Promise<boolean>|undefined;
 controller=new CastController({origin:context.origin,displayName:'Phone',controllerContext:context,describe:async()=> 'Film',
  controllerStorage:{get:k=>saved.get(k),set:(k,v)=>saved.set(k,v)},setTimer:(fn,ms)=>{timers.set(++counter,{fn,ms});return counter},clearTimer:id=>{const timer=timers.get(id as number);timers.delete(id as number);if(armed&&timer?.ms===5000){armed=false;next=controller.start(nextTV.transport,'next',{title:'Next'},0)}},
  api:{request:async<T,>(path:string,_method?:string,body?:unknown)=>path==='/v1/cast/bootstrap'?{bootstrap:{id:binding.bootstrapId,code:'ABC123'}} as T:{binding,attemptId:(body as any).attemptId,ticket:'b'.repeat(43)} as T}});
 const adopted=controller.adopt(old.transport);await settle();assert.equal(old.sent[0]?.type,'adopt');
 armed=true;old.reply({type:'pair-required'});assert.equal(await adopted,false);await settle();assert.ok(next);
 assert.equal(nextTV.listeners,1);assert.equal(controller.getSnapshot().phase,'pairing');assert.equal(nextTV.ended,undefined);
 nextTV.reply({type:'paired',binding,controlToken:'a'.repeat(48),controller:controllerStamp});assert.equal(await next,true);assert.equal(controller.getSnapshot().itemId,'next');controller.dispose();
});

test('CAST-03: same-occurrence statuses share a slow description and publish the newest clock',async()=>{
 let calls=0,resolve!:(title:string)=>void;
 const h=returning({describe:()=>++calls===1?Promise.resolve('Film'):new Promise<string>(done=>{resolve=done;})}),tv=receiver('TV','tv-a');
 try{
  const adopting=h.controller.adopt(tv.transport);await settle();const command=tv.sent[0];assert.equal(command.type,'adopt');if(command.type!=='adopt')return;
  const status={type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:80,durationSeconds:600,paused:false};
  tv.reply({...status,attemptId:command.attemptId});assert.equal(await adopting,true);
  tv.reply({...status,positionSeconds:90});await settle();
  tv.reply({...status,positionSeconds:92});tv.reply({...status,positionSeconds:94});await settle();
  assert.equal(calls,2,'regular status packets do not supersede the in-flight description');
  resolve('Film');await settle();assert.equal(h.controller.getSnapshot().title,'Film');assert.equal(h.controller.getSnapshot().positionSeconds,94);
 }finally{h.controller.dispose();}
});

test('CAST-03: transient descriptions keep authorized controls and retry; denial still redacts',async()=>{
 const timers=new Map<number,{fn:()=>void;ms:number}>();let next=0,calls=0,failure=0;
 const h=returning({describe:async()=>{calls++;if(failure)throw Object.assign(new Error('lookup failed'),{status:failure});return 'Film';},setTimer:(fn,ms)=>{timers.set(++next,{fn,ms});return next;},clearTimer:t=>{timers.delete(t as number);}}),tv=receiver('TV','tv-a');
 try{
  const adopting=h.controller.adopt(tv.transport);await settle();const command=tv.sent[0];assert.equal(command.type,'adopt');if(command.type!=='adopt')return;
  const status={type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:80,durationSeconds:600,paused:false};
  tv.reply({...status,attemptId:command.attemptId});assert.equal(await adopting,true);
  failure=503;tv.reply({...status,positionSeconds:90});await settle();
  assert.equal(h.controller.getSnapshot().phase,'casting');assert.equal(h.controller.getSnapshot().title,'Film');assert.equal(h.controller.getSnapshot().positionSeconds,90);assert.equal(tv.listeners,1);
  h.controller.pause();assert.equal(tv.sent.at(-1)?.type,'pause');
  tv.reply({...status,positionSeconds:92});await settle();assert.equal(calls,2,'status packets respect the backoff');
  const retry=[...timers].find(([,t])=>t.ms===1000);assert.ok(retry);timers.delete(retry[0]);failure=0;retry[1].fn();await settle();
  assert.equal(calls,3);assert.equal(h.controller.getSnapshot().positionSeconds,92);
  failure=403;tv.reply({...status,positionSeconds:94});await settle();assert.equal(h.controller.getSnapshot().phase,'off');assert.equal(h.controller.getSnapshot().title,undefined);assert.equal(h.controller.getSnapshot().remoteIdentity,undefined);assert.equal(tv.listeners,0);assert.equal(tv.ended,undefined);
 }finally{h.controller.dispose();}
});

test('V01: selected audio identity travels with load and malformed identities are omitted',async()=>{
 assert.deepEqual(castChoiceFields({audioTrackId:'a2'}),{audioTrackId:'a2'});
 assert.deepEqual(castChoiceFields({audioTrackId:'a b'}),{});
 const h=harness(),r=receiver();
 try{
  const started=h.controller.start(r.transport,'item-1',{title:'Pilot'},0,{audioTrackId:'a2',subtitleTrackId:null});await settle();r.reply({type:'paired'});assert.equal(await started,true);
  const load=r.sent.find(m=>m.type==='load');assert.ok(load);assert.equal(load.audioTrackId,'a2');assert.equal(load.subtitleTrackId,'off');
 }finally{h.controller.dispose();}
});

test('V02: bounded version choices survive capture and load; malformed optional ids are omitted',async()=>{
 for(const versionId of ['edition-2', 'v'.repeat(128)])assert.deepEqual(castChoiceFields(captureCastChoices({versionId})),{versionId});
 for(const versionId of ['', 'bad id', 'v'.repeat(129), 7, null])assert.deepEqual(castChoiceFields(captureCastChoices({versionId} as any)),{});
 const h=harness(),tv=receiver();
 try{
  const choices={versionId:'requested-edition'};
  const start=h.controller.start(tv.transport,'movie',{title:'Film'},0,choices);choices.versionId='mutated';
  await settle();tv.reply({type:'paired'});assert.equal(await start,true);
  const load=tv.sent.find(m=>m.type==='load');assert.equal(load?.versionId,'requested-edition');
  assert.equal(h.controller.getSnapshot().versionId,undefined,'a requested version is not an accepted version');
  tv.reply({type:'status',itemId:'movie',versionId:'unbound-edition',positionSeconds:2,durationSeconds:60});
  assert.equal(h.controller.getSnapshot().versionId,undefined);assert.equal(h.controller.getSnapshot().reported?.versionId,undefined);
 }finally{h.controller.dispose();}
});

test('V02: private status validates the version without turning malformed ids into facts',()=>{
 for(const versionId of ['edition-2','v'.repeat(128),'', 'bad id','v'.repeat(129),7,null,undefined]){
  const event=parseCastEvent({type:'status',itemId:'movie',versionId,positionSeconds:2,durationSeconds:60});
  assert.equal(event?.type,'status');if(event?.type!=='status')throw new Error('missing status');
  assert.equal(event.versionId,typeof versionId==='string'&&versionId.length>0&&versionId.length<=128&&!versionId.includes(' ')?versionId:undefined);
 }
});

test('V02: adoption and correlated Stop retain the accepted edition; successors never inherit it',async()=>{
 const h=returning(),tv=receiver('TV','tv-a');
 try{
  const adopting=h.controller.adopt(tv.transport);await settle();const command=tv.sent[0];if(command.type!=='adopt')throw new Error('no adoption');
  const status={type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:80,durationSeconds:600,paused:false,versionId:'edition-a'};
  tv.reply({...status,attemptId:command.attemptId});assert.equal(await adopting,true);
  assert.equal(h.controller.getSnapshot().versionId,'edition-a');assert.equal(h.controller.getSnapshot().reported?.versionId,'edition-a');
  h.controller.stop(h.controller.getSnapshot().remoteIdentity);const stop=tv.sent.at(-1);if(stop?.type!=='stop')throw new Error('no Stop');
  tv.reply({type:'stopped',requestId:stop.requestId,target:stop.target,positionSeconds:81,durationSeconds:600,versionId:'untrusted'});
  assert.equal(h.controller.getSnapshot().versionId,'edition-a');assert.equal(h.controller.getSnapshot().reported?.versionId,'edition-a');
  const next={...status,occurrence:{...occurrence,sessionId:'edition-b-session',generation:8},versionId:'edition-b'};
  tv.reply(next);await settle();assert.equal(h.controller.getSnapshot().versionId,'edition-b');assert.equal(h.controller.getSnapshot().reported?.versionId,'edition-b');
  tv.reply({...next,occurrence:{...next.occurrence,sessionId:'unknown-edition',generation:9},versionId:undefined});await settle();
  assert.equal(h.controller.getSnapshot().versionId,undefined);assert.equal(h.controller.getSnapshot().reported?.versionId,undefined);
  tv.reply({...next,occurrence:{...next.occurrence,sessionId:'edition-b-again',generation:10}});await settle();assert.equal(h.controller.getSnapshot().versionId,'edition-b');
  tv.reply({type:'replaced'});assert.equal(h.controller.getSnapshot().versionId,undefined);assert.equal(h.controller.getSnapshot().reported,undefined);
 }finally{h.controller.dispose();}
});

test('V02: redaction clears the accepted edition and its reported return state',async()=>{
 let denied=false;const h=returning({describe:async()=>denied?undefined:'Film'}),tv=receiver('TV','tv-a');
 try{
  const adopting=h.controller.adopt(tv.transport);await settle();const command=tv.sent[0];if(command.type!=='adopt')throw new Error('no adoption');
  const status={type:'status',controller:controllerStamp,occurrence,itemId:'movie',positionSeconds:2,durationSeconds:60,versionId:'edition'};
  tv.reply({...status,attemptId:command.attemptId});assert.equal(await adopting,true);
  denied=true;tv.reply(status);await settle();assert.equal(h.controller.getSnapshot().disclosureRefused,true);
  assert.equal(h.controller.getSnapshot().versionId,undefined);assert.equal(h.controller.getSnapshot().reported,undefined);
 }finally{h.controller.dispose();}
});
