import assert from 'node:assert/strict';
import {test} from 'node:test';
import {ApiError} from '@core/index.ts';
import {MFARequiredError} from '@core/hosted-security.ts';
import {ServicesAuthFlow} from '../src/services/auth-flow.ts';
import type {ServicesAccount} from '../src/services/session.ts';

const options = {password: true, recovery: true, registration: true, providers: [], verifiedContactRequired: true, accountSecurity: true};
function port() {
  let authenticated = 0, completed = 0;
  const calls: unknown[][] = [];
  const value = {
    api: {origin: 'https://web.getportico.tv'},
    client: {
      options: async () => options,
      transaction: async () => null,
      registration: async () => null,
      login: async (...input: unknown[]) => { calls.push(input); return {}; },
      mfa: async (...input: unknown[]) => { calls.push(input); return {}; },
      request: async (...input: unknown[]) => { calls.push(input); return {message: 'raw response must not become UI copy'}; },
    },
    service: {authenticateIfSignedOut: async (work: (signal: AbortSignal) => Promise<unknown>) => { authenticated++; return work(new AbortController().signal); }},
  } as unknown as ServicesAccount;
  return {value, calls, authenticated: () => authenticated, completed: () => completed, done: () => { completed++; }};
}

test('an explicit recovery page does not resume an unrelated registration or provider transaction', async () => {
  const p = port(); let privateReads = 0;
  p.value.client.registration = async () => { privateReads++; throw new Error('unexpected registration'); };
  p.value.client.transaction = async () => { privateReads++; throw new Error('unexpected transaction'); };
  const flow = new ServicesAuthFlow(p.value, p.done, 'forgot');
  await flow.initialize();
  assert.equal(privateReads, 0); assert.equal(flow.getSnapshot().mode, 'forgot');
  await flow.recover(' Person@Example.test ');
  assert.equal(flow.getSnapshot().mode, 'sent'); assert.equal(p.authenticated(), 0);
  assert.deepEqual(p.calls[0].slice(0, 2), ['/v1/account/recovery/request', 'POST']);
  assert.deepEqual(Object.keys(p.calls[0][2] as object).sort(), ['identifier', 'requestId']);
  assert.equal((p.calls[0][2] as {identifier: string}).identifier, 'person@example.test');
  assert.equal(Object.hasOwn(flow.getSnapshot(), 'message'), false);
  flow.dispose();
});

test('recovery takes a username or an email alone, and nothing from a blank field or a retired form', async () => {
  const p = port(), flow = new ServicesAuthFlow(p.value, p.done, 'forgot');
  await flow.initialize();
  await flow.recover('  ');
  assert.equal(p.calls.length, 0); assert.equal(flow.getSnapshot().mode, 'forgot');
  await flow.recover(' Person ');
  assert.equal(p.calls.length, 1); assert.equal(flow.getSnapshot().mode, 'sent');
  assert.equal((p.calls[0][2] as {identifier: string}).identifier, 'person');
  flow.go('signin');
  await flow.recover('person');
  assert.equal(p.calls.length, 1); assert.equal(flow.getSnapshot().mode, 'signin');
  flow.dispose();
});

test('recovery retries retain the exact account request until its acknowledgement', async () => {
  const p = port(), flow = new ServicesAuthFlow(p.value, p.done, 'forgot');
  const requests: Array<{identifier: string; requestId: string}> = [];
  p.value.client.request = async (_path, _method, body) => {
    requests.push(body as typeof requests[number]);
    if (requests.length === 1) throw new TypeError('network');
    return {} as never;
  };
  await flow.initialize();
  await flow.recover(' PERSON ');
  await flow.recover('person');
  assert.equal(requests[0].requestId, requests[1].requestId);
  flow.go('forgot'); await flow.recover('other@example.test');
  assert.notEqual(requests[1].requestId, requests[2].requestId);
  assert.equal(requests[2].identifier, 'other@example.test');
  flow.go('forgot'); await flow.recover('other@example.test');
  assert.notEqual(requests[2].requestId, requests[3].requestId);
  assert.equal(p.authenticated(), 0); flow.dispose();
});

