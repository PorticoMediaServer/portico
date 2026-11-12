import test from 'node:test';
import assert from 'node:assert/strict';
import {makeMemberResetCode,replaceMemberResetCode} from '../src/account-reset.ts';

const fresh='K7QP-2MXD-9HRT', proof='4WNC-8ZTA-3MQX', until=new Date(Date.now()+60_000).toISOString();
function recorder(reply: unknown = {code: fresh, expiresAt: until}) {
 const calls: {path: string; method?: string; body?: unknown; signal?: AbortSignal}[]=[];
 const api={request:async(path: string,method?: string,body?: unknown,signal?: AbortSignal)=>{calls.push({path,method,body,signal});return reply;}};
 return {api:api as never,calls};
}

test('making a reset code is one request to the member\'s password reset, with the two-step choice', async () => {
 const {api,calls}=recorder(),signal=new AbortController().signal;
 const out=await makeMemberResetCode(api,'member 1',3,false,signal);
 assert.deepEqual(calls,[{path:'/v1/admin/members/member%201/password-reset',method:'POST',body:{resetTwoFactor:false},signal}]);
 assert.deepEqual(out,{code:fresh,expiresAt:until}); assert.equal(Object.isFrozen(out),true);
});

test('changing the two-step choice makes a new code on the same route, and needs the code shown as its proof', async () => {
 const {api,calls}=recorder();
 assert.equal((await replaceMemberResetCode(api,'member',7,proof,true)).code,fresh);
 assert.deepEqual(calls[0]!.body,{resetTwoFactor:true});
 for(const original of ['',' '+proof,proof+' ',proof.slice(1),'r'.repeat(43),null,undefined,42])
  await assert.rejects(()=>replaceMemberResetCode(api,'member',1,original as never,false),/original reset code/);
 for(const choice of [undefined,null,1,'true'])await assert.rejects(()=>replaceMemberResetCode(api,'member',1,proof,choice as never),/two-step choice/);
 assert.equal(calls.length,1,'a refused proof or choice sends nothing');
});

test('an API refusal keeps its identity and is never retried, and an unreadable code is not a success', async () => {
 let calls=0;
 const failure=Object.assign(new Error('refused'),{code:'not_permitted',status:403});
 await assert.rejects(()=>makeMemberResetCode({request:async()=>{calls++;throw failure;}} as never,'member',1),error=>error===failure);
 assert.equal(calls,1);
 for(const reply of [{code:'short',expiresAt:until},{code:fresh,expiresAt:'not-a-date'},{code:fresh+'\n',expiresAt:until},{code:'r'.repeat(43),expiresAt:until}])
  await assert.rejects(()=>makeMemberResetCode(recorder(reply).api,'member',1),/member reset response/);
});
