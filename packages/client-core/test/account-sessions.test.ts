import test from 'node:test';import assert from 'node:assert/strict';
import {AccountSessionReviewService,accountSessionDevice,type AccountSessionReviewApi} from '../src/account-sessions.ts';
import {resetSkippedParts,skippedParts} from '../src/tolerant.ts';
const scope={accountId:'account',familyId:'current'};
const row={id:'other',mode:'native',createdAt:'2026-09-05T10:00:00Z',expiresAt:'2026-09-12T10:00:00Z',status:'active',current:false};
const current={...row,id:'current',mode:'browser',current:true};
const page=(items:unknown[]=[current,row],nextCursor='')=>({accountId:'account',currentFamilyId:'current',observedAt:'2026-09-05T12:00:00Z',items,nextCursor});
const api=(fn:(p:string,m?:string)=>Promise<unknown>):AccountSessionReviewApi=>({request:fn as AccountSessionReviewApi['request']});
const deferred=()=>{let resolve!:(v:unknown)=>void;return {promise:new Promise(r=>resolve=r),resolve};};
test('records only whitelisted facts and separates current from other session revocation',async()=>{
 let revoked=false;const service=new AccountSessionReviewService({scope,api:api(async(p,m)=>{if(m==='DELETE'){revoked=true;return {id:'other',revoked:true,current:false};}return page([current,{...row,status:revoked?'revoked':'active',refreshToken:'never retain'}]);})});await service.refresh();assert.equal('refreshToken' in service.getSnapshot().items[1],false);await service.revoke('other');assert.equal(service.getSnapshot().phase,'ready');assert.equal(service.getSnapshot().items[1].status,'revoked');assert.equal(service.getSnapshot().items[0].current,true);assert.throws(()=>service.revoke('other'),/Refresh/);service.dispose();
});
test('current-family receipt ends local review without issuing an unauthorized follow-up read',async()=>{
 let reads=0;const service=new AccountSessionReviewService({scope,api:api(async(p,m)=>m==='DELETE'?{id:'current',revoked:true,current:true}:(reads++,page()))});await service.refresh();await service.revoke('current');assert.equal(service.getSnapshot().phase,'ended');assert.equal(service.getSnapshot().items.length,0);assert.equal(reads,1);service.dispose();
});
test('expired authority ends view without claiming the requested remote operation succeeded',async()=>{
 const service=new AccountSessionReviewService({scope,api:api(async(p,m)=>{if(m==='DELETE')throw Object.assign(new Error('expired'),{status:401});return page();})});await service.refresh();await service.revoke('other');assert.equal(service.getSnapshot().phase,'ended');assert.match(service.getSnapshot().notice,/Sign in again/);assert.doesNotMatch(service.getSnapshot().notice,/revoked|removed/i);service.dispose();
});
test('read failure removes stale controls and preserves true error instead of empty success',async()=>{
 let fail=false;const service=new AccountSessionReviewService({scope,api:api(async()=>{if(fail)throw new Error('offline');return page();})});await service.refresh();fail=true;await service.refresh();assert.equal(service.getSnapshot().phase,'error');assert.equal(service.getSnapshot().items.length,0);assert.throws(()=>service.revoke('other'),/Refresh/);service.dispose();
});
test('cross-account, wrong current-family and invented current markers fail closed',async()=>{
 for(const raw of [{...page(),accountId:'elsewhere'},{...page(),currentFamilyId:'different'},page([{...row,current:true}])]){const service=new AccountSessionReviewService({scope,api:api(async()=>raw)});await service.refresh();assert.equal(service.getSnapshot().error?.code,'invalid_session_review');assert.equal(service.getSnapshot().items.length,0);service.dispose();}
});
test('late account-view completion cannot publish a current-session end',async()=>{
 const late=deferred();const service=new AccountSessionReviewService({scope,api:api(async(p,m)=>m==='DELETE'?late.promise:page())});await service.refresh();const pending=service.revoke('current');service.dispose();late.resolve({id:'current',revoked:true,current:true});await pending;assert.notEqual(service.getSnapshot().phase,'ended');
});
test('deadline retains exact target for safe retry and rejects another overlapping intent',async()=>{
 const commands:string[]=[];let first=true;const never=deferred();const service=new AccountSessionReviewService({scope,timeoutMs:10,api:api(async(p,m)=>{if(m==='DELETE'){commands.push(p);if(first){first=false;return never.promise;}return {id:'other',revoked:true,current:false};}return page();})});await service.refresh();const pending=service.revoke('other');assert.equal(service.revoke('other'),pending);assert.throws(()=>service.revoke('current'),/Wait/);await pending;assert.equal(service.getSnapshot().retryId,'other');assert.throws(()=>service.revoke('current'),/Resolve/);await service.retryRevoke();assert.equal(commands.length,2);assert.equal(commands[0],commands[1]);assert.equal(service.getSnapshot().retryId,null);service.dispose();never.resolve({id:'other',revoked:true,current:false});
});
test('pagination replaces one40row page and refresh resets continuation',async()=>{
 const forty=Array.from({length:40},(_,n)=>({...row,id:'page-'+n}));const paths:string[]=[];const service=new AccountSessionReviewService({scope,api:api(async p=>{paths.push(p);return p.includes('?')?page([row]):page(forty,'next-page');})});await service.refresh();await service.next();assert.equal(service.getSnapshot().items.length,1);assert.equal(service.getSnapshot().hasPrevious,true);await service.previous();assert.equal(service.getSnapshot().items.length,40);await service.refresh();assert.equal(service.getSnapshot().hasPrevious,false);assert.equal(paths.at(-1),'/v1/account/sessions');service.dispose();
});
test('expired/revoked rows cannot be acted on and malformed receipts do not claim success',async()=>{
 const service=new AccountSessionReviewService({scope,api:api(async(p,m)=>m==='DELETE'?{id:'someone-else',revoked:true,current:false}:page([current,{...row,status:'expired'}]))});await service.refresh();assert.throws(()=>service.revoke('other'),/Refresh/);await service.revoke('current');assert.equal(service.getSnapshot().mutationError?.code,'invalid_session_review');assert.equal(service.getSnapshot().retryId,'current');assert.equal(service.getSnapshot().notice,'');service.dispose();
});

