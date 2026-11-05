import test from 'node:test';
import assert from 'node:assert/strict';

function held<T>() {
  let resolve!: (value: T) => void, reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes,no) => {resolve=yes;reject=no;});
  return {promise,resolve,reject};
}

test('notification transport retains the original issuer and bearer for deferred calls', async () => {
  const {createHostedNotificationClient} = await import('../src/query/hosted-account-transport.ts');
  let origin = 'https://original-account.test';
  const owner = {authorityOrigin: () => origin} as import('../src/credentials.ts').CredentialSessionService;
  const session = {accessToken: 'original-access'}, seen: {url?: unknown; token?: string | null} = {};
  const client = createHostedNotificationClient(owner, session, async (url, options) => {
    seen.url = url;seen.token = new Headers(options?.headers).get('Authorization');
    return Response.json({serverOffline: false});
  });
  origin = 'https://replacement-account.test';session.accessToken = 'replacement-access';
  await client.preferences();
  assert.equal(seen.url, 'https://original-account.test/v1/account/notification-preferences');
  assert.equal(seen.token, 'Bearer original-access');
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
