import {decode} from '../../contracts/src/runtime.ts';
import {wireFixture} from './wire-fixture.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import {parseFeedbackReport} from '../src/notifications.ts';
import {ConsoleClient,parseSettings,parsePage,parseEvidenceRecord as read_parseEvidenceRecord,type ConsoleAPI,type RuntimeSettings,type Schedule} from '../src/console.ts';
import {consoleUTF8Size,readConsoleJson,ConsoleTransportError} from '../src/console-transport.ts';
const parseEvidenceRecord=(value:unknown,...args:any[]) => (read_parseEvidenceRecord as any)(wireFixture(value,{},['createdAt', 'updatedAt', 'expiresAt', 'readAt', 'archivedAt', 'observedAt', 'freshUntil', 'at', 'nextAt', 'from', 'to']),...args);

const fence='a'.repeat(64);
const envelope=(data:unknown,serverId='server-one',viewerFence=fence)=>({scope:{serverId,viewerFence},data});
const values:RuntimeSettings={'integrations.compatibilityEnabled':false,name:'My server',conversionMode:'all',outputFormat:'auto',downloadMaxQuality:'original',musicAwayQuality:'original',ownAddressEnabled:false,perAccountCap:null,serverCap:null,diagnosticDays:30,notificationDays:180,jobDays:30,
 hardwareBackend:'auto',hardwareDevice:'',hardwareSelection:'auto',hardwareDecode:true,hdrToneMapping:false,hdrToneMappingAlgorithm:'hable',x264Preset:'veryfast',directStreamRemux:true,
 analysisStorageGiB:32,analysisStageStorageMiB:256,subtitleTrackLimit:128,subtitleFileLimitMiB:128,planningPolicy:'maximum_fidelity',throttleBufferMs:60000,playedRetentionMs:180000,temporaryDirectory:'',ffmpegPath:'',sessionStorageMiB:4096,memoryCap:'quarter',transcodeLocation:'memory',
 maxConcurrentSessions:0,maxHardwareSessions:0,maxSoftwareSessions:0,maxBackgroundSessions:0,
 playHistoryDays:0,'home.communityActivityEnabled':false,'updates.releaseChannel':'stable','updates.feedUrl':'','library.missingTitles':'after_days','library.missingTitlesDays':30,'library.segmentDetection':'standard',
 remoteSignInPolicy:'allow',remoteBitrateLimitKbps:0,lanNetworks:[],accessUrls:[],lanDiscoveryEnabled:true,dlnaEnabled:false,
 trustedProxies:[],uploadCapacityKbps:0,pausedSessionTimeoutMinutes:0,advertisedInterface:'',customCertificatePath:'',customCertificateKeyPath:'',customCertificateDomain:'',secureConnections:'preferred',connectionLimit:0,advertiseUrl:'',apiRequestsPerMinute:0,logLevel:'info',logRetention:[]};
