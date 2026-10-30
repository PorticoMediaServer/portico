// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {NativeShellNavigation} from '../src-next/shell/routes.ts';
const platform={isTV:false};
const {NativeWatchChannelRoute}=await screenModule(new URL('../src-next/screens/watch/ChannelRoute.tsx',import.meta.url),{
 react:{...React,default:React},'react-native':{Platform:platform,View:'View'},'react-native-safe-area-context':{useSafeAreaInsets:()=>({top:20})},
 '../../../../../packages/i18n/src':{defaultI18n:{t:(key:string)=>key}},'../../../../../packages/player-core/src':await import('../../../packages/player-core/src/index.ts'),
 '../../kit/controls':{Button:'Button'},
});
const nodes=(value:any):any[]=>Array.isArray(value)?value.flatMap(nodes):value?.props?[value,...nodes(value.props.children)]:[];
const authority={serverId:'server',accountId:'account',profileId:'profile',epoch:'family',viewerFence:'viewer'};
async function fixture(){
 let live=true,request:any,resolve!:()=>void,reject!:(error:Error)=>void,fence:any,attempts=0;
 const navigation=new NativeShellNavigation(authority,()=>authority);navigation.top({kind:'live'},authority);const origin=navigation.getSnapshot().route;
 const route={kind:'channel' as const,sourceId:'actual-source',channelId:'actual-channel'};navigation.open(route,authority);
 const authorityListeners=new Set<()=>void>();
 const runtime={getSnapshot:()=>({fence}),startChannel:async(value:any)=>{request=value;attempts++;await new Promise<void>((done,fail)=>{resolve=done;reject=fail;});}};
 const context={current:()=>live,navigation,subscribeAuthority:(listener:()=>void)=>{authorityListeners.add(listener);return()=>authorityListeners.delete(listener);},playback:{runtime}};
 let output:any,retry:()=>void=()=>{};const title={renderError:(error:unknown,again:()=>void)=>{retry=again;return React.createElement('Error',{error});}};
 const host=createFabricHookHost(()=>{output=NativeWatchChannelRoute({context,route,title});});await act(()=>host.mount());
 const back=()=>nodes(output).find(node=>node.props.testID==='native-channel-back').props.onPress;
 const admitted={kind:'channel',channelKind:'live-source',sourceId:route.sourceId,channelId:route.channelId,channelGeneration:'source-g',sessionId:'session',generation:1,playback:{scope:{serverId:'server'},run:'original'}};
 return {navigation,origin,route,host,request:()=>request,back,attempts:()=>attempts,retry:()=>retry,output:()=>output,
  accept:(value=admitted)=>{fence=value;request.onAccepted?.(value);},resolve:()=>resolve(),reject:(error:Error)=>reject(error),
  retire:()=>{live=false;for(const listener of authorityListeners)listener();},dispose:async ()=>{resolve();await host.unmount();navigation.dispose();}};
}
for(const retirement of ['navigation','authority','unmount'] as const)test(`pending real channel route owns Back and original caller: ${retirement}`,async()=>{
 const f=await fixture();try{
  assert.equal(f.request().lifetime.isCurrent(),true);assert.equal(f.request().sourceId,f.route.sourceId);assert(f.back());
  if(retirement==='unmount')await f.host.unmount();else if(retirement==='authority')f.retire();else f.navigation.open({kind:'account'},authority);
  assert.equal(f.request().lifetime.isCurrent(),false);assert.equal(f.request().lifetime.signal.aborted,true);
 }finally{await f.dispose();}
});
test('failed channel launch retains original Back and guarded Retry; old controls never pop or restart a successor',async()=>{
 const f=await fixture();try{
  await act(()=>{f.reject(new Error('tune failed'));});assert(nodes(f.output()).some(node=>node.type==='Error'));const back=f.back(),retry=f.retry();
  await act(()=>retry());assert.equal(f.attempts(),2);f.navigation.open({kind:'account'},authority);const successor=f.navigation.getSnapshot();
  await act(()=>{back();retry();});assert.equal(f.navigation.getSnapshot(),successor);assert.equal(f.attempts(),2);
 }finally{await f.dispose();}
});
test('only this actual admitted receipt consumes the launch route once after its original start completes',async()=>{
 const f=await fixture();try{
  const launch=f.navigation.getSnapshot(),oldBack=f.back();f.accept();assert.equal(f.navigation.getSnapshot(),launch,'receipt capture leaves the same start free to finish guide metadata');
  await act(()=>f.resolve());assert.equal(f.navigation.getSnapshot().route,f.origin);assert.equal(f.request().lifetime.signal.aborted,true);
  const restored=f.navigation.getSnapshot();await act(()=>{oldBack();f.accept();});assert.equal(f.navigation.getSnapshot(),restored);assert.equal(f.attempts(),1);
 }finally{await f.dispose();}
});
test('void refusal, unrelated receipt, and a successor between accepted return and await continuation never pop',async()=>{
 for(const kind of ['refusal','other','successor'] as const){const f=await fixture();try{
  const launch=f.navigation.getSnapshot();if(kind==='other')f.accept({kind:'channel',channelKind:'live-source',sourceId:'other',channelId:'other',channelGeneration:'g',sessionId:'s',generation:1,playback:{scope:{serverId:'server'},run:'other'}});else if(kind==='successor')f.accept();
  f.resolve();if(kind==='successor')f.navigation.open({...f.route},authority);
  const expected=f.navigation.getSnapshot();await act(()=>{});assert.equal(f.navigation.getSnapshot(),expected);if(kind!=='successor')assert.equal(expected,launch);
 }finally{await f.dispose();}}
});

test('TV keeps its existing pending/error outlet and does not consume a touch launch receipt',async()=>{
 platform.isTV=true;const f=await fixture();try{assert.equal(f.output(),null);assert.equal(f.request().onAccepted,undefined);await act(()=>f.reject(new Error('failure')));assert(nodes(f.output()).some(node=>node.type==='Error'));}
 finally{await f.dispose();platform.isTV=false;}
});


test('mocked late admission after successful return consumes once; unsuccessful return never authorizes a late receipt',async()=>{
 for(const completed of [true,false]){const f=await fixture();try{
  const launch=f.navigation.getSnapshot();
  await act(()=>completed?f.resolve():f.reject(new Error('start failed')));
  assert.equal(f.navigation.getSnapshot(),launch,'successful return without an actual receipt is not admission');
  await act(()=>f.accept());
  assert.equal(f.navigation.getSnapshot().route,completed?f.origin:f.route);
  const after=f.navigation.getSnapshot();await act(()=>{f.accept();f.accept();});assert.equal(f.navigation.getSnapshot(),after);assert.equal(f.attempts(),1);
 }finally{await f.dispose();}}
});

for(const retirement of ['route','authority','unmount','same-id-revisit'] as const)test(`mocked late original receipt refuses after ${retirement}`,async()=>{
 const f=await fixture();try{
  await act(()=>f.resolve());
  if(retirement==='authority')f.retire();else if(retirement==='unmount')await f.host.unmount();
  else if(retirement==='same-id-revisit'){f.navigation.back(authority);f.navigation.open(f.route,authority);}
  else f.navigation.open({kind:'account'},authority);
  const original=f.navigation.getSnapshot();await act(()=>{f.accept();f.accept();});assert.equal(f.navigation.getSnapshot(),original);assert.equal(f.attempts(),1);
 }finally{await f.dispose();}
});
