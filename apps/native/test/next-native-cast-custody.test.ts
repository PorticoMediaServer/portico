import assert from 'node:assert/strict';
import test from 'node:test';
import {castProofKey} from '../../../packages/client-core/src/cast/controller-proof.ts';
import {CastController, sameCastRemote, parseCastEvent, type CastCommand} from '../../../packages/client-core/src/cast/index.ts';
import {sameFence} from '../../../packages/player-core/src/index.ts';
import {castOffered} from '../src-next/platform/play-on/cast-offered.ts';
import {castOrigin} from '../../../packages/client-core/src/connection-policy.ts';
import {screenModule} from './helpers/screen-harness.mjs';

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {resolve = done;});
  return {promise, resolve};
};

/** Real caller and shared controller; only the native SDK and server IO are held
 * at their public boundaries so the queue event can precede either ACK or status. */
async function fixture(options:{returning?:boolean;legacy?:boolean;validation?:Promise<void>;continuation?:Promise<void>;storage?:Promise<void>}={}) {
  const listeners = new Map<string, Set<(value?: any) => void>>();
  const emit = (name: string, value?: unknown) => {for (const listener of [...(listeners.get(name) ?? [])]) listener(value);};
  class Emitter {
    addListener(name: string, listener: (value?: any) => void) {
      const held = listeners.get(name) ?? new Set(); held.add(listener); listeners.set(name, held);
      return {remove: () => held.delete(listener)};
    }
  }
  const sent: CastCommand[] = [], requests: {path: string; method?: string; body?: any}[] = [], effects:any[]=[],starts:any[]=[];
  const loaded = deferred<Extract<CastCommand, {type: 'load'}>>(), read = deferred<void>(), cancellation = deferred<void>();
  const receipt = deferred<unknown>(), cancelReceipt = deferred<unknown>();
  const message = (value: unknown) => emit('PorticoCastMessage', value);
  const attachmentId='11111111-1111-4111-8111-111111111111';
  const stamp={bindingId:'cct_return',controllerGeneration:2,connectionGeneration:3},occurrence={sessionId:'receiver',generation:2,itemId:'song'};
  let selected=options.returning?{deviceName:'TV',deviceId:'sdk_device',attachmentId}:null;
  let chooserCalls = 0;
  const bridge = {applicationId: 'APP', available: async () => true,current:async()=>selected, choose() {chooserCalls++;}};
  const transport = {
    send: async (value: CastCommand) => {
      sent.push(value);
      if (value.type === 'pair') message({type:'paired',binding,controller:stamp,controlToken:'a'.repeat(48)});
      if(value.type==='adopt')message({type:'status',controller:stamp,occurrence,attemptId:value.attemptId,itemId:'song',positionSeconds:80,durationSeconds:240,paused:false,audioTracks:[],textTracks:[]});
      if (value.type === 'load') loaded.resolve(value);
      if (value.type === 'status') message({type:'status',controller:stamp,occurrence,itemId:'song', positionSeconds: 12, durationSeconds: 240, paused: false});
    },
    onMessage: (listener: (value: unknown) => void) => {const sub = new Emitter().addListener('PorticoCastMessage', listener); return () => sub.remove();},
    end() {},
  };
  const scope = {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', controllerId: 'device', controllerEpoch: 'epoch', commandLaneId: 'lane'};
  const fence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'original', itemId: 'song', sourceId: 'version', entryId: 'entry', generation: 1};
  let dock: any = {local: {kind: 'music', fence, queue: null, positionSeconds: 12, playing: true, expanded: false}, remote: null};
  let snapshot: any = {authority: {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'viewer', viewerFence: 'viewer_1'},
    fence, dock, playback: {phase: 'ready', positionSeconds: 12, playbackRun: 1, session: {id: 'original', generation: 1}, itemId: 'song'},
    session: {id: 'original', itemId: 'song', presentation: {generation: 1}, queue: {queueId: 'queue', entryId: 'entry'}},
    queue: {view: {queue: {id: 'queue', revision: '10', currentEntryId: 'entry', currentPlaybackId: 'original'}}},
    entry: {id: 'song', kind: 'song', title: 'Actual song', playback: {itemId: 'song'}},
    subject: {kind: 'music', item: {id: 'song', kind: 'song', title: 'Actual song'}}};
  const playbackListeners = new Set<() => void>();
  const adapter = {
    start:async(value:any)=>{starts.push(value);},
    getSnapshot: () => ({...snapshot, dock}), subscribe: (listener: () => void) => {playbackListeners.add(listener); return () => playbackListeners.delete(listener);},
    dock: {getSnapshot: () => dock},
    onTransition: async (transition: any) => {effects.push(...transition.effects);},
    beginRemote: async (remote: any) => {if (!dock.local || sameFence(dock.local.fence, remote.playback.fence)) dock = {...dock, remote};},
    updateRemote: async (remote: any) => {if (dock.remote?.id === remote.id) dock = {...dock, remote};},
    dismissRemote: async (id: string) => {if (dock.remote?.id === id) dock = {...dock, remote: null};},
  };
  const proofScope={serverId:scope.serverId,authority:scope.authority,accountId:scope.accountId,profileId:scope.profileId,senderDeviceId:'device',senderFamilyId:'family'};
  const proofContext={...proofScope,origin:'https://server.test',publicKey:'a'.repeat(43)};
  const binding={id:'cct_return',bootstrapId:'cbs_intent',receiverId:'sdk_device',targetId:'cdv_target',targetFamilyId:'target-family',targetGeneration:'1',scope:proofScope};
  const records=new Map(options.legacy?[[`portico.cast.controller:https://server.test:id:sdk_device`,'a'.repeat(48)]]:[[castProofKey(proofContext,'sdk_device'),JSON.stringify({version:2,context:proofContext,binding,controlToken:'a'.repeat(48)})]]);
  let current=true;
  const api = {baseUrl: 'https://server.test', request: async (path: string, method?: string, body?: any) => {
    requests.push({path, method, body});
    if(path==='/v1/cast/controllers/validate'){await options.validation;return {binding,attemptId:body.attemptId,ticket:'b'.repeat(43)};}
    if(path==='/v1/items/song'){if(requests.filter(request=>request.path===path).length>1)await options.continuation;return {id:'song',kind:'song',title:'Actual title'};}
    if (path === '/v1/cast/configuration') return {applicationId: 'APP'};
    if (path === '/v1/cast/bootstrap' && method === 'POST') return {bootstrap: {id: 'cbs_intent', code: '123456', transfer: {id: 'cbs_intent', state: 'pending'}}};
    if (path === '/v1/cast/bootstrap/cbs_intent' && method === 'GET') {read.resolve(); return receipt.promise;}
    if (path === '/v1/cast/bootstrap/cbs_intent' && method === 'DELETE') {cancellation.resolve(); return cancelReceipt.promise;}
    throw new Error(`Unexpected request: ${method} ${path}`);
  }};
  const {NativeCastCaller} = await screenModule(new URL('../src-next/platform/play-on/cast.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeEventEmitter: Emitter, NativeModules: {PorticoCast: bridge}, Platform: {isTV: false}},
    '../../../../../packages/client-core/src/connection-policy': {castOrigin},
    '../../../../../packages/client-core/src/cast': {CastController,sameCastRemote,parseCastEvent},
    '../../../../../packages/player-core/src': {sameFence}, './cast-transport': {castTransport: transport,validCastAttachment:(value:unknown)=>value===attachmentId},
    './cast-controller-storage': {castControllerStorage: {get: (key:string) => records.get(key), set: (key: string, value: string) => {records.set(key, value);}}, castControllerStorageReady: options.storage ?? Promise.resolve()},
    './cast-offered': {castOffered},
  });
  const session = {sessionFamilyId: 'family',deviceId:'device',serverIdentity:{publicKey:'a'.repeat(43),fingerprint:'b'.repeat(43)}, viewer: {...scope, role: 'member'}};
  const caller = new NativeCastCaller({api, session: () => session, playback: () => adapter, current: () => current, deviceName: () => 'Phone', reportError: (error: unknown) => {throw error;}});
  const ready = deferred<void>();
  const off = caller.subscribe(() => {if (caller.getSnapshot().cast.available) ready.resolve();});
  if (!caller.getSnapshot().cast.available) await ready.promise;
  off();
  const change = (patch: any) => {snapshot = {...snapshot, ...patch}; if (patch.dock) dock = patch.dock; for (const listener of [...playbackListeners]) listener();};
  const connectCurrent = () => {selected={deviceName:'TV',deviceId:'sdk_device',attachmentId};};
  const choose = () => {connectCurrent();emit('PorticoCastSession', {state: 'started',...selected});};
  const committed = {bootstrap: {transfer: {id: 'cbs_intent', state: 'committed', itemId: 'song', playbackState: 'playing'}}};
  return {starts,stamp,occurrence,attachmentId,retireViewer:()=>{current=false;change({});},caller, fence, requests, sent, effects, loaded, read, receipt, cancellation, cancelReceipt, message, choose, connectCurrent, chooserCalls:()=>chooserCalls, change, committed, snapshot: () => ({...snapshot, dock})};
}

