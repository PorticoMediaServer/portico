import test from 'node:test';
import assert from 'node:assert/strict';
import {SetupFlow, type SetupPorts, type SetupServer} from '../src/screens/setup/setup-flow.ts';
import {defaultSetupServerName, localPasswordValid, setupErrorMessage, validLocalUsername} from '../src/screens/setup/setup-model.ts';
import {SetupStageFailure} from '../src/platform/setup-failure.ts';
import type {SetupDraft} from '@core/setup-onboarding.ts';

function session(serverId = 'server-1') {
  const now = Date.now();
  return {accessToken: 'access-token', expiresAt: new Date(now + 3_600_000).toISOString(),
    authorizationHorizon: new Date(now + 7_200_000).toISOString(), sessionFamilyId: 'setup-family', tokenGeneration: '1',
    viewer: {accountId: 'owner-account', profileId: 'main-profile', serverId, authority: 'local', role: 'owner'}};
}

function fixture(input: Readonly<{draft?: boolean; resume?: unknown; hosted?: () => Promise<void>; add?: (value: unknown) => Promise<void>; admittedOwner?: boolean; setupRequired?: boolean}> = {}) {
  const calls: {name: string; value?: unknown}[] = [], completions: number[] = [];
  let requests = 0;
  const server: SetupServer = {
    server: {id: 'server-1', name: 'Portico demo', version: '1', signIn: 'local', move: null, setupRequired: input.setupRequired ?? true, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []},
    ...(input.admittedOwner ? {admittedOwner: true as const} : {}),
    api: {getRouteConnection: () => ({pin: {serverId: 'server-1'}}), request: async (path: string) => {requests++; calls.push({name: 'request', value: path});
      if (path.includes('setup/browser')) throw Object.assign(new Error('not allowed from this address'), {status: 401});
      return input.resume ?? session();}} as never,
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
  const f = fixture({draft: true, resume: session()});
  try {
    await f.flow.start();
    assert.equal(f.accepted(), 1);
    assert.equal(f.flow.getSnapshot().phase, 'library');
    assert.deepEqual(f.calls.find(call => call.name === 'request')?.value, '/v1/setup/resume');
  } finally {f.flow.dispose();}
});

test('the first library sends the drawn kind, name and path, and Skip completes without a write', async () => {
  const f = fixture({draft: true, resume: session()});
  try {
    await f.flow.start();
    f.flow.setKind('tv'); f.flow.setLibraryName('Shows'); f.flow.setPath('/srv/media/TV');
    await f.flow.addLibrary();
    assert.deepEqual(f.calls.find(call => call.name === 'addLibrary')?.value, {kind: 'tv', name: 'Shows', path: '/srv/media/TV'});
    assert.equal(f.completions.length, 1);
  } finally {f.flow.dispose();}
  const skip = fixture({draft: true, resume: session()});
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

// Auth1d source vectors. The server refuses browser-start while onboarding is
// pending, so an accepted recovery must use its original protected continuation.
for (const replace of [false, true]) test(`local recovery ${replace ? 'refuses a replaced draft' : 'uses the captured setup proof without browser-start'}`, async () => {
  let draft: SetupDraft | undefined;
  const failure = new SetupStageFailure('claim', {code: 'claim_unavailable', status: 503}, undefined, async () => {});
  const f = fixture({hosted: async () => {
    draft = {version: 1, serverId: 'server-1', requestId: '11111111-2222-4333-8444-555555555555', setupToken: 'original-private-proof', name: 'Portico demo', authMode: 'hosted'};
    throw failure;
  }});
  f.server.draft.read = async () => draft;
  f.server.draft.change = async update => {draft = update(draft);};
  const requests: {path: string; body: unknown}[] = [];
  f.server.api.request = (async (path: string, _method: string, body: unknown) => {
    requests.push({path, body});
    if (path === '/v1/setup/resume') return {serverId: 'server-1', requestId: draft!.requestId, claimRequired: true, authMode: 'hosted', revision: '1'};
    if (path === '/v1/setup') return session();
    throw new Error('No new browser setup proof may be requested.');
  }) as SetupServer['api']['request'];
  try {
    await f.flow.start(); await f.flow.submitHosted();
    assert.equal(f.flow.getSnapshot().failure, failure);
    await f.flow.useLocalFailure(failure);
    assert.equal(f.flow.getSnapshot().phase, 'form');
    if (replace) draft = {...draft!, requestId: 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'};
    const original = draft;
    f.flow.setUsername('owner'); f.flow.setPassword('a long enough password 1'); f.flow.setConfirm('a long enough password 1');
    await f.flow.submitLocal();
    assert.equal(requests.some(value => value.path === '/v1/setup/browser'), false);
    if (replace) {assert.equal(requests.length, 0); assert.equal(draft, original); assert.equal(f.accepted(), 0);}
    else {
      assert.deepEqual(requests.map(value => value.path), ['/v1/setup/resume', '/v1/setup']);
      assert.equal((requests[1].body as {setupToken: string}).setupToken, 'original-private-proof');
      assert.equal((requests[1].body as {requestId: string}).requestId, original!.requestId);
      assert.equal(f.flow.getSnapshot().phase, 'library'); assert.equal(f.accepted(), 1);
    }
  } finally {f.flow.dispose();}
});

test('a recovery callback cannot publish the local form after replacing its original proof', async () => {
  let draft: SetupDraft | undefined;
  const failure = new SetupStageFailure('claim', {code: 'claim_unavailable', status: 503}, undefined, async () => {draft = {...draft!, setupToken: 'foreign-proof'};});
  const f = fixture({hosted: async () => {
    draft = {version: 1, serverId: 'server-1', requestId: '11111111-2222-4333-8444-555555555555', setupToken: 'original-proof', name: 'Portico demo', authMode: 'hosted'};
    throw failure;
  }});
  f.server.draft.read = async () => draft;
  try {
    await f.flow.start(); await f.flow.submitHosted(); await f.flow.useLocalFailure(failure);
    assert.equal(f.flow.getSnapshot().phase, 'error'); assert.equal(f.flow.getSnapshot().mode, 'hosted');
    assert.equal(draft?.setupToken, 'foreign-proof'); assert.equal(f.requests(), 0);
  } finally {f.flow.dispose();}
});