test('MFA requires a separate factor submission through the same canonical coordinator', async () => {
  const p = port();
  p.value.client.login = async () => { throw new MFARequiredError({mfaRequired: true, challengeId: 'challenge', challengeToken: 'private', expiresAt: '2030-01-01T00:00:00Z'}); };
  const flow = new ServicesAuthFlow(p.value, p.done);
  await flow.signIn('account', 'password', '');
  assert.equal(flow.getSnapshot().mode, 'factor'); assert.equal(p.completed(), 0);
  assert.equal(JSON.stringify(flow.getSnapshot()).includes('private'), false);
  flow.recovery(); assert.equal(flow.getSnapshot().mode, 'recovery');
  await flow.signIn('account', '', 'one-use-recovery');
  assert.equal(p.authenticated(), 2); assert.equal(p.completed(), 1);
  assert.equal(p.calls[0][1], 'one-use-recovery');
  flow.dispose();
});

test('server retry guidance prevents another credential attempt until its actual deadline', async () => {
  const p = port(); let attempts = 0;
  p.value.client.login = async () => { attempts++; throw new ApiError(429, 'rate_limited', 'untrusted service text', true, 30); };
  const flow = new ServicesAuthFlow(p.value, p.done);
  await flow.signIn('account', 'password', '');
  assert.ok(flow.getSnapshot().retryAt > Date.now() + 29000);
  await flow.signIn('account', 'password', '');
  assert.equal(attempts, 1); flow.dispose();
});

test('disposing a pending bootstrap prevents late state publication or automatic sign-in', async () => {
  const p = port(); let resolveOptions!: (value: typeof options) => void;
  p.value.client.options = () => new Promise(resolve => { resolveOptions = resolve; });
  const flow = new ServicesAuthFlow(p.value, p.done); const pending = flow.initialize();
  const snapshot = flow.getSnapshot(); flow.dispose(); resolveOptions(options); await pending;
  assert.equal(flow.getSnapshot(), snapshot); assert.equal(p.authenticated(), 0); assert.equal(p.completed(), 0);
});

test('canceling provider onboarding cancels the actual browser-bound transaction without adopting a session', async () => {
  const p = port();
  p.value.client.transaction = async () => ({transactionId: 'current', provider: 'apple', mode: 'login', status: 'onboarding', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: true, needsUsername: true, mfaRequired: false, suggestedName: 'Actual callback name', verifiedEmail: 'actual@privaterelay.appleid.com'});
  const flow = new ServicesAuthFlow(p.value, p.done);
  await flow.initialize(); assert.equal(flow.getSnapshot().mode, 'provider');
  assert.equal(flow.getSnapshot().verifiedEmail, 'actual@privaterelay.appleid.com');
  await flow.cancelProvider();
  assert.deepEqual(p.calls[0].slice(0, 3), ['/v1/identity/cancel', 'POST', undefined]);
  assert.equal(flow.getSnapshot().mode, 'signin'); assert.equal(flow.getSnapshot().suggestedName, undefined);
  assert.equal(flow.getSnapshot().verifiedEmail, undefined);
  assert.equal(p.authenticated(), 0); assert.equal(p.completed(), 0); flow.dispose();
});

test('provider onboarding never substitutes an existing account contact or invents a callback name', async () => {
  const p = port();
  p.value.client.transaction = async () => ({transactionId: 'current', provider: 'google', mode: 'login', status: 'onboarding', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: true, needsUsername: true, mfaRequired: false});
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize();
  assert.equal(flow.getSnapshot().provider, 'google');
  assert.equal(flow.getSnapshot().verifiedEmail, undefined);
  assert.equal(flow.getSnapshot().suggestedName, undefined);
  assert.equal(p.authenticated(), 0); flow.dispose();
});

test('provider creation keeps the drawn ready step and refuses Continue after a family change', async () => {
  const p = port();
  let session = {account: {id: 'account', username: 'harbourview', displayName: 'Alex'}, familyId: 'created-family', accessToken: 'test-access', expiresAt: '2030-01-01T00:00:00Z'};
  p.value.client.transaction = async () => ({transactionId: 'current', provider: 'apple', mode: 'login', status: 'onboarding', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: true, needsUsername: true, mfaRequired: false});
  p.value.client.complete = async () => session;
  p.value.service.synchronize = async () => {};
  p.value.service.accessSession = async () => session;
  p.value.service.getSnapshot = () => ({phase: 'ready', session, pendingRevocations: 0});
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize();
  await flow.finishProvider('harbourview', 'Alex');
  assert.equal(flow.getSnapshot().mode, 'ready'); assert.equal(p.completed(), 0);
  assert.deepEqual(flow.readyAccount(), {name: 'Alex', username: 'harbourview'});
  assert.equal(Object.hasOwn(flow.getSnapshot(), 'session'), false);
  session = {...session, familyId: 'another-family'};
  assert.equal(flow.readyAccount(), undefined);
  await flow.continue();
  assert.equal(p.completed(), 0); assert.equal((flow.getSnapshot().error as ApiError).code, 'account_changed');
  flow.dispose();
});

