import test from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {mkdtempSync,readFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {decode} from '../src/runtime.ts';
import {decode as hostedDecode} from '../src/hosted/runtime.ts';
import {validateRegisteredReturnLocation} from '../../client-core/src/same-tab-return.ts';
import {parseDirectSignIn} from '../../client-core/src/profile-management.ts';
test('actual PostgreSQL/HTTP Hosted return passes generated producer and server/client consumers',{skip:!process.env.PORTICO_RETURN_HOSTED_CHECKOUT},()=>{
 const dir=mkdtempSync(join(tmpdir(),'portico-return-contract-'));
 try{const path=join(dir,'consumer.json');execFileSync('go',['test','./internal/hosted','-run','^TestRegisteredReturnActualHostedProducerConsumer$','-count=1'],{cwd:fileURLToPath(new URL('../../../server/',import.meta.url)),env:{...process.env,PORTICO_RETURN_CONSUMER_JSON:path},timeout:180_000,stdio:'pipe'});
 const actual=JSON.parse(readFileSync(path,'utf8'));assert.ok(hostedDecode('ServerReturnExchangeResponse',actual.producer.grant));
 const completion=hostedDecode('ServerReturnCompleteResponse',actual.producer.completion) as {location:string};assert.equal(validateRegisteredReturnLocation(completion.location,{id:actual.producer.request.registrationId,origin:'https://server.example:32500'},'s'.repeat(43),actual.producer.start.transactionId),completion.location);
 const parsed=parseDirectSignIn(decode('PostAuthPorticoReturnExchangeResponse',actual.consumer));assert.equal(parsed.kind,'signed-in');if(parsed.kind==='signed-in'){assert.equal(parsed.snapshot.account.hostedAccountId,'strict-owner');assert.equal(parsed.snapshot.account.role,'owner');assert.equal(parsed.resetFileOwnerSession,undefined);}
 }finally{rmSync(dir,{recursive:true,force:true})}
});
