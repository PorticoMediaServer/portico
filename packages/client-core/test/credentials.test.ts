import test from 'node:test';import assert from 'node:assert/strict';
import {ApiError,CredentialSessionService,type CredentialRecord,type HostedSession} from '../src/index.ts';
const uuid=async()=>crypto.randomUUID();const delay=()=>new Promise(r=>setTimeout(r,10));
const session=(token='one'):HostedSession=>({accessToken:'access-'+token,refreshToken:'refresh-'+token,familyId:'family',expiresAt:new Date(Date.now()+3600000).toISOString(),refreshExpiresAt:new Date(Date.now()+86400000).toISOString(),account:{id:'a',username:'a',displayName:'A'}});
function store(){let value:CredentialRecord|undefined;return{load:async()=>structuredClone(value),save:async(v:CredentialRecord)=>{value=structuredClone(v)},value:()=>value}}
const api=(refresh:any,revoke=async(_token:string)=>{})=>({authorityFromSession:(s:HostedSession)=>s.refreshToken!,refresh,revoke});
function held<T>(){let resolve!:(value:T)=>void;const promise=new Promise<T>(r=>{resolve=r});return{promise,resolve};}
test('a retired authentication caller cannot begin credential work',async()=>{
 const storage=store();let calls=0;
 const service=new CredentialSessionService(api(async()=>session()),storage,uuid);
 await assert.rejects(service.authenticateIfSignedOut(async()=>{calls++;return session();},{},()=>false));
 assert.equal(calls,0);assert.equal(storage.value()?.active,undefined);
});
test('a known grant returned after its caller retires is revoked without adoption',async()=>{
 const storage=store(),entered=held<void>(),answer=held<HostedSession>();let current=true;
 const revoked:string[]=[],seen:string[]=[];
 const service=new CredentialSessionService(api(async()=>session(),async token=>{revoked.push(token)}),storage,uuid);
 service.subscribe(()=>{if(service.getSnapshot().session)seen.push(service.getSnapshot().session!.familyId!)});
 const result=service.authenticateIfSignedOut(()=>{entered.resolve();return answer.promise},{},()=>current);
 await entered.promise;current=false;answer.resolve({...session('abandoned'),familyId:'abandoned'});
 await assert.rejects(result);await service.synchronize();
 assert.deepEqual(seen,[]);assert.equal(storage.value()?.active,undefined);assert.deepEqual(revoked,['refresh-abandoned']);
});
test('authentication caller retirement during storage cannot publish that family',async()=>{
 const storage=store(),writing=held<void>(),release=held<void>();let current=true,first=true;
 const revoked:string[]=[],seen:string[]=[];
 const service=new CredentialSessionService(api(async()=>session(),async token=>{revoked.push(token)}),{
  load:storage.load,save:async value=>{if(first&&value.active){first=false;writing.resolve();await release.promise}await storage.save(value)},
 },uuid);
 service.subscribe(()=>{if(service.getSnapshot().session)seen.push(service.getSnapshot().session!.familyId!)});
 const result=service.authenticateIfSignedOut(async()=>({...session('abandoned'),familyId:'abandoned'}),{},()=>current);
 await writing.promise;current=false;release.resolve();await assert.rejects(result);
 assert.deepEqual(seen,[]);assert.equal(storage.value()?.active,undefined);assert.deepEqual(revoked,['refresh-abandoned']);
});
test('late authentication cleanup preserves the same family already accepted by a current flow',async()=>{
 const storage=store(),entered=held<void>(),answer=held<HostedSession>(),revoked:string[]=[];
 const service=new CredentialSessionService(api(async()=>session(),async token=>{revoked.push(token)}),storage,uuid);
 const old=service.authenticateIfSignedOut(()=>{entered.resolve();return answer.promise});
 await entered.promise;
 // The current explicit adoption queues ahead of compensation for the late
 // response, whose one-time replay names this very same server-issued family.
 const accepted=service.adopt(session('accepted'));
 answer.resolve(session('late-replay'));await assert.rejects(old);await accepted;await service.synchronize();
 assert.equal(storage.value()?.active?.session.refreshToken,'refresh-accepted');assert.deepEqual(revoked,[]);
});
test('intentional form retirement from successful session publication keeps the accepted family',async()=>{
 const storage=store(),revoked:string[]=[];let current=true;
 const service=new CredentialSessionService(api(async()=>session(),async token=>{revoked.push(token)}),storage,uuid);
 service.subscribe(()=>{if(service.getSnapshot().session)current=false});
 await service.authenticateIfSignedOut(async()=>session(),{},()=>current);
 assert.equal(storage.value()?.active?.session.familyId,'family');assert.deepEqual(revoked,[]);
});
test('queued compensation retains the exact issued authority before waiting behind a new adoption',async()=>{
 const storage=store(),entered=held<void>(),answer=held<HostedSession>(),writing=held<void>(),release=held<void>();
 const revoked:string[]=[];let gate=true;
 const service=new CredentialSessionService(api(async()=>session(),async token=>{revoked.push(token)}),{
  load:storage.load,save:async value=>{if(gate&&value.active?.session.familyId==='accepted'){gate=false;writing.resolve();await release.promise}await storage.save(value)},
 },uuid);
 const old=service.authenticateIfSignedOut(()=>{entered.resolve();return answer.promise});await entered.promise;
 const accepted=service.adopt({...session('accepted'),familyId:'accepted'});
 const late={...session('abandoned'),familyId:'abandoned'};answer.resolve(late);
 await assert.rejects(old);await writing.promise;
 // Response objects are not authority containers after their handoff. Cleanup
 // already captured the original values before its coordinator turn queued.
 late.familyId='accepted';late.refreshToken='refresh-accepted';
 release.resolve();await accepted;await service.synchronize();
 assert.deepEqual(revoked,['refresh-abandoned']);assert.equal(storage.value()?.active?.session.familyId,'accepted');
});
test('refresh is single flight, stores receipt before request, hides refresh secret',async()=>{const storage=store();let calls=0;const service=new CredentialSessionService(api(async()=>{calls++;assert.ok(storage.value()?.active?.pendingRequestId);await delay();return session('two')}),storage,uuid);await service.adopt(session(),{profileId:'p'});assert.equal('refreshToken' in service.getSnapshot().session!,false);const [a,b]=await Promise.all([service.accessSession(true),service.accessSession(true)]);assert.equal(calls,1);assert.equal(a.accessToken,b.accessToken);assert.equal(storage.value()?.active?.pendingRequestId,undefined);assert.equal(service.getSnapshot().context?.profileId,'p')});
test('ambiguous rotation survives restart with exact receipt',async()=>{const storage=store();const ids:string[]=[];const first=new CredentialSessionService(api(async(_token:string,id:string)=>{ids.push(id);throw new Error('network')}),storage,uuid);await first.adopt(session());await assert.rejects(first.accessSession(true));const second=new CredentialSessionService(api(async(_token:string,id:string)=>{ids.push(id);return session('two')}),storage,uuid);await second.accessSession();assert.equal(ids[0],ids[1]);assert.equal(ids.length,2)});
test('logout fences a late refresh and revokes without restoring session',async()=>{const storage=store();let resolve:any;const revoked:string[]=[];const service=new CredentialSessionService(api(()=>new Promise(r=>resolve=r),async token=>{revoked.push(token)}),storage,uuid);await service.adopt(session());const pending=service.accessSession(true);await delay();const logout=service.logout();assert.equal(service.getSnapshot().phase,'signedOut');resolve(session('two'));await assert.rejects(pending);await logout;await delay();assert.equal(service.getSnapshot().phase,'signedOut');assert.equal(storage.value()?.active,undefined);assert.ok(revoked.includes('refresh-two'))});
test('storage failure before refresh blocks the network',async()=>{const storage=store();let calls=0,fail=false;const service=new CredentialSessionService(api(async()=>{calls++;return session('two')}),{load:storage.load,save:async v=>{if(fail)throw new Error('disk');await storage.save(v)}},uuid);await service.adopt(session());fail=true;await assert.rejects(service.accessSession(true));assert.equal(calls,0)});
test('cookie authority supports login callback inside complete coordinator lock',async()=>{let locked=false;let value:any;const coordinator={runExclusive:async<T>(fn:()=>Promise<T>)=>{assert.equal(locked,false);locked=true;try{return await fn()}finally{locked=false}}};const service=new CredentialSessionService({authorityFromSession:s=>({familyId:s.familyId}),refresh:async()=>{assert.ok(locked);return {...session('two'),refreshToken:undefined}},revoke:async()=>{assert.ok(locked)}},{load:async()=>value,save:async record=>{assert.ok(locked);value=structuredClone(record);if(value.active){delete value.active.session.refreshToken;value.active.session.accessToken='';}}},uuid,100,coordinator);await service.authenticate(async()=>{assert.ok(locked);return {...session(),refreshToken:undefined}});await service.accessSession(true);assert.equal(value.active.session.accessToken,'');assert.deepEqual(value.active.authority,{familyId:'family'})});
test('failed pair commit retains original receipt for retry',async()=>{const storage=store();let fail=false;const ids:string[]=[];const service=new CredentialSessionService(api(async(_token:string,id:string)=>{ids.push(id);fail=true;return session('two')}),{load:storage.load,save:async v=>{if(fail&&!v.active?.pendingRequestId){fail=false;throw new Error('atomic write failed')}await storage.save(v)}},uuid);await service.adopt(session());await assert.rejects(service.accessSession(true));await assert.rejects(service.accessSession(true));assert.equal(ids[0],ids[1]);assert.ok(storage.value()?.active?.pendingRequestId)});
test('late authentication after logout is revoked and cannot publish',async()=>{const storage=store();let resolve:any;const revoked:string[]=[];const service=new CredentialSessionService(api(async()=>session(),async token=>{revoked.push(token)}),storage,uuid,20);const pending=service.authenticate(()=>new Promise(r=>resolve=r));await delay();const logout=service.logout();resolve(session());await assert.rejects(pending);await logout;await delay();assert.equal(service.getSnapshot().phase,'signedOut');assert.ok(revoked.includes('refresh-one'))});