test('restored provider completion never supplies return consent; explicit credential Continue does', async () => {
  const restored = port(), reasons: (boolean | undefined)[] = [];
  restored.value.client.transaction = async () => ({transactionId: 'current', provider: 'apple', mode: 'login', status: 'verified', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: true, needsUsername: false, mfaRequired: false});
  restored.value.client.complete = async () => ({} as never);
  const resumed = new ServicesAuthFlow(restored.value, gesture => reasons.push(gesture));
  await resumed.initialize(); assert.deepEqual(reasons, [false]); resumed.dispose();
  const direct = port(), clicked = new ServicesAuthFlow(direct.value, gesture => reasons.push(gesture));
  await clicked.signIn('account', 'memory-only-test-input', '');
  assert.deepEqual(reasons, [false, true]); clicked.dispose();
});


test('a failed original provider transaction draws a stopped choice without completion or email-account inference', async () => {
  const p = port(); let completions = 0;
  p.value.client.transaction = async () => ({transactionId: 'failed-original', provider: 'google', mode: 'login', status: 'failed', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: false, needsUsername: false, mfaRequired: false});
  p.value.client.complete = async () => {completions++; return {} as never;};
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize();
  assert.equal(flow.getSnapshot().mode, 'provider-stopped'); assert.equal(flow.getSnapshot().provider, 'google');
  assert.equal(flow.getSnapshot().verifiedEmail, undefined); assert.equal(p.authenticated(), 0); assert.equal(completions, 0);
  flow.go('signin'); assert.equal(flow.getSnapshot().mode, 'signin'); assert.equal(p.completed(), 0); flow.dispose();
});
test('provider login navigates synchronously without waiting for a request or saved-session work', async () => {
  const p = port(), destinations: string[] = [];
  p.value.client.start = async () => {throw new Error('provider start must be a navigation');};
  const flow = new ServicesAuthFlow(p.value, p.done, 'signin', url => {destinations.push(url);});
  const pending=flow.provider('google');
  assert.deepEqual(destinations,['https://web.getportico.tv/auth/sso/google/start']);
  await pending; await flow.provider('apple');
  assert.equal(destinations[1],'https://web.getportico.tv/auth/sso/apple/start');
  assert.equal(p.authenticated(),0); flow.dispose();
});
test('navigation failures remain visible and stopped callbacks do not resume stale transactions',async()=>{
  const p=port();let privateReads=0;
  p.value.client.transaction=async()=>{privateReads++;throw new Error('unrelated transaction');};
  p.value.client.registration=async()=>{privateReads++;throw new Error('unrelated registration');};
  const flow=new ServicesAuthFlow(p.value,p.done,'signin',()=>{throw new Error('navigation refused');},'google');
  await flow.initialize();assert.equal(privateReads,0);assert.equal(flow.getSnapshot().mode,'provider-stopped');
  assert.equal(flow.getSnapshot().provider,'google');
  await flow.provider('google');assert.match(String(flow.getSnapshot().error),/navigation refused/);
  assert.equal(flow.getSnapshot().busy,false);flow.dispose();
});