const settings=(revision=1)=>({revision,digest:'b'.repeat(64),requested:values,effective:values,activeRevision:revision,restartFields:[],registryRevision:'p08.0'});
// One idempotent console command stands in for every mutation the client replays.
const queued={id:'job-one',kind:'library-scan',resource:'library',trigger:'owner',state:'queued',phase:'',revision:1,attempt:1,createdAt:1,updatedAt:2,nextAt:3,settingsRevision:1,lane:'background-media',actions:['cancel']};
const run=(c:ConsoleClient,resource='library')=>c.runJob('library-scan',resource);
function client(handler:(path:string,method:string,body:any,signal:AbortSignal)=>Promise<unknown>){
 const api:ConsoleAPI={async request<T>(){throw new Error('unbounded fallback must not be used');},async requestConsole<T>(path,method,body,signal){if(path==='/v1/admin/console/settings'&&method==='PATCH')decode('ChangeConsoleSettingsRequest',body);return wireFixture(await handler(path,method,body,signal),{},['createdAt', 'updatedAt', 'expiresAt', 'readAt', 'archivedAt', 'observedAt', 'freshUntil', 'at', 'nextAt', 'from', 'to']) as T;}};
 return new ConsoleClient(api,{serverId:'server-one',viewerId:'local:one:profile'});
}
test('settings preserve explicit unlimited caps and reject invalid limits',()=>{
 assert.equal(parseSettings(settings()).effective.serverCap,null);
 assert.throws(()=>parseSettings({...settings(),effective:{...values,perAccountCap:0}}));
 assert.throws(()=>parseSettings({...settings(),effective:{...values,diagnosticDays:31}}));
 // The retired per-account overrides are never sent: neither the values the
 // client saves nor the parsed documents carry an accountCaps field, so a
 // save cannot carry it back to a server that rejects it.
 assert.equal('accountCaps' in values,false);
 assert.equal('accountCaps' in parseSettings(settings()).requested,false);
 assert.equal('accountCaps' in parseSettings(settings()).effective,false);
});
test('transcoding settings are parsed and values this server would refuse are rejected',()=>{
 const parsed=parseSettings(settings()).effective;
 assert.equal(parsed.hardwareBackend,'auto');
 assert.equal(parsed.x264Preset,'veryfast');
 assert.equal(parsed.maxConcurrentSessions,0);
 for(const bad of [{hardwareBackend:'magic'},{hdrToneMappingAlgorithm:'filmic'},{x264Preset:'placebo'},{planningPolicy:'fastest'},
  {throttleBufferMs:2000},{playedRetentionMs:99999000},{maxHardwareSessions:5000},{hdrToneMapping:'yes'},{temporaryDirectory:42}])
  assert.throws(()=>parseSettings({...settings(),effective:{...values,...bad}}),/Invalid/);
 // A document missing the transcoding half is not a settings document.
 const {hardwareBackend:_omitted,...incomplete}=values as Record<string,unknown>;
 assert.throws(()=>parseSettings({...settings(),effective:incomplete}));
});
test('lost responses reuse the identical mutation key and payload',async()=>{
 const calls:any[]=[];const c=client(async(_p,_m,b)=>{calls.push(b);if(calls.length===1)throw new Error('response lost');return envelope(queued);});
 await assert.rejects(run(c));assert.equal(c.hasUnconfirmed(),true);
 await run(c);assert.deepEqual(calls[0],calls[1]);assert.equal(c.hasUnconfirmed(),false);c.dispose();
});
test('unknown mutations cannot silently become a changed intent; explicit recovery works',async()=>{
 let fail=true;const bodies:any[]=[];const c=client(async(_p,_m,b)=>{bodies.push(b);if(fail)throw new Error('lost');return envelope(queued);});
 c.setDraft('unrelated',{keep:true});let recovered=0;c.onReconciled(()=>recovered++);
 await assert.rejects(run(c));await assert.rejects(run(c,'other'),/unconfirmed/);assert.equal(bodies.length,1);
 fail=false;await c.reconcilePending();assert.deepEqual(bodies[0],bodies[1]);assert.equal(recovered,1);assert.deepEqual(c.getDraft('unrelated'),{keep:true});c.dispose();
});
test('known revision conflicts keep drafts but permit a deliberate revised submission',async()=>{
 let conflict=true,current=1;const c=client(async(_path,method)=>{if(method==='GET')return envelope(settings(current));if(conflict)throw new ConsoleTransportError(409,'console_conflict',4,['values.serverCap']);return envelope(settings(5));});
 await c.settings();current=4;
 c.setDraft('runtime-settings',{revision:1,values});await assert.rejects(c.applySettings(1,values),/revision: 4/);assert.equal(c.hasUnconfirmed(),false);assert.ok(c.getDraft('runtime-settings'));
 conflict=false;assert.equal((await c.applySettings(4,values)).revision,5);c.dispose();
});
test('a covered edit keeps its original revision until the owner explicitly reviews current settings',async()=>{
 const sent:any[]=[];let current=4;
 const c=client(async(_path:string,method?:string,body?:any)=>{if(method==='GET')return envelope(settings(current));sent.push(body);if(body.expectedRevision!==current)throw new ConsoleTransportError(409,'console_conflict',current,[]);return envelope(settings(current+1));});
 current=1;await c.settings();current=4;
 await assert.rejects(c.applySettings(1,{...values,name:'Renamed'}),/revision: 4/);
 assert.equal(sent.length,1,'no automatic revision rewrite');
 await c.settings();assert.equal((await c.applySettings(4,{...values,name:'Renamed'})).revision,5);
 assert.deepEqual(sent.map(b=>b.expectedRevision),[1,4]);
 assert.deepEqual(sent[1].values,sent[0].values,'the same change, against the newer document');
 assert.equal(c.hasUnconfirmed(),false);c.dispose();
});
test('server and viewer fences revoke the entire selected scope',async()=>{
 let foreign=false;const c=client(async()=>envelope(settings(),foreign?'server-two':'server-one'));
 await c.settings();c.setDraft('private','do not retain');let denied=0;c.onDenied(()=>denied++);foreign=true;
 await assert.rejects(c.settings(),/selected viewer changed/);assert.equal(c.hasDrafts(),false);assert.equal(denied,1);await assert.rejects(c.settings(),/closed/);c.dispose();
});
test('authority failures clear drafts and late responses after disposal never publish',async()=>{
 const c=client(async()=>{throw new ConsoleTransportError(401,'unauthorized');});c.setDraft('private',true);await assert.rejects(c.settings());assert.equal(c.hasDrafts(),false);c.dispose();
 let resolve!:(v:unknown)=>void;const d=client(async()=>new Promise(r=>resolve=r));const request=d.settings();d.dispose();resolve(envelope(settings()));await assert.rejects(request,/ended/);
});
test('viewer-fence rotation is detected independently of server identity',async()=>{
 let changed=false;const c=client(async()=>envelope(settings(),'server-one',changed?'b'.repeat(64):fence));await c.settings();changed=true;await assert.rejects(c.settings(),/selected viewer changed/);c.dispose();
});
test('schedule writes exclude server-owned revision and admission history',async()=>{
 let sent:any;const schedule:Schedule={id:'schedule-one',revision:7,kind:'retention-cleanup',resource:'',enabled:true,timezone:'America/Toronto',startMinute:60,windowMinutes:90,catchUp:true,lastSlot:'2026-09-06',createdAt:20};
 const c=client(async(path,method,body)=>{assert.equal(path,'/v1/admin/console/schedules');assert.equal(method,'PUT');sent=body;return envelope(schedule);});
 await c.saveSchedule(schedule,7);assert.equal(sent.expectedRevision,7);for(const key of ['revision','createdAt','lastSlot'])assert.equal(key in sent.value,false);assert.ok(sent.idempotencyKey);c.dispose();
});
test('receipt-bearing alerts and capture stay on selected-server routes',async()=>{
 const paths:string[]=[];const c=client(async(path,_method,body)=>{paths.push(path);assert.ok(body.idempotencyKey);return envelope({accepted:true});});
 await c.capture('scheduler',500);await c.acknowledge({id:'alert-one',revision:1,code:'job-failed',severity:'warning',status:'open',firstAt:1,lastAt:2,occurrences:1});assert.ok(paths.every(p=>p.startsWith('/v1/admin/console/')));c.dispose();
});
test('UTF-8 bounds do not require native TextEncoder',()=>{for(const value of ['plain','caf\u00e9','\ud83c\udf7f','\ud800','\ud800X','\u4e2d\u6587'])assert.equal(consoleUTF8Size(value),Buffer.byteLength(value));});
function nativeResponse(value:string,status=200,length:string|null=String(Buffer.byteLength(value))):Response{
 const headers=new Headers({'Content-Type':'application/json'});if(length!==null)headers.set('Content-Length',length);
 return {ok:status>=200&&status<300,status,headers,body:null,text:async()=>value} as Response;
}
test('bounded native fetch accepts text without browser stream primitives',async()=>{assert.deepEqual(await readConsoleJson(nativeResponse('{"value":true}'),new AbortController().signal),{value:true});});
test('native fetch accepts a buffered body without a declared length but rejects deceptive and excessive bounds',async()=>{
 // A compressing proxy (chunked gzip) sends no Content-Length; native fetch has already buffered the body.
 const signal=new AbortController().signal;assert.deepEqual(await readConsoleJson(nativeResponse('{"ok":1}',200,null),signal),{ok:1});
 await assert.rejects(readConsoleJson(nativeResponse(' '.repeat(1048577),200,null),signal));
 await assert.rejects(readConsoleJson(nativeResponse('{}',200,'1048577'),signal));await assert.rejects(readConsoleJson(nativeResponse(' '.repeat(1048577),200,'2'),signal));
});
test('browser streaming enforces its real byte bound and abort state',async()=>{
 const signal=new AbortController().signal;assert.deepEqual(await readConsoleJson(new Response('{"ok":1}',{headers:{'Content-Type':'application/json'}}),signal),{ok:1});
 await assert.rejects(readConsoleJson(new Response(' '.repeat(1048577),{headers:{'Content-Type':'application/json'}}),signal));
 const aborted=new AbortController();aborted.abort();await assert.rejects(readConsoleJson(nativeResponse('{}'),aborted.signal));
});
test('server error text is never rendered, but validated field and revision hints survive',async()=>{
 try{await readConsoleJson(nativeResponse(JSON.stringify({error:{code:'console_conflict',message:'secret /private/token',currentRevision:5,fields:['values.name']}}),409),new AbortController().signal);assert.fail('must reject');}
 catch(e){assert.ok(e instanceof ConsoleTransportError);assert.equal(e.currentRevision,5);assert.deepEqual(e.fields,['values.name']);assert.ok(!e.message.includes('/private'));}
});
test('invalid media type, JSON and injected field names fail closed',async()=>{
 const signal=new AbortController().signal;await assert.rejects(readConsoleJson(new Response('<html/>',{headers:{'Content-Type':'text/html'}}),signal));await assert.rejects(readConsoleJson(nativeResponse('not JSON'),signal));
 try{await readConsoleJson(nativeResponse(JSON.stringify({error:{code:'invalid_console_request',fields:['secret/token\n']}}),400),signal);}catch(e){assert.ok(e instanceof ConsoleTransportError);assert.equal(e.fields,undefined);}
});
test('paged projections reject unbounded and malformed cursors',()=>{assert.throws(()=>parsePage({items:Array.from({length:41},()=>1),nextCursor:''},v=>v));assert.throws(()=>parsePage({items:[],nextCursor:'../secret'},v=>v));});

