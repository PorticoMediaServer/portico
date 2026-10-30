// Authored source vectors; execution is deliberately separate from this change.
import test from 'node:test';
import assert from 'node:assert/strict';
import {RemoteSourceService,sourceReadPreset,type SourceAdvanced,type SourceAdvancedSettings,type SourceReadSettings,type SourceChoiceOpening} from '../src/remote-sources.ts';
import type {LibraryContentApi} from '../src/library-content.ts';
import {parseSourceChoiceBaseline,sourceChoiceAccepted,sourceChoiceAdopt,sourceChoiceCommand,sourceChoiceComparison,sourceChoiceFields,sourceChoicePutBody,sourceChoiceResetDefinition,type SourceChoiceBaseline,type SourceChoiceDefinition,type SourceChoiceField} from '../src/remote-source-choice.ts';

const scope={serverId:'server',viewerId:'owner'},wire={serverId:'server',viewerFence:'fence'};
const operation='1788580000000-12345678-1234-1234-1234-123456789012';
const reading={scanDepth:'basic',wholeFileReads:false,connections:4,requestsPerSecond:4,bytesPerSecond:0} as const;
const advanced:SourceAdvanced={scanIntervalSeconds:21600,watchForChanges:false,scanOnStart:false,fullRecheckDays:0,cacheBytes:0,cacheFloorBytes:0,seekPreviews:true,chapterImages:true,waveforms:true,loudness:true,checksums:true,introsMode:'inherit',doubleCheckFolders:false,dirCacheSeconds:0,readBufferBytes:0,connectTimeoutSeconds:0,idleTimeoutSeconds:0,retries:0,lowLevelRetries:0};
const definitions:readonly SourceChoiceDefinition[]=Object.freeze(['list','once','everything','ask'].map(name=>Object.freeze({name:name as SourceChoiceDefinition['name'],definitionRevision:1,reading:Object.freeze(sourceReadPreset(name as SourceChoiceDefinition['name'])),advanced:Object.freeze({...advanced})})));
// This fixture is the server's published direct-DAV set, not a client classifier.
const applicable=Object.freeze(sourceChoiceFields.filter(field=>!['dirCacheSeconds','readBufferBytes','connectTimeoutSeconds','idleTimeoutSeconds','retries','lowLevelRetries'].includes(field)));
const originalAdvanced={...advanced,dirCacheSeconds:123,retries:7};
const originalRead=(over:Partial<SourceReadSettings>={}):SourceReadSettings=>({sourceId:'dav',generation:'7',revision:2,saved:true,configurationGeneration:'11',values:{...reading,wholeFileReads:true,allowAdvancedMediaAnalysis:false},choiceDefinitions:definitions,choiceApplicableFields:applicable,...over});
const advancedRead=(over:Partial<SourceAdvancedSettings>={}):SourceAdvancedSettings=>({sourceId:'dav',generation:'7',revision:5,saved:true,cacheSupported:false,cacheApplied:false,cacheUsedBytes:0,scanIntervalObserved:0,values:{...originalAdvanced},...over});
function parent(readRevision=2,advancedRevision=6,generation='7',configurationGeneration='11'):SourceReadSettings{
 return originalRead({revision:readRevision,generation,configurationGeneration,choiceBaseline:{sourceId:'dav',generation,configurationGeneration,name:'once',definitionRevision:1,readRevision,advancedRevision,reading:{...reading},advanced:{...advanced},currentAdvanced:{...originalAdvanced},applicableFields:applicable,changedFields:[]}});
}
function command(){return sourceChoiceCommand(originalRead(),advancedRead(),definitions[1]!)!;}
function receipt(operationId=operation,settings=parent()){
 return {scope:wire,receipt:{operationId,accepted:true,removed:false,source:{id:'dav',kind:'webdav',name:'DAV',rootPath:'/server/remote/dav',generation:7,state:'ready',rangeSupport:'conditional',insecureLocal:false,credentialPresent:true},readSettings:settings}};
}
const opening=(isCurrent:()=>boolean=()=>true):SourceChoiceOpening=>({signal:new AbortController().signal,isCurrent,consentDirty:false});
function deferred<T>(){let resolve!:(value:T)=>void,reject!:(error:unknown)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};}
type Call={path:string;method:string;body:unknown;signal?:AbortSignal};
function harness(hooks:Partial<{put:(call:Call)=>Promise<unknown>;fresh:()=>Promise<unknown>;recover:()=>Promise<unknown>;acknowledge:(id:string)=>Promise<void>;initial:SourceReadSettings;advanced:SourceAdvancedSettings;operationId:()=>Promise<string>}>= {}){
 const calls:Call[]=[];let submitted=false;
 const api:LibraryContentApi={acknowledgeUserMutation:hooks.acknowledge,request:async<T>(path:string,method='GET',body?:unknown,signal?:AbortSignal):Promise<T>=>{
  const call={path,method,body,signal};calls.push(call);let result:unknown;
  if(method==='PUT'){submitted=true;result=hooks.put?await hooks.put(call):receipt();}
  else if(path.includes('/source-operations/'))result=hooks.recover?await hooks.recover():receipt();
  else if(path.endsWith('/settings/advanced'))result={scope:wire,settings:hooks.advanced??advancedRead()};
  else if(path.endsWith('/settings/read-profile'))result=submitted?(hooks.fresh?await hooks.fresh():{scope:wire,settings:parent()}):{scope:wire,settings:hooks.initial??originalRead()};
  else throw new Error('Unexpected request '+method+' '+path);
  return result as T;
 }};
 const service=new RemoteSourceService(api,scope,hooks.operationId??(async()=>operation));
 return {service,calls,pair:async()=>{const read=await service.readProfile('dav'),adv=await service.readAdvanced('dav');return {read,adv,offered:read.choiceDefinitions![1]!};}};
}

