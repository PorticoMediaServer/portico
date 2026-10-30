import test from 'node:test';
import assert from 'node:assert/strict';
import {createSources} from '../src/screens/settings/server/channels/channel-sources.ts';
import {FreshnessHub,dependsOn} from '@core/freshness-hub.ts';
import {mountQuietInput} from '../src/screens/settings/quiet-refresh.ts';
import {quietClock,deferred,tick} from './settings-quiet-clock.ts';
const config=(name='A')=>({protocolVersion:'1.0',id:'one',name,description:'',enabled:true,position:0,timezone:'UTC',seed:'s',defaultRuleId:'rule',viewerAccess:'owner-only',logoItemId:'',templateId:'',quality:{mode:'automatic',maxBitrate:8000000,maxHeight:1080,allowLossy:true,allowHdrToSdr:true},overlay:{enabled:false,corner:'br',sizePercent:10,insetPercent:5,treatment:'none'},rules:[],blocks:[]});
const channel=(name='A',revision=1)=>({config:config(name),revision,state:'ok',healthCode:'',generation:'g1',generatedThrough:'',candidateCount:0,unresolvedDurationCount:0,replacementBoundary:''});
const list=(channels:unknown[]=[channel()])=>({protocolVersion:'1.0',serverId:'server',channels});
const health=()=>({protocolVersion:'1.0',serverId:'server',result:{activeGeneration:'g1',candidates:1,channelId:'one',enabled:true,generatedThrough:'',healthCode:'',healthy:true,message:'',name:'A',observedAt:'2026-10-28T12:00:00Z',pendingGeneration:false,scheduledEntries:1,state:'ok',unresolved:0}});
function fixture(){
 let rows=list(),held:ReturnType<typeof deferred<unknown>>|undefined,failWrite:unknown,healthError:unknown;const calls:{path:string;method?:string;body?:any;signal?:AbortSignal}[]=[];
 const sources=createSources({serverId:'server',isCurrent:()=>true,reportError:()=>{},api:{async request<T>(path:string,method?:string,body?:any,signal?:AbortSignal){calls.push({path,method,body,signal});if(method==='POST'){if(failWrite)throw failWrite;return {protocolVersion:'1.0',serverId:'server',channel:{...channel(body.config.name,body.expectedRevision+1),config:body.config}} as T;}if(path.endsWith('/health')){if(healthError)throw healthError;return health() as T;}if(held){const promise=held.promise;held=undefined;return promise as Promise<T>;}return rows as T;}},uploadLogo:async()=>({protocolVersion:'1.0',serverId:'server',result:{channelId:'one',revision:8,width:80,height:80,bytes:100,mediaType:'image/png',digest:'digest',updatedAt:'2026-10-28T12:00:00Z',url:'/logo'}})});
 return {sources,calls,rows:(value:unknown[])=>{rows=list(value);},hold:()=>{held=deferred<unknown>();return held;},failWrite:(error:unknown)=>{failWrite=error;},healthError:(error:unknown)=>{healthError=error;}};
}

test('mounted reader and exact editor rebase before row publication without recreating owners or dirty draft',async()=>{
 const f=fixture(),s=f.sources;await s.reader.load();const editor=s.editor,presets=s.presets,logos=s.logos;s.editor.edit(s.reader.getSnapshot().channels![0]);s.editor.change(value=>({...value,name:'B'}));f.rows([{...channel('A',2),config:{...config('A'),description:'remote'}}]);
 let observed=false;s.reader.subscribe(()=>{if(s.reader.getSnapshot().channels?.[0]?.revision===2){observed=true;assert.equal(s.editor.getSnapshot().config?.name,'B');assert.equal(s.editor.getSnapshot().config?.description,'remote');}});
 assert.deepEqual(await s.refreshQuiet(),{kind:'adopted'});assert.equal(observed,true);assert.equal(s.editor,editor);assert.equal(s.presets,presets);assert.equal(s.logos,logos);await s.editor.save();assert.equal(f.calls.find(call=>call.method==='POST')?.body.expectedRevision,2);s.dispose();
});