test('Root42 queue/header event before ACK and original receipt does not cancel the accepted target', async t => {
  const f = await fixture(); t.after(() => f.caller.dispose());
  f.caller.start(); f.choose(); const load = await f.loaded.promise;
  f.change({queue: {view: {queue: {id: 'queue', revision: '11', currentEntryId: 'entry', currentPlaybackId: 'receiver'}}},
    session: {...f.snapshot().session, id: 'receiver'}});
  assert.equal(f.sent.some(command => command.type === 'stop'), false);
  assert.equal(f.requests.some(request => request.method === 'DELETE'), false);
  assert.equal(f.effects.length, 0, 'header movement alone does not retire the browser/phone stream');
  assert.deepEqual(f.requests.find(request => request.method === 'POST')?.body.source,
    {sessionId: 'original', generation: 1, queueId: 'queue', entryId: 'entry', queueRevision: '10'});
  f.message({type: 'loaded', requestId: load.requestId, itemId: 'song'}); await f.read.promise;
  assert.equal(f.effects.length, 0, 'receiver ACK still waits for the same authenticated receipt');
  const accepted = deferred<void>(); const off = f.caller.subscribe(() => {if (f.caller.getSnapshot().cast.phase === 'casting') accepted.resolve();});
  f.receipt.resolve(f.committed); await accepted.promise; off();
  assert.equal(f.effects.length, 1); assert.deepEqual(f.effects[0].playback.fence, f.fence);
  assert.equal(f.requests.filter(request => request.method === 'DELETE').length, 0);
  assert.equal(f.sent.some(command => command.type === 'stop'), false);
});