test('choice acknowledgement failure retains its original parent and retries only consumption before readback',async()=>{
 const acks:string[]=[];let fail=true;
 const h=harness({acknowledge:async id=>{acks.push(id);if(fail)throw new Error('consume failed');}});
 try{
  const {read,adv,offered}=await h.pair(),unknown=await h.service.saveProfileStartChoice(read,adv,offered,opening());
  assert.equal(unknown.kind,'unknown');if(unknown.kind!=='unknown')return;
  const original=unknown.intent;assert.equal(original.operationId,operation);assert.equal(original.body.expectedRevision,read.revision);assert.equal(h.service.getSnapshot().ambiguous,true);
  assert.equal(h.calls.filter(call=>call.method==='PUT').length,1);fail=false;
  const recovered=await h.service.recoverStartChoice(original);assert.equal(recovered.kind,'applied');assert.deepEqual(acks,[operation,operation]);
  assert.equal(h.calls.filter(call=>call.method==='PUT').length,1);assert.equal(h.calls.filter(call=>call.path.includes('/source-operations/')).length,0,'the already parsed historical parent remains owned until consumption');
  assert.equal(h.service.getSnapshot().ambiguous,false);
 }finally{h.service.dispose();}
});

test('read-only baseline comparison is coherent, empty is explicit, consent and legacy are not counted',()=>{
 const settings=parent();assert.deepEqual(sourceChoiceComparison(settings)?.changedFields,[]);
 assert.equal(sourceChoiceComparison(originalRead()),undefined,'absent is not zero');
 assert.equal(sourceChoiceComparison({...settings,revision:3}),undefined);
 assert.equal(sourceChoiceComparison({...settings,values:{...settings.values,connections:8}}),undefined,'server changed set must agree');
 const parsed=parseSourceChoiceBaseline(settings.choiceBaseline)!;assert.deepEqual(parsed.changedFields,[]);
 assert.throws(()=>parseSourceChoiceBaseline({...parsed,changedFields:['allowAdvancedMediaAnalysis']}));
 assert.throws(()=>parseSourceChoiceBaseline({...parsed,applicableFields:['connections','connections']}));
});

test('capture uses one original pair, actual saved booleans and exact offered identity',()=>{
 const c=command();assert.ok(c);assert.equal(Object.isFrozen(c),true);assert.equal(Object.isFrozen(c.originalAdvanced),true);
 assert.equal(sourceChoiceCommand(originalRead(),advancedRead(),{...definitions[1]!}),undefined);
 assert.equal(sourceChoiceCommand(originalRead(),advancedRead({generation:'8'}),definitions[1]!),undefined);
 assert.equal(sourceChoiceCommand(originalRead({configurationGeneration:undefined}),advancedRead(),definitions[1]!),undefined);
 assert.equal(sourceChoiceCommand(originalRead({revision:0,saved:true}),advancedRead(),definitions[1]!),undefined);
 assert.equal(sourceChoiceCommand(originalRead(),advancedRead({revision:0,saved:true}),definitions[1]!),undefined);
 assert.equal(sourceChoiceCommand({...originalRead(),saved:undefined} as never,advancedRead(),definitions[1]!),undefined);
 assert.ok(sourceChoiceCommand(originalRead({revision:0,saved:false}),advancedRead({revision:0,saved:false}),definitions[1]!));
});

