import test from 'node:test';
import assert from 'node:assert/strict';
import {SetupFlow, type SetupPorts, type SetupServer} from '../src/screens/setup/setup-flow.ts';
import {defaultSetupServerName, localPasswordValid, setupErrorMessage, validLocalUsername} from '../src/screens/setup/setup-model.ts';
import {SetupStageFailure, addressSetupFailure, notLocalSourceFailure} from '../src/platform/setup-failure.ts';
import type {SetupDraft} from '@core/setup-onboarding.ts';

function session(serverId = 'server-1') {
  const now = Date.now();
  return {accessToken: 'access-token', expiresAt: new Date(now + 3_600_000).toISOString(),
    authorizationHorizon: new Date(now + 7_200_000).toISOString(), sessionFamilyId: 'setup-family', tokenGeneration: '1',
    viewer: {accountId: 'owner-account', profileId: 'main-profile', serverId, authority: 'local', role: 'owner'}};
}

function fixture(input: Readonly<{draft?: boolean; resume?: unknown; hosted?: () => Promise<void>; add?: (value: unknown) => Promise<void>; admittedOwner?: boolean; setupRequired?: boolean; discardStale?: () => Promise<boolean>}> = {}) {
  const calls: {name: string; value?: unknown}[] = [], completions: number[] = [];
  let requests = 0;
  const server: SetupServer = {
    server: {id: 'server-1', name: 'Portico demo', version: '1', signIn: 'local', move: null, setupRequired: input.setupRequired ?? true, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []},
    ...(input.admittedOwner ? {admittedOwner: true as const} : {}),
    api: {getRouteConnection: () => ({pin: {serverId: 'server-1'}}), request: async (path: string) => {requests++; calls.push({name: 'request', value: path});
      if (path.includes('setup/browser')) throw Object.assign(new Error('not allowed from this address'), {status: 401});
      return input.resume ?? {variant: 'session', ...session()};}} as never,
    pin: {serverId: 'server-1', publicKey: 'x', fingerprint: 'y'} as never,
    draft: {read: async () => input.draft ? {version: 1, serverId: 'server-1', requestId: '11111111-2222-4333-8444-555555555555', setupToken: 'token', name: 'Portico demo', authMode: 'local'} : undefined,
      change: async () => {}} as never,
    dispose: () => {calls.push({name: 'dispose'});},
  };
  let accepted = 0;
  const ports: SetupPorts = {
    connect: async () => server,
    acceptLocal: async () => {accepted++; calls.push({name: 'accept'});},
    continueHosted: input.hosted ?? (async () => {calls.push({name: 'hosted'});}),
    addLibrary: input.add ?? (async value => {calls.push({name: 'addLibrary', value});}),
    complete: () => {completions.push(1);},
    hostname: () => 'demo.getportico.tv',
    ...(input.discardStale ? {discardStaleDraft: input.discardStale} : {}),
  };
  return {flow: new SetupFlow(ports), calls, completions, requests: () => requests, accepted: () => accepted, server};
}

test('a fresh server opens the drawn form with its own name and no requests before submit', async () => {
  const f = fixture();
  try {
    await f.flow.start();
    const state = f.flow.getSnapshot();
    assert.equal(state.phase, 'form'); assert.equal(state.serverName, 'Portico demo'); assert.equal(state.name, 'Portico demo');
    assert.equal(state.mode, 'hosted'); assert.equal(state.busy, false);
    assert.deepEqual(f.calls.filter(call => call.name === 'request'), []);
  } finally {f.flow.dispose();}
});

test('Continue to Portico Account hands the exact name to the guarded hosted port', async () => {
  const f = fixture();
  try {
    await f.flow.start(); f.flow.setName('  Family server  ');
    await f.flow.submitHosted();
    assert.deepEqual(f.calls.find(call => call.name === 'hosted'), {name: 'hosted'});
    assert.equal(f.flow.getSnapshot().phase, 'waiting');
  } finally {f.flow.dispose();}
});

test('local setup refuses invalid fields without spending a setup authorization', async () => {
  const f = fixture();
  try {
    await f.flow.start(); f.flow.setMode('local');
    f.flow.setUsername('a'); f.flow.setPassword('short'); f.flow.setConfirm('short');
    await f.flow.submitLocal();
    f.flow.setUsername('owner name'); f.flow.setPassword('a long enough password 1'); f.flow.setConfirm('different password 2');
    await f.flow.submitLocal();
    assert.equal(f.requests(), 0);
  } finally {f.flow.dispose();}
});

