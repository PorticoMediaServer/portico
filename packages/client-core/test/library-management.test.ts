import test,{type TestContext} from 'node:test';import {getEventListeners} from 'node:events';import assert from 'node:assert/strict';
import {LibraryManagementService,fetchEpisodeIssues} from '../src/library-management.ts';
const scope={serverId:'server',viewerId:JSON.stringify(['local','owner','profile'])},wireScope={serverId:'server',viewerFence:'owner-fence'};
function library(id='one',name='Named library',revision=1):any{return {id,name,kind:'movie',revision,source:{path:'/owner/source/'+id,availability:'unknown',basis:'not_checked'},lastScan:null,actions:['rename','scan']};}
function job(id='job-one',libraryId='one'):any{return {id,libraryId,status:'running',processed:12,createdAt:'2026-09-05T00:00:00Z',updatedAt:null,finishedAt:null,actions:['cancel']};}
function data(path:string):any{const u=new URL(path,'https://x');if(u.pathname==='/v1/admin/libraries')return {scope:wireScope,revision:1,items:[library()],nextCursor:''};if(u.pathname==='/v1/admin/jobs')return {scope:wireScope,revision:1,items:[job('job-one',u.searchParams.get('libraryId')??'one')],nextCursor:''};return {scope:wireScope,library:library(decodeURIComponent(u.pathname.split('/').at(-1)!))};}
function service(fn:(path:string,method:string,body:unknown,signal?:AbortSignal)=>Promise<unknown>,timeoutMs=1000){return new LibraryManagementService({scope,timeoutMs,api:{request:<T>(p:string,m='GET',b?:unknown,s?:AbortSignal)=>fn(p,m,b,s) as Promise<T>}});}
function deferred(){let resolve!:(v:unknown)=>void;const promise=new Promise<unknown>(r=>resolve=r);return {promise,resolve};}
test('owner directory and selected source/jobs are bounded independent server projections',async()=>{const paths:string[]=[];const s=service(async p=>{paths.push(p);return data(p);});await s.loadLibraries();await s.selectLibrary('one');assert.equal(s.getSnapshot().directory.phase,'ready');assert.equal(s.getSnapshot().selected.data!.library.source.availability,'unknown');assert.equal(s.getSnapshot().jobs.data!.items[0].processed,12);assert(paths.some(p=>p==='/v1/admin/jobs?limit=40&libraryId=one'));assert.throws(()=>{(s.getSnapshot().directory.data!.items as any).push(library("other"));});s.dispose();});
test('late selected resource and jobs cannot overwrite rapid library switch',async()=>{const d=deferred();let signal:AbortSignal|undefined;const s=service(async(p,_m,_b,a)=>{if(p==='/v1/admin/libraries/one'){signal=a;return d.promise;}return data(p);});const pending=s.selectLibrary('one');await s.selectLibrary('two');await pending;assert(signal?.aborted);d.resolve(data('/v1/admin/libraries/one'));await Promise.resolve();assert.equal(s.getSnapshot().selected.data!.library.id,'two');assert.equal(s.getSnapshot().jobs.data!.items[0].libraryId,'two');s.dispose();});
test('directory continuation revision changes require refresh and retain no stale source paths',async()=>{const s=service(async p=>({...data(p),revision:p.includes('cursor=')?2:1,nextCursor:'next'}));await s.loadLibraries();await s.nextLibraries();assert.equal(s.getSnapshot().directory.phase,'refresh-required');assert.equal(s.getSnapshot().directory.data,null);await s.loadLibraries();assert.equal(s.getSnapshot().directory.phase,'ready');s.dispose();});
test('paging uses opaque server cursor and Previous exact page',async()=>{const paths:string[]=[];const s=service(async p=>{paths.push(p);return {...data(p),nextCursor:p.includes('cursor=')?'':'opaque&two'};});await s.loadLibraries();await s.nextLibraries();assert.equal(s.getSnapshot().directory.cursor,'opaque&two');await s.previousLibraries();assert.equal(s.getSnapshot().directory.cursor,null);assert(new URL(paths[1],'https://x').searchParams.get('cursor')==='opaque&two');s.dispose();});
test('rename uses exact CAS and refreshes authoritative reads without automatic replay',async()=>{let name='Named library',revision=1;const writes:unknown[]=[];const s=service(async(p,m,b)=>{if(m==='PATCH'){writes.push(b);name=(b as any).name;revision++;return {scope:wireScope,library:library('one',name,revision)};}const raw=data(p);if(raw.library)raw.library=library('one',name,revision);return raw;});await s.selectLibrary('one');await s.rename('Renamed',1);assert.deepEqual(writes,[{name:'Renamed',expectedRevision:1}]);assert.equal(s.getSnapshot().selected.data!.library.name,'Renamed');assert.equal(s.getSnapshot().mutation.phase,'complete');assert.throws(()=>s.rename('Again',1));s.dispose();});
test('lost scan response stays ambiguous; reconciliation issues GETs only',async()=>{let writes=0;const s=service(async(p,m)=>{if(m==='POST'){writes++;throw new TypeError('Network response lost');}return data(p);});await s.selectLibrary('one');await s.scan();assert.equal(s.getSnapshot().mutation.phase,'ambiguous');await s.reconcile();assert.equal(writes,1);assert.equal(s.getSnapshot().mutation.phase,'ambiguous');s.dispose();});
test('duplicate scan is singleflight and different concurrent command is rejected',async()=>{const d=deferred();let writes=0;const s=service(async(p,m)=>{if(m==='POST'){writes++;return d.promise;}return data(p);});await s.selectLibrary('one');const a=s.scan(),b=s.scan();assert.equal(a,b);assert.equal(writes,1);assert.throws(()=>s.rename('New',1));d.resolve({id:'new-job',status:'queued',processed:0});await a;assert.equal(s.getSnapshot().mutation.phase,'complete');s.dispose();});
test('CAS conflict remains an error until owner reads current state, no auto rename',async()=>{let writes=0;const s=service(async(p,m)=>{if(m==='PATCH'){writes++;throw Object.assign(new Error('Changed'),{code:'library_configuration_conflict'});}return data(p);});await s.selectLibrary('one');await s.rename('New',1);assert.equal(s.getSnapshot().mutation.phase,'error');await s.reconcile();assert.equal(writes,1);s.dispose();});
test('job cancellation requires server action and only refreshes after acknowledgement',async()=>{const writes:string[]=[];const s=service(async(p,m)=>{if(m==='DELETE'){writes.push(p);return undefined;}return data(p);});await s.selectLibrary('one');assert.throws(()=>s.cancelJob('foreign'));await s.cancelJob('job-one');assert.deepEqual(writes,['/v1/admin/ingestion/jobs/job-one']);assert.equal(s.getSnapshot().mutation.phase,'complete');s.dispose();});
test('scope change during mutation fences late response and clears source paths',async()=>{const d=deferred();const s=service(async(p,m)=>m==='PATCH'?d.promise:data(p));await s.selectLibrary('one');const pending=s.rename('New',1);s.setScope({...scope,viewerId:'another'},{request:async<T>()=>{throw new Error('unused');}});await pending;d.resolve({scope:wireScope,library:library('one','New',2)});await Promise.resolve();assert.equal(s.getSnapshot().selected.data,null);assert.equal(s.getSnapshot().directory.data,null);assert.equal(s.getSnapshot().mutation.phase,'idle');s.dispose();});
test('owner denial on any lane clears every privileged cached projection',async()=>{let denied=false;const s=service(async p=>{if(denied)throw Object.assign(new Error('Denied'),{status:403,code:'forbidden'});return data(p);});await s.loadLibraries();await s.selectLibrary('one');denied=true;await s.loadJobs();assert.equal(s.getSnapshot().directory.data,null);assert.equal(s.getSnapshot().selected.data,null);assert.equal(s.getSnapshot().jobs.phase,'error');s.dispose();});
test('oversized pages, an unreadable revision or cursor and a changed owner fence fail closed',async()=>{for(const bad of ['size','fence','revision','cursor','shape']){const s=service(async p=>{const raw=data(p);if(p.includes('/jobs')){if(bad==='size')raw.items=Array(41).fill(job());if(bad==='fence')raw.scope={...wireScope,viewerFence:'changed'};if(bad==='revision')raw.revision='1';if(bad==='cursor')raw.nextCursor=7;if(bad==='shape')raw.items={};}return raw;});await s.loadLibraries();await s.selectLibrary('one');assert.equal(s.getSnapshot().jobs.phase,'error',bad);assert.equal(s.getSnapshot().jobs.data,null);s.dispose();}});
test('a scan or a library this app cannot read is left out and the rest of the page is kept',async()=>{
 const s=service(async p=>{const raw=data(p);const u=new URL(p,'https://x');
  if(u.pathname==='/v1/admin/jobs')raw.items=[job(),{...job('job-foreign'),libraryId:'foreign'},{...job('job-future'),status:'hibernating'},{...job('job-two'),actions:['cancel','teleport','cancel'],futureField:1},{...job(),processed:99},{...job('job-done'),status:'complete',actions:['cancel','retry']},'nonsense'];
  if(u.pathname==='/v1/admin/libraries')raw.items=[library(),{...library('podcasts'),kind:'podcast'},{...library('two'),source:{path:'/owner/source/two',availability:'mounting',basis:'watcher'},lastScan:{...job('scan-two','two'),status:'hibernating'},actions:['scan','teleport'],futureField:1},{...library('three'),revision:0}];
  return {...raw,futureField:true};});
 await s.loadLibraries();await s.selectLibrary('one');
 assert.equal(s.getSnapshot().jobs.phase,'ready');assert.deepEqual(s.getSnapshot().jobs.data!.items.map(j=>[j.id,j.processed,j.actions]),[['job-one',12,['cancel']],['job-two',12,['cancel']],['job-done',12,['retry']]]);
 const libraries=s.getSnapshot().directory.data!.items;assert.deepEqual(libraries.map(l=>l.id),['one','two']);
 assert.deepEqual(libraries[1].source,{path:'/owner/source/two',availability:'unknown',basis:'not_checked'});assert.equal(libraries[1].lastScan,null);assert.deepEqual(libraries[1].actions,['scan']);s.dispose();
 // The one library a page is about is never shown as something else: a kind this app does not know still refuses it.
 const one=service(async p=>{const raw=data(p);if(raw.library)raw.library.kind='podcast';return raw;});await one.selectLibrary('one');assert.equal(one.getSnapshot().selected.phase,'error');one.dispose();
});
test('create selects acknowledged library and never automatically scans',async()=>{const writes:string[]=[];const s=service(async(p,m,b)=>{if(m==='POST'){writes.push(p);return {id:'created',name:(b as any).name,kind:'movie',defaultView:'discover'};}return data(p);});await s.loadLibraries();await s.createLibrary({name:'Created',kind:'movie',path:'/server/source'});assert.deepEqual(writes,['/v1/libraries']);assert.equal(s.getSnapshot().selectedLibraryId,'created');assert.equal(s.getSnapshot().selected.data!.library.id,'created');assert.equal(s.getSnapshot().mutation.phase,'complete');s.dispose();});
test('lost create and command timeout never trigger automatic duplicate creation',async()=>{let writes=0;const s=service(async(p,m)=>{if(m==='POST'){writes++;return new Promise(()=>{});}return data(p);},5);await s.loadLibraries();await s.createLibrary({name:'Created',kind:'movie',path:'/server/source'});assert.equal(s.getSnapshot().mutation.phase,'ambiguous');assert.equal(s.getSnapshot().mutation.error?.code,'timeout');await s.reconcile();assert.equal(writes,1);assert.equal(s.getSnapshot().selectedLibraryId,null);s.dispose();});
test('slow isolated source inspection cannot block directory or filtered job reads',async()=>{const s=service(async p=>p==='/v1/admin/libraries/one'?new Promise(()=>{}):data(p),5);await s.loadLibraries();await s.selectLibrary('one');assert.equal(s.getSnapshot().directory.phase,'ready');assert.equal(s.getSnapshot().jobs.phase,'ready');assert.equal(s.getSnapshot().selected.error?.code,'timeout');s.dispose();});
test('same-library reselection fences the canceled command even if its response is late',async()=>{const d=deferred();const s=service(async(p,m)=>m==='PATCH'?d.promise:data(p));await s.selectLibrary('one');const old=s.rename('Old command',1);await s.selectLibrary('one');await old;d.resolve({scope:wireScope,library:library('one','Old command',2)});await Promise.resolve();assert.equal(s.getSnapshot().selected.data!.library.name,'Named library');assert.equal(s.getSnapshot().mutation.phase,'idle');s.dispose();});
test('directory paging history remains bounded and selected response identity cannot be relabelled',async()=>{const s=service(async p=>{const u=new URL(p,'https://x'),index=Number(u.searchParams.get('cursor')??0);return {...data(p),nextCursor:String(index+1)};});await s.loadLibraries();for(let i=0;i<70;i++)await s.nextLibraries();for(let i=0;i<64;i++)await s.previousLibraries();assert.equal(s.getSnapshot().directory.canPrevious,false);assert.equal(s.getSnapshot().directory.cursor,'6');s.dispose();const bad=service(async p=>p==='/v1/admin/libraries/one'?{scope:wireScope,library:library('foreign')}:data(p));await bad.selectLibrary('one');assert.equal(bad.getSnapshot().selected.phase,'error');assert.equal(bad.getSnapshot().selected.data,null);bad.dispose();});
test('source already configured is a definite409 rejection, never ambiguous or automatically recreated',async()=>{let creates=0;const s=service(async(p,m)=>{if(m==='POST'){creates++;throw Object.assign(new Error('This source is already configured as a library. Open that library to manage or scan it.'),{status:409,code:'source_already_configured'});}return data(p);});await s.loadLibraries();await s.createLibrary({name:'Another name',kind:'movie',path:'/already/configured'});assert.equal(s.getSnapshot().mutation.phase,'error');assert.equal(s.getSnapshot().mutation.error?.code,'source_already_configured');assert.equal(s.getSnapshot().selectedLibraryId,null);await s.reconcile();assert.equal(creates,1);s.dispose();});

