import test from 'node:test';
import assert from 'node:assert/strict';
import {transferLocalOwnership} from '../src/portico-servers.ts';
import {routePath} from '../../contracts/src/routes.ts';

const member='member_1', password='  two  spaces  ', revision=4;
function recorder(reply: unknown = undefined) {
 const calls: {path: string; method?: string; body?: unknown; signal?: AbortSignal}[]=[];
 const api={request:async(path: string,method?: string,body?: unknown,signal?: AbortSignal)=>{calls.push({path,method,body,signal});return reply;}};
 return {api:api as never,calls};
}

test('the local transfer sends only the three scalars on the same ownership route',async()=>{
 const {api,calls}=recorder(),controller=new AbortController();
 const out=await transferLocalOwnership(api,member,password,revision,controller.signal);
 assert.equal(out,undefined);
 assert.equal(calls.length,1);
 assert.equal(calls[0]!.path,routePath('post_direct_ownership'));
 assert.equal(calls[0]!.method,'POST');
 assert.equal(calls[0]!.signal,controller.signal);
 assert.deepEqual(calls[0]!.body,{accountId:member,currentPassword:password,expectedRevision:revision});
 assert.equal(Object.keys(calls[0]!.body as object).sort().join(','),'accountId,currentPassword,expectedRevision','no porticoIdentity or other field');
});

test('the password keeps its spaces and accepts unicode up to the 72-byte bound',async()=>{
 for(const value of [password,'€'.repeat(24),'a'.repeat(72)]){
  const {api,calls}=recorder();
  await transferLocalOwnership(api,member,value,1);
  assert.equal((calls[0]!.body as {currentPassword:string}).currentPassword,value);
 }
 for(const value of ['€'.repeat(24)+'a','a'.repeat(73),'€'.repeat(25)]){
  const {api,calls}=recorder();
  await assert.rejects(()=>transferLocalOwnership(api,member,value,1),/current password/);
  assert.equal(calls.length,0);
 }
});

test('an unsafe account id, revision or password sends no request',async()=>{
 for(const id of ['', 'a'.repeat(129), 'ab\n', 'a\nb', 'a b', 'a/b', '\u0000', 'é', null, undefined, 42]){
  const {api,calls}=recorder();
  await assert.rejects(()=>transferLocalOwnership(api,id as never,password,revision),/target account/);
  assert.equal(calls.length,0);
 }
 for(const expected of [0,-1,1.5,NaN,Infinity,'1',null,undefined]){
  const {api,calls}=recorder();
  await assert.rejects(()=>transferLocalOwnership(api,member,password,expected as never),/owner revision/);
  assert.equal(calls.length,0);
 }
 for(const value of ['', 'a'.repeat(73), '\uD800', 'a\uDC00', '\uDC00', null, undefined, 42]){
  const {api,calls}=recorder();
  await assert.rejects(()=>transferLocalOwnership(api,member,value as never,revision),/current password/);
  assert.equal(calls.length,0);
 }
});

test('an already-aborted lifetime sends nothing and keeps AbortError identity',async()=>{
 const {api,calls}=recorder(),controller=new AbortController();
 controller.abort();
 await assert.rejects(()=>transferLocalOwnership(api,member,password,revision,controller.signal),error=>error instanceof Error&&error.name==='AbortError');
 assert.equal(calls.length,0);
});

test('one call, the API error keeps its identity and there is no Hosted or custody fallback',async()=>{
 let calls=0,paths:string[]=[];
 const failure=Object.assign(new Error('denied'),{code:'not_permitted',status:403});
 const api={request:async(path: string)=>{calls++;paths.push(path);throw failure;}} as never;
 await assert.rejects(()=>transferLocalOwnership(api,member,password,revision),error=>error===failure);
 assert.equal(calls,1);
 assert.deepEqual(paths,[routePath('post_direct_ownership')]);
});

test('new local owner is one closed atomic ownership request with captured scalars',async()=>{
 const {transferLocalOwnershipToNewAccount}=await import('../src/portico-servers.ts');
 const {api,calls}=recorder(), abort=new AbortController();
 const newAccount={username:'newowner',name:'New owner',password:'New-password2!'};
 await transferLocalOwnershipToNewAccount(api,newAccount,password,revision,abort.signal);
 assert.deepEqual(calls,[{path:routePath('post_direct_ownership'),method:'POST',body:{accountId:'',currentPassword:password,expectedRevision:revision,newAccount},signal:abort.signal}]);
 assert.equal(Object.hasOwn(calls[0]!.body as object,'porticoIdentity'),false);
});

test('invalid new account, current proof or retired command never creates a member',async()=>{
 const {transferLocalOwnershipToNewAccount}=await import('../src/portico-servers.ts');
 const valid={username:'newowner',name:'New owner',password:'New-password2!'};
 for(const change of [{username:'bad user'},{name:'\n'},{password:'short'},{password:'x'.repeat(73)}]){
  const {api,calls}=recorder();
  await assert.rejects(transferLocalOwnershipToNewAccount(api,{...valid,...change},password,revision));assert.equal(calls.length,0);
 }
 const {api,calls}=recorder(), abort=new AbortController();abort.abort();
 await assert.rejects(transferLocalOwnershipToNewAccount(api,valid,password,revision,abort.signal));
 await assert.rejects(transferLocalOwnershipToNewAccount(api,valid,'',revision));
 assert.equal(calls.length,0);
});