test('a refused setup authorization shows the drawn network copy and clears the password', async () => {
  const f = fixture();
  try {
    await f.flow.start(); f.flow.setMode('local'); f.flow.setUsername('owner'); f.flow.setPassword('a long enough password 1'); f.flow.setConfirm('a long enough password 1');
    await f.flow.submitLocal();
    const state = f.flow.getSnapshot();
    assert.equal(state.phase, 'form'); assert.equal(state.busy, false);
    assert.equal(state.password, ''); assert.equal(state.confirm, '');
    assert.equal(state.error, 'Setup could not be authorized at this address. Open the server using its IP address, or allow this hostname in the server configuration.');
  } finally {f.flow.dispose();}
});

test('an interrupted local setup resumes its protected draft and admits the owner before the first library', async () => {
  const f = fixture({draft: true, resume: {variant: 'session', ...session()}});
  try {
    await f.flow.start();
    assert.equal(f.accepted(), 1);
    assert.equal(f.flow.getSnapshot().phase, 'library');
    assert.deepEqual(f.calls.find(call => call.name === 'request')?.value, '/v1/setup/resume');
  } finally {f.flow.dispose();}
});

test('the first library sends the drawn kind, name and path, and Skip completes without a write', async () => {
  const f = fixture({draft: true, resume: {variant: 'session', ...session()}});
  try {
    await f.flow.start();
    f.flow.setKind('tv'); f.flow.setLibraryName('Shows'); f.flow.setPath('/srv/media/TV');
    await f.flow.addLibrary();
    assert.deepEqual(f.calls.find(call => call.name === 'addLibrary')?.value, {kind: 'tv', name: 'Shows', path: '/srv/media/TV'});
    assert.equal(f.completions.length, 1);
  } finally {f.flow.dispose();}
  const skip = fixture({draft: true, resume: {variant: 'session', ...session()}});
  try {
    await skip.flow.start(); skip.flow.skip();
    assert.equal(skip.completions.length, 1);
    assert.equal(skip.calls.some(call => call.name === 'addLibrary'), false);
  } finally {skip.flow.dispose();}
});

test('only a trusted admitted-owner return opens the first-library phase on a configured server', async () => {
  const refused = fixture({setupRequired: false});
  try {
    await refused.flow.start();
    assert.equal(refused.flow.getSnapshot().phase, 'error');
  } finally {refused.flow.dispose();}
  const returned = fixture({setupRequired: false, admittedOwner: true});
  try {
    await returned.flow.start();
    assert.equal(returned.flow.getSnapshot().phase, 'library');
    assert.equal(returned.accepted(), 0);
  } finally {returned.flow.dispose();}
});

test('the setup values keep the drawn boundary rules', () => {
  assert.equal(defaultSetupServerName('Portico', 'demo.getportico.tv'), 'demo.getportico.tv');
  assert.equal(defaultSetupServerName('Portico', '192.168.1.20'), 'My Portico server');
  assert.equal(defaultSetupServerName('Stored name', 'demo'), 'Stored name');
  assert.equal(validLocalUsername('demo'), true);
  assert.equal(validLocalUsername('two words'), false);
  assert.equal(validLocalUsername('demo@example.com'), false);
  assert.equal(localPasswordValid('a long enough password 1', 'a long enough password 1'), true);
  assert.equal(localPasswordValid('a long enough password 1', 'another password 2'), false);
  assert.match(setupErrorMessage(new SetupStageFailure('browser-start', {status: 403})), /could not be authorized/);
  assert.doesNotMatch(setupErrorMessage(new SetupStageFailure('claim', {status: 403})), /could not be authorized at this address/);
  assert.doesNotMatch(setupErrorMessage({status: 403}), /could not be authorized at this address/);
});

// A Portico Account setup never falls back to local (Justin, 31 Oct): no failure carries
// a switch to local sign-in, and the flow has no way to make one.
test('no setup failure offers local sign-in', () => {
  const failure = new SetupStageFailure('claim', {code: 'claim_unavailable', status: 503}, async () => {});
  assert.equal('useLocal' in failure, false);
  assert.equal('useLocalFailure' in new SetupFlow({} as SetupPorts), false);
});