test('a real replacement cancels the original intent, and its late committed receipt cannot retire the replacement', async t => {
  const f = await fixture(); t.after(() => f.caller.dispose());
  f.caller.start(); f.choose(); const load = await f.loaded.promise;
  const replacement = {...f.fence, sessionId: 'later', generation: 2, playback: {...f.fence.playback, run: 2}};
  f.change({fence: replacement, dock: {...f.snapshot().dock, local: {...f.snapshot().dock.local, fence: replacement}},
    session: {...f.snapshot().session, id: 'later', presentation: {generation: 2}}});
  await f.cancellation.promise;
  assert.equal(f.requests.filter(request => request.method === 'DELETE' && request.path === '/v1/cast/bootstrap/cbs_intent').length, 1);
  f.message({type: 'loaded', requestId: load.requestId, itemId: 'song'});
  const resolved = deferred<void>(); const off = f.caller.subscribe(() => {if (!f.caller.getSnapshot().cast.transferPending) resolved.resolve();});
  f.cancelReceipt.resolve(f.committed); await resolved.promise; off();
  assert.deepEqual(f.snapshot().fence, replacement);
  assert.equal(f.effects.length, 0, 'the old receipt never touches the later local output');
});

test('a changed queue before dispatch still refuses the captured chooser attempt', async t => {
  const f = await fixture(); t.after(() => f.caller.dispose());
  f.caller.start(); f.change({queue: {view: {queue: {...f.snapshot().queue.view.queue, revision: '11'}}}}); f.choose();
  assert.equal(f.requests.some(request => request.method === 'POST'), false);
  assert.equal(f.sent.some(command => command.type === 'load'), false);
  assert.equal(f.effects.length, 0);
  assert.deepEqual(f.snapshot().fence, f.fence);
});

test('Cast dispatch uses the same source clock after the picker and storage wait', async t => {
  const storage = deferred<void>(), f = await fixture({storage: storage.promise}); t.after(() => f.caller.dispose());
  f.caller.start(); f.choose();
  f.change({playback: {...f.snapshot().playback, positionSeconds: 81}});
  storage.resolve();
  const load = await f.loaded.promise;
  assert.equal(load.startSeconds, 81);
  assert.deepEqual(f.requests.find(request => request.method === 'POST')?.body.source,
    {sessionId: 'original', generation: 1, queueId: 'queue', entryId: 'entry', queueRevision: '10'});
  assert.equal(f.effects.length, 0, 'dispatch still does not retire the source before acceptance');
});

test('an already-connected receiver starts without waiting for a second SDK started event', async t => {
  const f = await fixture(); t.after(() => f.caller.dispose());
  f.connectCurrent(); f.caller.start();
  const load = await f.loaded.promise;
  assert.equal(load.startSeconds, 12);
  assert.equal(f.chooserCalls(), 0);
  f.choose(); await new Promise(setImmediate);
  assert.equal(f.sent.filter(command => command.type === 'load').length, 1, 'a late started event cannot dispatch twice');
  assert.equal(f.effects.length, 0);
});

test('an already-connected attachment still refuses a replaced source before dispatch', async t => {
  const storage = deferred<void>(), f = await fixture({storage: storage.promise}); t.after(() => f.caller.dispose());
  f.connectCurrent(); f.caller.start();
  f.change({queue: {view: {queue: {...f.snapshot().queue.view.queue, revision: '11'}}}});
  storage.resolve(); await new Promise(setImmediate);
  assert.equal(f.requests.some(request => request.method === 'POST'), false);
  assert.equal(f.sent.some(command => command.type === 'load'), false);
  assert.equal(f.effects.length, 0);
});

