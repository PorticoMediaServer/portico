import test from 'node:test';
import assert from 'node:assert/strict';
import {IdentityClient, type IdentityApi} from '../src/identity-client.ts';

const valid=['AAAA-BBBB-CCCC','DDDD-EEEE-FFFF'];
test('local replacement returns only the actual response codes under the exact proof and signal',async()=>{
 const calls:unknown[]=[];const abort=new AbortController();
 const client=new IdentityClient({request:async <T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>{calls.push({path,method,body,signal});return {codes:valid} as T;}});
 const result=await client.replaceRecoveryCodes(' original password ','AAAA-BBBB-CCCC',abort.signal);
 assert.deepEqual(calls,[{path:'/v1/direct/two-factor/recovery-codes',method:'POST',body:{password:' original password ',code:'AAAA-BBBB-CCCC'},signal:abort.signal}]);
 assert.deepEqual(result,valid);assert.equal(Object.isFrozen(result),true);
});
for(const raw of [null,[],{}, {codes:[]},{codes:valid,token:'unexpected'},{codes:['same-code','same-code']},{codes:['with spaces']},{codes:[1234]}])test('malformed code receipt cannot be shown: '+JSON.stringify(raw),async()=>{
 const client=new IdentityClient({request:async()=>raw} as IdentityApi);
 await assert.rejects(client.replaceRecoveryCodes('password','code'));
});
