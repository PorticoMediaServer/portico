import test from 'node:test';
import assert from 'node:assert/strict';
import {PreparedMediaService,parsePreparedView,parsePreparedVersion,parsePreparedChoice,preparedChoice} from '../src/prepared-media.ts';
import {resetSkippedParts,skippedParts} from '../src/tolerant.ts';
const sha='a'.repeat(64);
const job=():any=>({id:'job',itemId:'item',sourceId:'source',profileId:'portable-720-v1',targetId:'prepared-library',state:'running',phase:'encoding',generation:1,bytes:10,errorCode:'',revision:1,createdMs:1,updatedMs:2,versionId:''});
function view():any{return {itemId:'item',canManage:true,configured:true,configurationReason:'',preparedOffersRevision:sha,pollAfterMs:3000,profiles:[{id:'portable-720-v1',name:'Portable',kind:'video',width:1280,height:720,videoKbps:2500,audioKbps:192,description:'Keeps original'}],targets:[{id:'prepared-library',name:'This server'}],sources:[{id:'source',revision:sha,partIndex:0,container:'mp4',height:1080,available:true,reason:''}],jobs:[],versions:[{id:'version',itemId:'item',sourceId:'source',profileId:'portable-720-v1',targetId:'prepared-library',sourceRevision:sha,partIndex:0,editionId:null,digest:sha,size:256,facts:{container:'mp4',videoCodec:'h264',audioCodec:'aac',width:1280,height:720,durationMs:60000,audioTracks:1},state:'published',revision:1,createdMs:1,selectable:true,reason:''}]};}
function fixture(){let raw=view(),error:unknown=null;let serial=0;const posts:{path:string;body:any}[]=[];const api={request:async<T>(path:string,method?:string,body?:unknown):Promise<T>=>{if(method==='POST'){posts.push({path,body:structuredClone(body)});if(error)throw error;return {} as T;}return structuredClone(raw) as T;}};const service=new PreparedMediaService({api,scope:{serverId:'server',viewerId:'viewer'},itemId:'item',requestId:()=>`request-${++serial}`});return {service,posts,setError:(e:unknown)=>{error=e;},setView:(v:any)=>{raw=v;},get keys(){return serial;}};}
test('prepared version offers are bounded, scoped and separate from original identity',()=>{const data=parsePreparedView(view(),'item');assert(Object.isFrozen(data.versions));assert.equal(data.versions[0].editionId,null);assert.deepEqual(preparedChoice(data,data.versions[0]),{versionId:'version',expectedRevision:1,offersRevision:sha});for(const change of [(v:any)=>v.itemId='other',(v:any)=>v.canManage='yes',(v:any)=>v.preparedOffersRevision='latest',(v:any)=>v.configured=1,(v:any)=>v.configurationReason='Encoder\u0000missing',(v:any)=>v.pollAfterMs=10,(v:any)=>v.pollAfterMs=600000,(v:any)=>v.versions={0:v.versions[0]},(v:any)=>v.versions=Array.from({length:129},()=>v.versions[0]),(v:any)=>v.jobs=Array.from({length:65},()=>job()),(v:any)=>v.sources=Array.from({length:33},()=>v.sources[0]),(v:any)=>v.profiles=Array.from({length:17},()=>v.profiles[0]),(v:any)=>v.targets=Array.from({length:17},()=>v.targets[0]),(v:any)=>{v.canManage=false;v.jobs=[job()];},(v:any)=>{v.canManage=false;v.jobs=[{...job(),state:'archived'}];}]){const v=view();change(v);assert.throws(()=>parsePreparedView(v,'item'));}assert.throws(()=>parsePreparedView([view()],'item'));const offline=view();offline.sources[0]={...offline.sources[0],available:false,revision:'',reason:'source_unavailable'};assert.equal(parsePreparedView(offline,'item').versions[0].selectable,true,'offline original does not disable a valid derivative');});
test('uncertain optimization response retains exact immutable mutation identity',async t=>{const f=fixture();t.after(()=>f.service.dispose());await f.service.refresh();const v=f.service.getSnapshot().data!;f.setError(new Error('response lost'));await f.service.submit(v.sources[0],v.profiles[0],v.targets[0].id);assert.equal(f.service.getSnapshot().uncertain,true);await f.service.submit(v.sources[0],v.profiles[0],v.targets[0].id);assert.equal(f.keys,1);f.setError(null);await f.service.retryPending();assert.equal(f.posts.length,2);assert.deepEqual(f.posts[1],f.posts[0]);assert.equal(f.service.getSnapshot().uncertain,false);});
test('definitive CAS rejection stays visible after successful refresh; next request gets new identity',async t=>{const f=fixture();t.after(()=>f.service.dispose());await f.service.refresh();const v=f.service.getSnapshot().data!;f.setError(Object.assign(new Error(),{status:409}));await f.service.remove(v.versions[0]);assert.equal(f.service.getSnapshot().uncertain,false);assert.match(f.service.getSnapshot().error!,/changed/);f.setError(null);await f.service.remove(v.versions[0]);assert.equal(f.keys,2);assert.notEqual(f.posts[0].body.idempotencyKey,f.posts[1].body.idempotencyKey);});
test('denied current authority clears cached data and cannot be masked by refresh',async t=>{const f=fixture();t.after(()=>f.service.dispose());await f.service.refresh();const v=f.service.getSnapshot().data!;f.setError(Object.assign(new Error(),{status:403}));await f.service.remove(v.versions[0]);assert.equal(f.service.getSnapshot().data,null);assert.match(f.service.getSnapshot().error!,/owner/);});
test('read-only viewers cannot submit or delete, and disposed reads do not publish',async()=>{const f=fixture();const v=view();v.canManage=false;f.setView(v);await f.service.refresh();await f.service.remove(f.service.getSnapshot().data!.versions[0]);assert.equal(f.posts.length,0);f.service.dispose();const prior=f.service.getSnapshot();await f.service.refresh();assert.equal(f.service.getSnapshot(),prior);f.service.connect();await f.service.refresh();assert.equal(f.service.getSnapshot().data?.canManage,false);f.service.dispose();});
test('a read that ignores cancellation cannot restore prepared authority after denial',async()=>{
 let finish!:(v:unknown)=>void;let initial=true;
 const api={request:async<T>(_path:string,method?:string):Promise<T>=>{
  if(method==='POST')throw Object.assign(new Error('denied'),{status:403});
  if(initial){initial=false;return view() as T;}
  return await new Promise<unknown>(resolve=>{finish=resolve;}) as T;
 }};
 const service=new PreparedMediaService({api,scope:{serverId:'server',viewerId:'viewer'},itemId:'item',requestId:()=> 'request'});
 await service.refresh();const version=service.getSnapshot().data!.versions[0];
 const stale=service.refresh();await service.remove(version);
 assert.equal(service.getSnapshot().data,null);
 finish(view());await stale;
 assert.equal(service.getSnapshot().data,null);assert.equal(service.getSnapshot().loading,false);
 assert.match(service.getSnapshot().error!,/owner/);service.dispose();
});