test('cookie family change clears stale local session and revokes only bound old authority',async()=>{
 let record:any;let currentCookieFamily='old';const revoked:string[]=[];
 const service=new CredentialSessionService({authorityFromSession:s=>({familyId:s.familyId!}),refresh:async()=>{throw new ApiError(409,'family_changed','Session changed.')},revoke:async authority=>{revoked.push(authority.familyId);if(currentCookieFamily===authority.familyId)currentCookieFamily=''}},{load:async()=>structuredClone(record),save:async v=>{record=structuredClone(v)}},uuid);
 await service.adopt({...session(),familyId:'old',refreshToken:undefined});currentCookieFamily='new';
 await assert.rejects(service.accessSession(true));assert.equal(service.getSnapshot().phase,'signedOut');assert.equal(record.active,undefined);assert.deepEqual(revoked,['old']);assert.equal(currentCookieFamily,'new');
});

test('context storage reload cannot publish a signed-in snapshot after immediate logout',async()=>{
 const storage=store();let unblock:(()=>void)|undefined;let gate=false;
 const service=new CredentialSessionService(api(async()=>session()),{load:async()=>{const captured=await storage.load();if(gate){gate=false;await new Promise<void>(resolve=>unblock=resolve)}return captured},save:storage.save},uuid);
 await service.adopt(session());gate=true;const update=service.updateContext({profileId:'other'});await delay();const logout=service.logout();const seen:string[]=[];service.subscribe(()=>seen.push(service.getSnapshot().phase));assert.equal(service.getSnapshot().phase,'signedOut');unblock!();await update;await logout;assert.ok(seen.every(phase=>phase==='signedOut'));assert.equal(storage.value()?.active,undefined);
});

