import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';
import {decode as hostedDecode} from '../src/hosted/runtime.ts';
const start={serverId:'srv_fixture',purpose:'setup',claimOperationId:'op',registrationId:'ret_fixture',state:'s'.repeat(43),codeChallenge:'c'.repeat(43),serverChallenge:'d'.repeat(32),installationId:'i'.repeat(43)};
test('actual Hosted generated request variants retain const, enum and string bounds',()=>{
 for(const purpose of ['setup','switch','change-owner'])assert.equal((hostedDecode('ServerReturnStartRequest',{...start,purpose}) as typeof start).purpose,purpose);
 for(const bad of [{...start,purpose:'custody'},{...start,state:'short'},{...start,state:'s'.repeat(129)},{...start,codeChallenge:'+'+'c'.repeat(42)}])assert.throws(()=>hostedDecode('ServerReturnStartRequest',bad));
 const exchange={code:'c'.repeat(43),transactionId:'srt_fixture',verifier:'v'.repeat(43),registrationId:'ret_fixture',serverChallenge:'d'.repeat(32),installationId:'i'.repeat(43),purpose:'setup'};
 assert.ok(hostedDecode('ServerReturnExchangeRequest',exchange));assert.throws(()=>hostedDecode('ServerReturnExchangeRequest',{...exchange,purpose:'custody'}));assert.throws(()=>hostedDecode('ServerReturnExchangeRequest',{...exchange,verifier:'short'}));
});
test('actual server generated location constrains the file name and reissue bounds, and no path',()=>{
 const location={fileName:'reset-code.txt',retryAfterSeconds:60};
 assert.ok(decode('PostAuthOwnerResetRequestResponse',location));
 for(const bad of [{...location,fileName:'secret.txt'},{...location,retryAfterSeconds:61},{fileName:'reset-code.txt'}])assert.throws(()=>decode('PostAuthOwnerResetRequestResponse',bad));
});
