import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {HttpLocalApi, type LocalSession} from '../src/index.ts';
import {emptyUserMutationState, type UserMutationState} from '../src/user-mutations.ts';
import {UserMutationRecoveryModel} from '../src/user-mutation-recovery.ts';
import {saveChannelPreference, type GuideChannel} from '../src/channel-guide.ts';
import {liveDefaultsForm, recordingForm, maintenanceForm, logsForm, type ServerFormDeps} from '../src/server-admin/server-forms.ts';
import {saveLibrarySettings} from '../src/server-admin/libraries.ts';
import {parseLibraryDocument} from '../src/administration.ts';
import {RemoteSourceService, sourceReadPreset, type SourceReadSettings} from '../src/remote-sources.ts';

const clone=<T>(value:T):T=>JSON.parse(JSON.stringify(value));
const session={accessToken:'test',sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}} as LocalSession;
const op='1788580000000-12345678-1234-1234-1234-123456789012';
const wireScope={serverId:'server',viewerFence:'fence'};
const maintenance=JSON.parse(readFileSync(new URL('../../../server/testdata/contracts/get-admin-maintenance-settings.json',import.meta.url),'utf8')).response;
const live={scope:'server',revision:1,digest:'live',settings:{streamBufferMs:12000,retryWindowMs:60000,userAgent:'',guideDays:7,logoImport:true,discoveryEnabled:true,importLimits:{bytes:1000,channels:100,programmes:1000}}};
const dvr={revision:1,digest:'dvr',settings:{prePaddingMs:0,postPaddingMs:0,keepPolicy:{mode:'keep-all',keepCount:0,keepDays:0,keepUntilWatched:false},conversion:{mode:'none',ladderId:'',deleteOriginal:false},folderTemplate:'',movieFolderTemplate:'',recordingProfile:'original',sidecars:{nfo:false,poster:false,fanart:false,thumbnail:false},tunerPreference:'auto',liveTunerConflict:'keep-watching'},folderTokens:[],enumerations:{}};
const logs={revision:1,logLevel:'info',effectiveLevel:'info',retention:[],levels:['info','debug'],categories:[]};

/** The mounted transport and coordinator use an actual durable CAS boundary;
 * the HTTP seam returns the same typed wire documents as the named routes. */