test('directory receipt fields do not corrupt persisted credential selection',async()=>{
 const storage=store();const service=new CredentialSessionService(api(async()=>session()),storage,uuid);
 await service.adopt(session());
 const server={id:'server',name:'Demo',baseUrl:'https://demo.example.com',publicKey:'key',policyRevision:1,endpointVerified:true,challengeId:'receipt'};
 await service.updateContext({profileId:'p',server});
 const {parseNativeCredentialRecord}=await import('../src/credential-format.ts');
 const restored=parseNativeCredentialRecord(storage.value());
 assert.equal(restored.active?.context.server?.id,'server');
 assert.equal('endpointVerified' in restored.active!.context.server!,false);
});

test('Hosted credentials contain no profiles; the server account keeps its eight-profile cap',async()=>{
 const {parseNativeCredentialRecord}=await import('../src/credential-format.ts');
 const {parseManagedProfiles}=await import('../src/profile-management.ts');
 const current={version:1,revocations:[],active:{session:session(),authority:'refresh-one',context:{}}};
 assert.equal(Object.hasOwn(parseNativeCredentialRecord(current).active!.session,'profiles'),false);
 for(const profiles of [[],[{id:'p',name:'P'}]])assert.throws(()=>parseNativeCredentialRecord({...current,active:{...current.active,session:{...current.active.session,profiles}}}));
 const eight=Array.from({length:8},(_,i)=>({id:`profile-${i}`,name:`Profile ${i}`,primary:i===0,position:i,art:'blue',revision:1,pinRevision:1,pinRequired:false}));
 assert.equal(parseManagedProfiles(eight).length,8);
 assert.throws(()=>parseManagedProfiles([...eight,{id:'profile-8',name:'P8',primary:false,position:8,art:'blue',revision:1,pinRevision:1,pinRequired:false}]));
});