test('body is one frozen exact intent, compatibility preserved, consent entirely omitted',()=>{
 const c=command(),body=sourceChoicePutBody(c,operation);
 assert.deepEqual(Object.keys(body).sort(),['expectedGeneration','expectedRevision','operationId','startChoice','values']);
 assert.deepEqual(body.values,{...reading,wholeFileReads:true});assert.equal(Object.hasOwn(body.values,'allowAdvancedMediaAnalysis'),false);
 assert.deepEqual(body.startChoice,{name:'once',definitionRevision:1,expectedAdvancedRevision:5,expectedConfigurationGeneration:'11'});
 assert.equal(Object.isFrozen(body),true);assert.equal(Object.isFrozen(body.values),true);
 assert.throws(()=>sourceChoicePutBody(c,'fresh-invalid-key'));
});

test('parent accepts no-op read2 and actual Advanced6; child uses own generations and accepted pair',()=>{
 const c=command(),accepted=sourceChoiceAccepted(parent(),c)!;assert.ok(accepted);
 assert.equal(accepted.readRevision,2);assert.equal(accepted.advancedRevision,6);
 assert.ok(sourceChoiceAdopt(parent(2,6,'9','18'),c,accepted),'real child is not forced to historical generations');
 assert.equal(sourceChoiceAdopt(parent(2,7,'9','18'),c,accepted),undefined,'later equal values are not this operation');
 assert.equal(sourceChoiceAdopt(parent(3,6,'9','18'),c,accepted),undefined);
 assert.equal(sourceChoiceAdopt({...parent(),choiceBaseline:undefined},c,accepted),undefined);
 assert.equal(sourceChoiceAccepted(parent(2,5),c),undefined,'pre-write Advanced revision is not accepted');
});

test('parent requires exact baseline, definition, applicability and preserved independent fields',()=>{
 const c=command(),p=parent(),b=p.choiceBaseline!;
 for(const bad of [
  {...p,choiceBaseline:undefined},{...p,sourceId:'other'},{...p,generation:'8'},{...p,configurationGeneration:'12'},
  {...p,choiceBaseline:{...b,configurationGeneration:'12'}},{...p,choiceBaseline:{...b,definitionRevision:2}},
  {...p,choiceApplicableFields:sourceChoiceFields,choiceBaseline:{...b,applicableFields:sourceChoiceFields}},
  {...p,values:{...p.values,allowAdvancedMediaAnalysis:true}},{...p,values:{...p.values,wholeFileReads:false}},
  {...p,choiceBaseline:{...b,currentAdvanced:{...b.currentAdvanced,dirCacheSeconds:0}}},
  {...p,choiceBaseline:{...b,currentAdvanced:{...b.currentAdvanced,loudness:false},changedFields:['loudness'] as SourceChoiceField[]}},
 ])assert.equal(sourceChoiceAccepted(bad,c),undefined);
});

test('reset returns the recorded exact offered version; never substitutes a newer same-name definition',()=>{
 const p=parent();assert.equal(sourceChoiceResetDefinition(p),p.choiceDefinitions![1]);
 assert.equal(sourceChoiceResetDefinition({...p,choiceDefinitions:definitions.map(d=>d.name==='once'?{...d,definitionRevision:2}:d)}),undefined);
 assert.equal(sourceChoiceResetDefinition(originalRead()),undefined);
});

test('atomic apply makes ONE PUT, no Advanced save, first explicit choice needs no baseline',async()=>{
 const h=harness();try{const {read,adv,offered}=await h.pair();const result=await h.service.saveProfileStartChoice(read,adv,offered,opening());
 assert.equal(result.kind,'applied');const puts=h.calls.filter(c=>c.method==='PUT');assert.equal(puts.length,1);
 assert.match(puts[0]!.path,/\/settings\/read-profile$/);assert.deepEqual(puts[0]!.body,sourceChoicePutBody(command(),operation));
 assert.equal(h.service.getSnapshot().ambiguous,false);assert.equal(h.service.getSnapshot().busy,false);
 }finally{h.service.dispose();}
});