function mounted(initial:unknown,apply:(body:any,path:string)=>unknown){
 let state=emptyUserMutationState(),current=clone(initial),failConsume=false,lost=false,malformed=false,conflict=false;
 const calls:{path:string;method:string;body:any;key:string|null}[]=[],acks:string[]=[];
 const store={read:async()=>clone(state),compareAndSwap:async(revision:number,next:UserMutationState)=>{
  if(revision!==state.revision)return false;
  if(failConsume&&next.entries.length<state.entries.length)throw new Error('durable consume unavailable');
  state=clone(next);return true;
 }};
 const api=new HttpLocalApi('http://server.test','test',async(input,init)=>{
  const path=new URL(String(input)).pathname,method=init?.method??'GET',body=typeof init?.body==='string'?JSON.parse(init.body):undefined,key=new Headers(init?.headers).get('Idempotency-Key');
  calls.push({path,method,body,key});
  if(path==='/v1/user-mutations/policy')return Response.json({serverTimeMs:Date.now()});
  if(path.startsWith('/v1/user-mutations/')){
   const entry=state.entries.find(entry=>entry.key===path.split('/').pop())!;
   return Response.json({key:entry.key,operation:entry.operation,status:'applied',committedMs:Date.now(),expiresMs:Date.now()+2592000000});
  }
  if(method==='GET')return Response.json(current);
  const retained=state.entries.find(entry=>entry.key===key);
  assert.ok(retained,'the frozen command is durable before HTTP dispatch');
  assert.equal(retained.state,'pending');
  if(conflict)return Response.json({error:{code:'revision_mismatch',currentRevision:2}},{status:409});
  const result=apply(body,path);current=clone(result);
  if(lost)throw new Error('lost after commit');
  return Response.json(malformed?{}:result);
 });
 api.setUserMutationSession(session);const coordinator=api.configureUserMutations(store);
 const acknowledge=api.acknowledgeUserMutation.bind(api);
 api.acknowledgeUserMutation=async id=>{acks.push(id);await acknowledge(id);};
 return {api,coordinator,calls,acks,state:()=>state,writes:()=>calls.filter(call=>call.method!=='GET'),setCurrent:(value:unknown)=>{current=clone(value);},failConsume:(value:boolean)=>{failConsume=value;},lost:(value:boolean)=>{lost=value;},malformed:(value:boolean)=>{malformed=value;},conflict:(value:boolean)=>{conflict=value;},close:()=>coordinator.dispose()};
}
async function reconcileLost(f:ReturnType<typeof mounted>){
 for(let n=0;n<100&&!(f.state().entries[0]?.attempts===1&&!f.state().entries[0]?.leaseOwner);n++)await new Promise(resolve=>setTimeout(resolve,2));
 assert.equal(f.state().entries[0]?.attempts,1);assert.equal(f.state().entries[0]?.leaseOwner,undefined);
 f.state().entries[0].dueMs=0;await f.coordinator.reconcile();
}
function deps(f:ReturnType<typeof mounted>,next:()=>string):ServerFormDeps{return {api:f.api,serverId:'server',operationId:next,console:{} as ServerFormDeps['console']};}
const channel={id:'channel',sourceId:'source',provenance:'live-source',preferenceRevision:7} as GuideChannel;
for(const provenance of ['live-source','library-channel'] as const)test('parsed '+provenance+' preference consumes the original request ID',async()=>{
 const f=mounted({},()=>({protocolVersion:'1.0',serverId:'server',revision:8}));
 try{assert.equal(await saveChannelPreference(f.api,'server',{...channel,provenance},'original-channel',{favorite:true,hidden:false}),8);assert.deepEqual(f.acks,['original-channel']);assert.equal(f.state().entries.length,0);assert.equal(f.writes().length,1);assert.equal(f.writes()[0].body.expectedRevision,7);assert.equal(f.writes()[0].body.requestId,f.writes()[0].key);}
 finally{f.close();}
});
test('an unreadable preference never consumes its confirmed command',async()=>{
 const f=mounted({},()=>({protocolVersion:'1.0',serverId:'other',revision:8}));
 try{await assert.rejects(saveChannelPreference(f.api,'server',channel,'original-channel',{favorite:true,hidden:false}));assert.deepEqual(f.acks,[]);assert.equal(f.state().entries[0].intentId,'original-channel');assert.equal(f.state().entries[0].state,'confirmed');}
 finally{f.close();}
});
test('a compact channel receipt remains for review without inventing a preference revision',async()=>{
 const f=mounted({},()=>({protocolVersion:'1.0',serverId:'server',revision:8}));f.lost(true);
 try{const pending=saveChannelPreference(f.api,'server',channel,'original-channel',{favorite:true,hidden:false});const rejected=assert.rejects(pending,(error:any)=>error.code==='mutation_needs_review');await reconcileLost(f);await rejected;assert.equal(f.writes().length,1);assert.equal(f.state().entries[0].state,'confirmed');assert.deepEqual(f.acks,[]);}
 finally{f.close();}
});

