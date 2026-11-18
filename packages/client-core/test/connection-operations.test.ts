import test from 'node:test';
import assert from 'node:assert/strict';
import {readServerSystem, readDirectAccount, readViewerIdentity, revokeCurrentSession,
  lookupBrowserHostedClaim, decideBrowserHostedClaim, completeBrowserHostedReturn, readConnectionFacts} from '../src/query/connection-operations.ts';
import {routePath as hostedPath, encodedRoutePath} from '../../contracts/src/hosted/routes.ts';

test('connection reads and sign-out keep the captured requester, response and cancellation', async () => {
  const signal = new AbortController().signal, response = {from: 'captured issuer'};
  const calls: unknown[][] = [];
  const api = {request: async <T,>(...args: unknown[]): Promise<T> => {calls.push(args); return response as T;}};
  assert.equal(await readServerSystem(api, signal), response);
  assert.deepEqual(await readDirectAccount(api, signal), {me: response, profiles: response});
  await revokeCurrentSession(api, signal);
  assert.deepEqual(calls, [
    ['/v1/system','GET'],['/v1/me','GET'],['/v1/profiles','GET'],['/v1/auth/session','DELETE'],
  ].map(([path,method]) => [path,method,undefined,signal]));
  // Who the sign-in acts for comes back in the shape a session keeps, on the server it was asked of.
  const me = {request: async <T,>(): Promise<T> => ({account: {id: 'acct', username: 'sam', role: 'owner'}, profile: {id: 'main'}, role: 'owner', canManageAccount: true}) as T};
  assert.deepEqual(await readViewerIdentity(me, {serverId: 'srv'}), {viewer: {accountId: 'acct', profileId: 'main', serverId: 'srv', authority: 'local', role: 'owner'}});
});

test('Hosted claims carry the exact token and encode return IDs once', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal;
  const api = {send: async <T,>(...args: unknown[]): Promise<T> => {calls.push(args); return undefined as T;}};
  await lookupBrowserHostedClaim(api,'CODE',signal,'captured-token');
  await decideBrowserHostedClaim(api,'deny','CODE',signal,'captured-token');
  await completeBrowserHostedReturn(api,'id/with slash',signal,'captured-token');
  assert.deepEqual(calls,[
    [hostedPath('server_claim_lookup'),'POST',{code:'CODE'},signal,'captured-token'],
    [hostedPath('server_claim_deny'),'POST',{code:'CODE'},signal,'captured-token'],
    [encodedRoutePath('server_return_complete',{id:'id%2Fwith%20slash'}),'POST',{},signal,'captured-token'],
  ]);
});


function held<T>() {
  let resolve!: (value: T) => void, reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes,no) => {resolve=yes;reject=no;});
  return {promise,resolve,reject};
}

test('raw system operations settle the original request before caller guards and parsing', async () => {
  const {parseServerSignIn} = await import('../src/sign-in.ts');
  const io=held<unknown>(), events: string[]=[], cancelled=new Error('original opening cancelled');let current=true;
  const api={request:<T,>():Promise<T>=>{events.push('request');return io.promise as Promise<T>;}};
  const result=(async()=>{const raw=await readServerSystem(api);events.push('guard');if(!current)throw cancelled;events.push('parse');return parseServerSignIn(raw);})();
  assert.deepEqual(events,['request']);
  current=false;io.resolve({unreadable:true});await assert.rejects(result,e=>e===cancelled);
  assert.deepEqual(events,['request','guard']);
  const refused=held<unknown>(), wire=new Error('original wire failure');
  const rejection=readServerSystem({request:<T,>()=>refused.promise as Promise<T>}).then(()=>{throw new Error('must not validate a rejected request');});
  refused.reject(wire);await assert.rejects(rejection,e=>e===wire);
});