test('Root50: a native returning caller adopts the validated receiver without a captured local source or retirement effect',async t=>{
 const f=await fixture({returning:true});t.after(()=>f.caller.dispose());
 const accepted=deferred<void>(),off=f.caller.subscribe(()=>{if(f.caller.getSnapshot().cast.phase==='casting')accepted.resolve();});
 if(f.caller.getSnapshot().cast.phase!=='casting')await accepted.promise;off();
 const state=f.caller.getSnapshot();assert(state.resumed);assert.equal(state.source,undefined);assert.equal(state.cast.acceptedSource,undefined);
 assert.equal(state.title,'Actual title');assert.equal(state.receiver?.positionSeconds,80);assert.equal(f.effects.length,0);
 assert.equal(f.sent.some(command=>command.type==='pair'||command.type==='load'||command.type==='stop'),false);
 assert(f.requests.some(request=>request.path==='/v1/cast/controllers/validate'));
});
test('Root50: native legacy proof and a retired viewer never send a retained proof or publish a late receiver',async t=>{
 const legacy=await fixture({returning:true,legacy:true});t.after(()=>legacy.caller.dispose());
 await Promise.resolve();assert.equal(legacy.sent.some(command=>command.type==='adopt'),false);assert.equal(legacy.caller.getSnapshot().resumed,undefined);
 const validation=deferred<void>(),f=await fixture({returning:true,validation:validation.promise});t.after(()=>f.caller.dispose());
 f.retireViewer();validation.resolve();await new Promise(setImmediate);await new Promise(setImmediate);
 assert.equal(f.sent.some(command=>command.type==='adopt'),false);assert.equal(f.caller.getSnapshot().cast.itemId,undefined);assert.equal(f.effects.length,0);
});


test('cold-return view controls carry the original actual occurrence and reject retained callbacks after receiver advance',async t=>{
 const f=await fixture({returning:true});t.after(()=>f.caller.dispose());
 for(let n=0;n<20&&!f.caller.getSnapshot().resumed;n++)await new Promise(setImmediate);
 const state=f.caller.getSnapshot(),target=state.control;assert.ok(target);assert.equal(state.displayKind,'music');assert.equal(state.source,undefined);
 assert.equal(f.caller.viewIsCurrent(target),true);f.caller.controls('pause',undefined,target);
 const pause=f.sent.at(-1);assert.equal(pause?.type,'pause');if(pause?.type!=='pause')throw new Error('missing pause');assert.deepEqual(pause.target,target.remote);
 f.message({type:'status',controller:f.stamp,occurrence:{...f.occurrence,sessionId:'successor',generation:3},itemId:'song',paused:false});
 for(let n=0;n<10;n++)await new Promise(setImmediate);
 assert.equal(f.caller.getSnapshot().receiver,undefined,'a same-item successor without a clock cannot inherit the old occurrence time');
 assert.equal(f.caller.viewIsCurrent(target),false);const count=f.sent.length;f.caller.controls('stop',undefined,target);f.caller.controls('seek',40,target);assert.equal(f.sent.length,count);assert.equal(f.effects.length,0);
 f.retireViewer();assert.equal(f.caller.viewIsCurrent(f.caller.getSnapshot().control!),false);
});


test('a returning Continue here held on metadata cannot replace a newer local run',async t=>{
 const gate=deferred<void>(),f=await fixture({returning:true,continuation:gate.promise});t.after(()=>f.caller.dispose());
 for(let n=0;n<20&&!f.caller.getSnapshot().resumed;n++)await new Promise(setImmediate);
 const target=f.caller.getSnapshot().control!;assert.ok(target);f.caller.controls('stop',undefined,target);
 const stop=f.sent.at(-1);if(stop?.type!=='stop')throw new Error('missing targeted Stop');
 f.message({type:'stopped',requestId:stop.requestId,target:stop.target,positionSeconds:83,durationSeconds:240});
 const continued=f.caller.continueHere(target);for(let n=0;n<10;n++)await new Promise(setImmediate);
 f.change({fence:{...f.fence,sessionId:'new-local',generation:2},playback:{...f.snapshot().playback,playbackRun:2}});
 gate.resolve();await continued;assert.equal(f.starts.length,0);assert.equal(f.effects.length,0);
});

test('V02/V01: the receiver loads the same edition and audio track this device is playing', async t => {
  const f = await fixture(); t.after(() => f.caller.dispose());
  f.change({session: {...f.snapshot().session, versionId: 'edition-b', presentation: {...f.snapshot().session.presentation, audioTrackId: 'a2'}}});
  f.caller.start(); f.choose(); const load = await f.loaded.promise;
  assert.equal(load.versionId, 'edition-b');
  assert.equal(load.audioTrackId, 'a2', 'V01: and the audio track chosen here');
});