test('unissued/cloned reads and dirty consent refuse without operation-id or write',async()=>{
 let ids=0;const h=harness({operationId:async()=>{ids++;return operation;}});try{
 const {read,adv,offered}=await h.pair();
 await assert.rejects(h.service.saveProfileStartChoice({...read},adv,offered,opening()));
 await assert.rejects(h.service.saveProfileStartChoice(read,adv,offered,{...opening(),consentDirty:true} as never));
 assert.equal(ids,0);assert.equal(h.calls.filter(c=>c.method==='PUT').length,0);
 }finally{h.service.dispose();}
});

test('claim precedes opening getters, publication, and operation-id await',async()=>{
 const id=deferred<string>(),h=harness({operationId:()=>id.promise});try{
 const {read,adv,offered}=await h.pair();let nested:Promise<unknown>|undefined;
 const o={get signal(){nested=h.service.saveProfileStartChoice(read,adv,offered,opening());return new AbortController().signal;},isCurrent:()=>true,consentDirty:false as const};
 const first=h.service.saveProfileStartChoice(read,adv,offered,o);await assert.rejects(nested!);
 await assert.rejects(h.service.saveAdvanced(adv,adv.values));await assert.rejects(h.service.saveProfile(read,read.values));
 id.resolve(operation);assert.equal((await first).kind,'applied');
 }finally{h.service.dispose();}
});

test('an ordinary source operation-id wait excludes choice before transport exists',async()=>{
 const id=deferred<string>(),h=harness({operationId:()=>id.promise});try{
 const {read,adv,offered}=await h.pair();const old=h.service.saveProfile(read,read.values);
 await assert.rejects(h.service.saveProfileStartChoice(read,adv,offered,opening()));
 id.reject(new Error('cancel original'));await assert.rejects(old);
 assert.equal(h.calls.filter(c=>c.method==='PUT').length,0);
 }finally{h.service.dispose();}
});

test('unknown retains exact intent; cloned recovery and every other source mutation refuse',async()=>{
 const h=harness({put:async()=>{throw new TypeError('lost');}});try{
 const {read,adv,offered}=await h.pair(),result=await h.service.saveProfileStartChoice(read,adv,offered,opening());
 assert.equal(result.kind,'unknown');if(result.kind!=='unknown')return;
 assert.equal(h.service.getSnapshot().ambiguous,true);assert.equal(h.service.getSnapshot().operationId,operation);
 await assert.rejects(h.service.recoverStartChoice({...result.intent}));
 await assert.rejects(h.service.saveAdvanced(adv,adv.values));await assert.rejects(h.service.setPolicy({libraryId:'library',enabled:false,revision:1},true));
 assert.equal((await h.service.recoverStartChoice(result.intent)).kind,'applied');
 assert.equal(h.calls.filter(c=>c.method==='PUT').length,1,'recovery is GET only');
 }finally{h.service.dispose();}
});

test('404 and request_in_progress recovery remain unresolved, no new ID/body or automatic resend',async()=>{
 let ids=0,recoveries=0;const h=harness({operationId:async()=>{ids++;return operation;},put:async()=>{throw new TypeError('lost');},recover:async()=>{recoveries++;throw Object.assign(new Error('pending'),{status:recoveries===1?404:409,code:recoveries===1?'not_found':'request_in_progress'});}});try{
 const {read,adv,offered}=await h.pair(),result=await h.service.saveProfileStartChoice(read,adv,offered,opening());
 assert.equal(result.kind,'unknown');if(result.kind!=='unknown')return;
 for(let n=0;n<2;n++){const next=await h.service.recoverStartChoice(result.intent);assert.equal(next.kind,'unknown');if(next.kind==='unknown')assert.equal(next.intent,result.intent);}
 assert.equal(ids,1);assert.equal(h.calls.filter(c=>c.method==='PUT').length,1);assert.equal(h.service.getSnapshot().ambiguous,true);
 }finally{h.service.dispose();}
});

