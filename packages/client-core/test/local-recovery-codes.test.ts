import test from 'node:test';
import assert from 'node:assert/strict';
import {IdentityClient, type IdentityApi} from '../src/identity-client.ts';

const valid=['AAAA-BBBB-CCCC','DDDD-EEEE-FFFF'];
test('new recovery codes are asked for with the password and a code, and only the codes answered are shown',async()=>{
 const calls:unknown[]=[];const abort=new AbortController();
 const client=new IdentityClient({request:async <T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>{calls.push({path,method,body,signal});return {recoveryCodes:valid,later:'a field this app does not know'} as T;}});
 const result=await client.replaceRecoveryCodes(' original password ','AAAA-BBBB-CCCC',abort.signal);
 assert.deepEqual(calls,[{path:'/v1/me/two-factor/recovery-codes',method:'POST',body:{currentPassword:' original password ',code:'AAAA-BBBB-CCCC'},signal:abort.signal}]);
 assert.deepEqual(result,valid);assert.equal(Object.isFrozen(result),true);
});
for(const raw of [null,[],{},{recoveryCodes:[]},{recoveryCodes:['same-code','same-code']},{recoveryCodes:['with spaces']},{recoveryCodes:[1234]}])test('codes that cannot be shown are refused: '+JSON.stringify(raw),async()=>{
 const client=new IdentityClient({request:async()=>raw} as IdentityApi);
 await assert.rejects(client.replaceRecoveryCodes('password','code'));
});
