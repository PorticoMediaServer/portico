import test from 'node:test';
import assert from 'node:assert/strict';
import {readServerSystem, readDirectAccount, readViewerIdentity, revokeCurrentSession,
  readOwnerClaim, cancelOwnerClaim, prepareOwnerClaim, previewLocalInvitation, acceptLocalInvitation,
  lookupBrowserHostedClaim, decideBrowserHostedClaim, completeBrowserHostedReturn, readConnectionFacts,
  requestHostedRecovery} from '../src/query/connection-operations.ts';
import {routePath} from '../../contracts/src/routes.ts';
import {routePath as hostedPath, encodedRoutePath} from '../../contracts/src/hosted/routes.ts';

test('connection reads and sign-out keep the captured requester, response and cancellation', async () => {
  const signal = new AbortController().signal, response = {from: 'captured issuer'};
  const calls: unknown[][] = [];
  const api = {request: async <T,>(...args: unknown[]): Promise<T> => {calls.push(args); return response as T;}};
  for (const operation of [readServerSystem, readDirectAccount, readViewerIdentity, revokeCurrentSession, readOwnerClaim])
    assert.equal(await operation(api, signal), response);
  assert.deepEqual(calls, [
    ['get_system','GET'],['get_direct_account','GET'],['get_me','GET'],['delete_sessions_current','DELETE'],['get_networking_claim','GET'],
  ].map(([id,method]) => [routePath(id as 'get_system'),method,undefined,signal]));
});

test('claim and invitation intents preserve omission, frozen authority witnesses and bodies', async () => {
  const calls: unknown[][] = [], signal = new AbortController().signal, expected = Object.freeze({revision: 7});
  const api = {request: async <T,>(...args: unknown[]): Promise<T> => {calls.push(args); return undefined as T;}};
  await cancelOwnerClaim(api, expected, signal);
  await prepareOwnerClaim(api, 'switch', undefined, expected, signal);
  await prepareOwnerClaim(api, 'change-owner', 'administrator', expected, signal);
  const capability = Object.freeze({shortCode:'BOUND'});
  await previewLocalInvitation(api, capability, signal);
  await acceptLocalInvitation(api, capability, 'alex', 'private-input', '  ', signal);
  assert.deepEqual(calls.map(c=>c[2]), [{expected},{purpose:'switch',expected},
    {purpose:'change-owner',previousOwnerRole:'administrator',expected},capability,
    {shortCode:'BOUND',username:'alex',password:'private-input'}]);
  assert.ok(calls.every(c=>c[1]==='POST' && c[3]===signal));
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

test('anonymous connection facts use the platform executor without credentials or redirects', async () => {
  const signal = new AbortController().signal;
  let request: unknown[] = [];
  const fetcher: typeof fetch = async (url,options) => {request=[url,options];return new Response('{"remote":false}',{headers:{'Content-Type':'application/json'}});};
  assert.deepEqual(await readConnectionFacts('https://server.test',signal,fetcher),{remote:false});
  assert.deepEqual(request,['https://server.test'+routePath('get_connection'),{method:'GET',redirect:'error',credentials:'omit',referrerPolicy:'no-referrer',signal,headers:{Accept:'application/json'}}]);
  await assert.rejects(readConnectionFacts('https://server.test',signal,async()=>new Response('x'.repeat(8193))),/./);
});

test('account recovery keeps a single submitted intent and its refusal without retry', async () => {
  const refusal=Object.assign(new Error('busy'),{status:503});let calls=0;
  const api={request:async()=>{calls++;throw refusal;}};
  await assert.rejects(requestHostedRecovery(api,'alex','request-id'),e=>e===refusal);
  assert.equal(calls,1);
});

test('Hosted API factory takes the canonical authority origin and exact admitted bearer', async () => {
  const {createHostedAccountApi, captureHostedCommandBody} = await import('../src/query/hosted-account-transport.ts');
  const seen: {url?: unknown; headers?: Headers} = {};
  const owner = {authorityOrigin: () => 'https://canonical-account.test'} as import('../src/credentials.ts').CredentialSessionService;
  const api = createHostedAccountApi(owner, {accessToken:'admitted-access'}, async (url,options) => {
    seen.url=url;seen.headers=new Headers(options?.headers);return new Response('{"ok":true}',{headers:{'Content-Type':'application/json'}});
  });
  await api.request('/v1/me', 'GET');
  assert.equal(seen.url,'https://canonical-account.test/v1/me');
  assert.equal(seen.headers?.get('Authorization'),'Bearer admitted-access');
  const body={nested:{value:1}}, captured=captureHostedCommandBody(body);
  body.nested.value=2;assert.deepEqual(captured,{nested:{value:1}});
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

test('native account response parsing stays behind both original lifetime checks', async () => {
  const {createNativeAccountClient} = await import('../src/query/native-account-client.ts');
  for (const retireAt of ['headers','body'] as const) {
    const response=held<Response>(), body=held<string>();let current=true,textCalls=0;
    const fetcher:typeof fetch=async()=>response.promise;
    const client=createNativeAccountClient('https://account.test','b'.repeat(43),fetcher,()=>current);
    const result=client.request('/v1/me','GET');
    const raw=new Response('',{headers:{'Content-Type':'application/json'}});
    raw.text=async()=>{textCalls++;if(retireAt==='body')current=false;return body.promise;};
    if(retireAt==='headers')current=false;
    response.resolve(raw);body.resolve('not JSON');
    await assert.rejects(result,(error:any)=>error.name==='AbortError' && error.message==='native_account_cancelled');
    assert.equal(textCalls,retireAt==='headers'?0:1);
  }
});

test('native sign-in command factories preserve each original wire/error priority', async () => {
  const {createNativeHostedCommandTransport} = await import('../src/query/hosted-account-transport.ts');
  for (const checkedReturn of [true,false]) {
    const io=held<Response>(), entered=held<void>();let current=true;
    const cancelled=new Error('original account opening ended'), wire=new Error('original request refused');
    const session={account:{id:'account'},familyId:'family',accessToken:'captured-access'};
    const owner={authorityOrigin:()=> 'https://account.test',getSnapshot:()=>({session}),subscribe:()=>()=>{},synchronize:async()=>{},accessSession:async()=>session} as unknown as import('../src/credentials.ts').CredentialSessionService;
    const transport=createNativeHostedCommandTransport(owner,async()=>{entered.resolve();return io.promise;},
      {accountId:'account',familyId:'family',accessToken:'captured-access'},new AbortController().signal,()=>current,
      checkedReturn?{assertCurrent:()=>{if(!current)throw cancelled;},unavailable:()=>undefined}:{});
    const pending=transport.request('/v1/profiles','POST',{});
    await entered.promise;current=false;io.reject(wire);
    await assert.rejects(pending,error=>error===(checkedReturn?cancelled:wire));
  }
});
