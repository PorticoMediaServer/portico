import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsLibraryChannelsSource} from '../src/server-admin/channels-source.ts';
import type {ChannelApi} from '../src/channel-guide.ts';

const config=(id='one',name='Channel')=>({protocolVersion:'1.0',id,name,description:'',enabled:true,position:0,timezone:'UTC',seed:'s',defaultRuleId:'rule',viewerAccess:'owner-only',logoItemId:'',templateId:'',quality:{mode:'automatic',maxBitrate:8000000,maxHeight:1080,allowLossy:true,allowHdrToSdr:true},overlay:{enabled:false,corner:'br',sizePercent:10,insetPercent:5,treatment:'none'},rules:[],blocks:[]});
const channel=(id='one',name='Channel',revision=1)=>({config:config(id,name),revision,state:'ok',healthCode:'',generation:'g1',generatedThrough:'',candidateCount:0,unresolvedDurationCount:0,replacementBoundary:''});
const list=(channels:unknown[]=[channel()])=>({protocolVersion:'1.0',serverId:'server',channels});
const health=(id='one',name='Channel')=>({protocolVersion:'1.0',serverId:'server',result:{activeGeneration:'g1',candidates:1,channelId:id,enabled:true,generatedThrough:'',healthCode:'',healthy:true,message:'',name,observedAt:'2026-10-28T12:00:00Z',pendingGeneration:false,scheduledEntries:1,state:'ok',unresolved:0}});
function deferred<T>(){let resolve!:(value:T)=>void;const promise=new Promise<T>(yes=>resolve=yes);return {promise,resolve};}
function source(fn:(path:string,signal?:AbortSignal)=>Promise<unknown>,current=()=>true,report=(_e:unknown)=>{}){return new SettingsLibraryChannelsSource({request:<T>(p:string,_m?:string,_b?:unknown,s?:AbortSignal)=>fn(p,s) as Promise<T>} satisfies ChannelApi,'server',current,report);}

test('quiet channel refresh preserves unchanged row/selection identity, templates and visible health during I/O',async()=>{
 let quiet=false;const read=deferred<unknown>(),paths:string[]=[];
 const s=source(async p=>{paths.push(p);if(p.endsWith('/health'))return health();if(p.endsWith('/templates'))return {serverId:'server',protocolVersion:'1.0',templates:[]};return quiet?read.promise:list();});
 await s.load();await s.templates();const selected=s.getSnapshot().channels![0];await s.health(selected);const before=s.getSnapshot();quiet=true;paths.length=0;
 const pending=s.refreshQuiet();assert.equal(s.getSnapshot(),before);read.resolve(list());assert.deepEqual(await pending,{kind:'adopted'});
 assert.deepEqual(paths,['/v1/admin/library-channels','/v1/admin/library-channels/one/health']);assert.equal(s.getSnapshot().channels![0],selected);assert.equal(s.getSnapshot().selected,selected);assert.equal(s.getSnapshot().templates,before.templates);assert.equal(s.getSnapshot().healthLoading,false);s.dispose();
});

test('changed channel replaces only the same selected id; exact health carries the current target',async()=>{
 let fresh=false;const paths:string[]=[];const s=source(async p=>{paths.push(p);return p.endsWith('/health')?health('one',fresh?'Renamed':'Channel'):list([channel('two'),channel('one',fresh?'Renamed':'Channel',fresh?2:1)]);});
 await s.load();await s.health(s.getSnapshot().channels!.find(c=>c.config.id==='one')!);const before=s.getSnapshot();fresh=true;paths.length=0;
 assert.deepEqual(await s.refreshQuiet(),{kind:'adopted'});assert.equal(s.getSnapshot().selected!.config.id,'one');assert.equal(s.getSnapshot().selected!.revision,2);assert.notEqual(s.getSnapshot().selected,before.selected);assert.equal(s.getSnapshot().health!.name,'Renamed');assert.equal(paths.at(-1),'/v1/admin/library-channels/one/health');s.dispose();
});