for(const [name,initial,factory,patch]of [
 ['live defaults',live,liveDefaultsForm,{guideDays:8}],
 ['DVR',dvr,recordingForm,{prePaddingMs:1000}],
 ['maintenance',maintenance,maintenanceForm,{backupKeepCount:8}],
 ['logs',logs,logsForm,{logLevel:'debug'}],
] as const)test('mounted '+name+' form parses and consumes before releasing its draft',async()=>{
 const f=mounted(initial,body=>({...clone(initial),revision:2,...('settings'in initial?{settings:body.settings}:{logLevel:body.logLevel})}));let ids=0;
 const form=factory(deps(f,()=>`form-${++ids}`)) as any;
 try{await form.load();form.set(patch);await form.save();assert.equal(form.getSnapshot().dirty,false);assert.equal(form.getSnapshot().status.revision,2);assert.equal(ids,1);assert.equal(f.writes()[0].body.expectedRevision,1);assert.deepEqual(f.acks,['form-1']);assert.equal(f.state().entries.length,0);}
 finally{form.dispose();f.close();}
});
test('failed form acknowledgement retains the exact body and ID and retries consumption without another HTTP write',async()=>{
 const f=mounted(maintenance,body=>({...clone(maintenance),revision:2,settings:body.settings}));let ids=0;const form=maintenanceForm(deps(f,()=>`form-${++ids}`));
 try{
  await form.load();form.set({backupKeepCount:8});f.failConsume(true);await assert.rejects(form.save());
  assert.equal(form.getSnapshot().dirty,true);assert.equal(form.getSnapshot().saved?.backupKeepCount,7);assert.equal(form.getSnapshot().draft?.backupKeepCount,8);assert.equal(form.getSnapshot().status?.revision,1);assert.equal(f.state().entries[0].state,'confirmed');
  const original=clone(f.writes()[0]);form.set({backupKeepCount:9});await assert.rejects(form.save());assert.equal(ids,1);assert.deepEqual(f.writes(),[original]);
  form.set({backupKeepCount:8});f.failConsume(false);await form.save();assert.equal(ids,1);assert.deepEqual(f.writes(),[original]);assert.equal(f.state().entries.length,0);assert.equal(form.getSnapshot().dirty,false);assert.deepEqual(f.acks,['form-1','form-1']);
 }finally{form.dispose();f.close();}
});
test('an unreadable form answer retains the original ID until a typed current GET parses',async()=>{
 const f=mounted(maintenance,body=>({...clone(maintenance),revision:2,settings:body.settings}));f.malformed(true);let ids=0;const form=maintenanceForm(deps(f,()=>`form-${++ids}`));
 try{await form.load();form.set({backupKeepCount:8});await assert.rejects(form.save());assert.equal(form.getSnapshot().dirty,true);assert.equal(f.state().entries[0].state,'confirmed');assert.deepEqual(f.acks,[]);await form.save();assert.equal(f.writes().length,1);assert.equal(ids,1);assert.deepEqual(f.acks,['form-1']);assert.equal(f.state().entries.length,0);assert.equal(form.getSnapshot().status?.revision,2);}
 finally{form.dispose();f.close();}
});
test('root current-state review and Keep as is release a conflicted form for a new explicit command',async()=>{
 const f=mounted(maintenance,body=>({...clone(maintenance),revision:3,settings:body.settings}));f.conflict(true);let ids=0;const form=maintenanceForm(deps(f,()=>`form-${++ids}`));
 const recovery=new UserMutationRecoveryModel({api:f.api,coordinator:f.coordinator,scope:()=>f.api.getUserMutationScope()});
 try{
  await form.load();form.set({backupKeepCount:8});await assert.rejects(form.save());
  assert.equal(form.getSnapshot().status?.revision,1);assert.equal(form.getSnapshot().draft?.backupKeepCount,8);assert.equal(f.state().entries[0].state,'review');assert.equal((f.state().entries[0].body as any).expectedRevision,1);
  form.set({backupKeepCount:9});await assert.rejects(form.save());assert.equal(ids,1);assert.equal(f.writes().length,1);
  const key=f.state().entries[0].key;await assert.rejects(recovery.keepAsIs(key));f.setCurrent({...clone(maintenance),revision:2});await recovery.review(key);await recovery.keepAsIs(key);assert.equal(f.state().entries.length,0);
  await form.load();f.conflict(false);await form.save();assert.equal(ids,2);assert.equal(f.writes().length,2);assert.equal(f.writes()[1].body.expectedRevision,2);assert.equal(f.writes()[1].body.settings.backupKeepCount,9);assert.notEqual(f.writes()[1].key,key);assert.equal(f.state().entries.length,0);
 }finally{recovery.dispose();form.dispose();f.close();}
});

