// Production binding/parser parity probes from the independent receiver review.
import test from 'node:test';
import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';
import {resolve, sep} from 'node:path';
const root = process.env.PORTICO_TEST_SOURCE_ROOT
 ? pathToFileURL(resolve(process.env.PORTICO_TEST_SOURCE_ROOT)+sep) : new URL('../../../',import.meta.url);
const load = p => import(new URL(p,root));
const {bindNativeTogether}=await load('apps/native/src-next/screens/settings-live/together-playback.ts');
const {nativeTogetherSession}=await load('apps/native/src-next/screens/settings-live/together-session.ts');
const {roomFixture}=await load('apps/native/test/helpers/room-fixture.ts');
const {screenModule}=await load('apps/native/test/helpers/screen-harness.mjs');
const cast=await load('packages/client-core/src/cast/index.ts');
const connection=await load('packages/client-core/src/connection-policy.ts');
const player=await load('packages/player-core/src/index.ts');
const offered=await load('apps/native/src-next/platform/play-on/cast-offered.ts');
const {NativeCastCaller}=await screenModule(new URL('apps/native/src-next/platform/play-on/cast.ts',root),{
 react:{default:{}},'react-native':{NativeModules:{},Platform:{isTV:false}},
 '../../../../../packages/client-core/src/presentation/service-text':{serviceText:id=>id},
  '../../../../../packages/client-core/src/connection-policy':connection,
 '../../../../../packages/client-core/src/cast':cast,
 '../../../../../packages/player-core/src':player,
 './cast-transport':{},'./cast-controller-storage':{},'./cast-offered':offered,
});
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};};
const settle=()=>new Promise(r=>setImmediate(r));
const authority={serverId:'server',accountId:'account',profileId:'profile',epoch:'epoch',viewerFence:'home'};
function together(id){
 const read=deferred(),calls=[],notices=[],starts=[];let current=true;
 const playback={getSnapshot:()=>({authority,queue:{},playback:{playbackRun:0,intent:'paused',phase:'idle'}}),start:async x=>{starts.push(x);},
 toggle:async()=>{},seek:async()=>{},skip:async()=>{},next:async()=>{},previous:async()=>{},replay:async()=>{},setRate(){},open(){}};
 const context={events:{on:()=>()=>{},onResync:()=>()=>{},subscribeStatus:()=>()=>{},getStatus:()=>({connected:false})},api:{baseUrl:'https://server.test',requestRaw:async()=>({status:200,headers:{},body:{}}),request(...args){calls.push(args);return read.promise;}},authority:()=>authority,current:()=>current,session:()=>undefined,subscribeAuthority:()=>()=>{},playback,
 navigation:{getSnapshot:()=>({route:{kind:'home'}}),subscribe:()=>()=>{}}};
 const service=nativeTogetherSession(context);
 const stamp=new Date().toISOString();
 const group=roomFixture({clock:{entryId:'entry',positionMs:0,at:stamp,playing:false,ready:1,waitingFor:0},queue:[{entryId:'entry',itemId:id,available:true}]});
 service.getSnapshot=()=>({phase:'live',group,clockOffsetMs:0});service.report=async()=>{};service.notice=e=>notices.push(e);
 const off=bindNativeTogether(context,true);
 return {read,calls,notices,starts,retire:()=>{current=false;},close:()=>{read.resolve(null);off();service.dispose();}};
}
for(const id of ['.','..'])test('native Together '+id+' retains local route refusal before a pending transport refusal',async t=>{
 const f=together(id);t.after(f.close);await settle();
 assert.equal(f.calls.length,0,'original route validation must refuse before transport');
 assert.equal(f.notices.length,1,'the original refusal must settle while transport would still be pending');
});
test('native Together stale malformed success retains original parse-before-current ordering',async t=>{
 const f=together('item');t.after(f.close);let parsed=0;
 assert.equal(f.calls.length,1);f.retire();
 f.read.resolve(new Proxy({}, {get(_,key){if(key==='then')return undefined;parsed++;throw new Error('malformed');}}));
 await settle();assert.ok(parsed>0);assert.equal(f.starts.length,0);assert.equal(f.notices.length,0);
});
function continueHere(){
 const read=deferred(),calls=[],starts=[];
 const adapter={getSnapshot:()=>({authority,dock:{remote:{id:'source',phase:'stopped'}}}),start:async x=>starts.push(x)};
 const session={sessionFamilyId:'family',viewer:{...authority,authority:'local',role:'member'}};
 const caller=new NativeCastCaller({api:{baseUrl:'https://server.test',request(...args){calls.push(args);return read.promise;}},session:()=>session,playback:()=>adapter,current:()=>true,deviceName:()=>'',reportError:e=>{throw e;}});
 caller.source={id:'source',adapter,fence:{}};caller.receiver={itemId:'item',positionSeconds:12};caller.state={cast:{phase:'off',transferPending:false},stopped:true};
 return {caller,read,calls,starts};
}
test('native Cast Continue here ignores malformed metadata after original source replacement',async t=>{
 const f=continueHere();t.after(()=>f.caller.dispose());let parsed=0;
 const task=f.caller.continueHere();assert.equal(f.calls.length,1);
 f.read.resolve(new Proxy({}, {get(_,key){if(key==='then')return undefined;parsed++;throw new Error('malformed');}}));
 queueMicrotask(()=>{f.caller.source=undefined;});
 await task;assert.equal(parsed,0);assert.equal(f.starts.length,0);
});
test('native Cast Continue here preserves transport refusal over later source replacement',async t=>{
 const f=continueHere();t.after(()=>f.caller.dispose());const refusal=new Error('transport refusal');
 const task=f.caller.continueHere();const assertion=assert.rejects(task,e=>e===refusal);
 f.caller.source=undefined;f.read.reject(refusal);await assertion;assert.equal(f.starts.length,0);
});