test('malformed or mismatched parent receipts do not confirm acceptance',async()=>{
 for(const change of [(r:ReturnType<typeof receipt>)=>{r.receipt.operationId='other';},(r:ReturnType<typeof receipt>)=>{r.receipt.removed=true;},(r:ReturnType<typeof receipt>)=>{r.receipt.source.id='other';},(r:ReturnType<typeof receipt>)=>{r.receipt.source.generation=8;},(r:ReturnType<typeof receipt>)=>{r.receipt.readSettings={...parent(),choiceBaseline:undefined};}]){
  const h=harness({put:async()=>{const r=receipt();change(r);return r;}});try{const {read,adv,offered}=await h.pair();assert.equal((await h.service.saveProfileStartChoice(read,adv,offered,opening())).kind,'unknown');}finally{h.service.dispose();}
 }
});

test('confirmed parent remains accepted when refresh fails or association is unavailable',async()=>{
 for(const fresh of [async()=>{throw new TypeError('offline');},async()=>({scope:wire,settings:originalRead()}),async()=>({scope:wire,settings:parent(2,7,'9','18')})]){
  const h=harness({fresh});try{const {read,adv,offered}=await h.pair();const result=await h.service.saveProfileStartChoice(read,adv,offered,opening());
   assert.equal(result.kind,'accepted-unavailable');if(result.kind==='accepted-unavailable'){assert.equal(result.settings.generation,'7');assert.equal(result.accepted.advancedRevision,6);}
   assert.equal(h.service.getSnapshot().ambiguous,false);assert.equal(h.calls.filter(c=>c.method==='PUT').length,1);
   await assert.rejects(h.service.recoverStartChoice({operationId:operation,body:sourceChoicePutBody(command(),operation),command:command()}));
  }finally{h.service.dispose();}
 }
});

test('retired original opening before ID settles cannot dispatch, and cannot be replaced for recovery',async()=>{
 const id=deferred<string>();let current=true;const h=harness({operationId:()=>id.promise});try{
 const {read,adv,offered}=await h.pair();const started=h.service.saveProfileStartChoice(read,adv,offered,opening(()=>current));current=false;id.resolve(operation);
 await assert.rejects(started);assert.equal(h.calls.filter(c=>c.method==='PUT').length,0);
 }finally{h.service.dispose();}
 const next=harness({put:async()=>{throw new TypeError('lost');}});try{
 const {read,adv,offered}=await next.pair();let live=true;const result=await next.service.saveProfileStartChoice(read,adv,offered,opening(()=>live));
 if(result.kind!=='unknown')assert.fail('expected unknown');live=false;await assert.rejects(next.service.recoverStartChoice(result.intent));
 assert.equal(next.calls.filter(c=>c.method==='PUT').length,1);
 }finally{next.service.dispose();}
});

test('late PUT and late reconciliation cannot publish or clear a successor after disposal',async()=>{
 for(const at of ['put','fresh'] as const){
  const gate=deferred<unknown>(),entered=deferred<void>();const h=harness({[at]:async()=>{entered.resolve();return gate.promise;}});try{
   const {read,adv,offered}=await h.pair();const pending=h.service.saveProfileStartChoice(read,adv,offered,opening());await entered.promise;
   h.service.dispose();const retired=h.service.getSnapshot();gate.resolve(at==='put'?receipt():{scope:wire,settings:parent()});
   await assert.rejects(pending);assert.equal(h.service.getSnapshot(),retired);
  }finally{h.service.dispose();}
 }
});

test('abort reaches the original transport and a late recovery cannot settle',async()=>{
 const h=harness({put:async()=>{throw new TypeError('lost');}});try{
 const {read,adv,offered}=await h.pair();const ctrl=new AbortController();const result=await h.service.saveProfileStartChoice(read,adv,offered,{signal:ctrl.signal,isCurrent:()=>true,consentDirty:false});
 if(result.kind!=='unknown')assert.fail('expected unknown');ctrl.abort();await assert.rejects(h.service.recoverStartChoice(result.intent));
 }finally{h.service.dispose();}
 const gate=deferred<unknown>(),entered=deferred<void>();const next=harness({put:async()=>{throw new TypeError('lost');},recover:async()=>{entered.resolve();return gate.promise;}});try{
 const {read,adv,offered}=await next.pair();let current=true;const result=await next.service.saveProfileStartChoice(read,adv,offered,opening(()=>current));
 if(result.kind!=='unknown')assert.fail('expected unknown');const recover=next.service.recoverStartChoice(result.intent);await entered.promise;current=false;gate.reject(new TypeError('late'));
 await assert.rejects(recover);assert.equal(next.service.getSnapshot().ambiguous,true);
 }finally{next.service.dispose();}
});