test('scan source recovery reason survives failed history only and an unknown reason is never shown',async()=>{for(const code of ['source_unavailable','private_internal_reason']){const s=service(async p=>{const raw=data(p);if(p.includes('/jobs'))raw.items=[{...job(),status:'failed',errorCode:code,error:'Source unavailable',finishedAt:'2026-09-05T01:00:00Z',actions:['retry']}];return raw;});await s.selectLibrary('one');assert.equal(s.getSnapshot().jobs.phase,'ready');assert.equal(s.getSnapshot().jobs.data!.items[0].errorCode,code==='source_unavailable'?code:undefined);assert.equal(s.getSnapshot().jobs.data!.items[0].processed,12);s.dispose();}const s=service(async p=>{const raw=data(p);if(p.includes('/jobs'))raw.items[0].errorCode='source_unavailable';return raw;});await s.selectLibrary('one');assert.equal(s.getSnapshot().jobs.phase,'ready');assert.equal(s.getSnapshot().jobs.data!.items[0].errorCode,undefined);s.dispose();});
test('create sends the chosen metadata source, and omits it for the server default',async()=>{const bodies:unknown[]=[];const s=service(async(p,m,b)=>{if(m==='POST'){bodies.push(b);return {id:'created'+bodies.length,name:(b as any).name,kind:'movie',defaultView:'discover'};}return data(p);});await s.loadLibraries();await s.createLibrary({name:'Home videos',kind:'movie',path:'/server/home',metadataAgent:'local'});await s.createLibrary({name:'Movies',kind:'movie',path:'/server/movies'});assert.deepEqual(bodies,[{name:'Home videos',kind:'movie',path:'/server/home',metadataAgent:'local'},{name:'Movies',kind:'movie',path:'/server/movies'}]);assert.throws(()=>s.createLibrary({name:'Bad',kind:'movie',path:'/x',metadataAgent:'elsewhere' as never}));s.dispose();});

