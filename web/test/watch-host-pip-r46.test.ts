import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {transformTsx} from './transform-tsx.mjs';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../src/platform/playback/index.ts';
import type {PictureInPictureDocument} from '../src/screens/watch/WatchPlayback.tsx';
import {parseSession} from '@core/playback-v1/types.ts';
import type {PlaybackFence, QueuePlaybackFence} from '../../packages/player-core/src/index.ts';

// The host imports the app router, which mounts screens; this check needs only the retained
// media element and the playback runtime, so the router stays out of the test entirely.
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === '../../router' && context.parentURL?.endsWith('/src/screens/watch/WatchPlayback.tsx')) return {url: 'watch-host-pip:router', shortCircuit: true};
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'watch-host-pip:router') return {format: 'module', source: 'export const router = {state: {location: {href: "/", pathname: "/"}}, subscribe: () => () => {}};', shortCircuit: true};
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.css')) return {format: 'module', source: 'export default {};', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  },
});

const {browserPictureInPictureSupported, createPictureInPictureOperations, pictureInPictureCurrent, remotePictureKeepsCaptions,
  pictureInPictureReady, pictureInPictureRetained} = await import('../src/screens/watch/WatchPlayback.tsx');

const scope = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
const authority = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', epoch: 1, viewerFence: 'viewer'};
const session=parseSession({id:'session',revision:'1',kind:'vod',role:'local',state:'playing',itemId:'item',versionId:'version',queue:{queueId:'queue',entryId:'entry'},
  presentation:{generation:1,mode:'direct',url:'/v1/media/current',startPositionMs:0,subtitles:[],decision:{}}});
const current={sessionId:'session',generation:1,state:'playing',sourceId:'source',delivery:'direct',audioSelection:'platform_default',subtitleSelection:'off',qualityId:'original'};


function snapshot(change: Partial<WebPlaybackRuntimeSnapshot> = {}): WebPlaybackRuntimeSnapshot {
  const plan=change.offers===null?null:change.offers&&'subtitlePlan' in change.offers?change.offers.subtitlePlan:subtitlePlan({mode:'off'});
  const offers=change.offers===null?null:{current:{...current,subtitleSelection:plan?.mode??'disabled'},subtitlePlan:plan,...change.offers};
  return {authority, sequence: 1, disposed: false, fence, session,nativeVideoSurfaceActive: false, playback: {} as never,
    dock: {} as never, presentations: [], queueView: null, options: null,...change,offers:offers as never};
}
function subtitlePlan(change: Partial<NonNullable<NonNullable<WebPlaybackRuntimeSnapshot['offers']>['subtitlePlan']>> = {}) {
  return {sessionId: 'session', generation: 1, sourceId: 'source', mode: 'track', renderer: 'external_text', offsetUs: '0', ...change};
}
function runtimeFor(live: () => WebPlaybackRuntimeSnapshot, ready: (value: PlaybackFence) => boolean = () => true): WebShellPlaybackRuntime {
  return {getSnapshot: live, browserMediaReady: (value: PlaybackFence) => ready(value)} as unknown as WebShellPlaybackRuntime;
}

class FakeDocument {
  pictureInPictureEnabled = true;
  pictureInPictureElement: Element | null = null;
  exits = 0;
  exitPictureInPicture = async () => { this.exits++; this.pictureInPictureElement = null; };
}
class FakeVideo {
  isConnected = true;
  requests = 0;
  enter?: () => void;
  reject?: (error:Error)=>void;
  constructor(private readonly doc: FakeDocument, private readonly deferred = false) {}
  requestPictureInPicture = (): Promise<void> => {
    this.requests++;
    const enter = () => { this.doc.pictureInPictureElement = this as unknown as Element; };
    if (!this.deferred) { enter(); return Promise.resolve(); }
    return new Promise<void>((resolve,reject) => { this.enter = () => { enter(); resolve(); }; this.reject=reject; });
  };
}
function operations(doc: FakeDocument, element: FakeVideo, live: () => WebPlaybackRuntimeSnapshot) {
  const runtime = runtimeFor(live);
  return {runtime, operations: createPictureInPictureOperations({
    document: doc as unknown as PictureInPictureDocument,
    element: () => element as unknown as HTMLVideoElement,
    target: () => pictureInPictureReady(runtime, live()) ? {runtime, fence} : null,
    current: (value, valueFence) => pictureInPictureCurrent(value, valueFence),
  })};
}