test('listener reentrancy and listener faults cannot overlap writes or lose acceptance',async()=>{
 const h=harness();try{
 const {read,adv,offered}=await h.pair();const attempts:Promise<unknown>[]=[];
 h.service.subscribe(()=>{attempts.push(h.service.saveAdvanced(adv,adv.values));throw new Error('observer fault');});
 const result=await h.service.saveProfileStartChoice(read,adv,offered,opening());assert.equal(result.kind,'applied');
 for(const attempt of attempts)await assert.rejects(attempt);
 assert.equal(h.calls.filter(c=>c.method==='PUT').length,1);assert.equal(h.service.getSnapshot().busy,false);
 }finally{h.service.dispose();}
});

test('abort after actual dispatch cancels only that opening transport and refuses late outcome',async()=>{
 const put=deferred<unknown>(),entered=deferred<void>(),h=harness({put:async()=>{entered.resolve();return put.promise;}});try{
 const {read,adv,offered}=await h.pair(),controller=new AbortController();
 const pending=h.service.saveProfileStartChoice(read,adv,offered,{signal:controller.signal,isCurrent:()=>true,consentDirty:false});await entered.promise;
 controller.abort();await assert.rejects(pending);assert.equal(h.calls.find(c=>c.method==='PUT')!.signal!.aborted,true);
 put.resolve(receipt());await assert.rejects(h.service.saveAdvanced(adv,adv.values),'unresolved original operation still excludes new writes');
 assert.equal(h.calls.filter(c=>c.method==='PUT').length,1);
 }finally{h.service.dispose();}
});

test('actual unsaved zero revisions can make a first explicit choice',async()=>{
 const accepted=parent(1,1),h=harness({initial:originalRead({revision:0,saved:false}),advanced:advancedRead({revision:0,saved:false}),put:async()=>receipt(operation,accepted),fresh:async()=>({scope:wire,settings:accepted})});try{
 const {read,adv,offered}=await h.pair();const result=await h.service.saveProfileStartChoice(read,adv,offered,opening());
 assert.equal(result.kind,'applied');if(result.kind==='applied'){assert.equal(result.operationId,operation);assert.equal(result.accepted.readRevision,1);assert.equal(result.accepted.advancedRevision,1);}
 }finally{h.service.dispose();}
});

test('pure pair capture reads getter-backed source witnesses once',()=>{
 const read=originalRead(),adv=advancedRead();let readGets=0,advGets=0;
 Object.defineProperty(read,'generation',{get(){assert.equal(readGets++,0);return '7';}});
 Object.defineProperty(adv,'generation',{get(){assert.equal(advGets++,0);return '7';}});
 const c=sourceChoiceCommand(read,adv,definitions[1]!);assert.ok(c);assert.equal(readGets,1);assert.equal(advGets,1);
});