test('a 503 on the library read is retried with backoff, then shows the error; a success in between loads the library',async()=>{
 const busy=()=>Object.assign(new Error('Busy'),{status:503,code:'unavailable',retryable:true});
 let calls=0;const quick=(fn:(p:string)=>Promise<unknown>)=>new LibraryManagementService({scope,timeoutMs:1000,retryBaseMs:1,api:{request:<T>(p:string)=>fn(p) as Promise<T>}});
 const failing=quick(async p=>{if(p==='/v1/admin/libraries/one'){calls++;throw busy();}return data(p);});
 await failing.selectLibrary('one');
 assert.equal(calls,4);assert.equal(failing.getSnapshot().selected.phase,'error');assert.equal(failing.getSnapshot().selected.error?.code,'unavailable');
 let second=0;const recovering=quick(async p=>{if(p==='/v1/admin/libraries/one'&&++second<3)throw busy();return data(p);});
 await recovering.selectLibrary('one');
 assert.equal(recovering.getSnapshot().selected.phase,'ready');assert.equal(recovering.getSnapshot().selected.data?.library.id,'one');
 let refused=0;const never=quick(async p=>{if(p==='/v1/admin/libraries/one'){refused++;throw Object.assign(new Error('Nope'),{status:500,code:'internal',retry:'never'});}return data(p);});
 await never.selectLibrary('one');
 assert.equal(refused,1);assert.equal(never.getSnapshot().selected.phase,'error');
 failing.dispose();recovering.dispose();never.dispose();
});