test('only this browser platform may lift the one retained picture', () => {
  const doc = new FakeDocument() as unknown as PictureInPictureDocument;
  assert.equal(browserPictureInPictureSupported(doc, 'web'), true);
  assert.equal(browserPictureInPictureSupported(doc, undefined), true);
  assert.equal(browserPictureInPictureSupported(doc, 'desktop'), false);
  assert.equal(browserPictureInPictureSupported({...doc, pictureInPictureEnabled: false}, 'web'), false);
  assert.equal(browserPictureInPictureSupported({...doc, exitPictureInPicture: undefined}, 'web'), false);
  assert.equal(browserPictureInPictureSupported(undefined, 'web'), false);
});

test('a native surface, a remote takeover, a loading generation or a retired source cannot float', async () => {
  let live = snapshot();
  const runtime = runtimeFor(() => live);
  assert.equal(pictureInPictureReady(runtime, live), true);
  assert.equal(pictureInPictureReady(runtime, snapshot({nativeVideoSurfaceActive: true})), false);
  assert.equal(pictureInPictureReady(runtime, snapshot({dock: {remote: {phase: 'playing'}} as never})), false);
  assert.equal(pictureInPictureReady(runtime, snapshot({disposed: true})), false);
  assert.equal(pictureInPictureReady(runtimeFor(() => live, () => false), live), false);
  assert.equal(pictureInPictureCurrent(runtime, fence), true);
  live = snapshot({fence: {...fence, generation: 2}});
  assert.equal(pictureInPictureCurrent(runtime, fence), false);

  const doc = new FakeDocument(), element = new FakeVideo(doc);
  live = snapshot({nativeVideoSurfaceActive: true});
  await operations(doc, element, () => live).operations.toggle();
  live = snapshot({dock: {remote: {phase: 'playing'}} as never});
  await operations(doc, element, () => live).operations.toggle();
  live = snapshot({disposed: true});
  await operations(doc, element, () => live).operations.toggle();
  assert.equal(element.requests, 0);
  assert.equal(doc.pictureInPictureElement, null);
});

test('a page-drawn caption keeps the picture on the page; off and already-burned tracks may float or AirPlay', () => {
  assert.equal(remotePictureKeepsCaptions(snapshot()), true);
  assert.equal(remotePictureKeepsCaptions(snapshot({offers:null})),false);
  assert.equal(remotePictureKeepsCaptions(snapshot({offers:{subtitlePlan:null,current:{...current,subtitleSelection:'platform_default'}} as never})),false);
  assert.equal(remotePictureKeepsCaptions(snapshot({offers:{subtitlePlan:null,current:{...current,subtitleSelection:'disabled'}} as never})),true);
  assert.equal(remotePictureKeepsCaptions(snapshot({offers: {subtitlePlan: subtitlePlan()} as never})), false);
  assert.equal(remotePictureKeepsCaptions(snapshot({offers: {subtitlePlan: subtitlePlan({renderer: 'client'})} as never})), false);
  assert.equal(remotePictureKeepsCaptions(snapshot({offers: {subtitlePlan: subtitlePlan({mode: 'off'})} as never})), true);
  assert.equal(remotePictureKeepsCaptions(snapshot({offers: {subtitlePlan: subtitlePlan({renderer: 'burn_in'})} as never})), true);
  // Another session, generation or source is not this picture's caption state.
  assert.equal(remotePictureKeepsCaptions(snapshot({offers: {subtitlePlan: subtitlePlan({generation: 2})} as never})), false);
  assert.equal(remotePictureKeepsCaptions(snapshot({offers: {subtitlePlan: subtitlePlan({sessionId: 'replacement'})} as never})), false);
  assert.equal(remotePictureKeepsCaptions(snapshot({offers: {subtitlePlan: subtitlePlan({sourceId: 'replacement'})} as never})), false);

  const runtime = runtimeFor(() => snapshot());
  assert.equal(pictureInPictureReady(runtime, snapshot({offers: {subtitlePlan: subtitlePlan()} as never})), false);
  assert.equal(pictureInPictureReady(runtime, snapshot({offers: {subtitlePlan: subtitlePlan({renderer: 'burn_in'})} as never})), true);
});