test('recovered support exports remain available for a manual preview without creating another export',async()=>{
 let calls=0;const value={id:'export-one',expiresAt:Date.now()+3600000,manifest:{version:'1',from:1,to:2,included:['snapshot'],excluded:['raw logs'],redaction:'typed projection',maxBytes:1048576}};
 const c=client(async()=>{if(++calls===1)throw new Error('lost export response');return envelope(value);});
 await assert.rejects(c.createExport(1,2,['snapshot']));await c.reconcilePending();assert.equal(calls,2);
 assert.equal(c.takeRecoveredExport()?.id,value.id);assert.equal(c.takeRecoveredExport(),undefined);c.dispose();
});

test('multi-source job pause and resume preserve selected-server revision fencing',async()=>{
 const calls:{path:string;body:any}[]=[];
 const job={id:'operation-one',kind:'library-scan',resource:'library',trigger:'owner',state:'running' as const,phase:'all sources',revision:2,attempt:1,createdAt:1,updatedAt:2,nextAt:3,domainId:'operation-one',settingsRevision:1,lane:'background-media',actions:['pause','cancel'] as ('pause'|'cancel')[]};
 const c=client(async(path,_method,body)=>{calls.push({path,body});const paused=path.endsWith('/pause');return envelope({...job,revision:paused?3:4,state:paused?'paused':'running',actions:paused?['resume','cancel']:['pause','cancel']});});
 const paused=await c.jobCommand(job,'pause');assert.equal(paused.state,'paused');assert.deepEqual(paused.actions,['resume','cancel']);
 const resumed=await c.jobCommand(paused,'resume');assert.equal(resumed.state,'running');
 assert.equal(calls[0].path,'/v1/admin/console/jobs/operation-one/pause');assert.equal(calls[0].body.expectedRevision,2);
 assert.equal(calls[1].path,'/v1/admin/console/jobs/operation-one/resume');assert.equal(calls[1].body.expectedRevision,3);
 assert.notEqual(calls[0].body.idempotencyKey,calls[1].body.idempotencyKey);c.dispose();
});