function providerRetryPort() {
  const p = port(), expiresAt = new Date(Date.now() + 60000).toISOString();
  const original = {transactionId: 'original', provider: 'google' as const, mode: 'login' as const, status: 'onboarding' as const, expiresAt, needsUsername: true, contactVerified: true, mfaRequired: false, verifiedEmail: 'proved@example.test', onboardingToken: 'original-onboarding'};
  let reads = 0;
  p.value.client.transaction = async () => {reads++; return reads === 1 ? original : {...original, onboardingToken: 'rotated-onboarding'};};
  return {...p, original, reads: () => reads};
}
test('confirmed provider username refusal refreshes only the original transaction before an explicit retry', async () => {
  const p = providerRetryPort(), proofs: string[] = [];
  p.value.client.complete = async (_id, input) => {
    proofs.push(input.onboardingToken ?? '');
    if (proofs.length === 1) throw new ApiError(409, 'username_unavailable', 'untrusted server message', false);
    return {account: {id: 'new-account', username: 'different_name', displayName: 'Actual name'}, familyId: 'accepted-family', accessToken: 'test-access', expiresAt: p.original.expiresAt};
  };
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize(); await flow.finishProvider('taken_name', 'Actual name');
  assert.equal((flow.getSnapshot().error as ApiError).code, 'username_unavailable'); assert.equal(p.reads(), 2); assert.deepEqual(proofs, ['original-onboarding']);
  assert.doesNotMatch(JSON.stringify(flow.getSnapshot()), /original-onboarding|rotated-onboarding/);
  await flow.finishProvider('different_name', 'Actual name'); assert.deepEqual(proofs, ['original-onboarding','rotated-onboarding']); assert.equal(flow.getSnapshot().mode, 'ready'); flow.dispose();
});
test('generic provider conflict never rotates or substitutes the original completion proof', async () => {
  const p = providerRetryPort(); p.value.client.complete = async () => {throw new ApiError(409, 'conflict', 'conflict', false);};
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize(); await flow.finishProvider('chosen_name', 'Actual name');
  assert.equal(p.reads(), 1); assert.equal((flow.getSnapshot().error as ApiError).code, 'conflict'); assert.equal(flow.getSnapshot().mode, 'provider'); flow.dispose();
});
test('provider proof refresh refuses the browser binding successor without adopting or completing it', async () => {
  const p = providerRetryPort(); let complete = 0;
  p.value.client.complete = async () => {complete++; throw new ApiError(409, 'username_unavailable', 'taken', false);};
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize();
  p.value.client.transaction = async () => ({...p.original, transactionId: 'successor', onboardingToken: 'successor-secret'});
  await flow.finishProvider('taken_name', 'Actual name'); assert.equal(flow.getSnapshot().mode, 'signin'); assert.equal((flow.getSnapshot().error as {code: string}).code, 'account_changed');
  await flow.finishProvider('new_name', 'Actual name'); assert.equal(complete, 1); assert.doesNotMatch(JSON.stringify(flow.getSnapshot()), /successor-secret/); flow.dispose();
});
test('a failed refresh retains the exact rejected transaction for a later explicit retry', async () => {
  const p = providerRetryPort(), proofs: string[] = [];
  p.value.client.complete = async (_id, input) => {proofs.push(input.onboardingToken ?? ''); if (proofs.length === 1) throw new ApiError(409, 'username_unavailable', 'taken', false); return {account: {id: 'new-account', username: 'new_name', displayName: 'Actual name'}, familyId: 'accepted-family', accessToken: 'test-access', expiresAt: p.original.expiresAt};};
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize();
  p.value.client.transaction = async () => {throw new ApiError(503, 'unavailable', 'unavailable', true);};
  await flow.finishProvider('taken_name', 'Actual name'); assert.equal((flow.getSnapshot().error as ApiError).code, 'unavailable');
  p.value.client.transaction = async () => ({...p.original, onboardingToken: 'rotated-onboarding'});
  await flow.finishProvider('new_name', 'Actual name'); assert.deepEqual(proofs, ['original-onboarding','rotated-onboarding']); assert.equal(flow.getSnapshot().mode, 'ready'); flow.dispose();
});

test('registration collision refreshes only the original verified binding and completes it with a replacement name', async () => {
  const p = port(), calls: Array<{id: string; input: {code: string; username?: string; displayName?: string}}> = [];
  const original = {kind: 'registration' as const, registrationId: 'original', status: 'contact_pending' as const, revision: '1', maskedAddress: 'a***@example.test', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: false};
  const verified = {...original, contactVerified: true};
  p.value.client.registration = async id => id === undefined ? original : (assert.equal(id, 'original'), verified);
  p.value.client.verifyRegistration = async (id, input) => {calls.push({id, input}); if (calls.length < 3) throw new ApiError(409, 'username_unavailable', 'taken', false); return {} as never;};
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize();
  await flow.verify('123456'); assert.equal(flow.getSnapshot().registration?.contactVerified, true);
  await flow.verify('', 'Second', 'Name'); assert.equal(flow.getSnapshot().registration?.contactVerified, true);
  await flow.verify('', 'Available', 'Name');
  assert.deepEqual(calls.map(x => x.id), ['original', 'original', 'original']);
  assert.deepEqual(calls[2].input, {code: '', username: 'available', displayName: 'Name'});
  assert.equal(p.completed(), 1); flow.dispose();
});