const notLocalLine = 'Setup has to start from a device on the same network as this server. Open this page from there, or use a claim token.';
test('a setup_source_not_local refusal shows the one honest line, with no retry to dead-end on', async () => {
  const refusal = new SetupStageFailure('browser-start', Object.assign(new Error('forbidden'), {status: 403, code: 'setup_source_not_local'}));
  assert.equal(setupErrorMessage(refusal), notLocalLine);
  assert.equal(setupErrorMessage(Object.assign(new Error('forbidden'), {status: 403, code: 'setup_source_not_local'})), notLocalLine);
  assert.equal(addressSetupFailure(refusal), false);
  assert.equal(notLocalSourceFailure(refusal), true);
  const f = fixture({hosted: async () => {throw refusal;}});
  try {
    await f.flow.start(); await f.flow.submitHosted();
    const state = f.flow.getSnapshot();
    assert.equal(state.phase, 'error'); assert.equal(state.error, notLocalLine);
    assert.equal(state.failure, refusal); assert.equal(refusal.retry, undefined);
  } finally {f.flow.dispose();}
});
test('a not-local refusal on a local setup keeps the form and says the same line', async () => {
  const f = fixture();
  (f.server.api as {request: unknown}).request = async () => {throw Object.assign(new Error('forbidden'), {status: 403, code: 'setup_source_not_local'});};
  try {
    await f.flow.start(); f.flow.setMode('local'); f.flow.setUsername('owner'); f.flow.setPassword('a long enough password 1'); f.flow.setConfirm('a long enough password 1');
    await f.flow.submitLocal();
    assert.equal(f.flow.getSnapshot().error, notLocalLine);
  } finally {f.flow.dispose();}
});

test('a saved draft whose attempt expired unapproved is dropped for the start form and a quiet line, never a failure', async () => {
  const f = fixture({draft: true, discardStale: async () => true});
  try {
    await f.flow.start();
    const state = f.flow.getSnapshot();
    assert.equal(state.phase, 'form'); assert.equal(state.earlierExpired, true); assert.equal(state.error, undefined); assert.equal(state.failure, undefined);
    assert.equal(f.accepted(), 0); assert.equal(f.requests(), 0, 'no resume request for a dead draft');
  } finally {f.flow.dispose();}
});
test('a saved draft that is still live resumes as before', async () => {
  const f = fixture({draft: true, discardStale: async () => false});
  try {
    await f.flow.start();
    const state = f.flow.getSnapshot();
    assert.equal(state.phase, 'library'); assert.equal(state.earlierExpired, undefined); assert.equal(f.accepted(), 1);
  } finally {f.flow.dispose();}
});
test('setup_in_progress says so honestly with a local time, and Try again just connects again', async () => {
  const refusal = () => new SetupStageFailure('browser-start', Object.assign(new Error('another browser'), {status: 409, code: 'setup_in_progress', retryAfterSeconds: 600}));
  const now = Date.UTC(2030, 0, 2, 3, 4, 5), time = new Date(now + 600_000).toLocaleTimeString(undefined, {hour: 'numeric', minute: '2-digit'});
  assert.equal(setupErrorMessage(refusal(), now), `Setup is already in progress in another browser. You can start over after ${time}.`);
  assert.doesNotMatch(setupErrorMessage(refusal(), now), /changed while you were looking/);
  assert.equal(setupErrorMessage(Object.assign(new Error('x'), {status: 409, code: 'setup_in_progress'})), 'Setup is already in progress in another browser.');
  let attempts = 0;
  const f = fixture();
  const flow = new SetupFlow({...(f as never as {flow: {ports: SetupPorts}}).flow?.ports, connect: async () => {if (attempts++ === 0) throw refusal(); return f.server;},
    acceptLocal: async () => {}, continueHosted: async () => {}, addLibrary: async () => {}, complete: () => {}, hostname: () => 'demo.getportico.tv'});
  try {
    await flow.start();
    assert.equal(flow.getSnapshot().phase, 'error'); assert.match(flow.getSnapshot().error ?? '', /already in progress in another browser/);
    await flow.retry();
    assert.equal(attempts, 2); assert.equal(flow.getSnapshot().phase, 'form');
  } finally {flow.dispose();}
});
