import test from 'node:test';
import assert from 'node:assert/strict';
import type {browserAccount} from '../src/platform/account.ts';
import {createAccountServerAccess} from '../src/platform/account-server-access.ts';

function fixture(){
  let family='actual-family',gate:Promise<void>|undefined,release!:()=>void,started!:()=>void;
  const listeners=new Set<()=>void>(),calls:unknown[][]=[];
  const account={api:{origin:'https://accounts.test',send:async(...args:unknown[])=>{calls.push(args);started?.();await gate;return {actual:true};}},
    service:{getSnapshot:()=>({session:{account:{id:'actual-account'},familyId:family,accessToken:'actual-private-bearer'}}),synchronize:async()=>{},accessSession:async()=>({account:{id:'actual-account'},familyId:family,accessToken:'actual-private-bearer'}),
      subscribe:(listener:()=>void)=>{listeners.add(listener);return()=>listeners.delete(listener);}}} as unknown as ReturnType<typeof browserAccount>;
  return {account,calls,replace:()=>{family='successor-family';for(const listener of listeners)listener();},hold:()=>{gate=new Promise(resolve=>{release=resolve;});return new Promise<void>(resolve=>{started=resolve;});},release:()=>release()};
}
test('account Server access is inert and captures actual account/family without copying a bearer',async()=>{
  const f=fixture(),scope={accountId:'actual-account',familyId:'actual-family'},access=createAccountServerAccess(f.account,scope);
  scope.familyId='caller-mutated';assert.equal(access.isCurrent(),true);assert.deepEqual(f.calls,[]);
  assert.equal('accessToken' in access,false);assert.equal('familyId' in access,false);
  assert.deepEqual(await access.request('/v1/server-routes/actual','GET'),{actual:true});
  assert.equal(f.calls[0][0],'/v1/server-routes/actual');assert.equal(f.calls[0][4],'actual-private-bearer');
  let changed=0;const stop=access.observe!(()=>{changed++;});f.replace();assert.equal(changed,1);assert.equal(access.isCurrent(),false);stop();
  await assert.rejects(access.request('/v1/identity','POST'),{code:'account_changed'});assert.equal(f.calls.length,1);
});
test('a family replaced during a Hosted request cannot return predecessor facts',async()=>{
  const f=fixture(),access=createAccountServerAccess(f.account,{accountId:'actual-account',familyId:'actual-family'}),started=f.hold(),pending=access.request('/v1/identity','POST');
  await started;f.replace();f.release();await assert.rejects(pending,{code:'account_changed'});
});
