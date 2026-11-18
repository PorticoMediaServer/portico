import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
test('certificate session subscriptions invalidate cached owner UI across token changes',()=>{
 const api=new HttpLocalApi('https://server.example:32500','a');
 const seen:number[]=[];
 const unsubscribe=api.subscribeCertificateSession(()=>seen.push(api.getCertificateSessionEpoch()));
 api.setAccessToken('b');api.setAccessToken('a');api.setAccessToken('a');
 assert.deepEqual(seen,[1,2]);
 unsubscribe();api.setAccessToken('');assert.deepEqual(seen,[1,2]);
});