test('diagnostic records reject invalid dates and unsafe field values before rendering',()=>{
 const value={sequence:1,at:Date.now(),severity:'info',component:'scheduler',code:'job_completed',fields:{count:2,durationMs:50}};
 assert.deepEqual(parseEvidenceRecord(value),value);
 for(const change of [{at:8640000000000001},{severity:{}},{fields:{count:null}},{fields:{count:Infinity}},{fields:[]}])assert.throws(()=>parseEvidenceRecord({...value,...change}));
});

test('SIM-08: a feedback report names the app it came from, and an unknown online state stays unknown',()=>{
 const base={id:'report-one',cursor:'1',revision:1,status:'open',kind:'playback',category:'buffering',message:'Playback stopped.',createdAt:'2026-10-22T18:20:00Z',updatedAt:'2026-10-22T18:20:00Z',
  expiresAt:'2027-04-20T18:20:00Z',reporter:{name:'Alex',authority:'local',role:'member',self:false},diagnostics:{decision:'not-requested'},duplicates:0,thread:[]};
 assert.deepEqual(parseFeedbackReport({...base,client:{platform:'android',appVersion:'1.4.2',online:false}}).client,{platform:'android',appVersion:'1.4.2',online:false});
 assert.equal(parseFeedbackReport({...base,client:{platform:'web',appVersion:'2026.10'}}).client?.online,undefined);
 assert.equal(parseFeedbackReport(base).client,undefined);
 for(const client of [{platform:'',appVersion:'1'},{platform:'web',appVersion:''},{platform:'web',appVersion:'1',online:'yes'},{platform:'x'.repeat(41),appVersion:'1'},'web'])
  assert.throws(()=>parseFeedbackReport({...base,client}),JSON.stringify(client));
});
test('a console list keeps its other rows when one row cannot be read, and keeps the first of a repeated id',async()=>{
 const job={id:'job-one',kind:'library-scan',resource:'library',trigger:'owner',state:'running',phase:'',revision:2,attempt:1,createdAt:1,updatedAt:2,nextAt:3,settingsRevision:1,lane:'background-media',actions:['pause','cancel']};
 const alert={id:'alert-one',code:'state-permissions',severity:'warning',status:'open',firstAt:1,lastAt:2,occurrences:1,revision:1};
 const c=client(async path=>envelope(
  path.includes('/jobs')?{items:[job,{...job,id:'job-two',state:'hibernating'},{...job,id:'job-three',actions:['pause','teleport'],futureField:1},{...job,phase:'again'},{id:'job-four'}],nextCursor:'',futureField:1}:
  path.includes('/alerts')?[alert,{...alert,id:'alert-two',revision:0},{...alert,severity:'again'},'nonsense']:
  path.includes('/stream-options')?{libraries:[{id:'films',label:'Films'},{id:'',label:'Nameless'},{id:'films',label:'Again'}],viewers:[{id:'v',label:'Viewer'},null]}:
  path.includes('/panels/')?{name:'host',observedAt:1000,freshUntil:31000,facts:{cpu:{state:'measured',value:3},gpu:'unknown',disk:{state:7}}}:
  {items:[{id:'n1',createdAt:1,readAt:null,code:'c',message:'Hello'},{id:'n2',createdAt:1,readAt:'never',code:'c',message:'Broken'}],nextCursor:'',unread:2}));
 const jobs=await c.jobs();assert.deepEqual(jobs.items.map(row=>[row.id,row.phase,row.actions]),[['job-one','',['pause','cancel']],['job-three','',['pause']]]);
 assert.deepEqual((await c.alerts()).map(row=>[row.id,row.severity]),[['alert-one','warning']]);
 assert.deepEqual(await c.streamOptions(),{libraries:[{id:'films',label:'Films'}],viewers:[{id:'v',label:'Viewer'}]});
 assert.deepEqual(Object.keys((await c.measurement('host')).facts),['cpu']);
 const inbox=await c.inbox();assert.deepEqual(inbox.items.map(row=>row.id),['n1']);assert.equal(inbox.unread,2);c.dispose();
});
test('list sizes, cursors, revisions and freshness windows still refuse the whole console answer',async()=>{
 const job={id:'job-one',kind:'library-scan',state:'running',revision:2,actions:[]};
 for(const data of [{items:new Array(41).fill(job),nextCursor:''},{items:[job],nextCursor:'../secret'},{items:{},nextCursor:''}]){const c=client(async()=>envelope(data));await assert.rejects(()=>c.jobs());c.dispose();}
 const alerts=client(async()=>envelope(new Array(101).fill({id:'a',code:'c',revision:1})));await assert.rejects(()=>alerts.alerts());alerts.dispose();
 const panel=client(async()=>envelope({name:'host',observedAt:1000,freshUntil:90000,facts:{}}));await assert.rejects(()=>panel.measurement('host'));panel.dispose();
 const single=client(async()=>envelope({...job,state:'hibernating'}));await assert.rejects(()=>single.runJob('library-scan','library'));single.dispose();
});