const librarySettings={scanDepth:'basic',groupCollections:true,localTags:'prefer',lyricsSource:'files',titleSource:'tags',episodeNumbering:'season',findExtras:true,sortIgnoreArticles:true,refreshMode:'replace_unlocked',acoustidEnabled:false,continueWatching:{weeks:16,maximumItems:40,includeSeasonPremieres:true,videoPlayedThreshold:95,videoCompletion:'earliest'},allowMediaDeletion:false,monitorChanges:true,providers:[],analysis:['probe'],navigation:{trickplayIntervalMs:10000,trickplayTileWidth:320,trickplayMaxTiles:1080,chapterThumbnailMode:'embedded',videoPreviewEnabled:false,videoPreviewMs:30000}};
const libraryDoc={libraryId:'library',libraryName:'Library',libraryKind:'movie',revision:1,digest:'library',audience:{everyone:true,accounts:0},actions:['scan','remove','rename'],eligibleControls:[],settings:librarySettings,defaults:librarySettings,analysisMatrix:{costClasses:[],operations:[]},availableProviders:[],enumerations:{},sources:[]};
test('library settings consume only a validated exact-library document',async()=>{
 const before=parseLibraryDocument(libraryDoc),f=mounted(libraryDoc,body=>({...clone(libraryDoc),revision:2,settings:body.settings}));
 try{const saved=await saveLibrarySettings(f.api,before,before.settings,'original-library');assert.equal(saved.revision,2);assert.deepEqual(f.acks,['original-library']);assert.equal(f.state().entries.length,0);}
 finally{f.close();}
 const bad=mounted(libraryDoc,()=>({...clone(libraryDoc),libraryId:'other',revision:2}));
 try{await assert.rejects(saveLibrarySettings(bad.api,before,before.settings,'original-library'));assert.deepEqual(bad.acks,[]);assert.equal(bad.state().entries[0].state,'confirmed');}
 finally{bad.close();}
});