test('files needing attention are read from the library, a full scan names its mode, and issues page with unreadable rows skipped',async()=>{
 const posts:string[]=[];const s=service(async(p,m)=>{if(m==='POST'){posts.push(p);return {id:'job',libraryId:'one',status:'queued',processed:0,createdAt:'2026-09-05T00:00:00Z',updatedAt:null,finishedAt:null,actions:[]};}const raw=data(p);if(p==='/v1/admin/libraries/one')raw.library.attention={files:1000,atLeast:true};return raw;});
 await s.selectLibrary('one');
 assert.deepEqual(s.getSnapshot().selected.data?.library.attention,{files:1000,atLeast:true});
 await s.scan('full');
 assert.equal(posts[0],'/v1/libraries/one/scans?mode=full');
 const bare=service(async p=>{const raw=data(p);if(p==='/v1/admin/libraries/one')raw.library.attention={files:'many'};return raw;});
 await bare.selectLibrary('one');
 assert.equal(bare.getSnapshot().selected.phase,'ready');assert.equal(bare.getSnapshot().selected.data?.library.attention,undefined);
 let asked='';const page=await fetchEpisodeIssues({request:async<T>(p:string)=>{asked=p;return {issues:[{assetId:'a1',sourceName:'Show.S01.mkv',code:'no_episode_number'},{assetId:'',sourceName:'bad',code:'x'}],nextCursor:'a1'} as T;}},'one','a0');
 assert.equal(asked,'/v1/libraries/one/episode-issues?limit=40&cursor=a0');
 assert.deepEqual(page.issues.map(x=>x.assetId),['a1']);assert.equal(page.nextCursor,'a1');
 s.dispose();bare.dispose();
});
test('create carries the metadata language only when shown, and an unoffered language is a definite refusal',async()=>{const bodies:unknown[]=[];const s=service(async(p,m,b)=>{if(m==='POST'){bodies.push(b);return {id:'created'+bodies.length,name:(b as any).name,kind:(b as any).kind,defaultView:'discover'};}return data(p);});await s.loadLibraries();await s.createLibrary({name:'Movies',kind:'movie',path:'/server/movies',metadataAgent:'online',metadataLanguage:'ja'});await s.createLibrary({name:'Home videos',kind:'movie',path:'/server/home'});assert.deepEqual(bodies,[{name:'Movies',kind:'movie',path:'/server/movies',metadataAgent:'online',metadataLanguage:'ja'},{name:'Home videos',kind:'movie',path:'/server/home'}]);for(const bad of ['en-US','EN','e','eng',''])assert.throws(()=>s.createLibrary({name:'Bad',kind:'movie',path:'/x',metadataLanguage:bad}));s.dispose();const refused=service(async(p,m)=>{if(m==='POST')throw Object.assign(new Error('Choose a metadata language offered for this library.'),{status:400,code:'invalid_metadata_language'});return data(p);});await refused.loadLibraries();await refused.createLibrary({name:'Movies',kind:'movie',path:'/server/movies',metadataLanguage:'xx'});assert.equal(refused.getSnapshot().mutation.phase,'error');assert.equal(refused.getSnapshot().mutation.error?.code,'invalid_metadata_language');refused.dispose();});
test('create sends all selected folders in one operation and keeps the metadata language',async()=>{const bodies:unknown[]=[];const s=service(async(p,m,b)=>{if(m==='POST'){bodies.push(b);return {id:'created',name:(b as any).name,kind:(b as any).kind};}return data(p);});await s.createLibrary({name:'Films',kind:'movie',folders:['/disk/a','/disk/b'],metadataAgent:'online',metadataLanguage:'en'});assert.deepEqual(bodies,[{name:'Films',kind:'movie',folders:['/disk/a','/disk/b'],metadataAgent:'online',metadataLanguage:'en'}]);assert.equal(s.getSnapshot().mutation.phase,'complete');s.dispose();});