test('prepared facts reject empty media, partial dimensions and unsupported video dimensions',()=>{
 for(const facts of [
  {videoCodec:'',audioCodec:'',audioTracks:0,width:0,height:0},
  {videoCodec:'',audioCodec:'aac',audioTracks:1,width:0,height:720},
  {videoCodec:'h264',audioCodec:'aac',audioTracks:1,width:3840,height:2160},
 ]){const v=view();Object.assign(v.versions[0].facts,facts);assert.throws(()=>parsePreparedVersion(v.versions[0],'item'));assert.deepEqual(parsePreparedView(v,'item').versions,[],'the version is not offered and the rest of the view is kept');}
});

test('one prepared version, job, source, profile or target that cannot be read leaves the rest',()=>{
 resetSkippedParts();
 const v=view(),version=v.versions[0],other='c'.repeat(64);
 Object.assign(v,{canManage:true,encoderQueue:'new'});
 v.versions=[{...version,badge:'new'},{...version,id:'other-item',itemId:'other'},{...version,id:'unbounded',facts:{...version.facts,durationMs:Infinity}},{...version,id:'tracks',facts:{...version.facts,audioTracks:17}},{...version,id:'state',state:'archived'},{...version,id:'withdrawn',state:'deleting'},{...version,id:'codec',facts:{...version.facts,videoCodec:'av1'}},{...version,id:'digest',digest:'unverified'},{...version,id:'size',size:0},{...version,id:'revision',revision:0},{...version,revision:9},'not a version',{...version,id:'second',digest:other}];
 v.jobs=[job(),{...job(),id:'paused',state:'paused'},{...job(),id:'elsewhere',itemId:'other'},{...job(),revision:5},{...job(),id:'second',revision:2}];
 v.sources=[v.sources[0],{...v.sources[0],id:'pathlike',revision:'path-to-source'},{...v.sources[0],id:'second'}];
 v.profiles=[v.profiles[0],{...v.profiles[0],id:'spatial',kind:'spatial'},{...v.profiles[0],name:'Again'}];
 v.targets=[v.targets[0],{id:'bad id!',name:'Elsewhere'},{id:'second',name:'Second',region:'new'}];
 const data=parsePreparedView(v,'item');
 assert.deepEqual(data.versions.map(x=>[x.id,x.revision,x.digest]),[['version',1,sha],['second',1,other]]);
 assert.deepEqual(data.jobs.map(x=>[x.id,x.revision]),[['job',1],['second',2]]);
 assert.deepEqual(data.sources.map(x=>x.id),['source','second']);
 assert.deepEqual(data.profiles.map(x=>x.name),['Portable']);
 assert.deepEqual(data.targets,[{id:'prepared-library',name:'This server'},{id:'second',name:'Second'}]);
 assert.equal(data.preparedOffersRevision,sha);
 assert.deepEqual(preparedChoice(data,data.versions[1]),{versionId:'second',expectedRevision:1,offersRevision:sha});
 assert.deepEqual(skippedParts().map(p=>p.document+':'+p.part+':'+p.count).sort(),['prepared-versions:jobs:3','prepared-versions:profiles:2','prepared-versions:sources:1','prepared-versions:targets:1','prepared-versions:versions:11']);
});

test('a playback choice the client builds stays exact',()=>{
 assert.deepEqual(parsePreparedChoice({versionId:'version',expectedRevision:1,offersRevision:sha}),{versionId:'version',expectedRevision:1,offersRevision:sha});
 for(const bad of [{versionId:'version',expectedRevision:1,offersRevision:sha,note:'new'},{versionId:'version',expectedRevision:0,offersRevision:sha},{versionId:'version',expectedRevision:1,offersRevision:'latest'},{versionId:'bad id!',expectedRevision:1,offersRevision:sha}])assert.throws(()=>parsePreparedChoice(bad));
});