const profile:SourceReadSettings={sourceId:'dav',generation:'7',revision:2,saved:true,values:sourceReadPreset('once')};
const source={id:'dav',kind:'webdav',name:'DAV',rootPath:'/server/remote/dav',generation:7,state:'ready',rangeSupport:'conditional',insecureLocal:false,credentialPresent:true};
test('source profile acknowledgement failure keeps its original request and parsed settings',async()=>{
 const next={...profile,revision:3},f=mounted({},body=>({scope:wireScope,receipt:{operationId:body.operationId,accepted:true,removed:false,source,readSettings:next}}));let ids=0;
 const service=new RemoteSourceService(f.api,{serverId:'server',viewerId:'profile'},async()=>{ids++;return op;});
 try{f.failConsume(true);await assert.rejects(service.saveProfile(profile,profile.values));assert.equal(f.state().entries[0].intentId,op);const original=clone(f.writes()[0]);await assert.rejects(service.saveProfile({...profile,revision:9},profile.values));assert.equal(service.getSnapshot().operationId,op);assert.equal(service.getSnapshot().ambiguous,true);assert.equal(ids,1);f.failConsume(false);assert.deepEqual(await service.saveProfile(profile,profile.values),next);assert.equal(f.writes().length,1);assert.deepEqual(f.writes()[0],original);assert.deepEqual(f.acks,[op,op]);assert.equal(f.state().entries.length,0);}
 finally{service.dispose();f.close();}
});
test('lost source profile result consumes only the current scoped GET without fabricating a historical source receipt',async()=>{
 const next={...profile,revision:20,generation:'9'},f=mounted({},()=>({scope:wireScope,settings:next}));f.lost(true);
 const service=new RemoteSourceService(f.api,{serverId:'server',viewerId:'profile'},async()=>op);
 try{const pending=service.saveProfile(profile,profile.values);await reconcileLost(f);assert.deepEqual(await pending,next);assert.equal(service.getSnapshot().receipt,null);assert.deepEqual(f.acks,[op]);assert.equal(f.writes().length,1);assert.equal(f.state().entries.length,0);}
 finally{service.dispose();f.close();}
});
test('source program acknowledgement retries its original parsed result and rejects a replacement body',async()=>{
 const program={executable:'/usr/bin/rclone',version:'1.68.0',revision:2,included:true},f=mounted({},()=>({scope:wireScope,program}));let ids=0;
 const service=new RemoteSourceService(f.api,{serverId:'server',viewerId:'profile'},async()=>{ids++;return op;});
 try{f.failConsume(true);await assert.rejects(service.setProgram('dav',1,{useIncluded:true}));await assert.rejects(service.setProgram('dav',2,{executable:'/new/rclone'}));assert.equal(ids,1);f.failConsume(false);assert.deepEqual(await service.setProgram('dav',1,{useIncluded:true}),program);assert.equal(f.writes().length,1);assert.deepEqual(f.writes()[0].body,{operationId:f.writes()[0].key,expectedRevision:1,useIncluded:true});assert.deepEqual(f.acks,[op,op]);assert.equal(f.state().entries.length,0);}
 finally{service.dispose();f.close();}
});
test('an unconsumed profile command also blocks replacement through the start-choice entry point',async()=>{
 const f=mounted({},body=>({scope:wireScope,receipt:{operationId:body.operationId,accepted:true,removed:false,source,readSettings:{...profile,revision:3}}}));let ids=0;
 const service=new RemoteSourceService(f.api,{serverId:'server',viewerId:'profile'},async()=>{ids++;return op;});
 try{f.failConsume(true);await assert.rejects(service.saveProfile(profile,profile.values));await assert.rejects(service.saveProfileStartChoice(profile,{} as any,{} as any,{signal:new AbortController().signal,isCurrent:()=>true,consentDirty:false}),(error:any)=>error.code==='mutation_needs_review');assert.equal(ids,1);assert.equal(f.writes().length,1);assert.equal(f.state().entries[0].intentId,op);}
 finally{service.dispose();f.close();}
});
test('compact source program acceptance remains in Pending changes for current-state review',async()=>{
 const f=mounted({},()=>({scope:wireScope,program:{executable:'/usr/bin/rclone',version:'1.68.0',revision:2,included:true}}));f.lost(true);
 const service=new RemoteSourceService(f.api,{serverId:'server',viewerId:'profile'},async()=>op);
 try{const rejected=assert.rejects(service.setProgram('dav',1,{useIncluded:true}),(error:any)=>error.code==='mutation_needs_review');await reconcileLost(f);await rejected;assert.equal(f.writes().length,1);assert.deepEqual(f.acks,[]);assert.equal(f.state().entries[0].state,'confirmed');}
 finally{service.dispose();f.close();}
});
test('root review releases a source profile conflict for a new explicitly authored body',async()=>{
 const next={...profile,revision:3},f=mounted({scope:wireScope,settings:profile},body=>({scope:wireScope,receipt:{operationId:body.operationId,accepted:true,removed:false,source,readSettings:next}}));f.conflict(true);let ids=0;
 const service=new RemoteSourceService(f.api,{serverId:'server',viewerId:'profile'},async()=>{ids++;return op.replace(/012$/,String(11+ids).padStart(3,'0'));});
 const recovery=new UserMutationRecoveryModel({api:f.api,coordinator:f.coordinator,scope:()=>f.api.getUserMutationScope()});
 try{await assert.rejects(service.saveProfile(profile,profile.values));const original=clone(f.state().entries[0]);await assert.rejects(service.saveProfile(profile,{...profile.values,connections:8}));assert.equal(ids,1);assert.equal(f.writes().length,1);assert.deepEqual(f.state().entries[0].body,original.body);await recovery.review(original.key);await recovery.keepAsIs(original.key);f.conflict(false);await service.saveProfile(profile,{...profile.values,connections:8});assert.equal(ids,2);assert.equal(f.writes().length,2);assert.notEqual(f.writes()[1].key,original.key);assert.equal(f.writes()[1].body.values.connections,8);assert.equal(f.state().entries.length,0);}
 finally{recovery.dispose();service.dispose();f.close();}
});