test('typed owner settings errors retain revision and bounded field paths',async()=>{
 const response=new Response(JSON.stringify({error:{code:'revision_mismatch',current:{revision:9},fields:[{path:'values.sessionStorageMiB',code:'invalid_value',message:'untrusted text'}]}}),{status:409,headers:{'Content-Type':'application/json'}});
 await assert.rejects(readConsoleJson(response,new AbortController().signal),e=>{
  assert.ok(e instanceof ConsoleTransportError);assert.equal(e.currentRevision,9);assert.deepEqual(e.fields,['values.sessionStorageMiB']);assert.doesNotMatch(e.message,/untrusted/);return true;
 });
});

test('complete owner settings retain missing-title and API policies after the merge',()=>{
 const read=parseSettings(settings()).requested;
 assert.equal(read['library.missingTitles'],'after_days');assert.equal(read['library.missingTitlesDays'],30);
 assert.equal(read.connectionLimit,0);assert.equal(read.apiRequestsPerMinute,0);assert.equal(read.conversionMode,'all');
 assert.equal(read.dlnaEnabled,false);
 const legacy=parseSettings({...settings(),requested:{...values,blockMemberAccessTokens:true,deviceApprovalRequired:true,transcodingEnabled:false}}).requested;
 assert.equal(legacy.conversionMode,'all');for(const field of ['blockMemberAccessTokens','deviceApprovalRequired','transcodingEnabled'])assert.equal(field in legacy,false);
 const {dlnaEnabled:_,...older}=values;assert.equal(parseSettings({...settings(),requested:older,effective:older}).requested.dlnaEnabled,false);
 for(const retired of ['library.trashRetentionDays','secureConnectionsPolicy','treatWanAsLan','blockMemberAccessTokens','deviceApprovalRequired','transcodingEnabled'])assert.equal(retired in read,false);
 for(const bad of [{'library.missingTitles':'trash'},{'library.missingTitlesDays':0},{connectionLimit:65537},{apiRequestsPerMinute:100001},{conversionMode:'automatic'},{outputFormat:'vp9'},{downloadMaxQuality:'automatic'},{musicAwayQuality:'256'},{dlnaEnabled:'yes'}])assert.throws(()=>parseSettings({...settings(),requested:{...values,...bad}}));
});

