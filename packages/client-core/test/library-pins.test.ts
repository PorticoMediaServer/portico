import test from 'node:test';
import assert from 'node:assert/strict';
import {movedPin,parseLibraryNavigationPins,parsePinOrder,parsePinnedLibraries,withLibraryPinned,withPinRetry} from '../src/library-pins.ts';

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
 for(const patch of [{pinnedLibraryIds:['a','a']},{pinnedLibraryIds:['a',7]},{revision:'3'},{pinnedLibraryIds:Array.from({length:101},(_,i)=>'l'+i)}])
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