// Preserve the six published read-only projection vectors unchanged in their own scope.
{
const scope={serverId:'server',viewerId:'owner'},wire={serverId:'server',viewerFence:'fence'};
const reading={scanDepth:'basic',wholeFileReads:false,connections:4,requestsPerSecond:4,bytesPerSecond:0} as const;
const advanced={scanIntervalSeconds:21600,watchForChanges:false,scanOnStart:false,fullRecheckDays:0,cacheBytes:0,cacheFloorBytes:0,seekPreviews:true,chapterImages:true,waveforms:true,loudness:true,checksums:true,introsMode:'inherit',doubleCheckFolders:false,dirCacheSeconds:0,readBufferBytes:0,connectTimeoutSeconds:0,idleTimeoutSeconds:0,retries:0,lowLevelRetries:0} as const;
const names=['list','once','everything','ask'] as const;
const definitions:SourceChoiceDefinition[]=names.map(name=>Object.freeze({name,definitionRevision:1,reading:Object.freeze(sourceReadPreset(name as SourceChoiceDefinition['name'])),advanced:Object.freeze({...advanced})}));
const applicable=Object.freeze(sourceChoiceFields.filter(field=>!['dirCacheSeconds','readBufferBytes','connectTimeoutSeconds','idleTimeoutSeconds','retries','lowLevelRetries'].includes(field)) as SourceChoiceField[]);
const currentAdvanced=Object.freeze({...advanced,loudness:false});
const baseline:SourceChoiceBaseline=Object.freeze({sourceId:'dav',generation:'7',configurationGeneration:'11',name:'once',definitionRevision:1,
 readRevision:2,advancedRevision:3,reading:Object.freeze({...reading}),advanced:Object.freeze({...advanced}),currentAdvanced,
 applicableFields:applicable,changedFields:Object.freeze(['loudness'] as SourceChoiceField[])});
const settings=(over:Record<string,unknown>={})=>({sourceId:'dav',generation:'7',revision:2,configurationGeneration:'11',values:{...reading},choiceDefinitions:definitions,choiceApplicableFields:applicable,choiceBaseline:baseline,...over});
function api(fn:(path:string,method:string,body:unknown)=>Promise<unknown>){return {request:<T>(p:string,m='GET',b?:unknown)=>fn(p,m,b) as Promise<T>};}

test('a coherent baseline compares the actual read facts and same-snapshot Advanced over the applicable set',()=>{
 const result=sourceChoiceComparison(settings());
 assert.deepEqual(result,{name:'once',definitionRevision:1,applicableFields:applicable,changedFields:['loudness']});
 assert.equal(Object.isFrozen(result),true);
 assert.deepEqual(sourceChoiceComparison(settings({values:{...reading,connections:6}})),undefined,'a changed read value without an exact server diff is withheld');
 assert.deepEqual(sourceChoiceComparison(settings({choiceBaseline:{...baseline,changedFields:[]}})),undefined,'a missing changed row is never a synthetic zero');
 assert.deepEqual(sourceChoiceComparison(settings({choiceBaseline:{...baseline,changedFields:['loudness','connections']}})),undefined,'an extra changed row is refused');
});

test('every original witness must match the same snapshot',()=>{
 for(const over of [
  {sourceId:'other'},{generation:'8'},{configurationGeneration:'12'},{revision:3},
  {choiceBaseline:{...baseline,name:'everything'}},{choiceBaseline:{...baseline,definitionRevision:2}},
  {choiceBaseline:{...baseline,applicableFields:applicable.slice(0,-1)}},
  {choiceDefinitions:definitions.map(value=>value.name==='once'?{...value,advanced:{...value.advanced,loudness:false}}:value)},
 ])assert.equal(sourceChoiceComparison(settings(over as never)),undefined,JSON.stringify(over));
 assert.equal(sourceChoiceComparison({...settings(),configurationGeneration:undefined}),undefined);
 assert.equal(sourceChoiceComparison({...settings(),choiceBaseline:undefined}),undefined);
});

test('the read parser keeps the actual facts and adds only supplied choice facts',async()=>{
 const full=new RemoteSourceService(api(async()=>({scope:wire,settings:{sourceId:'dav',generation:'7',revision:2,saved:true,
  values:{...reading,allowAdvancedMediaAnalysis:true},configurationGeneration:'11',choiceDefinitions:definitions,choiceApplicableFields:applicable,choiceBaseline:baseline}})),scope,async()=>'op');
 const parsed=(await full.readProfile('dav'));
 assert.equal(parsed.configurationGeneration,'11');
 assert.equal(parsed.values.allowAdvancedMediaAnalysis,true,'actual consent stays published');
 assert.equal(Object.isFrozen(parsed.choiceDefinitions),true);
 assert.deepEqual(parsed.choiceDefinitions?.map(value=>value.name),names);
 assert.deepEqual(parsed.choiceBaseline?.changedFields,['loudness']);
 assert.deepEqual(sourceChoiceComparison(parsed)?.changedFields,['loudness']);
 full.dispose();
 const older=new RemoteSourceService(api(async()=>({scope:wire,settings:{sourceId:'dav',generation:'7',revision:2,saved:true,values:{...reading,allowAdvancedMediaAnalysis:false}}})),scope,async()=>'op');
 const plain=(await older.readProfile('dav'));
 assert.equal(Object.hasOwn(plain,'configurationGeneration'),false);
 assert.equal(Object.hasOwn(plain,'choiceDefinitions'),false);
 assert.equal(plain.values.allowAdvancedMediaAnalysis,false);
 assert.equal(sourceChoiceComparison(plain),undefined);
 older.dispose();
});

test('malformed choice projections are refused instead of being dropped or defaulted',async()=>{
 const base={scope:wire,settings:{sourceId:'dav',generation:'7',revision:2,saved:true,values:reading}};
 for(const extra of [
  {configurationGeneration:'0'},{configurationGeneration:11},
  {choiceDefinitions:definitions.slice(0,3)},
  {choiceDefinitions:[...definitions,{...definitions[0]!}]},
  {choiceDefinitions:definitions.map(value=>value.name==='once'?{...value,reading:{...reading,wholeFileReads:true}}:value)},
  {choiceDefinitions:definitions.map(value=>value.name==='once'?{...value,reading:{...reading,allowAdvancedMediaAnalysis:true}}:value)},
  {choiceApplicableFields:['loudness','loudness']},
  {choiceApplicableFields:['consent']},
  {choiceBaseline:{...baseline,changedFields:['loudness','loudness']}},
  {choiceBaseline:{...baseline,changedFields:['not_a_field']}},
  {choiceBaseline:{...baseline,unknown:true}},
 ]){
  const service=new RemoteSourceService(api(async()=>({...base,settings:{...base.settings,...extra}})),scope,async()=>'op');
  await assert.rejects(()=>service.readProfile('dav'),/Invalid remote source response/);service.dispose();
 }
});

test('an unchanged applied baseline parses zero changes, and a flags-only drift parses its exact row',async()=>{
 const zero=Object.freeze([...[]] as SourceChoiceField[]);
 const unchanged=Object.freeze({...baseline,currentAdvanced:Object.freeze({...advanced}),changedFields:zero});
 const parsed=sourceChoiceComparison(settings({choiceBaseline:unchanged}));
 assert.deepEqual(parsed,{name:'once',definitionRevision:1,applicableFields:applicable,changedFields:[]},'a freshly applied choice is a real zero, not a withheld comparison');
 const flags=Object.freeze({...baseline,currentAdvanced:Object.freeze({...advanced,checksums:false}),changedFields:Object.freeze(['checksums'] as SourceChoiceField[])});
 assert.deepEqual(sourceChoiceComparison(settings({choiceBaseline:flags})),{name:'once',definitionRevision:1,applicableFields:applicable,changedFields:['checksums']});
 assert.equal(sourceChoiceComparison(settings({choiceBaseline:{...flags,changedFields:[]}})),undefined,'actual drift with an empty server set stays withheld');
 const service=new RemoteSourceService(api(async()=>({scope:wire,settings:{sourceId:'dav',generation:'7',revision:2,saved:true,
  values:reading,configurationGeneration:'11',choiceDefinitions:definitions,choiceApplicableFields:applicable,choiceBaseline:unchanged}})),scope,async()=>'op');
 const read=await service.readProfile('dav');
 assert.deepEqual(read.choiceBaseline?.changedFields,[]);
 assert.deepEqual(sourceChoiceComparison(read)?.changedFields,[]);
 service.dispose();
});

test('the retired whole-file and consent flags never add a changed row',async()=>{
 const unchanged=Object.freeze({...baseline,currentAdvanced:Object.freeze({...advanced}),changedFields:Object.freeze([] as SourceChoiceField[])});
 const actual={...reading,wholeFileReads:true,allowAdvancedMediaAnalysis:true};
 const comparison=sourceChoiceComparison({...settings({choiceBaseline:unchanged}),values:actual});
 assert.deepEqual(comparison,{name:'once',definitionRevision:1,applicableFields:applicable,changedFields:[]},'legacy whole-file and independent consent are not choice rows');
 const service=new RemoteSourceService(api(async()=>({scope:wire,settings:{sourceId:'dav',generation:'7',revision:2,saved:true,
  values:actual,configurationGeneration:'11',choiceDefinitions:definitions,choiceApplicableFields:applicable,choiceBaseline:unchanged}})),scope,async()=>'op');
 const read=await service.readProfile('dav');
 assert.equal(read.values.wholeFileReads,true,'the actual compatibility fact stays published');
 assert.equal(read.values.allowAdvancedMediaAnalysis,true,'the actual consent stays published');
 assert.deepEqual(read.choiceBaseline?.changedFields,[]);
 assert.deepEqual(sourceChoiceComparison(read)?.changedFields,[]);
 service.dispose();
});
}