test('a runtime conflict retains the original body while a reviewed save preserves concurrent owner changes',async()=>{
 let stored={...values},rev=1;const bodies:any[]=[];
 const c=client(async(_path,method,body)=>{
  if(method==='GET')return envelope({...settings(rev),requested:stored,effective:stored});
  bodies.push(body);if(body.expectedRevision!==rev)throw new ConsoleTransportError(409,'console_conflict',rev,[]);
  stored={...stored,...body.values};rev++;return envelope({...settings(rev),requested:stored,effective:stored});
 });
 const initial=await c.settings();stored={...stored,connectionLimit:123};rev=2;
 try {await assert.rejects(c.applySettings(initial.revision,{...initial.requested,name:'Renamed'}));assert.equal(bodies.length,1);const reviewed=await c.settings();await c.applySettings(reviewed.revision,{...reviewed.requested,name:'Renamed'});
  assert.equal(stored.name,'Renamed');assert.equal(stored.connectionLimit,123);
  assert.deepEqual(Object.keys(bodies.at(-1).values).sort(),Object.keys(values).sort(),'the strict server receives a complete document');
  assert.equal(bodies[0].values.connectionLimit,values.connectionLimit);assert.equal(bodies.at(-1).values.connectionLimit,123);
 }finally{c.dispose();}
});

