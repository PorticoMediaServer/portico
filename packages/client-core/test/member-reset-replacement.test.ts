import test from 'node:test';
import assert from 'node:assert/strict';
import {makeMemberResetCode,replaceMemberResetCode} from '../src/account-reset.ts';
import {routePath} from '../../contracts/src/routes.ts';

const fresh='r'.repeat(43), proof='A-_'+'b'.repeat(40), until=new Date(Date.now()+60_000).toISOString();
function recorder(reply: unknown = {code: fresh, expiresAt: until}) {
 const calls: {path: string; method?: string; body?: unknown; signal?: AbortSignal}[]=[];
 const api={request:async(path: string,method?: string,body?: unknown,signal?: AbortSignal)=>{calls.push({path,method,body,signal});return reply;}};
 return {api:api as never,calls};
}

test('the replacement proof is forwarded exactly and untrimmed on the same route',async()=>{
 const {api,calls}=recorder(),signal=new AbortController().signal;
 const out=await replaceMemberResetCode(api,'member',7,proof,true,signal);
 assert.equal(calls.length,1);
 assert.equal(calls[0]!.path,routePath('post_direct_members_id_reset_code',{id:'member'}));
 assert.equal(calls[0]!.method,'POST');
 assert.equal(calls[0]!.signal,signal);
 assert.deepEqual(calls[0]!.body,{expectedRevision:7,turnOffTwoStep:true,originalResetCode:proof});
 assert.equal((calls[0]!.body as {originalResetCode:string}).originalResetCode,proof,'the original code is not trimmed or rewritten');
 assert.equal(out.code,fresh);assert.equal(out.expiresAt,until);assert.equal(Object.isFrozen(out),true);
});

test('a missing or malformed original proof sends no request at all',async()=>{
 const {api,calls}=recorder();
 for(const original of ['', ' '.repeat(43), ' '+proof, proof+' ', proof.slice(1), proof+'a', 'a'.repeat(42), 'a'.repeat(44),
  proof+'\n', proof+'\r', proof+'\u2028', proof+'\u2029', '\u0000'+proof.slice(1), proof.slice(0,42)+'\n', null, undefined, 42, {}, ['x']]){
  await assert.rejects(()=>replaceMemberResetCode(api,'member',1,original as never,false),/original reset code/);
 }
 assert.equal(calls.length,0,'a refused proof must not fall back to a fresh reset code');
});

test('an unstated two-step choice is refused before I/O',async()=>{
 const {api,calls}=recorder();
 for(const choice of [undefined,null,1,0,'false','true',{},[]]){
  await assert.rejects(()=>replaceMemberResetCode(api,'member',1,proof,choice as never),/two-step choice/);
 }
 assert.equal(calls.length,0);
});

test('an API refusal keeps its identity and is never retried or replaced',async()=>{
 let calls=0;
 const failure=Object.assign(new Error('refused'),{code:'not_permitted',status:403});
 const api={request:async()=>{calls++;throw failure;}} as never;
 await assert.rejects(()=>replaceMemberResetCode(api,'member',1,proof,false),error=>error===failure);
 assert.equal(calls,1);
});

test('the shared response parser refuses an invalid replacement and still serves freshMake',async()=>{
 for(const reply of [{code:'short',expiresAt:until},{code:fresh,expiresAt:'not-a-date'},{code:fresh,expiresAt:until,serverId:42},
  {code:fresh+'\n',expiresAt:until},{code:fresh+'\u2028',expiresAt:until},{code:'\u0000'+fresh.slice(1),expiresAt:until}]){
  const {api}=recorder(reply);
  await assert.rejects(()=>replaceMemberResetCode(api,'member',1,proof,false),/member reset response/);
 }
 const {api,calls}=recorder();
 await makeMemberResetCode(api,'member',3);
 assert.deepEqual(calls[0]!.body,{expectedRevision:3,turnOffTwoStep:false});
 assert.equal(Object.hasOwn(calls[0]!.body as object,'originalResetCode'),false,'freshMake omits the replacement proof');
});