test('a browser credential Hosted refuses (CSRF, origin, mode) ends the sign-in; other refusals do not',async()=>{
 for(const [status,code,signedOut] of [[403,'csrf_required',true],[403,'origin_denied',true],[403,'session_mode_denied',true],[403,'forbidden',false],[503,'unavailable',false]] as const){
  const storage=store();
  const service=new CredentialSessionService(api(async()=>{throw new ApiError(status,code,'refused')}),storage,uuid);
  await service.adopt(session());
  await assert.rejects(service.accessSession(true));
  assert.equal(service.getSnapshot().phase==='signedOut',signedOut,code);
  assert.equal(storage.value()?.active===undefined,signedOut,code);
 }
});


test('a failed initial credential read can be retried without discarding the saved family or revocations', async () => {
 const storage=store();const saved:CredentialRecord={version:1,active:{session:session(),authority:'refresh-one',context:{profileId:'p'}},revocations:['retired']};
 await storage.save(saved);let loads=0,available=false;
 const service=new CredentialSessionService(api(async()=>session()),{load:async()=>{loads++;if(!available)throw new Error('secure store locked');return storage.load();},save:storage.save},uuid);
 await assert.rejects(service.synchronize(),/secure store locked/);
 assert.equal(service.getSnapshot().phase,'error');assert.deepEqual(storage.value(),saved);
 available=true;await service.synchronize();
 assert.equal(service.getSnapshot().phase,'ready');assert.equal(service.getSnapshot().session?.accessToken,'access-one');
 assert.equal(service.getSnapshot().pendingRevocations,1);assert.deepEqual(storage.value(),saved);assert.ok(loads>1);
});

test('authentication retries initial loading and concurrent recovery shares the in-flight load', async () => {
 const storage=store(),release=held<void>();let loads=0,initial=true,callbacks=0;
 const service=new CredentialSessionService(api(async()=>session()),{load:async()=>{loads++;if(initial)throw new Error('locked');if(loads===2)await release.promise;return storage.load();},save:storage.save},uuid);
 await assert.rejects(service.synchronize(),/locked/);initial=false;
 const a=service.synchronize(),b=service.synchronize();
 await new Promise(r=>setImmediate(r));assert.equal(loads,2,'one shared retry is waiting for storage');
 release.resolve();await Promise.all([a,b]);
 await service.authenticate(async()=>{callbacks++;return session();});
 assert.equal(callbacks,1);assert.equal(service.getSnapshot().phase,'ready');assert.equal(storage.value()?.active?.session.accessToken,'access-one');
});

test('authentication after a failed initial read never calls the account service while storage remains unknown', async () => {
 const storage=store();let available=false,calls=0;
 const service=new CredentialSessionService(api(async()=>session()),{load:async()=>{if(!available)throw new Error('locked');return storage.load();},save:storage.save},uuid);
 await assert.rejects(service.synchronize(),/locked/);
 await assert.rejects(service.authenticate(async()=>{calls++;return session();}),/locked/);
 assert.equal(calls,0);assert.equal(service.getSnapshot().phase,'error');assert.equal(storage.value(),undefined);
 available=true;await service.authenticate(async()=>{calls++;return session();});
 assert.equal(calls,1);assert.equal(service.getSnapshot().phase,'ready');
});