test('registration continuation refuses changed binding and restores a verified registration without a code', async () => {
  const p = port(); const original = {kind: 'registration' as const, registrationId: 'original', status: 'contact_pending' as const, revision: '1', maskedAddress: 'a***@example.test', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: false};
  p.value.client.registration = async id => id === undefined ? original : {...original, registrationId: 'successor', contactVerified: true};
  p.value.client.verifyRegistration = async () => {throw new ApiError(409, 'username_unavailable', 'taken', false);};
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize(); await flow.verify('123456');
  assert.equal(flow.getSnapshot().registration?.contactVerified, false); assert.equal((flow.getSnapshot().error as {code: string}).code, 'account_changed'); flow.dispose();
  const q = port(); q.value.client.registration = async () => ({...original, contactVerified: true});
  let sent: unknown; q.value.client.verifyRegistration = async (_id, input) => {sent = input; return {} as never;};
  const restored = new ServicesAuthFlow(q.value, q.done); await restored.initialize(); await restored.verify('', 'replacement', '');
  assert.deepEqual(sent, {code: '', username: 'replacement', displayName: ''}); assert.equal(q.completed(), 1); restored.dispose();
});

test('registration transport uncertainty retains the same original attempt and generic conflict does not refresh', async () => {
  const p = port(); const original = {kind: 'registration' as const, registrationId: 'original', status: 'contact_pending' as const, revision: '1', maskedAddress: 'a***@example.test', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: true};
  let reads = 0; const calls: unknown[] = [];
  p.value.client.registration = async () => {reads++; return original;};
  p.value.client.verifyRegistration = async (id, input) => {calls.push({id, input}); if (calls.length === 1) throw new TypeError('response lost'); if (calls.length === 2) throw new ApiError(409, 'conflict', 'conflict', false); return {} as never;};
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize();
  await flow.verify('', 'replacement', 'Name'); await flow.verify('', 'replacement', 'Name'); await flow.verify('', 'replacement', 'Name');
  assert.equal(reads, 1); assert.deepEqual(calls[0], calls[1]); assert.deepEqual(calls[1], calls[2]); assert.equal(p.completed(), 1); flow.dispose();
});

test('retired registration flow ignores a late original collision lookup', async () => {
  const p = port(); const original = {kind: 'registration' as const, registrationId: 'original', status: 'contact_pending' as const, revision: '1', maskedAddress: 'a***@example.test', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: false};
  let release!: (value: typeof original) => void, started!: () => void;
  const reading = new Promise<void>(resolve => {started = resolve;});
  p.value.client.registration = async id => id === undefined ? original : new Promise(resolve => {release = resolve; started();});
  p.value.client.verifyRegistration = async () => {throw new ApiError(409, 'username_unavailable', 'taken', false);};
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize(); const pending = flow.verify('123456');
  await reading; const snapshot = flow.getSnapshot(); flow.dispose(); release({...original, contactVerified: true}); await pending;
  assert.equal(flow.getSnapshot(), snapshot); assert.equal(p.completed(), 0);
});

test('an expired verified registration cannot submit a replacement name', async () => {
  const p = port(); let calls = 0;
  p.value.client.registration = async () => ({kind: 'registration', registrationId: 'original', status: 'contact_pending', revision: '1', maskedAddress: 'a***@example.test', expiresAt: new Date(Date.now() - 1000).toISOString(), contactVerified: true});
  p.value.client.verifyRegistration = async () => {calls++; return {} as never;};
  const flow = new ServicesAuthFlow(p.value, p.done); await flow.initialize(); await flow.verify('', 'replacement', 'Name');
  assert.equal(calls, 0); assert.equal(p.completed(), 0); flow.dispose();
});