test('conflicting channel row keeps the exact editor snapshot and Save still sends original base revision',async()=>{
 const f=fixture(),s=f.sources;await s.reader.load();s.editor.edit(s.reader.getSnapshot().channels![0]);s.editor.change(value=>({...value,name:'B'}));const before=s.editor.getSnapshot();f.rows([channel('C',2)]);assert.deepEqual(await s.refreshQuiet(),{kind:'conflict'});assert.equal(s.editor.getSnapshot(),before);await s.editor.save();const body=f.calls.find(call=>call.method==='POST')?.body;assert.equal(body.expectedRevision,1);assert.equal(body.config.name,'B');s.dispose();
});

test('Save can start and finish during directory read; sibling admission cancels stale adoption',async()=>{
 const f=fixture(),s=f.sources;await s.reader.load();s.editor.edit(s.reader.getSnapshot().channels![0]);s.editor.change(value=>({...value,name:'B'}));const held=f.hold(),pending=s.refreshQuiet();const signal=f.calls.at(-1)?.signal;await s.editor.save();assert.equal(signal?.aborted,true);const before=s.reader.getSnapshot();held.resolve(list([channel('stale',99)]));assert.deepEqual(await pending,{kind:'deferred'});assert.equal(s.reader.getSnapshot(),before);s.dispose();
});

test('uncertain sibling receipts gate quiet reads and retain immutable editor intent',async()=>{
 const f=fixture(),s=f.sources;await s.reader.load();s.editor.edit(s.reader.getSnapshot().channels![0]);s.editor.change(value=>({...value,name:'B'}));f.failWrite(Object.assign(new Error('lost'),{status:503}));await s.editor.save();const before=s.editor.getSnapshot(),count=f.calls.length;assert.equal(before.pending,true);assert.deepEqual(await s.refreshQuiet(),{kind:'deferred'});assert.equal(f.calls.length,count);assert.equal(s.editor.getSnapshot(),before);s.dispose();
});

test('logo write admission also cancels directory, preserves its own receipt revision and leaves editor owner intact',async()=>{
 const f=fixture(),s=f.sources;await s.reader.load();const row=s.reader.getSnapshot().channels![0],held=f.hold(),pending=s.refreshQuiet(),signal=f.calls.at(-1)?.signal,editor=s.editor;
 await s.logos.choose(row,{name:'logo.png',size:100,type:'image/png'} as File);assert.equal(signal?.aborted,true);assert.equal(s.logos.revision('one'),8);assert.equal(s.editor,editor);const before=s.reader.getSnapshot();held.resolve(list([channel('old',99)]));assert.deepEqual(await pending,{kind:'deferred'});assert.equal(s.reader.getSnapshot(),before);s.dispose();
});

test('a buffered editor input/incomplete conditions gate and cancel quiet adoption until settled',async()=>{
 const f=fixture(),s=f.sources;await s.reader.load();s.editor.edit(s.reader.getSnapshot().channels![0]);const held=f.hold(),pending=s.refreshQuiet(),before=s.editor.getSnapshot();s.editor.setQuietInputActive(true);held.resolve(list([channel('C',2)]));assert.deepEqual(await pending,{kind:'deferred'});assert.equal(s.editor.getSnapshot().config,before.config);s.editor.setQuietInputActive(false);s.editor.setQuietIncomplete('conditions',true);assert.deepEqual(await s.refreshQuiet(),{kind:'deferred'});s.editor.setQuietIncomplete('conditions',false);assert.deepEqual(await s.refreshQuiet(),{kind:'adopted'});s.dispose();
});

test('tolerant editor target omission needs exact 404 before closing the original target',async()=>{
 const f=fixture(),s=f.sources;await s.reader.load();s.editor.edit(s.reader.getSnapshot().channels![0]);s.editor.change(value=>({...value,name:'B'}));const before=s.editor.getSnapshot();f.rows([]);assert.deepEqual(await s.refreshQuiet(),{kind:'deferred'});assert.equal(s.editor.getSnapshot(),before);assert.equal(f.calls.at(-1)?.path,'/v1/admin/library-channels/one/health');f.healthError({status:404});assert.deepEqual(await s.refreshQuiet(),{kind:'adopted'});assert.equal(s.editor.getSnapshot().phase,'closed');s.dispose();
});