test('the same retained element enters and exits the floating picture, and only while the source allows it', async () => {
  const doc = new FakeDocument(), element = new FakeVideo(doc);
  let live = snapshot({offers: {subtitlePlan: subtitlePlan()} as never});
  const {operations: pip} = operations(doc, element, () => live);
  await pip.toggle();
  assert.equal(element.requests, 0); // page-drawn captions

  live = snapshot();
  await pip.toggle();
  assert.equal(element.requests, 1);
  assert.equal(doc.pictureInPictureElement, element);

  const runtime = runtimeFor(() => live), owner = runtimeFor(() => live);
  assert.equal(pictureInPictureRetained(runtime, live, runtime), true);
  assert.equal(pictureInPictureRetained(owner, live, runtime), false);
  assert.equal(pictureInPictureRetained(runtime, snapshot({offers: {subtitlePlan: subtitlePlan()} as never}), runtime), false);

  await pip.toggle();
  assert.equal(doc.exits, 1);
  assert.equal(doc.pictureInPictureElement, null);

  // Without the element's own callable request, the API flag alone is not a capability.
  (element as unknown as {requestPictureInPicture?: unknown}).requestPictureInPicture = undefined;
  await pip.toggle();
  assert.equal(doc.pictureInPictureElement, null);
});

test('a late browser dispatch cannot leave the picture on a replaced or retired source', async () => {
  const doc = new FakeDocument(), element = new FakeVideo(doc, true);
  let live = snapshot();
  const {operations: pip} = operations(doc, element, () => live);
  const pending = pip.toggle();
  assert.equal(element.requests, 1);
  live = snapshot({fence: {...fence, sessionId: 'replacement', generation: 2}});
  element.enter!();
  await pending;
  assert.equal(doc.exits, 1);
  assert.equal(doc.pictureInPictureElement, null);
});

test('a revoked pending entry exits when the browser finishes it', async () => {
  const doc = new FakeDocument(), element = new FakeVideo(doc, true);
  const live = snapshot();
  const {operations: pip} = operations(doc, element, () => live);
  const pending = pip.toggle();
  await pip.release();
  assert.equal(doc.exits, 0);
  element.enter!();
  await pending;
  assert.equal(doc.exits, 1);
  assert.equal(doc.pictureInPictureElement, null);
});


test('a missing caption plan never certifies a selected page caption, even with a matching session',()=>{
 const selected={...session,presentation:{...session.presentation,subtitle:{trackId:'s1',kind:'text' as const,delivery:'device' as const},subtitles:[{trackId:'s1',format:'vtt',url:'/v1/media/captions'}]}};
 assert.equal(remotePictureKeepsCaptions(snapshot({session:selected,offers:{subtitlePlan:null,current:{...current,subtitleSelection:'platform_default'}} as never})),false);
 assert.equal(remotePictureKeepsCaptions(snapshot({session:{...selected,presentation:{...selected.presentation,subtitle:{trackId:'s1',kind:'text',delivery:'burn'},subtitles:[]}},offers:{subtitlePlan:null,current:{...current,subtitleSelection:'platform_default'}} as never})),true);
 assert.equal(remotePictureKeepsCaptions(snapshot({session:{...session,id:'another'},offers:{subtitlePlan:subtitlePlan({mode:'off'})} as never})),false);
});

test('revocation survives coalesced gestures and a rejected later request cannot retain the original picture',async()=>{
 const doc=new FakeDocument(),element=new FakeVideo(doc,true);let live=snapshot();const {operations:pip}=operations(doc,element,()=>live);
 const original=pip.toggle();await pip.release();live=snapshot({disposed:true});
 await pip.toggle();assert.equal(element.requests,1,'a pending enter is coalesced, not dispatched concurrently');
 element.enter!();await original;assert.equal(doc.pictureInPictureElement,null);assert.equal(doc.exits,1);
 live=snapshot();const later=pip.toggle();const rejected=assert.rejects(later,/later browser request rejected/);
 element.reject!(new Error('later browser request rejected'));await rejected;assert.equal(doc.pictureInPictureElement,null);
 const fresh=pip.toggle();element.enter!();await fresh;assert.equal(doc.pictureInPictureElement,element);
});

test('an unavailable newer target cannot erase retirement of a pending original enter',async()=>{
 const doc=new FakeDocument(),element=new FakeVideo(doc,true);let live=snapshot();const {operations:pip}=operations(doc,element,()=>live);
 const pending=pip.toggle();live=snapshot({offers:null});await pip.toggle();assert.equal(element.requests,1);
 element.enter!();await pending;assert.equal(doc.pictureInPictureElement,null);
});

test('enter and exit dispatches are serialized on the same retained element',async()=>{
 const doc=new FakeDocument(),element=new FakeVideo(doc);const {operations:pip}=operations(doc,element,()=>snapshot());
 await pip.toggle();let finish!:()=>void,calls=0;
 doc.exitPictureInPicture=()=>{calls++;return new Promise<void>(resolve=>{finish=()=>{doc.pictureInPictureElement=null;resolve();};});};
 const exiting=pip.toggle();await pip.toggle();await pip.release();
 assert.equal(calls,1);assert.equal(element.requests,1);finish();await exiting;
 await pip.toggle();assert.equal(element.requests,2);assert.equal(doc.pictureInPictureElement,element);
});