test('a runtime save keeps the draft and refuses a true conflict on an edited field',async()=>{
 let stored={...values},rev=1;let writes=0;
 const c=client(async(_path,method)=>{if(method==='GET')return envelope({...settings(rev),requested:stored,effective:stored});writes++;throw new ConsoleTransportError(409,'console_conflict',rev,[]);});
 const initial=await c.settings();stored={...stored,name:'Other owner'};rev=2;
 c.setDraft('runtime-settings',{...initial.requested,name:'My edit'});
 try {await assert.rejects(c.applySettings(initial.revision,{...initial.requested,name:'My edit'}));assert.equal(writes,1);assert.ok(c.getDraft('runtime-settings'));}finally{c.dispose();}
});

test('a conflicting job command rereads its available actions before retrying',async()=>{
 const job={id:'operation-one',kind:'library-scan',resource:'library',trigger:'owner',state:'running' as const,phase:'all sources',revision:2,attempt:1,createdAt:1,updatedAt:2,nextAt:3,settingsRevision:1,lane:'background-media',actions:['pause','cancel'] as ('pause'|'cancel')[]};
 const calls:any[]=[];
 const c=client(async(_path,method,body)=>{if(method==='GET')return envelope({items:[{...job,revision:3}],nextCursor:''});calls.push(body);if(body.expectedRevision!==3)throw new ConsoleTransportError(409,'console_conflict',3,[]);return envelope({...job,revision:4,state:'paused',actions:['resume','cancel']});});
 try {assert.equal((await c.jobCommand(job,'pause')).state,'paused');assert.deepEqual(calls.map(x=>x.expectedRevision),[2,3]);}finally{c.dispose();}
});

test('a quiet form reload carries the owner\'s edit onto the newer document',async()=>{
 const {DraftForm}=await import('../src/server-admin/draft-form.ts');
 let remote={name:'Initial',limit:0};let sent:unknown;
 const form=new DraftForm({load:async()=>({value:remote,status:{revision:1}}),save:async(draft,saved)=>{sent={draft,saved};return {value:draft};}});
 await form.load();form.set({name:'My edit'});remote={name:'Initial',limit:123};await form.load();
 // What they did not touch shows the server's value; what they typed is still theirs, and still the only change.
 assert.deepEqual(form.getSnapshot().draft,{name:'My edit',limit:123});
 await form.save();
 assert.deepEqual(sent,{draft:{name:'My edit',limit:123},saved:{name:'Initial',limit:123}});
 form.dispose();
});

test('a save the server calls out of date is read again and sent again without a word',async()=>{
 const {DraftForm}=await import('../src/server-admin/draft-form.ts');
 for(const refusal of [{status:428,code:'revision_required'},{status:412,code:'revision_mismatch'},{status:409,code:'administration_conflict'}]){
  let remote={value:{name:'Initial',limit:0},status:{revision:1}};const sent:any[]=[];
  const form=new DraftForm<{name:string;limit:number},{revision:number}>({load:async()=>remote,save:async(draft,saved,status)=>{sent.push({draft,saved,revision:status?.revision});if(status?.revision!==remote.status.revision)throw Object.assign(new Error('moved'),refusal);remote={value:draft,status:{revision:remote.status.revision+1}};return remote;}});
  await form.load();form.set({name:'My edit'});
  // Somebody else changes another setting; this form has not heard yet.
  remote={value:{name:'Initial',limit:9},status:{revision:2}};
  await form.save();
  assert.deepEqual(sent.map(x=>x.revision),[1,2],refusal.code);
  assert.deepEqual(sent[1].draft,{name:'My edit',limit:9});
  const state=form.getSnapshot();
  assert.equal(state.dirty,false);assert.equal(state.saveError,undefined);assert.deepEqual(state.saved,{name:'My edit',limit:9});
  form.dispose();
 }
});

