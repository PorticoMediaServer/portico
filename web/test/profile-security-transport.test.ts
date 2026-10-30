import test from 'node:test';
import assert from 'node:assert/strict';
import type {ProfileAPI} from '@core/profile-management.ts';
import {profileSecurityTransport} from '../src/screens/settings/account-profile/security-transport.ts';

test('only exact original local security commands may keep known terminal acknowledgements',async()=>{
 let live=true,calls=0;let complete!:(value:unknown)=>void;
 const build=new AbortController(),failure=new Error('original retired');
 const original:ProfileAPI={request:async <T>()=>{calls++;return await new Promise(resolve=>{complete=resolve;}) as T;}};
 const api=profileSecurityTransport(original,()=>{if(!live)throw failure;},input=>input?AbortSignal.any([build.signal,input]):build.signal);
 for(const [path,method] of [['/v1/direct/two-factor','GET'],['/v1/direct/password','GET'],['/v1/admin/api-keys','POST']])await assert.rejects(api.request(path,method));
 assert.equal(calls,0);
 const pending=api.request('/v1/direct/password','POST',{currentPassword:'original',newPassword:'replacement'});
 assert.equal(calls,1);live=false;build.abort();complete(undefined);
 assert.equal(await pending,undefined,'known original acknowledgement is not erased by the resulting view retirement');
 await assert.rejects(api.request('/v1/direct/password','POST',{}),error=>error===failure);
 assert.equal(calls,1,'retired source cannot send another request');
});
test('an uncertain terminal command remains an error without replay or a guessed acknowledgement',async()=>{
 let calls=0;const failure=new Error('response lost');
 const api=profileSecurityTransport({request:async()=>{calls++;throw failure;}},()=>{},()=>new AbortController().signal);
 await assert.rejects(api.request('/v1/direct/two-factor','DELETE',{}),error=>error===failure);
 assert.equal(calls,1);
});