test('a failed entry that nonetheless acquired the element is retired before a subsequent entry',async()=>{
 const doc=new FakeDocument(),element=new FakeVideo(doc,true);const {operations:pip}=operations(doc,element,()=>snapshot());
 const attempt=pip.toggle(),rejected=assert.rejects(attempt,/browser failed/);doc.pictureInPictureElement=element as unknown as Element;
 element.reject!(new Error('browser failed'));await rejected;assert.equal(doc.pictureInPictureElement,null);
 const next=pip.toggle();element.enter!();await next;assert.equal(doc.pictureInPictureElement,element);
});

test('actual bound AirPlay availability and activation both refuse page captions and unknown plans',async t=>{
 const {createWatchPlayOn}=await import('../src/screens/watch/play-on-binding.ts');
 const previous=Object.getOwnPropertyDescriptor(globalThis,'navigator'),previousObserver=Object.getOwnPropertyDescriptor(globalThis,'MutationObserver');let observerStops=0;
 // Source readiness is driven by the real runtime subscription, not synthetic attribute changes.
 class Observer{constructor(_callback:MutationCallback){}observe(){}disconnect(){observerStops++;}}
 Object.defineProperty(globalThis,'MutationObserver',{value:Observer,configurable:true});
 Object.defineProperty(globalThis,'navigator',{value:{userAgent:'Version/18.0 Safari/605.1'},configurable:true});
 t.after(()=>{if(previous)Object.defineProperty(globalThis,'navigator',previous);else Reflect.deleteProperty(globalThis,'navigator');
  if(previousObserver)Object.defineProperty(globalThis,'MutationObserver',previousObserver);else Reflect.deleteProperty(globalThis,'MutationObserver');});
 const listeners=new Set<()=>void>();let live=snapshot(),ready=true,valid=true,picks=0,requests=0;
 const retention=new Set<unknown>();
 const runtime={getSnapshot:()=>live,browserMediaReady:()=>ready,browserAirPlayReady:()=>ready,subscribe:(changed:()=>void)=>{listeners.add(changed);return()=>{listeners.delete(changed);};},
  bindRemoteRetention:(source:unknown)=>{retention.add(source);return()=>{retention.delete(source);};}} as unknown as WebShellPlaybackRuntime;
 class AirVideo extends EventTarget{webkitShowPlaybackTargetPicker=()=>{picks++;};}
 const context={api:{baseUrl:'https://portico.test',getRouteConnection:()=>({pin:{publicKey:'route-pin'}}),request:async()=>{requests++;throw Error('unexpected API read');}},session:()=>({viewer:authority}),isCurrent:()=>valid,epoch:1};
 const binding={runtime,reportError:(error:unknown)=>{throw error;}};
 const owner=createWatchPlayOn(context as never,binding as never,new AirVideo() as unknown as HTMLVideoElement);
 const changed=()=>{for(const listener of [...listeners])listener();};
 assert.equal(retention.size,1);
 try{
  assert.equal(owner.controller.getSnapshot().airPlayAvailable,true);owner.controller.startAirPlay();assert.equal(picks,1);
  live=snapshot({offers:{subtitlePlan:subtitlePlan()} as never});
  // Even before subscribers update an old visible row, activation checks the actual port again.
  assert.equal(owner.controller.getSnapshot().airPlayAvailable,true);owner.controller.startAirPlay();assert.equal(picks,1);assert.equal(owner.controller.getSnapshot().airPlayAvailable,false);
  live=snapshot({offers:null});changed();assert.equal(owner.controller.getSnapshot().airPlayAvailable,false);owner.controller.startAirPlay();assert.equal(picks,1);
  live=snapshot({offers:{subtitlePlan:subtitlePlan({renderer:'burn_in'})} as never});changed();assert.equal(owner.controller.getSnapshot().airPlayAvailable,true);owner.controller.startAirPlay();assert.equal(picks,2);
  ready=false;changed();assert.equal(owner.controller.getSnapshot().airPlayAvailable,false);owner.controller.startAirPlay();assert.equal(picks,2);
  ready=true;valid=false;changed();assert.throws(()=>owner.controller.startAirPlay(),/authority_retired/);assert.equal(picks,2);assert.equal(requests,0);
 }finally{owner.dispose();assert.equal(observerStops,1);assert.equal(listeners.size,0);assert.equal(retention.size,0);}
});
