import test from 'node:test';
import assert from 'node:assert/strict';
import {withUnknownFields} from './unknown-fields.ts';
import {parseServerSignIn, ServerSignInProbe, signInLabelKey, serverSignInSettingsURL} from '../src/sign-in.ts';
/** `GET /v1/system` as the server sends it. A password form is offered for the method `password`. */
const system = (signIn = 'local') => ({id: 'server', name: 'Study', version: '1', apiMajor: 1, capabilities: [] as string[], setupRequired: false, signInMethods: [signIn === 'local' ? 'password' : signIn]});
test('Change this on the web opens Sign-in on the exact chosen server without secrets or alternate authorities',()=>{
  assert.equal(serverSignInSettingsURL('http://192.168.1.5:32500'),'http://192.168.1.5:32500/settings?scope=server&section=signin');
  for(const address of ['https://user:pass@server.test','https://server.test/?code=secret','https://server.test/#secret','javascript:alert(1)','https://server.test/path'])assert.throws(()=>serverSignInSettingsURL(address));
});
test('the sign-in method the server names decides the credential route and its label', async () => {
  for (const signIn of ['local', 'portico'] as const) {
    const calls: unknown[] = [];
    const probe = new ServerSignInProbe(async (url, init) => {calls.push([url, init]); return Response.json(system(signIn));});
    await probe.inspect('http://192.168.1.5:8096');
    assert.equal(probe.getSnapshot().destination, signIn === 'local' ? 'local-account' : 'portico-account');
    assert.equal(signInLabelKey(signIn), 'accounts.session.' + signIn);
    assert.equal(calls.length, 1); const [url, init] = calls[0] as [string, RequestInit];
    assert.equal(url, 'http://192.168.1.5:8096/v1/system'); assert.equal(init.credentials, 'omit'); assert.equal(init.redirect, 'error'); assert.equal(new Headers(init.headers).has('Authorization'), false);
    probe.dispose();
  }
});
test('a server that names no sign-in method this app knows never gets a password form; setup remains separate', async () => {
  for (const signInMethods of [undefined, [], ['passkey'], 'password', [7]]) assert.throws(() => parseServerSignIn({...system(), signInMethods}), {code: 'invalid_system'});
  assert.equal(parseServerSignIn({...system(), signInMethods: ['passkey', 'password']}).signIn, 'local', 'a method this app does not know is ignored beside one it does');
  const probe = new ServerSignInProbe(async () => Response.json({...system(), setupRequired: true}));
  await probe.inspect('https://server.test'); assert.equal(probe.getSnapshot().destination, 'setup'); probe.dispose();
});
test('a newer server that adds fields to /system still offers sign-in', async () => {
  assert.deepEqual(parseServerSignIn(withUnknownFields(system())), parseServerSignIn(system()));
  const probe = new ServerSignInProbe(async () => Response.json(withUnknownFields(system('portico'))));
  await probe.inspect('https://server.test'); assert.equal(probe.getSnapshot().destination, 'portico-account'); probe.dispose();
});
test('cancelled/offline/timed-out/late probes cannot reopen an old password form', async () => {
  let answer!: (v: Response) => void;
  const probe = new ServerSignInProbe(() => new Promise(resolve => {answer = resolve;}), 10);
  const pending = probe.inspect('https://old.test'); probe.cancel(); answer(Response.json(system())); await pending;
  assert.equal(probe.getSnapshot().phase, 'idle');
  await probe.inspect('https://offline.test'); assert.equal(probe.getSnapshot().phase, 'error');
  answer(Response.json(system('portico'))); await new Promise(resolve => setTimeout(resolve, 1));
  assert.equal(probe.getSnapshot().phase, 'error'); probe.dispose();
});

test('default browser fetch is not invoked with the probe as its receiver', async () => {
  const original = globalThis.fetch;
  const calls: unknown[][] = [];
  globalThis.fetch = async function(this: unknown, input, init) {
    // The browser's native fetch accepts a value call and rejects an arbitrary
    // instance receiver. Node fetch alone would hide this production defect.
    if (this !== undefined && this !== globalThis) throw new TypeError('Illegal invocation');
    calls.push([this, input, init]);
    return Response.json(system());
  };
  const probe = new ServerSignInProbe();
  try {
    assert.equal((await probe.inspect('https://server.test'))?.signIn, 'local');
    assert.equal(probe.getSnapshot().phase, 'ready');
    assert.equal(calls.length, 1);
    assert.equal(calls[0][1], 'https://server.test/v1/system');
    const init = calls[0][2] as RequestInit;
    assert.equal(init.credentials, 'omit');
    assert.equal(init.redirect, 'error');
    assert.equal(new Headers(init.headers).has('Authorization'), false);
  } finally {probe.dispose(); globalThis.fetch = original;}
});
test('injected value and explicitly bound fetch transports preserve their invocation contract', async () => {
  const receivers: unknown[] = [];
  const transport: typeof fetch = async function(this: unknown) {
    receivers.push(this);return Response.json(system('portico'));
  };
  const owner = {name: 'injected transport'};
  for (const [fetcher, expected] of [[transport, undefined], [transport.bind(owner), owner]] as const) {
    const probe = new ServerSignInProbe(fetcher);
    try {
      assert.equal((await probe.inspect('https://server.test'))?.signIn, 'portico');
      assert.equal(receivers.at(-1), expected);
    } finally {probe.dispose();}
  }
});

// If a server and a client can talk, allow the connection.
test('negotiation connects on a shared API major, gates on capabilities, and refuses a server with no major in common', async () => {
  const {parseServerSignIn, negotiateServer, API_LEVEL_MISMATCH, APP_UPDATE_REQUIRED, CLIENT_API} = await import('../src/sign-in.ts');
  const {API_LEVEL, CAPABILITIES} = await import('../../contracts/src/api-level.ts');
  const raw = {...system(), apiMajor: API_LEVEL};
  for (const apiMajor of [undefined, 0, -1, 1.5, '1', null]) assert.throws(() => parseServerSignIn({...raw, apiMajor}), /did not report how to sign in/);
  for (const capabilities of [undefined, 'x']) assert.throws(() => parseServerSignIn({...raw, capabilities}), /did not report how to sign in/);
  // The 1.0 floor connects; nothing baseline is ever gated.
  const floor = negotiateServer(parseServerSignIn(raw));
  assert.equal(floor.major, API_LEVEL); assert.deepEqual(floor.capabilities, []);
  // A capability added after 1.0 gates its own feature; one this app does not know is carried and ignored.
  const newer = negotiateServer(parseServerSignIn({...raw, capabilities: ['future.thing', 'future.thing', 'alpha', 7]}));
  assert.deepEqual(newer.capabilities, ['alpha', 'future.thing']); assert.equal(newer.can('alpha'), true); assert.equal(newer.can('beta'), false);
  // No overlap: named for whoever must update.
  assert.throws(() => negotiateServer(parseServerSignIn({...raw, apiMajor: API_LEVEL + 1})), (error: {code?: string}) => error.code === APP_UPDATE_REQUIRED);
  assert.throws(() => negotiateServer(parseServerSignIn(raw), {min: API_LEVEL + 1, max: API_LEVEL + 2}), (error: {code?: string}) => error.code === API_LEVEL_MISMATCH);
  assert.deepEqual(CLIENT_API, {min: 1, max: API_LEVEL}); assert.deepEqual(CAPABILITIES, [], '1.0 advertises nothing: every catalogue entry is baseline');
});