test('obsolete review field is refused even when false',async()=>{
 const service=new AccountSessionReviewService({scope,api:api(async()=>({...page(),legacySessionsPresent:false}))});
 await service.refresh();assert.equal(service.getSnapshot().error?.code,'invalid_session_review');service.dispose();
});

const review=async(raw:unknown)=>{const service=new AccountSessionReviewService({scope,api:api(async()=>raw)});await service.refresh();const snapshot=service.getSnapshot();service.dispose();return snapshot;};
test('a field this app does not know is ignored and never kept',async()=>{
 const snapshot=await review({...page([{...current,device:{name:'Den'}},row]),retentionDays:30});
 assert.equal(snapshot.phase,'ready');assert.deepEqual(snapshot.items.map(item=>item.id),['current','other']);assert.equal('device' in snapshot.items[0],false);
});
test('another sign-in that cannot be read is left out and the rest of the list is kept',async()=>{
 resetSkippedParts();
 const snapshot=await review(page([current,{...row,id:'undated',createdAt:'yesterday'},'not a row',{...row,id:'revoked',status:'active',revokedAt:'2026-09-06T10:00:00Z'},{...row,id:''},row,{...row,status:'expired'}]));
 assert.equal(snapshot.phase,'ready');assert.deepEqual(snapshot.items.map(item=>item.id),['current','other']);assert.equal(snapshot.items[1].status,'active');
 assert.deepEqual(skippedParts(),[{document:'account-sessions',part:'items',count:5}]);
});
test('another sign-in is never hidden for being of a kind or in a state this app does not know',async()=>{
 const snapshot=await review(page([current,{...row,id:'tv',mode:'television'},{...row,id:'paused',status:'suspended'},{...row,id:'ended',status:'withdrawn',revokedAt:'2026-09-06T10:00:00Z'}]));
 assert.equal(snapshot.phase,'ready');
 assert.deepEqual(snapshot.items.map(item=>[item.id,item.mode,item.status]),[['current','browser','active'],['tv','native','active'],['paused','native','active'],['ended','native','revoked']]);
});
test('this sign-in is never left out or guessed at: its row is exact or the list is refused',async()=>{
 for(const items of [[{...current,mode:'television'},row],[{...current,status:'expired'},row],[{...current,current:false},row],[{...current,createdAt:'yesterday'},row],[current,{...row,current:'yes'}],[current,{...row,current:undefined}],[current,{...row,id:'',current:true}],[current,current,row]]){
  const snapshot=await review(page(items));assert.equal(snapshot.error?.code,'invalid_session_review');assert.equal(snapshot.items.length,0);
 }
});
test('the page size, continuation and observation time are still exact',async()=>{
 const forty=Array.from({length:40},(_,n)=>({...row,id:'page-'+n}));
 for(const raw of [page([...forty,row]),page([current,row],'next-page'),page([current,row],'x'.repeat(2049)),{...page(),observedAt:'now'},{...page(),nextCursor:undefined},{...page(),items:{}},[]]){
  const snapshot=await review(raw);assert.equal(snapshot.error?.code,'invalid_session_review');assert.equal(snapshot.items.length,0);
 }
 // A full page keeps its continuation even when one of its rows was left out.
 const snapshot=await review(page([...forty.slice(1),{...row,id:'undated',createdAt:'yesterday'}],'next-page'));
 assert.equal(snapshot.phase,'ready');assert.equal(snapshot.items.length,39);assert.equal(snapshot.nextCursor,'next-page');
});