// Drive the actual service's request and retry clocks without a wall-clock wait.
function retryClock(t:TestContext){
 let next=0;const pending=new Map<number,{fn:()=>void;ms:number}>();
 t.mock.method(globalThis,'setTimeout',(fn:()=>void,ms:number)=>{pending.set(++next,{fn,ms});return next;});
 t.mock.method(globalThis,'clearTimeout',(id:number)=>{pending.delete(id);});
 return {pending,fire(){assert.equal(pending.size,1);const [id,timer]=[...pending][0];pending.delete(id);timer.fn();}};
}
const settleRetries=async()=>{for(let i=0;i<16;i++)await Promise.resolve();};
function retryFailure(after?:unknown,extra:Record<string,unknown>={}){return Object.assign(new Error('Server asks the same request to wait'),{status:503,code:'server_busy',retry:'same_request',retryable:true,retryAfterSeconds:after,...extra});}

test('library reads preserve server Retry-After floors, local backoff and the original request',async t=>{
 const clock=retryClock(t);
 for(const [after,expected] of [[60,60000],[0.25,1000],[undefined,1000],[0,1000],[-1,1000],['60',1000],[NaN,1000],[Infinity,1000],[1.0001,1001]] as const){
  const calls:{path:string;method:string;body:unknown;signal?:AbortSignal}[]=[];
  const s=service(async(path,method,body,signal)=>{calls.push({path,method,body,signal});if(calls.length===1)throw retryFailure(after);return data(path);});
  try{
   const read=s.loadLibraries();await settleRetries();
   assert.equal(calls.length,1);assert.deepEqual([...clock.pending.values()].map(t=>t.ms),[expected]);
   assert.equal(getEventListeners(calls[0].signal!,'abort').length,1);
   clock.fire();await read;
   assert.equal(s.getSnapshot().directory.phase,'ready');assert.equal(calls.length,2);
   assert.deepEqual(calls[1],calls[0]);assert.equal(clock.pending.size,0);
   assert.equal(getEventListeners(calls[0].signal!,'abort').length,0);
  }finally{s.dispose();}
 }
});

test('an unrepresentable server retry floor surfaces the original failure without a timer or follow-up',async t=>{
 const clock=retryClock(t);
 for(const after of [2147483.648,Number.MAX_VALUE]){
  let calls=0;const failure=retryFailure(after);const s=service(async()=>{calls++;throw failure;});
  try{await s.loadLibraries();assert.equal(calls,1);assert.equal(clock.pending.size,0);assert.deepEqual(s.getSnapshot().directory.error,{code:failure.code,message:failure.message,retryable:true});}
  finally{s.dispose();}
 }
});

test('dispose, cancel, scope replacement and a replacement read clear retry timers and listeners',async t=>{
 const clock=retryClock(t);
 for(const retire of ['dispose','cancel','scope','reload'] as const){
  let calls=0,signal:AbortSignal|undefined;
  const s=service(async(p,_m,_b,a)=>{calls++;signal??=a;if(calls===1)throw retryFailure(60);return data(p);});
  try{
   const read=s.loadLibraries();await settleRetries();assert.equal(clock.pending.size,1);
   if(retire==='dispose')s.dispose();else if(retire==='cancel')s.cancel();
   else if(retire==='scope')s.setScope({...scope,viewerId:'replacement'},{request:async<T>()=>{throw new Error('old retry must not call replacement API');}});
   else await s.loadLibraries();
   await read;assert.equal(calls,retire==='reload'?2:1);assert.equal(clock.pending.size,0);
   assert.equal(signal!.aborted,true);assert.equal(getEventListeners(signal!,'abort').length,0);
  }finally{s.dispose();}
 }
});

test('retry eligibility refuses never, false and authorization failures and preserves the three-retry bound',async t=>{
 const clock=retryClock(t);
 for(const extra of [{retry:'never'},{retry:false,status:503,retryable:false},{status:403,code:'forbidden'},{status:401,code:'unauthorized'}]){
  let calls=0;const s=service(async()=>{calls++;throw retryFailure(60,extra);});
  try{await s.loadLibraries();assert.equal(calls,1);assert.equal(clock.pending.size,0);assert.equal(s.getSnapshot().directory.phase,'error');}finally{s.dispose();}
 }
 let calls=0;const s=new LibraryManagementService({scope,retryBaseMs:4000,api:{request:async<T>()=>{calls++;throw retryFailure();}}});
 try{const read=s.loadLibraries();await settleRetries();for(const expected of [4000,8000,8000]){assert.deepEqual([...clock.pending.values()].map(t=>t.ms),[expected]);clock.fire();await settleRetries();}await read;assert.equal(calls,4);assert.equal(s.getSnapshot().directory.phase,'error');assert.equal(clock.pending.size,0);}finally{s.dispose();}
});
test('MOB-111: a library can be created without its first scan; omission keeps the server default',async()=>{const bodies:any[]=[];const s=service(async(p,m,b)=>{if(m==='POST'){bodies.push(b);return {id:'created',name:(b as any).name,kind:'movie',defaultView:'discover'};}return data(p);});await s.loadLibraries();
 await s.createLibrary({name:'Quiet',kind:'movie',path:'/server/quiet',scanAfter:false});await s.createLibrary({name:'Normal',kind:'movie',path:'/server/normal'});
 assert.equal(bodies[0].scanAfter,false);assert.equal('scanAfter' in bodies[1],false);
 assert.throws(()=>s.createLibrary({name:'Bad',kind:'movie',path:'/server/bad',scanAfter:1 as never}));s.dispose?.();});