test('tolerant omission is not removal proof; exact selected 404 closes that target only',async()=>{
 let mode='good';const s=source(async p=>{if(p.endsWith('/health')){if(mode==='gone')throw Object.assign(new Error('gone'),{status:404});return health();}return mode==='good'?list([channel(),channel('two')]):list([{...channel(),revision:0},channel('two')]);});
 await s.load();await s.health(s.getSnapshot().channels![0]);const selected=s.getSnapshot().selected;
 mode='dropped';assert.deepEqual(await s.refreshQuiet(),{kind:'adopted'});assert.equal(s.getSnapshot().selected,selected);assert(s.getSnapshot().channels!.includes(selected!));
 mode='gone';assert.deepEqual(await s.refreshQuiet(),{kind:'adopted'});assert.equal(s.getSnapshot().selected,undefined);assert.equal(s.getSnapshot().health,undefined);assert.deepEqual(s.getSnapshot().channels!.map(c=>c.config.id),['two']);s.dispose();
});

for(const lane of ['list','health'] as const)test(`quiet ${lane} failure retains complete visible state and original error`,async()=>{
 let failing=false;const error=new Error('original');const s=source(async p=>{if(failing&&(lane==='list'?!p.endsWith('/health'):p.endsWith('/health')))throw error;return p.endsWith('/health')?health():list();});
 await s.load();await s.health(s.getSnapshot().channels![0]);const before=s.getSnapshot();failing=true;
 const answer=await s.refreshQuiet();assert.equal(answer.kind,'unreadable');if(answer.kind==='unreadable')assert.equal(answer.error,error);assert.equal(s.getSnapshot(),before);s.dispose();
});

for(const action of ['close','switch','load','dispose'] as const)test(`quiet channel ${action} fences late list and exact health reads`,async()=>{
 let quiet=false;const read=deferred<unknown>();const s=source(async p=>p.endsWith('/health')?health(p.includes('/two/')?'two':'one'):quiet?read.promise:list([channel(),channel('two')]));
 await s.load();await s.health(s.getSnapshot().channels![0]);quiet=true;const pending=s.refreshQuiet();quiet=false;
 if(action==='close')s.closeHealth();else if(action==='switch')await s.health(s.getSnapshot().channels![1]);else if(action==='load')await s.load();else s.dispose();
 const before=s.getSnapshot();read.resolve(list());assert.deepEqual(await pending,{kind:'deferred'});assert.equal(s.getSnapshot(),before);s.dispose();
});

test('initial reads defer quiet work, malformed list does not adopt, and viewer denial keeps existing retirement path',async()=>{
 let mode='wait';const read=deferred<unknown>(),error=Object.assign(new Error('denied'),{status:403});let reported:unknown;
 const s=source(async()=>{if(mode==='wait')return read.promise;if(mode==='malformed')return {serverId:'foreign'};throw error;},()=>true,e=>{reported=e;});
 const loading=s.load();assert.deepEqual(await s.refreshQuiet(),{kind:'deferred'});read.resolve(list());await loading;mode='malformed';const before=s.getSnapshot();assert.equal((await s.refreshQuiet()).kind,'unreadable');assert.equal(s.getSnapshot(),before);
 mode='denied';const answer=await s.refreshQuiet();assert.equal(answer.kind,'unreadable');if(answer.kind==='unreadable')assert.equal(answer.error,error);assert.equal(reported,error);assert.equal(s.getSnapshot().channels,undefined);s.dispose();
});

test('sibling write admission invalidates quiet adoption even if the write finishes before its response',async()=>{
 let quiet=false;const read=deferred<unknown>();let signal:AbortSignal|undefined;
 const s=source(async(p,sig)=>{signal=sig;return quiet?read.promise:list();});await s.load();const before=s.getSnapshot();quiet=true;
 const pending=s.refreshQuiet();s.invalidateQuiet();assert.equal(signal!.aborted,true);read.resolve(list([channel('one','Old read',2)]));
 assert.deepEqual(await pending,{kind:'deferred'});assert.equal(s.getSnapshot(),before);s.dispose();
});

test('a late exact health response cannot reopen a closed target or install a mismatching receipt',async()=>{
 const read=deferred<unknown>(),started=deferred<void>();let mode='good';const s=source(async p=>{if(!p.endsWith('/health'))return list();if(mode==='wait'){started.resolve();return read.promise;}return health(mode==='wrong'?'two':'one');});
 await s.load();await s.health(s.getSnapshot().channels![0]);mode='wrong';const before=s.getSnapshot();assert.equal((await s.refreshQuiet()).kind,'unreadable');assert.equal(s.getSnapshot(),before);
 mode='wait';const pending=s.refreshQuiet();await started.promise;s.closeHealth();const closed=s.getSnapshot();read.resolve(health());assert.deepEqual(await pending,{kind:'deferred'});assert.equal(s.getSnapshot(),closed);s.dispose();
});