test('device facts are kept when reported and are explicit nulls when unknown or unreadable, never hiding the sign-in',async()=>{
 const snapshot=await review(page([{...current,deviceName:'Safari on a Mac',platform:'web',app:'Portico',lastUsedAt:'2026-09-05T11:00:00Z'},{...row,deviceName:null,platform:null,app:null,lastUsedAt:null},{...row,id:'odd',deviceName:'\u0000bad',platform:7,app:'',lastUsedAt:'yesterday'},{...row,id:'old'}]));
 assert.equal(snapshot.phase,'ready');assert.deepEqual(snapshot.items.map(item=>item.id),['current','other','odd','old']);
 assert.deepEqual([snapshot.items[0].deviceName,snapshot.items[0].platform,snapshot.items[0].app,snapshot.items[0].lastUsedAt],['Safari on a Mac','web','Portico','2026-09-05T11:00:00Z']);
 for(const item of snapshot.items.slice(1))assert.deepEqual([item.deviceName,item.platform,item.app,item.lastUsedAt],[null,null,null,null]);
});
test('a device is named from its reported facts, and an unknown name says only what the sign-in mode proves',async()=>{
 const snapshot=await review(page([{...current,deviceName:'Living room TV',platform:'tvos',app:'Portico'},{...row,platform:null,app:null},{...row,id:'phone',platform:'ios'},{...row,id:'custom',platform:'vega',app:'Portico for Fire TV'}]));
 const [tv,unknown,phone,custom]=snapshot.items.map(accountSessionDevice);
 assert.deepEqual(tv,{name:'Living room TV',app:'Portico on Apple TV',form:'tv'});
 assert.deepEqual(unknown,{name:'A Portico app',app:null,form:null});
 assert.equal(phone.form,'phone');assert.equal(phone.app,'Portico on iPhone');
 assert.deepEqual(custom,{name:'A Portico app',app:'Portico for Fire TV',form:null});
 assert.equal(accountSessionDevice(snapshot.items[0]).name,'Living room TV');
});

