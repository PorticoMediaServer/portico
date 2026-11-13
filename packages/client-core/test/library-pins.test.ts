import test from 'node:test';
import assert from 'node:assert/strict';
import {movedPin,parseLibraryNavigationPins,parsePinOrder,parsePinnedLibraries,withLibraryPinned,withPinRetry,readLibraryNavigation,readPinOrder,saveLibraryNavigation,savePinOrder,setResourcePin} from '../src/library-pins.ts';

const library=(id:string,pinned:boolean,patch:Record<string,unknown>={})=>({id,name:id.toUpperCase(),kind:'movie',defaultView:'browse',pinned,...patch});

test('a library this app cannot read is left out of the listing and a repeated one keeps its first row',()=>{
 const libraries=parsePinnedLibraries({items:[library('a',true),library('broken',true,{pinned:'yes'}),library('a',true,{name:'Again'}),library('b',false,{kind:'hologram',futureField:1}),null],futureField:true});
 assert.deepEqual(libraries,[library('a',true),library('b',false,{kind:'hologram'})]);
});

test('the size of the listing and its outer shape still refuse it',()=>{
 assert.throws(()=>parsePinnedLibraries({items:Array.from({length:2049},(_,i)=>library('l'+i,false))}),{code:'invalid_pins'});
 assert.throws(()=>parsePinnedLibraries({items:{}}),{code:'invalid_pins'});
 assert.throws(()=>parsePinnedLibraries([]),{code:'invalid_pins'});
});

test('an arrangement that is sent back whole stays exact',()=>{
 assert.deepEqual(parsePinOrder({serverId:'server',viewerFence:'fence',revision:2,order:[{kind:'view',id:'a'}],futureField:1},'server').order,[{kind:'view',id:'a',name:'',pinRevision:0}]);
 assert.deepEqual(parsePinOrder({serverId:'server',viewerFence:'fence',revision:2,order:[{kind:'playlist',id:'p',name:'Road trip',pinRevision:3}]},'server').order,[{kind:'playlist',id:'p',name:'Road trip',pinRevision:3}]);
 for(const patch of [{order:[{kind:'shelf',id:'a'}]},{order:[{kind:'view',id:'a'},{kind:'view',id:'a'}]},{order:[{kind:'view'}]},{revision:-1},{viewerFence:''},{serverId:'other'},{order:Array.from({length:201},(_,i)=>({kind:'view',id:'v'+i}))}])
  assert.throws(()=>parsePinOrder({serverId:'server',viewerFence:'fence',revision:2,order:[],...patch},'server'),{code:'invalid_pins'},JSON.stringify(patch));
 for(const patch of [{pinnedLibraryIds:['a','a']},{pinnedLibraryIds:['a',7]},{revision:'3'},{pinnedLibraryIds:Array.from({length:201},(_,i)=>'l'+i)}])
  assert.throws(()=>parseLibraryNavigationPins({pinnedLibraryIds:['a'],revision:3,...patch}),{code:'invalid_pins'},JSON.stringify(patch));
});

test('pinning and moving produce the arrangement that is sent whole',()=>{
 assert.deepEqual(withLibraryPinned(['a','b'],'c',true),['a','b','c']);
 assert.deepEqual(withLibraryPinned(['a','b','c'],'b',false),['a','c']);
 assert.deepEqual(withLibraryPinned(['a','b'],'a',true),['b','a']);
 assert.deepEqual(movedPin(['a','b','c'],0,1),['b','a','c']);
 assert.deepEqual(movedPin(['a','b','c'],2,-5),['c','a','b']);
 const same=['a','b'];
 assert.equal(movedPin(same,0,-1),same);
});

test('a pin change that met another device\'s change is applied again to the fresh pins',async()=>{
 let reads=0;const sent:number[]=[];
 const result=await withPinRetry(async()=>++reads,async revision=>{sent.push(revision);if(sent.length===1)throw Object.assign(new Error('changed'),{code:'pin_order_conflict'});return revision;},0);
 assert.equal(result,1);assert.deepEqual(sent,[0,1]);
 await assert.rejects(withPinRetry(async()=>0,async()=>{throw Object.assign(new Error('no'),{code:'not_found'});}),/no/);
});


test('profile pins use preferences and exact list reads, preserving the other kinds and unavailable pins',async()=>{
 let pins=['library:b','collection:c','view:v','playlist:road/one','library:a','collection:gone'];
 let failed=false;const calls:string[]=[],writes:unknown[]=[];
 const api={request:async<T>(path:string,method='GET',body?:unknown,signal?:AbortSignal):Promise<T>=>{
  calls.push(method+' '+path);assert.equal(signal?.aborted??false,false);
  if(path.startsWith('/v1/preferences')){
   if(method==='PATCH'){
    assert.deepEqual(Object.keys(body as object).sort(),['deviceClass','scope','values']);
    const next=body as {scope:string;deviceClass:string;values:{pins:string[]}};
    assert.equal(next.scope,'profile-server');assert.equal(next.deviceClass,'web');assert.deepEqual(Object.keys(next.values),['pins']);
    pins=[...next.values.pins];writes.push(body);
   }
   return {effective:{pins:[...pins]}} as T;
  }
  assert.ok(path.startsWith('/v1/lists?'),'no old navigation or saved-pin route is called');
  if(failed)throw Object.assign(new Error('unavailable'),{status:503});
  const ids=new URL(path,'https://test.invalid').searchParams.get('ids')!.split(',');
  return {items:ids.filter(key=>key!=='gone').sort().map(key=>({id:key,kind:key==='v'?'saved_view':key==='c'?'collection':'playlist',name:key.toUpperCase()}))} as T;
 }};
 assert.deepEqual((await readLibraryNavigation(api)).pinnedLibraryIds,['b','a']);
 assert.deepEqual((await readPinOrder(api,'server')).order.map(pin=>[pin.kind,pin.id,pin.name]),[['collection','c','C'],['view','v','V'],['playlist','road/one','ROAD/ONE']]);
 assert.equal(calls.filter(path=>path.startsWith('GET /v1/lists?')).length,1,'all names are read together, including pins beyond the first directory page');
 const batch=new URL(calls.find(path=>path.startsWith('GET /v1/lists?'))!.slice(4),'https://test.invalid').searchParams;
 assert.deepEqual(batch.get('ids')!.split(','),['c','v','road/one','gone']);
 await saveLibraryNavigation(api,0,['a','b']);
 assert.deepEqual(pins,['library:a','collection:c','view:v','playlist:road/one','library:b','collection:gone']);
 await savePinOrder(api,'server',0,[{kind:'playlist',id:'road/one'},{kind:'view',id:'v'},{kind:'collection',id:'c'}]);
 assert.deepEqual(pins,['library:a','playlist:road/one','view:v','collection:c','library:b','collection:gone']);
 await setResourcePin(api,'collection','c',false,0,'operation');
 await setResourcePin(api,'collection','new',true,0,'operation');
 assert.deepEqual(pins,['library:a','playlist:road/one','view:v','library:b','collection:gone','collection:new']);
 failed=true;await assert.rejects(readPinOrder(api,'server'),/unavailable/);
 assert.equal(writes.length,4,'a failed read never removes a pin');
 pins=Array.from({length:199},(_,n)=>'library:'+n);
 pins.push('collection:c');
 assert.equal((await readLibraryNavigation(api)).pinnedLibraryIds.length,199,'the library limit is the shared 200, not 100');
 await assert.rejects(setResourcePin(api,'view','v',true,0,'operation'),{code:'invalid_pins'});
 assert.equal(writes.length,4,'the shared 200 limit is checked before a write');
});