test('created and collision-refreshed registration aliases cannot retarget held coordinator admission or exact reread', async () => {
  const p=port(),expiresAt=new Date(Date.now()+60000).toISOString();
  const issued={kind:'registration' as const,registrationId:'original',status:'contact_pending' as const,revision:'1',maskedAddress:'a***@example.test',expiresAt,contactVerified:false};
  const fresh={...issued,contactVerified:true};const calls:any[]=[],reads:string[]=[];
  p.value.client.register=async()=>issued;p.value.client.registration=async id=>{reads.push(id!);return fresh;};
  p.value.client.verifyRegistration=async(id,input)=>{calls.push({id,input});if(calls.length===1)throw new ApiError(409,'username_unavailable','taken',false);return {} as never;};
  let release!:()=>void;
  p.value.service.authenticateIfSignedOut=async(work,_context,current)=>{
    await new Promise<void>(resolve=>{release=resolve;});assert.ok(current?.());return work(new AbortController().signal);
  };
  const flow=new ServicesAuthFlow(p.value,p.done,'create');await flow.initialize();await flow.create({username:'taken',displayName:'Name',password:'password',contactAddress:'a@example.test'});
  const original=flow.getSnapshot().registration!;assert.ok(Object.isFrozen(original));
  const first=flow.verify(' 123456 ');
  assert.equal(Reflect.set(original,'registrationId','successor'),false);issued.registrationId='successor';issued.revision='99';issued.maskedAddress='foreign';
  assert.equal(calls.length,0);release();await first;
  assert.deepEqual(calls,[{id:'original',input:{code:'123456'}}]);assert.deepEqual(reads,['original']);assert.equal(p.completed(),0);
  const verified=flow.getSnapshot().registration!;assert.ok(Object.isFrozen(verified));assert.notEqual(verified,fresh);assert.equal(verified.registrationId,'original');
  const second=flow.verify('', ' Available ', ' Name ');
  assert.equal(Reflect.set(verified,'registrationId','foreign'),false);fresh.registrationId='foreign';fresh.revision='88';fresh.maskedAddress='foreign';
  assert.equal(calls.length,1);release();await second;
  assert.deepEqual(calls[1],{id:'original',input:{code:'',username:'available',displayName:'Name'}});assert.deepEqual(reads,['original']);assert.equal(p.completed(),1);flow.dispose();
});

// Services viewTx projects a verified login as onboarding even when its
// identity is already linked and no username needs to be chosen.
for (const provider of ['google', 'apple'] as const) test(provider+' existing-account callback completes the Services onboarding projection once', async () => {
  const p = port(), completed: string[] = [], gestures: (boolean | undefined)[] = [];
  p.value.client.transaction = async () => ({transactionId: 'original', provider, mode: 'login', status: 'onboarding', expiresAt: new Date(Date.now()+60000).toISOString(), contactVerified: true, needsUsername: false, mfaRequired: false});
  p.value.client.complete = async (id, input) => {completed.push(id); assert.deepEqual(input, {}); return {} as never;};
  const flow = new ServicesAuthFlow(p.value, gesture => gestures.push(gesture));
  await flow.initialize(); await flow.initialize();
  assert.deepEqual(completed, ['original']); assert.equal(p.authenticated(), 1); assert.deepEqual(gestures, [false]);
  flow.dispose();
});
for (const variant of [
  {status: 'onboarding', contactVerified: false, mode: 'login'},
  {status: 'started', contactVerified: true, mode: 'login'},
  {status: 'exchanging', contactVerified: true, mode: 'login'},
  {status: 'contact_pending', contactVerified: false, mode: 'login'},
  {status: 'onboarding', contactVerified: true, mode: 'reauthenticate'},
  {status: 'onboarding', contactVerified: true, mode: 'link'},
] as const) test('callback projection does not complete '+JSON.stringify(variant), async () => {
  const p=port(); let completions=0;
  p.value.client.transaction=async()=>({transactionId:'original',provider:'google',expiresAt:new Date(Date.now()+60000).toISOString(),needsUsername:false,mfaRequired:false,...variant});
  p.value.client.complete=async()=>{completions++;return {} as never;};
  const flow=new ServicesAuthFlow(p.value,p.done);await flow.initialize();assert.equal(completions,0);assert.equal(p.authenticated(),0);flow.dispose();
});
test('existing-account onboarding completion preserves the MFA challenge boundary', async()=>{
  const p=port();p.value.client.transaction=async()=>({transactionId:'original',provider:'google',mode:'login',status:'onboarding',expiresAt:new Date(Date.now()+60000).toISOString(),needsUsername:false,contactVerified:true,mfaRequired:true});
  p.value.client.complete=async()=>{throw new MFARequiredError({mfaRequired:true,challengeId:'challenge',challengeToken:'private',expiresAt:'2030-01-01T00:00:00Z'});};
  const flow=new ServicesAuthFlow(p.value,p.done);await flow.initialize();assert.equal(flow.getSnapshot().mode,'factor');assert.equal(p.completed(),0);assert.doesNotMatch(JSON.stringify(flow.getSnapshot()),/private/);flow.dispose();
});
