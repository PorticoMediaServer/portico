import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as continuationQueue from '../src-next/platform/continuation-queue';
import {ApiError} from '../../../packages/client-core/src/index';
import {CodedError} from '../../../packages/client-core/src/server-messages';
import {MFARequiredError, parseNativeAccountContinuation} from '../../../packages/client-core/src/hosted-security';

async function port(beforeWork?: () => Promise<void>) {
  const original = {kind: 'registration', registrationId: 'original', status: 'contact_pending', revision: '1', maskedAddress: 'a***@example.test', expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: false};
  const requests: any[] = []; let read: any = {...original, contactVerified: true}, onRead = () => {};
  const api = {options: async () => ({providers: []}), register: async () => original,
    registration: async (id: string) => {assert.equal(id, 'original'); onRead(); return {...await read};},
    verifyRegistration: async (id: string, input: unknown) => {requests.push({id, input}); if (requests.length < 3) throw new ApiError(409, 'username_unavailable', 'taken', false); return {};}};
  const bridge = {loadContinuation: async () => null, newBinding: async () => 'a'.repeat(43), saveContinuation: async () => {}, newRequestId: async () => 'request', cancel() {}};
  const nativeAccounts = {onCancel: () => () => {}, accountClient: () => api, porticoClient: () => api, authenticatePorticoAccount: async (work: (signal: AbortSignal) => Promise<unknown>, current: () => boolean) => {assert.ok(current()); if (beforeWork) await beforeWork(); if (!current()) throw new Error('retired'); return work(new AbortController().signal);}};
  const {NativePorticoSignIn} = await screenModule(new URL('../src-next/platform/portico-sign-in.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {PorticoAccountAuth: bridge}},
    '../../../../packages/client-core/src/hosted-security': {MFARequiredError}, '../../../../packages/client-core/src/server-messages': {CodedError},
    '../../../../packages/client-core/src': {ApiError}, '../../../../packages/contracts/src/hosted/routes': {},
    './accounts': {nativeAccounts}, './abort-signals': {}, './account-authority': {hostedUrl: 'https://example.test'}, './continuation-queue': continuationQueue,
  });
  const source = new NativePorticoSignIn(); await source.prepare();
  const registration = await source.register('taken', 'Name', 'password', 'a@example.test');
  return {source, registration, requests, setRead: (value: unknown, notify?: () => void) => {read = value; onRead = notify ?? (() => {});}};
}

test('continuation storage serializes an old in-flight save, skips a retired waiter and releases a failed save', async () => {
  let record: string | null = null, release!: () => void, announce!: () => void, writes = 0, reads = 0, bindings = 0, held = true, fail = false;
  const started = new Promise<void>(resolve => {announce = resolve;});
  const bridge = {loadContinuation: async () => {reads++; return record;}, newBinding: async () => String(++bindings).padStart(43, 'a'),
    saveContinuation: async (_origin: string, bytes: string) => {writes++; if (held) {held = false; announce(); await new Promise<void>(resolve => {release = resolve;});} if (fail) {fail = false; throw new Error('write failed');} record = bytes;}, cancel() {}};
  const nativeAccounts = {onCancel: () => () => {}, porticoClient: () => ({options: async () => ({providers: []})})};
  const {NativePorticoSignIn} = await screenModule(new URL('../src-next/platform/portico-sign-in.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {PorticoAccountAuth: bridge}},
    '../../../../packages/client-core/src/hosted-security': {MFARequiredError, parseNativeAccountContinuation}, '../../../../packages/client-core/src/server-messages': {CodedError},
    '../../../../packages/client-core/src': {ApiError}, '../../../../packages/contracts/src/hosted/routes': {},
    './accounts': {nativeAccounts}, './abort-signals': {}, './account-authority': {hostedUrl: 'https://example.test'}, './continuation-queue': continuationQueue,
  });
  let oldLive = true, skippedLive = true;
  const old = new NativePorticoSignIn(() => oldLive), skipped = new NativePorticoSignIn(() => skippedLive), successor = new NativePorticoSignIn();
  const first = old.prepare(); await started;
  const second = skipped.prepare(), third = successor.prepare(); oldLive = false; skippedLive = false;
  assert.equal(reads, 1); assert.equal(writes, 1); release();
  await assert.rejects(first); await assert.rejects(second); await third;
  assert.equal(reads, 2); assert.equal(writes, 2); assert.equal(JSON.parse(record!).binding, 'a'.repeat(42) + '1');
  const failing = new NativePorticoSignIn(), after = new NativePorticoSignIn(); fail = true;
  const rejected = failing.prepare(), accepted = after.prepare(); await assert.rejects(rejected, /write failed/); await accepted;
  assert.equal(JSON.parse(record!).binding, 'a'.repeat(42) + '1');
  old.dispose(); skipped.dispose(); successor.dispose(); failing.dispose(); after.dispose();
});