const receipt=(body:unknown,patch:Record<string,unknown>={})=>({accountId:'account',currentFamilyId:'current',operationId:(body as {operationId:string}).operationId,revokedCount:2,...patch});
test('sign out every other device is one request that keeps this sign-in, then rereads the list',async()=>{
 const calls:{path:string;method?:string;body?:unknown}[]=[];let ended=false;
 const service=new AccountSessionReviewService({scope,api:{request:(async(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});if(method==='POST'){ended=true;return receipt(body);}return page(ended?[current,{...row,status:'revoked',revokedAt:'2026-09-05T12:00:00Z'}]:[current,row]);}) as AccountSessionReviewApi['request']}});
 await service.refresh();await service.revokeOthers();
 const post=calls.filter(call=>call.method==='POST');assert.equal(post.length,1);assert.equal(post[0].path,'/v1/account/sessions/revoke-others');
 assert.deepEqual(Object.keys(post[0].body as object),['operationId']);assert.match((post[0].body as {operationId:string}).operationId,/^[0-9a-f-]{36}$/);
 assert.equal(calls.some(call=>call.method==='DELETE'),false);
 const after=service.getSnapshot();assert.equal(after.phase,'ready');assert.equal(after.endingOthers,false);assert.equal(after.retryOthers,false);assert.match(after.notice,/2 other devices/);
 assert.deepEqual(after.items.filter(item=>item.status==='active').map(item=>item.id),['current']);service.dispose();
});
test('an unconfirmed sign-out of other devices retries the same operation and blocks every other intent until then',async()=>{
 const ops:string[]=[];let fail=true;
 const service=new AccountSessionReviewService({scope,timeoutMs:20,api:{request:(async(path:string,method?:string,body?:unknown)=>{if(method==='POST'){ops.push((body as {operationId:string}).operationId);if(fail){fail=false;throw {status:503,code:'unavailable',retryable:true};}return receipt(body,{revokedCount:1});}return page();}) as AccountSessionReviewApi['request']}});
 await service.refresh();await service.revokeOthers();
 assert.equal(service.getSnapshot().retryOthers,true);assert.equal(service.getSnapshot().retryId,null);
 assert.throws(()=>service.revoke('other'),/Resolve/);assert.throws(()=>service.revokeOthers(),/Resolve/);
 await service.retryRevoke();
 assert.equal(ops.length,2);assert.equal(ops[0],ops[1]);assert.equal(service.getSnapshot().retryOthers,false);assert.equal(service.getSnapshot().mutationError,null);
 await service.revokeOthers();assert.equal(ops.length,3);assert.notEqual(ops[2],ops[0]);service.dispose();
});
test('a receipt for another account, another kept sign-in or another operation does not claim success',async()=>{
 for(const patch of [{accountId:'elsewhere'},{currentFamilyId:'other'},{operationId:'not-this-one'},{revokedCount:-1},{revokedCount:'2'}]){
  const service=new AccountSessionReviewService({scope,api:{request:(async(_p:string,method?:string,body?:unknown)=>method==='POST'?receipt(body,patch):page()) as AccountSessionReviewApi['request']}});
  await service.refresh();await service.revokeOthers();
  assert.equal(service.getSnapshot().mutationError?.code,'invalid_session_review',JSON.stringify(patch));assert.equal(service.getSnapshot().retryOthers,true);assert.equal(service.getSnapshot().notice,'');service.dispose();
 }
});
test('ended authority during sign-out of other devices ends the view without claiming success, and overlapping intents are refused',async()=>{
 const late=deferred();
 const service=new AccountSessionReviewService({scope,api:{request:(async(_p:string,method?:string)=>method==='POST'?late.promise:page()) as AccountSessionReviewApi['request']}});
 await service.refresh();const pending=service.revokeOthers();
 assert.equal(service.revokeOthers(),pending);assert.throws(()=>service.revoke('other'),/Wait/);assert.equal(service.getSnapshot().endingOthers,true);
 late.resolve(Promise.reject(Object.assign(new Error('expired'),{status:401})));await pending;
 assert.equal(service.getSnapshot().phase,'ended');assert.doesNotMatch(service.getSnapshot().notice,/Signed out of/);service.dispose();
});