test('quiet directory refresh retains the exact current page continuation, selected/jobs and mutation receipts',async()=>{
 const paths:string[]=[];let fresh=false;
 const s=service(async p=>{paths.push(p);const raw=data(p);if(p.startsWith('/v1/admin/libraries?'))return {...raw,items:[library(p.includes('cursor=')?'two':'one',fresh?'Fresh':'Before')],nextCursor:p.includes('cursor=')?'next-three':'next-two'};return raw;});
 await s.loadLibraries();await s.nextLibraries();await s.selectLibrary('one');const before=s.getSnapshot(),op=before.directory;paths.length=0;fresh=true;
 assert.deepEqual(await s.refreshDirectory(),{kind:'adopted'});const after=s.getSnapshot();
 assert.deepEqual(paths,['/v1/admin/libraries?limit=40&cursor=next-two']);assert.equal(after.directory.cursor,op.cursor);assert.equal(after.directory.canPrevious,true);
 assert.equal(after.directory.data!.items[0].name,'Fresh');assert.equal(after.directory.data!.nextCursor,'next-three');
 assert.equal(after.selected,before.selected);assert.equal(after.jobs,before.jobs);assert.equal(after.mutation,before.mutation);assert.equal(after.selectedLibraryId,before.selectedLibraryId);
 await s.previousLibraries();assert.equal(s.getSnapshot().directory.cursor,null);s.dispose();
});

test('quiet directory is independent of an active command and never cancels it',async()=>{
 const write=deferred();let writeSignal:AbortSignal|undefined;
 const s=service(async(p,m,_b,signal)=>{if(m==='POST'){writeSignal=signal;return write.promise;}return data(p);});
 await s.loadLibraries();await s.selectLibrary('one');const pending=s.scan(),before=s.getSnapshot();
 assert.deepEqual(await s.refreshDirectory(),{kind:'adopted'});assert.equal(s.getSnapshot().mutation,before.mutation);assert.equal(s.getSnapshot().selected,before.selected);assert.equal(writeSignal!.aborted,false);
 write.resolve({id:'job-new',processed:0});await pending;s.dispose();
});

test('quiet directory failure retains original error and last window; newer paging and scope fence late reads',async()=>{
 const error=Object.assign(new Error('cursor expired'),{code:'stale_continuation'});let mode='good',pending=deferred();
 const s=service(async p=>{if(mode==='fail')throw error;if(mode==='wait')return pending.promise;return {...data(p),nextCursor:p.includes('cursor=')?'':'next'};});
 await s.loadLibraries();mode='fail';const before=s.getSnapshot(),answer=await s.refreshDirectory();assert.equal(answer.kind,'unreadable');if(answer.kind==='unreadable')assert.equal(answer.error,error);assert.equal(s.getSnapshot(),before);
 mode='wait';const old=s.refreshDirectory();mode='good';await s.nextLibraries();const page=s.getSnapshot();assert.deepEqual(await old,{kind:'deferred'});pending.resolve(data('/v1/admin/libraries'));assert.equal(s.getSnapshot(),page);
 pending=deferred();mode='wait';const stale=s.refreshDirectory();s.setScope({...scope,viewerId:'new'},{request:async<T>()=>data('/v1/admin/libraries') as T});assert.deepEqual(await stale,{kind:'deferred'});assert.equal(s.getSnapshot().directory.data,null);pending.resolve(data('/v1/admin/libraries'));s.dispose();
});

test('quiet unloaded directory parses the first page and malformed replies never claim adoption',async()=>{
 let malformed=true;const s=service(async p=>malformed?{revision:'wrong'}:data(p));const before=s.getSnapshot();assert.equal((await s.refreshDirectory()).kind,'unreadable');assert.equal(s.getSnapshot(),before);
 malformed=false;assert.deepEqual(await s.refreshDirectory(),{kind:'adopted'});assert.equal(s.getSnapshot().directory.data!.items[0].id,'one');s.dispose();
});

/** A revision-bound keyset endpoint: every cursor carries the admin revision and last row id. */
function revisionDirectory(count=8,pageSize=2){
 let revision=1;
 const items=Array.from({length:count},(_,index)=>({...library('lib-'+index,'Library '+index),position:index}));
 const paths:string[]=[],stale=Object.assign(new Error('original stale revision'),{code:'stale_continuation',status:409,retry:'after_refresh'});
 let onPage:((path:string,raw:unknown,signal?:AbortSignal)=>Promise<unknown>)|undefined;
 const api={request:async<T>(path:string,method='GET',body?:unknown,signal?:AbortSignal):Promise<T>=>{
  const u=new URL(path,'https://server.test');
  if(method==='PATCH'){
   const row=items.find(item=>item.id===u.pathname.split('/').at(-1))!;
   row.name=(body as {name:string}).name;row.revision++;revision++;return {scope:wireScope,library:row} as T;
  }
  if(u.pathname!=='/v1/admin/libraries')return data(path) as T;
  paths.push(path);const cursor=u.searchParams.get('cursor');let start=0;
  if(cursor){const [rev,after]=cursor.split(':');if(Number(rev)!==revision)throw stale;start=items.findIndex(row=>row.id===after)+1;if(!start)throw new Error('invalid fake keyset');}
  const limit=Number(u.searchParams.get('limit')),page=items.slice(start,start+limit);
  const raw={scope:wireScope,revision,items:page.map(row=>({...row})),nextCursor:start+limit<items.length?revision+':'+page.at(-1)!.id:''};
  return (onPage?await onPage(path,raw,signal):raw) as T;
 }};
 return {s:new LibraryManagementService({scope,pageSize,readRetries:0,api}),api,paths,stale,items,bump:()=>{revision++;},gate:(fn?:typeof onPage)=>{onPage=fn;}};
}