test('only a setting changed here and elsewhere is reported, and the newer document is kept under the draft',async()=>{
 const {DraftForm}=await import('../src/server-admin/draft-form.ts');
 let remote={value:{name:'Initial',limit:0},status:{revision:1}};let writes=0;
 const form=new DraftForm<{name:string;limit:number},{revision:number}>({load:async()=>remote,save:async(draft,_saved,status)=>{writes++;if(status?.revision!==remote.status.revision)throw Object.assign(new Error('moved'),{status:412,code:'revision_mismatch'});remote={value:draft,status:{revision:remote.status.revision+1}};return remote;}});
 await form.load();form.set({name:'My edit'});
 remote={value:{name:'Their edit',limit:5},status:{revision:2}};
 await assert.rejects(form.save(),{code:'revision_mismatch'});
 assert.equal(writes,1,'nothing is sent over a change the owner has not seen');
 let state=form.getSnapshot();
 assert.deepEqual(state.saved,{name:'Their edit',limit:5});assert.deepEqual(state.draft,{name:'My edit',limit:5});assert.equal(state.dirty,true);
 // Saving again is now a choice made knowing what changed.
 await form.save();
 state=form.getSnapshot();
 assert.equal(writes,2);assert.deepEqual(state.saved,{name:'My edit',limit:5});assert.equal(state.dirty,false);
 // The same value arriving from elsewhere is simply done.
 form.set({limit:7});remote={value:{name:'My edit',limit:7},status:{revision:4}};
 await form.save();
 assert.equal(writes,3,'one refused attempt, then nothing left to send');assert.equal(form.getSnapshot().dirty,false);
 form.dispose();
});

test('a reviewed save whose response is lost keeps its exact committed payload and key',async()=>{
 let stored={...values},rev=1,lost=true;const sent:any[]=[];
 const c=client(async(_path,method,body)=>{
  if(method==='GET')return envelope({...settings(rev),requested:stored,effective:stored});
  sent.push(body);
  if(sent.length>2&&body.idempotencyKey===sent[1].idempotencyKey)return envelope({...settings(rev),requested:stored,effective:stored});
  if(body.expectedRevision!==rev)throw new ConsoleTransportError(409,'console_conflict',rev,[]);
  stored={...stored,...body.values};rev++;if(lost){lost=false;throw new Error('lost');}return envelope({...settings(rev),requested:stored,effective:stored});
 });
 await c.settings();stored={...stored,connectionLimit:123};rev=2;
 try {await assert.rejects(c.applySettings(1,{...values,name:'Renamed'}));assert.equal(sent.length,1);assert.equal(c.hasUnconfirmed(),false);const reviewed=await c.settings();const body={...reviewed.requested,name:'Renamed'};await assert.rejects(c.applySettings(2,body));assert.equal(c.hasUnconfirmed(),true);await c.applySettings(2,body);assert.deepEqual(sent[2],sent[1]);assert.equal(stored.connectionLimit,123);}finally{c.dispose();}
});

test('a first partial edit reads a genuine complete document and refuses an unknown stale baseline',async()=>{
 const calls:any[]=[];
 const c=client(async(_path,method,body)=>{calls.push({method,body});return envelope(method==='GET'?settings(4):{...settings(5),requested:{...values,name:'First edit'},effective:{...values,name:'First edit'}});});
 try{
  await assert.rejects(c.applySettings(1,{name:'First edit'}),/Reload/);
  assert.deepEqual(calls.map(call=>call.method),['GET']);
  await c.applySettings(4,{name:'First edit'});
  assert.equal(calls.at(-1).body.expectedRevision,4);
  assert.deepEqual(calls.at(-1).body.values,{...values,name:'First edit'});
 }finally{c.dispose();}
});

test('a late duplicate receipt cannot clear a newer unconfirmed action on the same route',async()=>{
 const sent:{body:any;resolve:(value:unknown)=>void}[]=[];
 const c=client(async(_path,_method,body)=>new Promise(resolve=>sent.push({body,resolve})));
 try{
  const first=run(c),duplicate=run(c);
  assert.equal(sent.length,2);assert.equal(sent[0].body.idempotencyKey,sent[1].body.idempotencyKey);
  sent[0].resolve(envelope(queued));await first;
  const newer=run(c,'other');
  assert.equal(sent.length,3);assert.notEqual(sent[2].body.idempotencyKey,sent[0].body.idempotencyKey);
  sent[1].resolve(envelope(queued));await duplicate;
  assert.equal(c.hasUnconfirmed(),true,'the newer action has no receipt yet');
  sent[2].resolve(envelope({...queued,resource:'other'}));await newer;
  assert.equal(c.hasUnconfirmed(),false);
 }finally{c.dispose();}
});
