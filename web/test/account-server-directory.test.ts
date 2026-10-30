import test from 'node:test';
import assert from 'node:assert/strict';
import {createAccountServerDirectory} from '../src/platform/account-server-directory.ts';
import type {browserAccount} from '../src/platform/account.ts';

const scope={accountId:'account-current',familyId:'family-current'};
function fixture() {
  let session={account:{id:scope.accountId},familyId:scope.familyId,accessToken:'current-bearer'};
  let gate:Promise<void>|undefined;let release=()=>{},started=()=>{};
  const calls:unknown[][]=[];
  const row={id:'srv_'+ 'A'.repeat(43),name:'Lake house',baseUrl:'',publicKey:'A'.repeat(43),endpointVerified:false,secureConnection:'ready',policyRevision:1,
    lastSeenAt:'2026-10-18T12:00:00Z',ownedByMe:false,role:'admin',status:'active',presence:{lastSeenAt:'2026-10-18T12:00:00Z'}};
  const account={api:{send:async(...args:unknown[])=>{calls.push(['send',...args]);started();await gate;return {items:[row],version:'actual-version',watch:'actual-watch'};}},
    service:{synchronize:async()=>{calls.push(['sync']);},accessSession:async()=>{calls.push(['access']);return session;},getSnapshot:()=>({session})}} as unknown as ReturnType<typeof browserAccount>;
  return {account,calls,row,replace:(accountId=scope.accountId,familyId='family-successor')=>{session={...session,account:{id:accountId},familyId};},
    hold:()=>{gate=new Promise(done=>{release=done;});return new Promise<void>(done=>{started=done;});},release:()=>release()};
}
test('the public directory binding is inert and captures the account/family fence by value',()=>{
  const f=fixture(),input={...scope},directory=createAccountServerDirectory(f.account,input);input.familyId='retarget';
  assert.deepEqual(f.calls,[]);assert.deepEqual(directory.scope,scope);assert.equal(Object.isFrozen(directory.scope),true);
});
test('one canonical current-family read preserves actual status/presence/ownership and no guessed address',async()=>{
  const f=fixture(),controller=new AbortController(),directory=createAccountServerDirectory(f.account,scope),read=directory.read;
  const response=await read(controller.signal);
  assert.deepEqual(f.calls.map(c=>c[0]),['sync','access','send','sync']);
  assert.deepEqual(f.calls.find(c=>c[0]==='send'),['send','/v1/servers','GET',undefined,controller.signal,'current-bearer']);
  assert.equal(response.items[0].status,'active');assert.equal(response.items[0].presence.lastSeenAt,f.row.presence.lastSeenAt);
  assert.equal(response.items[0].ownedByMe,false);assert.equal(response.items[0].role,'admin');
  assert.equal(response.items[0].baseUrl,'');assert.equal(response.items[0].endpointVerified,false);
});
test('a replaced account or family refuses before any directory request',async()=>{
  for(const [account,family] of [[scope.accountId,'family-successor'],['account-other',scope.familyId]]){
    const f=fixture();f.replace(account,family);await assert.rejects(createAccountServerDirectory(f.account,scope).read(),{code:'account_changed'});
    assert.equal(f.calls.some(c=>c[0]==='send'),false);
  }
});
test('a family replaced during the request cannot return predecessor directory facts',async()=>{
  const f=fixture(),waiting=f.hold(),directory=createAccountServerDirectory(f.account,scope),pending=directory.read();await waiting;
  f.replace();f.release();await assert.rejects(pending,{code:'account_changed'});
});
test('cancellation before start has no effects and a transport ignoring abort cannot return data',async()=>{
  const early=fixture(),first=new AbortController();first.abort();await assert.rejects(createAccountServerDirectory(early.account,scope).read(first.signal),{name:'AbortError'});assert.deepEqual(early.calls,[]);
  const f=fixture(),waiting=f.hold(),controller=new AbortController(),pending=createAccountServerDirectory(f.account,scope).read(controller.signal);await waiting;
  controller.abort();f.release();await assert.rejects(pending,{name:'AbortError'});
});