test('revision-bound page2 rename quietly reanchors the current window with fresh next and Back cursors, publishing once',async()=>{
 const {s,api,paths}=revisionDirectory();await s.loadLibraries();await s.nextLibraries();await s.selectLibrary('lib-2');
 const before=s.getSnapshot(),seen:unknown[]=[];s.subscribe(()=>seen.push(s.getSnapshot()));
 await api.request('/v1/admin/libraries/lib-2','PATCH',{name:'Renamed'});paths.length=0;
 assert.deepEqual(await s.refreshDirectory(),{kind:'adopted'});const after=s.getSnapshot();
 assert.deepEqual(paths,['/v1/admin/libraries?limit=2&cursor=1%3Alib-1','/v1/admin/libraries?limit=2','/v1/admin/libraries?limit=2&cursor=2%3Alib-1']);
 assert.equal(seen.length,1);assert.deepEqual(after.directory.data!.items.map(row=>row.id),['lib-2','lib-3']);assert.equal(after.directory.data!.items[0].name,'Renamed');
 assert.equal(after.directory.cursor,'2:lib-1');assert.equal(after.directory.data!.nextCursor,'2:lib-3');assert.equal(after.directory.canPrevious,true);
 assert.equal(after.selected,before.selected);assert.equal(after.jobs,before.jobs);assert.equal(after.mutation,before.mutation);assert.equal(after.selectedLibraryId,before.selectedLibraryId);
 await s.nextLibraries();assert.deepEqual(s.getSnapshot().directory.data!.items.map(row=>row.id),['lib-4','lib-5']);
 await s.previousLibraries();assert.equal(s.getSnapshot().directory.cursor,'2:lib-1');await s.previousLibraries();assert.equal(s.getSnapshot().directory.cursor,null);s.dispose();
});

test('scope replacement during revision reanchor aborts the walk and cannot call the new API or publish an old page',async()=>{
 const server=revisionDirectory(),{s,paths}=server;await s.loadLibraries();await s.nextLibraries();server.bump();
 const waiting=deferred(),entered=deferred();let signal:AbortSignal|undefined;
 server.gate(async(path,raw,readSignal)=>{if(!path.includes('cursor=')){signal=readSignal;entered.resolve(undefined);await waiting.promise;}return raw;});
 paths.length=0;const refresh=s.refreshDirectory();await entered.promise;
 let replacementCalls=0;s.setScope({...scope,viewerId:'replacement'},{request:async<T>()=>{replacementCalls++;return data('/v1/admin/libraries') as T;}});
 const before=s.getSnapshot();assert.deepEqual(await refresh,{kind:'deferred'});assert.equal(signal!.aborted,true);waiting.resolve(undefined);await settleRetries();
 assert.equal(s.getSnapshot(),before);assert.equal(replacementCalls,0);assert.equal(paths.length,2);s.dispose();
});

test('new ordinary paging wins during the private reanchor walk with no intermediate quiet publication',async()=>{
 const server=revisionDirectory(),{s,paths}=server;await s.loadLibraries();await s.nextLibraries();server.bump();
 const waiting=deferred(),entered=deferred();let gated=false;
 server.gate(async(path,raw)=>{if(!gated&&!path.includes('cursor=')){gated=true;entered.resolve(undefined);await waiting.promise;}return raw;});
 const refresh=s.refreshDirectory();await entered.promise;const before=s.getSnapshot();
 // A newer explicit reload and navigation owns the directory before the private walk can finish.
 await s.loadLibraries();await s.nextLibraries();await s.nextLibraries();const newer=s.getSnapshot();assert.equal(newer.directory.phase,'ready');assert.equal(newer.directory.cursor,'2:lib-3');assert.notEqual(newer,before);
 assert.deepEqual(await refresh,{kind:'deferred'});const count=paths.length;waiting.resolve(undefined);await settleRetries();assert.equal(s.getSnapshot(),newer);assert.equal(paths.length,count);s.dispose();
});

test('private reanchor permits an independent pending command without replacing its receipt or cancelling the write',async()=>{
 const server=revisionDirectory(),{s}=server;await s.loadLibraries();await s.nextLibraries();await s.selectLibrary('one');
 const write=deferred();let writeSignal:AbortSignal|undefined;
 // Scope-preserving transport wrapper holds a scan, while the directory retains the same viewer fence.
 const original=server.api.request;server.api.request=async<T>(path,method='GET',body,signal)=>{if(method==='POST'){writeSignal=signal;return write.promise as Promise<T>;}return original<T>(path,method,body,signal);};
 const pending=s.scan(),receipt=s.getSnapshot().mutation;server.bump();assert.deepEqual(await s.refreshDirectory(),{kind:'adopted'});
 assert.equal(s.getSnapshot().mutation,receipt);assert.equal(writeSignal!.aborted,false);write.resolve({id:'scan-new',processed:0});await pending;s.dispose();
});