test('verification retains the original caller predicate across coordinator admission', async () => {
  let release!: () => void, started!: () => void, live = true;
  const waiting = new Promise<void>(resolve => {started = resolve;});
  const p = await port(() => {started(); return new Promise<void>(resolve => {release = resolve;});});
  const caller = {isCurrent: () => live};
  const attempt = p.source.verifyRegistration('123456', p.registration, caller); await waiting;
  live = false; caller.isCurrent = () => true; release();
  await assert.rejects(attempt); assert.equal(p.requests.length, 0); p.source.dispose();
});

test('a caller that disposes its source and returns true cannot allocate or dispatch original password/provider/registration work',async()=>{
  for(const kind of ['password','provider','verify','admission','source'] as const){
    const calls={ids:0,login:0,start:0,verify:0,adopt:0};let source:any;
    const bridge={loadContinuation:async()=>null,newBinding:async()=>'a'.repeat(43),saveContinuation:async()=>{},newRequestId:async()=>{calls.ids++;return 'original-id';},cancel(){}};
    const api={options:async()=>({providers:[]}),register:async()=>({kind:'registration',registrationId:'original',status:'contact_pending',revision:'1',maskedAddress:'a***@example.test',expiresAt:new Date(Date.now()+60000).toISOString(),contactVerified:false}),
      login:async()=>{calls.login++;return {};},start:async()=>{calls.start++;return {};},verifyRegistration:async()=>{calls.verify++;return {};}};
    let currentSource=()=>true;
    const nativeAccounts={onCancel:()=>()=>{},cancel(){},porticoClient:()=>api,authenticatePorticoAccount:async(work:any,current:()=>boolean)=>{if(!current())throw Object.assign(new Error('retired'),{name:'AbortError'});const value=await work(new AbortController().signal);if(!current())throw Object.assign(new Error('retired'),{name:'AbortError'});calls.adopt++;return value;}};
    const {NativePorticoSignIn}=await screenModule(new URL('../src-next/platform/portico-sign-in.ts',import.meta.url),{
      react:{default:{}},'react-native':{NativeModules:{PorticoAccountAuth:bridge}},
      '../../../../packages/client-core/src/hosted-security':{MFARequiredError},'../../../../packages/client-core/src/server-messages':{CodedError},'../../../../packages/client-core/src':{ApiError},
      '../../../../packages/contracts/src/hosted/routes':{},'./accounts':{nativeAccounts},'./abort-signals':{},'./account-authority':{hostedUrl:'https://predicate.example'},'./continuation-queue':continuationQueue,
    });
    source=new NativePorticoSignIn(()=>currentSource());await source.prepare();
    const original=kind==='verify'?await source.register('taken','Name','password','a@example.test'):undefined;calls.ids=0;
    // The second check is the account coordinator's admission (sign-in no longer waits for a request id first).
    let predicates=0;const caller=()=>{predicates++;if(kind!=='admission'||predicates===2)source.dispose();return true;};
    if(kind==='source')currentSource=()=>{source.dispose();return true;};
    if(kind==='verify')await assert.rejects(source.verifyRegistration('123456',original,{isCurrent:caller}));
    else if(kind==='provider')await assert.rejects(source.provider('google',caller));
    else await assert.rejects(source.password('person','password',kind==='source'?undefined:caller));
    assert.deepEqual(calls,{ids:0,login:0,start:0,verify:0,adopt:0},kind);source.dispose();
  }
});
