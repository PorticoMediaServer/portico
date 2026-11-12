import test from 'node:test';
import assert from 'node:assert/strict';
import {localOwnerChangePath, transferLocalOwnership} from '../src/portico-servers.ts';

const member='member_1', password='  two  spaces  ', revision=4;
function recorder(reply: unknown = undefined) {
 const calls: {path: string; method?: string; body?: unknown; signal?: AbortSignal}[]=[];
 const api={request:async(path: string,method?: string,body?: unknown,signal?: AbortSignal)=>{calls.push({path,method,body,signal});return reply;}};
 return {api:api as never,calls};
}

test('Change owner on a local server sends the password, the new owner and the previous owner\'s new role (area 2 rules 32, 33)',async()=>{
 const {api,calls}=recorder(),controller=new AbortController();
 const out=await transferLocalOwnership(api,member,password,revision,{previousOwner:'admin'},controller.signal);
 assert.equal(out,undefined);
 assert.equal(calls.length,1);
 assert.equal(calls[0]!.path,'/v1/admin/owner'); assert.equal(localOwnerChangePath,'/v1/admin/owner');
 assert.equal(calls[0]!.method,'POST');
 assert.equal(calls[0]!.signal,controller.signal);
 assert.deepEqual(calls[0]!.body,{password,accountId:member,previousOwner:'admin'},'the previous owner becomes an administrator; no Portico approval or other field');
});

test('the previous owner may instead stay as a member or leave the server; anything else sends nothing (area 2 rule 33)',async()=>{
 for(const previousOwner of ['member','removed'] as const){
  const {api,calls}=recorder();
  await transferLocalOwnership(api,member,password,revision,{previousOwner});
  assert.deepEqual(calls.map(call=>(call.body as {previousOwner?: string}).previousOwner),[previousOwner]);
 }
 const {api,calls}=recorder();
 await assert.rejects(()=>transferLocalOwnership(api,member,password,revision,{previousOwner:'owner'} as never),/previous owner/);
 assert.equal(calls.length,0);
});

test('the password keeps its spaces and accepts unicode up to the 72-byte bound',async()=>{
 for(const value of [password,'€'.repeat(24),'a'.repeat(72)]){
  const {api,calls}=recorder();
  await transferLocalOwnership(api,member,value,1,{previousOwner:'admin'});
  assert.equal((calls[0]!.body as {password:string}).password,value);
 }
 for(const value of ['€'.repeat(24)+'a','a'.repeat(73),'€'.repeat(25)]){
  const {api,calls}=recorder();
  await assert.rejects(()=>transferLocalOwnership(api,member,value,1,{previousOwner:'admin'}),/current password/);
  assert.equal(calls.length,0);
 }
});

test('an unsafe account id, revision or password sends no request',async()=>{
 for(const id of ['', 'a'.repeat(129), 'ab\n', 'a\nb', 'a b', 'a/b', '\u0000', 'é', null, undefined, 42]){
  const {api,calls}=recorder();
  await assert.rejects(()=>transferLocalOwnership(api,id as never,password,revision,{previousOwner:'admin'}),/target account/);
  assert.equal(calls.length,0);
 }
 for(const expected of [0,-1,1.5,NaN,Infinity,'1',null,undefined]){
  const {api,calls}=recorder();
  await assert.rejects(()=>transferLocalOwnership(api,member,password,expected as never,{previousOwner:'admin'}),/owner revision/);
  assert.equal(calls.length,0);
 }
 for(const value of ['', 'a'.repeat(73), '\uD800', 'a\uDC00', '\uDC00', null, undefined, 42]){
  const {api,calls}=recorder();
  await assert.rejects(()=>transferLocalOwnership(api,member,value as never,revision,{previousOwner:'admin'}),/current password/);
  assert.equal(calls.length,0);
 }
});

test('an already-aborted lifetime sends nothing and keeps AbortError identity',async()=>{
 const {api,calls}=recorder(),controller=new AbortController();
 controller.abort();
 await assert.rejects(()=>transferLocalOwnership(api,member,password,revision,{previousOwner:'admin'},controller.signal),error=>error instanceof Error&&error.name==='AbortError');
 assert.equal(calls.length,0);
});

test('one call, the API error keeps its identity and there is no Hosted or custody fallback',async()=>{
 let calls=0,paths:string[]=[];
 const failure=Object.assign(new Error('denied'),{code:'not_permitted',status:403});
 const api={request:async(path: string)=>{calls++;paths.push(path);throw failure;}} as never;
 await assert.rejects(()=>transferLocalOwnership(api,member,password,revision,{previousOwner:'admin'}),error=>error===failure);
 assert.equal(calls,1);
 assert.deepEqual(paths,['/v1/admin/owner']);
});

test('new local owner is one closed atomic ownership request with captured scalars',async()=>{
 const {transferLocalOwnershipToNewAccount}=await import('../src/portico-servers.ts');
 const {api,calls}=recorder(), abort=new AbortController();
 const newAccount={username:'newowner',name:'New owner',password:'New-password2!'};
 await transferLocalOwnershipToNewAccount(api,newAccount,password,revision,{previousOwner:'admin'},abort.signal);
 assert.deepEqual(calls,[{path:'/v1/admin/owner',method:'POST',body:{password,newAccount:{username:'newowner',password:'New-password2!',profileName:'New owner'},previousOwner:'admin'},signal:abort.signal}]);
 assert.equal(Object.hasOwn(calls[0]!.body as object,'approval'),false);
});

test('invalid new account, current proof or retired command never creates a member',async()=>{
 const {transferLocalOwnershipToNewAccount}=await import('../src/portico-servers.ts');
 const valid={username:'newowner',name:'New owner',password:'New-password2!'};
 for(const change of [{username:'bad user'},{name:'\n'},{password:'short'},{password:'x'.repeat(73)}]){
  const {api,calls}=recorder();
  await assert.rejects(transferLocalOwnershipToNewAccount(api,{...valid,...change},password,revision,{previousOwner:'admin'}));assert.equal(calls.length,0);
 }
 const {api,calls}=recorder(), abort=new AbortController();abort.abort();
 await assert.rejects(transferLocalOwnershipToNewAccount(api,valid,password,revision,{previousOwner:'admin'},abort.signal));
 await assert.rejects(transferLocalOwnershipToNewAccount(api,valid,'',revision,{previousOwner:'admin'}));
 assert.equal(calls.length,0);
});

test('an owner whose account uses two-step sign-in sends their code with the password; an empty one is left out', async()=>{
 const {api,calls}=recorder();
 await transferLocalOwnership(api,member,password,revision,{previousOwner:'admin',code:' 123456 '});
 await transferLocalOwnership(api,member,password,revision,{previousOwner:'admin',code:'  '});
 assert.deepEqual(calls.map(call=>call.body),[{password,accountId:member,previousOwner:'admin',code:'123456'},{password,accountId:member,previousOwner:'admin'}]);
});