test('reanchor is capped at64 fresh pages; truncated Back history cannot masquerade as a reachable logical window',async()=>{
 const server=revisionDirectory(140,1),{s,paths,stale}=server;await s.loadLibraries();for(let page=1;page<=65;page++)await s.nextLibraries();
 const before=s.getSnapshot();assert.equal(before.directory.cursor,'1:lib-64');server.bump();paths.length=0;let publications=0;s.subscribe(()=>{publications++;});
 const answer=await s.refreshDirectory();assert.equal(answer.kind,'unreadable');if(answer.kind==='unreadable')assert.equal(answer.error,stale);
 assert.equal(paths.length,65); // One rejected old cursor plus at most64 newly read pages.
 assert.equal(paths.at(-1),'/v1/admin/libraries?limit=1&cursor=2%3Alib-62');assert.equal(publications,0);assert.equal(s.getSnapshot(),before);s.dispose();
});

test('a removed logical page keeps the original window and stale error instead of resetting to the first or last page',async()=>{
 const server=revisionDirectory(6),{s,items,stale}=server;await s.loadLibraries();await s.nextLibraries();await s.nextLibraries();const before=s.getSnapshot();
 items.splice(2);server.bump();const answer=await s.refreshDirectory();assert.equal(answer.kind,'unreadable');if(answer.kind==='unreadable')assert.equal(answer.error,stale);assert.equal(s.getSnapshot(),before);s.dispose();
});

test('revision movement during reanchor is not silently mixed or restarted and changed viewer proof still retires',async()=>{
 for(const changed of ['revision','viewer'] as const){
  const server=revisionDirectory(),{s}=server;await s.loadLibraries();await s.nextLibraries();const before=s.getSnapshot();server.bump();
  server.gate(async(path,raw)=>{if(!path.includes('cursor=')){if(changed==='revision')server.bump();else (raw as any).scope={...wireScope,viewerFence:'another-owner'};}return raw;});
  const answer=await s.refreshDirectory();assert.equal(answer.kind,'unreadable');if(changed==='revision')assert.equal(s.getSnapshot(),before);else assert.equal(s.getSnapshot().directory.data,null);s.dispose();
 }
});

test('the64th fresh page is reachable and its entire Back chain uses the new revision',async()=>{
 const server=revisionDirectory(70,1),{s,paths}=server;await s.loadLibraries();for(let page=1;page<64;page++)await s.nextLibraries();server.bump();paths.length=0;
 assert.deepEqual(await s.refreshDirectory(),{kind:'adopted'});assert.equal(paths.length,65);assert.equal(s.getSnapshot().directory.cursor,'2:lib-62');
 for(let page=1;page<64;page++)await s.previousLibraries();assert.equal(s.getSnapshot().directory.cursor,null);assert.equal(s.getSnapshot().directory.canPrevious,false);s.dispose();
});


test('quiet directory cancellation is bound and preserves visible page, selected, jobs and mutation receipts',async()=>{
 let hold=false,signal:AbortSignal|undefined;const pending=deferred();
 const s=service(async(p,_m,_b,a)=>{if(hold&&p.startsWith('/v1/admin/libraries?')){signal=a;return pending.promise;}return data(p);});
 await s.loadLibraries();await s.selectLibrary('one');const before=s.getSnapshot();hold=true;
 const read=s.refreshDirectory();const cancel=s.invalidateQuietDirectory;cancel();
 assert.equal(signal?.aborted,true);assert.equal(s.getSnapshot(),before);
 assert.deepEqual(await read,{kind:'deferred'});pending.resolve({...data('/v1/admin/libraries'),items:[library('one','Late')]});await settleRetries();assert.equal(s.getSnapshot(),before);s.dispose();
});

test('quiet directory cancellation cannot abort newer ordinary paging or its selected/jobs/write owners',async()=>{
 let hold=false;const held:ReturnType<typeof deferred>[]=[],signals:AbortSignal[]=[];
 const s=service(async(p,m,_b,a)=>{if(hold&&(p.startsWith('/v1/admin/libraries?')||m==='PATCH')){const read=deferred();held.push(read);signals.push(a!);return read.promise;}return data(p);});
 await s.loadLibraries();await s.selectLibrary('one');hold=true;const quiet=s.refreshDirectory();const ordinary=s.loadLibraries();
 const selected=s.getSnapshot().selected,jobs=s.getSnapshot().jobs;
 s.invalidateQuietDirectory();assert.equal(signals[0].aborted,true);assert.equal(signals[1].aborted,false);
 held[1].resolve(data('/v1/admin/libraries'));await ordinary;assert.deepEqual(await quiet,{kind:'deferred'});assert.equal(s.getSnapshot().selected,selected);assert.equal(s.getSnapshot().jobs,jobs);
 const write=s.rename('Updated',1);assert.equal(signals[2].aborted,false);s.invalidateQuietDirectory();assert.equal(signals[2].aborted,false);
 hold=false;held[2].resolve({scope:wireScope,library:library('one','Updated',2)});await write;assert.equal(s.getSnapshot().mutation.phase,'complete');s.dispose();
});

test('an older quiet completion cannot detach the newer quiet read cancellation owner',async()=>{
 let hold=false;const held:ReturnType<typeof deferred>[]=[],signals:AbortSignal[]=[];
 const s=service(async(p,_m,_b,a)=>{if(hold&&p.startsWith('/v1/admin/libraries?')){const read=deferred();held.push(read);signals.push(a!);return read.promise;}return data(p);});
 await s.loadLibraries();const before=s.getSnapshot();hold=true;const first=s.refreshDirectory(),second=s.refreshDirectory();assert.equal(signals[0].aborted,true);assert.deepEqual(await first,{kind:'deferred'});s.invalidateQuietDirectory();assert.equal(signals[1].aborted,true);assert.deepEqual(await second,{kind:'deferred'});assert.equal(s.getSnapshot(),before);s.dispose();
});
