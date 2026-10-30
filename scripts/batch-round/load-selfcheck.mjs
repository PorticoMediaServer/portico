/** Offline two-user/four-second request rehearsal. No socket or full-app acceptance. */
import assert from 'node:assert/strict';
import {setTimeout as delay} from 'node:timers/promises';
import {createServer} from 'node:http';
import {randomBytes} from 'node:crypto';
import {mkdtemp, rm, writeFile, unlink} from 'node:fs/promises';
import {join, resolve} from 'node:path';
import {MemoryMutationStore, FileMutationStore, lockMutationDirectory, mutationClient, savedBatch, finalStop,
  FileConnectionStore, privateStoreDirectory, seedConnection, refreshBroker, refreshChild, readServerState, readPrepReceipt} from './load-adapters.ts';
import {parseRememberedServer} from '../../packages/client-core/src/remembered-server.ts';
import {HttpLocalApi} from '../../packages/client-core/src/index.ts';
import {LocalSessionRefresher} from '../../packages/client-core/src/session-refresh.ts';
import {SessionsClient} from '../../packages/client-core/src/playback-v1/sessions.ts';
import {ObservationReporter} from '../../packages/client-core/src/playback-v1/observation.ts';
import {personalBatchBody} from '../../packages/client-core/src/personal-saved.ts';
import {emptyUserMutationState, USER_MUTATION_MAX_ENTRIES} from '../../packages/client-core/src/user-mutations.ts';
import {readBrowsePage, readReadingFacet, releaseBrowseSelection} from '../../packages/client-core/src/browse-preparation.ts';
import {options, phaseWindows, Metrics, observedHardwareGate, validateManifest, ingestTrendQualification} from './load.ts';

export async function selfCheck(o) {
  assert.equal(o.execute, false);
  const sent = [], wire = [], clients = [], reporters = [];
  const directory = await mkdtemp(join(resolve(process.env.TMPDIR ?? '.'), 'load-selfcheck-'));
  let unlock;
  try {
    unlock = await lockMutationDirectory(directory);
    await assert.rejects(lockMutationDirectory(directory), {code: 'EEXIST'});
    const disk = new FileMutationStore(join(directory, 'cas.json'));
    const initial = emptyUserMutationState();
    assert.deepEqual(await disk.read(), initial);
    assert.deepEqual(await Promise.all([disk.compareAndSwap(0, {...initial, revision: 1}), disk.compareAndSwap(0, {...initial, revision: 1})]), [true, false]);
    assert.equal((await new FileMutationStore(join(directory, 'cas.json')).read()).revision, 1);
    const memory = new MemoryMutationStore();
    await assert.rejects(memory.compareAndSwap(0, {...initial, revision: 1, entries: Array(USER_MUTATION_MAX_ENTRIES + 1).fill({})}));
    for (let i = 0; i < 2; i++) {
      const store = i === 0 ? new FileMutationStore(join(directory, 'user0.json')) : new MemoryMutationStore(), receipts = new Map();
      const session = {accessToken: 'offline-token-' + i, expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z',
        tokenGeneration: '1', sessionFamilyId: 'family-' + i, deviceId: 'device-' + i,
        viewer: {serverId: 'offline-server', authority: 'local', accountId: 'account-' + i, profileId: 'profile-' + i, role: 'member'}};
      const transport = async (input, init = {}) => {
        const url = new URL(String(input)), path = url.pathname, method = init.method ?? 'GET', headers = new Headers(init.headers);
        assert.equal(url.origin, 'http://offline.invalid');
        assert.equal(headers.get('Authorization'), 'Bearer ' + session.accessToken);
        const body = init.body ? JSON.parse(init.body) : undefined;
        wire.push({i, path, method, body, key: headers.get('Idempotency-Key')});
        if (path === '/v1/user-mutations/policy') return Response.json({serverTimeMs: Date.now(), retiredBeforeMs: 0});
        if (path.startsWith('/v1/user-mutations/')) {
          const receipt = receipts.get(path.split('/').at(-1));
          return receipt ? Response.json(receipt) : Response.json({error: {code: 'receipt_unknown'}}, {status: 404});
        }
        if (path.endsWith('/observer')) return Response.json({epoch: 'abcdefghijklmnopqrstuv', everyMs: 1000});
        if (path.endsWith('/observation')) return Response.json({acceptedSeq: body.seq});
        if (method === 'DELETE' && path.startsWith('/v1/playback/sessions/')) {
          const entry = (await store.read()).entries.find(entry => entry.key === headers.get('Idempotency-Key'));
          assert.ok(entry, 'custody precedes Stop'); assert.equal(entry.scope.familyId, session.sessionFamilyId);
          assert.deepEqual(body, {generation: 7, positionMs: 3000});
          receipts.set(entry.key, {key: entry.key, operation: entry.operation, status: 'applied', generation: 7, positionMs: 3000,
            positionStatus: 'applied', committedMs: Date.now(), expiresMs: Date.now() + 86400000});
          return new Response(null, {status: 204});
        }
        if (path === '/v1/items/personal-state:batch') {
          const entry = (await store.read()).entries.find(entry => entry.key === headers.get('Idempotency-Key'));
          assert.ok(entry, 'custody precedes Saved'); assert.equal(body.operationId, entry.key);
          assert.equal(entry.scope.profileId, session.viewer.profileId);
          receipts.set(entry.key, {key: entry.key, operation: entry.operation, status: 'applied', committedMs: Date.now(), expiresMs: Date.now() + 86400000});
          // User 1 loses its answer after commit: real coordinator must look up the same key.
          if (i === 1) throw new Error('offline_lost_answer');
          return Response.json({serverId: 'offline-server', viewerFence: 'offline-fence', operationId: body.operationId,
            updated: 1, failed: 0, results: [{itemId: 'known-item', ok: true}]});
        }
        const pending = domain => Response.json({kind: domain + '-preparing', state: 'preparing', queryId: domain + '-' + i, retryAfterMs: 0}, {status: 202});
        if (path.endsWith('/browse') && method === 'POST') return pending('reading-browse');
        if (path.endsWith('/facets')) return pending('reading-facet');
        if (method === 'DELETE') return new Response(null, {status: 204});
        if (path.includes('/reading-browse-queries/')) return Response.json({pivot: 'books', applied: {query: null, sort: [], seek: null}, entries: [],
          pageInfo: {start: 0, total: 0, revision: 'r1', hasMore: false, nextCursor: ''},
          wholeSelectionPreparing: {kind: 'browse-selection-preparing', state: 'preparing', queryId: 'selection-' + i, retryAfterMs: 0}});
        if (path.includes('/reading-facet-queries/')) return Response.json({field: 'author', total: 1, values: [{value: 'Author', label: 'Author', count: 1}]});
        throw new Error('unexpected_offline_request');
      };
      const c = await mutationClient('http://offline.invalid', session, store, transport);
      clients.push({...c, store});
      const sessions = new SessionsClient({...c.http, send: request => {sent.push({i, ...request}); return c.http.send(request);}});
      const reporter = new ObservationReporter({everyMs: 1000,
        claim: body => sessions.claimObserver('session' + i, body), send: body => sessions.observe('session' + i, body)});
      reporter.setGeneration(7); reporter.observe({generation: 7, state: 'playing', positionMs: 0, rate: 1}); reporters.push(reporter);
      c.stop = finalStop(sessions, 'session' + i, 7);
      c.saved = savedBatch(c.api, 'offline-server', 'offline-selfcheck-' + i, [{itemId: 'known-item', watchlisted: true}], AbortSignal.timeout(3000));
      // Handle rejection immediately; joined below after the timed observer window.
      void c.saved.catch(() => {});
      const port = {request: c.api.request.bind(c.api), requestRaw: c.api.requestRaw.bind(c.api)};
      const lifetime = {isCurrent: () => true, signal: AbortSignal.timeout(3000)};
      c.read = (async () => {
        const result = await readBrowsePage(port, 'reading-library', {pivot: 'books', limit: 20}, lifetime);
        assert.equal(result.page.pivot, 'books'); releaseBrowseSelection(port, 'reading-library', result.response);
        assert.equal((await readReadingFacet(port, 'reading-library', '/v1/libraries/reading-library/facets?field=author', 'author', lifetime)).total, 1);
      })();
      void c.read.catch(() => {});
      clients.at(-1).work = c;
    }
    await delay(3200);
    reporters.forEach(reporter => reporter.stop());
    const saved = await Promise.all(clients.map(c => c.work.saved));
    assert.deepEqual(saved.map(result => result.recovered), [false, true]);
    assert.equal(saved[0].updated, 1); assert.equal(saved[1].updated, undefined);
    await Promise.all(clients.map(c => c.work.read));
    await Promise.all(clients.map(c => c.work.stop(3000, AbortSignal.timeout(2000))));
    // A second caller cannot change the first final position or issue a new command.
    await Promise.all(clients.map(c => c.work.stop(9000, AbortSignal.timeout(2000))));
    // The first completion was consumed by SessionsClient; the helper must memoize success.
    const observations = sent.filter(request => request.method === 'PUT');
    assert.equal(sent.filter(request => request.method === 'POST').length, 2);
    assert.equal(wire.filter(request => request.method === 'DELETE' && request.path.startsWith('/v1/playback/sessions/')).length, 2);
    assert.ok(observations.length >= 6);
    for (const request of observations) {
      assert.equal(request.singleAttempt, true); assert.equal(request.deadlineMs, 1500);
      assert.ok(request.body.monotonicMs >= request.body.playClockMs); assert.ok(request.body.seq >= 1);
    }
    for (const c of clients) assert.equal((await c.store.read()).entries.length, 0);
    assert.equal(wire.filter(request => request.path === '/v1/items/personal-state:batch').length, 2, 'lost answer never reauthors Saved');
    assert.equal(wire.filter(request => request.method === 'DELETE' && request.path.includes('-queries/')).length, 6, 'each prepared handle released');
    const body = personalBatchBody('offline-selfcheck', [{itemId: 'known-item', watchlisted: true}]);
    assert.equal(body.items[0].watchlisted, true); assert.throws(() => personalBatchBody('x', []));
    const before = wire.length; await delay(800); assert.equal(wire.length, before, 'no sends after retirement');
    const refreshStorage = await refreshStorageReadbackSelfCheck(directory);
    const orchestration = await orchestrationWindowsSelfCheck(directory);
    const modes = await modeBoundarySelfCheck(directory);
    assert.throws(() => options(['--execute', '--self-check']));
    for (const key of ['CI', 'GITHUB_ACTIONS']) {
      const previous = process.env[key]; process.env[key] = 'false';
      try {assert.throws(() => options(['--execute']), {code: 'execute_forbidden_in_ci'});}
      finally {if (previous === undefined) delete process.env[key]; else process.env[key] = previous;}
    }
    return {kind: 'offline-request-boundary-self-check', users: 2, seconds: 4, passed: true, observationRequests: observations.length,
      cleanupCommands: 2, networkRequests: 0, savedReceipts: 2, recoveredSavedReceipts: 1, preparedHandleReleases: 6,
      ...refreshStorage, ...orchestration, ...modes,
      scope: 'real HttpLocalApi custody/receipt lookup, observer, final Stop, Saved, Browse/Reading preparation, canonical refresh/storage/readback adapters; loopback only; full workload and capacity unrun'};
  } finally {
    reporters.forEach(reporter => reporter.stop()); clients.forEach(c => c.coordinator.dispose());
    await unlock?.(); await rm(directory, {recursive: true, force: true});
  }
}

/** Canonical refresh/storage/readback adapters against a loopback service. No external socket. */
async function refreshStorageReadbackSelfCheck(directory) {
  const encode = bytes => Buffer.from(bytes).toString('base64url');
  const digest = async bytes => new Uint8Array(await crypto.subtle.digest('SHA-256', bytes));
  // The same `srv_` derivation the server and `serverPin` use.
  const raw = randomBytes(32), context = new TextEncoder().encode('portico.server.identity.v1\0');
  const joined = new Uint8Array(context.length + raw.length); joined.set(context); joined.set(raw, context.length);
  const identity = {serverId: 'srv_' + encode(await digest(joined)), publicKey: encode(raw), fingerprint: encode(await digest(raw))};
  const newToken = () => 'refresh-' + randomBytes(18).toString('base64url');
  const stamp = offsetMs => new Date(Date.now() + offsetMs).toISOString().replace(/\.\d{3}Z$/, 'Z');
  const isoSecond = ms => new Date(ms).toISOString().replace(/\.\d{3}Z$/, 'Z');
  const installationId = 'installation-' + randomBytes(18).toString('hex');
  const otherInstallationId = 'installation-' + randomBytes(18).toString('hex');
  let refreshToken = 'initial-' + randomBytes(18).toString('base64url');
  let accessToken = 'access-' + randomBytes(18).toString('base64url'), generation = 1, revoked = false;
  const seen = [];
  const session = (expiresIn = 15 * 60_000) => ({accessToken, expiresAt: stamp(expiresIn), authorizationHorizon: stamp(86_400_000), sessionFamilyId: 'family-offline',
    tokenGeneration: String(generation), deviceId: 'device-offline', installationId, refreshToken, serverIdentity: {publicKey: identity.publicKey, fingerprint: identity.fingerprint},
    viewer: {serverId: identity.serverId, authority: 'local', accountId: 'account-offline', profileId: 'profile-offline', role: 'owner'}});
  const server = createServer(async (request, response) => {
    const chunks = []; for await (const chunk of request) chunks.push(chunk);
    const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString('utf8')) : undefined;
    seen.push({path: request.url, method: request.method, installation: request.headers['x-portico-installation-id'], body});
    const send = (status, value) => {response.writeHead(status, {'Content-Type': 'application/json'}); response.end(JSON.stringify(value));};
    if (request.url === '/v1/auth/refresh') {
      if (revoked || body.refreshToken !== refreshToken) return send(401, {error: {code: 'refresh_refused'}});
      // One canonical successor: new access, new refresh credential, higher generation, same family and installation.
      accessToken = newToken(); refreshToken = 'rotated-' + randomBytes(18).toString('base64url'); generation++;
      return send(200, session());
    }
    if (request.url === '/v1/admin/playback/diagnostics') {
      const at = Date.now();
      return send(200, {scope: {serverId: identity.serverId, viewerFence: 'a'.repeat(64)},
        observedAt: isoSecond(at), freshUntil: isoSecond(at + 30_000), diagnostics: {directConfigured: true, hlsConfigured: false,
          finiteHlsConfigured: false, lifecycle: 'unavailable', activeConversionSessions: null, conversionSessionLimit: null,
          conversionLimitSource: 'unavailable', outputPolicy: 'per_session_plan'}});
    }
    if (request.url === '/v1/admin/storage/status') return send(200, {databasePath: '/srv/appdata/portico/catalog.sqlite',
      cacheDirectory: '/srv/appdata/portico/cache', backupDirectory: '/srv/appdata/backups', reclaimableBytes: 0, lastReclaimedBytes: 0});
    send(404, {error: {code: 'not_found'}});
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  try {
    await privateStoreDirectory(directory);
    const storePath = join(directory, 'connection.json'), store = new FileConnectionStore(storePath);
    assert.equal(await store.read(), undefined);
    // A token that cannot cover the window: the canonical owner renews it rather than treating it as fresh.
    const recorded = session(30_000);
    const pin = await seedConnection(store, origin, recorded, {publicKey: identity.publicKey, fingerprint: identity.fingerprint});
    assert.equal(pin.serverId, identity.serverId); assert.equal(pin.fingerprint, identity.fingerprint);
    // A damaged record is told apart from "not now", and a read-back session is canonical.
    await writeFile(storePath, '{"version":"1"}', {mode: 0o600});
    await assert.rejects(store.read(), error => error.code === 'invalid_saved_connection');
    await unlink(storePath);
    await seedConnection(store, origin, recorded, {publicKey: identity.publicKey, fingerprint: identity.fingerprint});
    // A different family is operator work: never silently replaced.
    await assert.rejects(seedConnection(store, origin, {...recorded, sessionFamilyId: 'family-other'}, identity), /another_family/);
    const broker = refreshBroker({target: origin, directory, storePath, serverId: identity.serverId, family: 'family-offline', installationId});
    const api = new HttpLocalApi(origin, accessToken);
    // The isolated child is the only place the process-global installation identity is set.
    await assert.rejects(refreshChild([await requestFile(directory, {target: origin, store: storePath, serverId: identity.serverId,
      family: 'family-offline', installationId: otherInstallationId})]), /refresh_installation_mismatch/);
    const rotated = await broker({rejected: undefined});
    assert.notEqual(rotated.accessToken, recorded.accessToken);
    assert.equal(rotated.tokenGeneration, '2'); assert.equal(rotated.installationId, installationId);
    assert.equal(rotated.sessionFamilyId, 'family-offline'); assert.equal(rotated.viewer.accountId, 'account-offline');
    // The broker returns the validated stored record; the app-facing refresher holds only the
    // session without the rotating credential and adopts the new access token after each rotation.
    assert.match(rotated.refreshToken, /^rotated-/u);
    const {refreshToken: _secret, ...visible} = rotated;
    const refresher = new LocalSessionRefresher({env: {storage: store, fetcher: fetch}, api, session: visible,
      refresh: broker, leadMs: 60_000, retryMs: [60_000]});
    try {
      assert.ok(!('refreshToken' in refresher.current()), 'the app-facing session never carries the credential');
      assert.equal(refresher.current().accessToken, rotated.accessToken);
    } finally {refresher.stop();}
    const stored = parseRememberedServer(await store.read());
    assert.equal(stored.session.accessToken, rotated.accessToken);
    assert.match(stored.session.refreshToken, /^rotated-/u);
    const refreshes = seen.filter(entry => entry.path === '/v1/auth/refresh');
    assert.equal(refreshes.length, 1);
    // Exactly one installation claim travelled, and it was this identity's.
    assert.equal(refreshes[0].installation, installationId);
    assert.equal(refreshes[0].body.installationId, installationId);
    assert.match(refreshes[0].body.requestId, /^[-A-Za-z0-9_]{1,128}$/u);
    const state = await readServerState(api, {serverId: identity.serverId, appdataRoot: directory});
    assert.deepEqual(state, {directConfigured: true, conversionsConfigured: false, databaseUnderExpectedRoot: false, reclaimableBytes: 0});
    await writeFile(join(directory, 'prep-receipt.json'), JSON.stringify({version: 1, kind: 'portico-appdata', receipt: 'prep-1',
      bootstrap: {stopped: true}, scan: {terminal: true}, playable: {items: 12, minDurationSeconds: 2400, durationSource: 'probe'},
      storage: {databaseUnderAppdata: true}, serverId: 'srv-offline'}), {mode: 0o600});
    assert.deepEqual(await readPrepReceipt(directory), {receipt: 'prep-1', serverId: 'srv-offline', playableItems: 12,
      mediaMinDurationSeconds: 2400, mediaDurationSource: 'probe', databaseUnderAppdata: true, scanTerminal: true, stopped: true});
    await writeFile(join(directory, 'prep-receipt.json'), JSON.stringify({version: 1, kind: 'other'}), {mode: 0o600});
    await assert.rejects(readPrepReceipt(directory), /prep_receipt_invalid|invalid_prep_receipt/);
    // A refused credential ends the sign-in and removes the stored record; nothing is retried silently.
    revoked = true;
    await assert.rejects(broker({rejected: rotated.accessToken}), /refresh_child_refused/);
    assert.equal(await store.read(), undefined);
    return {connectionStoreChecks: 6, refreshRotations: 1, refreshRefusals: 1, readbackChecks: 2, prepReceiptChecks: 2, loopbackRequests: seen.length};
  } finally {await new Promise(resolve => server.close(resolve));}
}

/** Phase windows + item markers + per-op p50/p95/p99, exercised without a server. */
async function orchestrationWindowsSelfCheck(directory) {
  const control = join(directory, 'control.json'), metrics = new Metrics(), marks = [], phases = [];
  const write = (phase, items, event) => writeFile(control, JSON.stringify({phase, ingestedItems: items, event, at: new Date().toISOString()}), {mode: 0o600});
  await write('baseline', 0, 'phase-start');
  const windows = phaseWindows(control, [3, 9], (marker, items) => marks.push({marker, items}), phase => {phases.push(phase); metrics.beginPhase(phase);}, 20);
  try {
    await delay(80); await metrics.measure('read', async () => {});
    await write('phase-1', 3, 'resident-read'); await delay(100);
    await metrics.measure('read', async () => {});
    await write('phase-1', 3, 'resident-read'); await delay(80);
    await write('after', 9, 'fixture-end'); await delay(80);
    await metrics.measure('read', async () => {});
    await writeFile(control, 'not json', {mode: 0o600}); await delay(80);
  } finally {windows.stop(); metrics.beginPhase('done');}
  assert.deepEqual(marks, [{marker: 3, items: 3}, {marker: 9, items: 9}]);
  assert.ok(phases.includes('baseline') && phases.includes('phase-1') && phases.includes('after'));
  assert.ok(windows.errors > 0);
  for (const name of ['baseline', 'phase-1', 'after']) {
    const phase = metrics.phaseReceipt()[name];
    assert.equal(phase.operations.read.latency.count, 1);
    assert.ok(Number.isFinite(Date.parse(phase.startedAtEpoch)));
    assert.ok(Number.isFinite(Date.parse(phase.endedAtEpoch)));
  }
  return {phaseWindows: 3, markerReads: marks.length, controlErrors: windows.errors};
}

/** Mode boundaries: ingest is non-capacity and bounded, observed-hardware needs its gate. */
async function modeBoundarySelfCheck(directory) {
  const campaign = 'http://100.120.50.24:8443';
  const base = ['--execute', '--manifest', '/nonexistent/manifest.json', '--target', campaign,
    '--out', '/nonexistent/out.json', '--mutation-store', '/nonexistent/store'];
  assert.throws(() => options([...base, '--ingest', '--users', '21', '--control', join(directory, 'c.json')]), {code: 'ingest_bounds_one_to_twenty'});
  assert.throws(() => options([...base, '--ingest', '--users', '10']), {code: 'ingest_requires_marker_file'});
  assert.throws(() => options([...base, '--observed-hardware']), {code: 'observed_hardware_requires_receipt'});
  assert.throws(() => options(['--observed-hardware', '--self-check']), {code: 'observed_hardware_requires_execute'});
  const ingest = options([...base, '--ingest', '--users', '10', '--control', join(directory, 'c.json')]);
  assert.equal(ingest.ingest, true); assert.equal(ingest.phases, true); assert.equal(ingest.observedHardware, false);
  // Ingest needs the six 50k buckets plus both ends; a campaign run keeps baseline/during/after.
  assert.deepEqual(ingest.requiredPhases, ['baseline', 'phase-1', 'phase-2', 'phase-3', 'phase-4', 'phase-5', 'phase-6', 'after']);
  assert.deepEqual(options([...base, '--users', '80']).requiredPhases, ['baseline', 'during', 'after']);
  // Ingest trend qualification is its own bar: evidence + measurements, never 80/100 capacity.
  const evidence = {evidence: true, qualified: true, missingPhases: [], missingMarkers: [], shortPhases: {}};
  const goodGates = {readLatency: true, noErrorsOrRefusals: true, mediaBytes: true, observations: true};
  assert.equal(ingestTrendQualification(evidence, goodGates), true);
  assert.equal(ingestTrendQualification({...evidence, evidence: false}, goodGates), false);
  assert.equal(ingestTrendQualification(evidence, {...goodGates, readLatency: false}), false);
  assert.equal(ingestTrendQualification(evidence, {...goodGates, mediaBytes: false}), false);
  assert.equal(ingestTrendQualification(null, goodGates), false);
  const paired = options([...base, '--observed-hardware', '--hardware-receipt', join(directory, 'gate.json'), '--users', '80']);
  assert.equal(paired.observedHardware, true);
  // The manifest mode is a union only under the opt-in; 'never' stays the default requirement.
  const manifest = {version: 1, target: campaign, serverId: 'srv-x', conversionMode: 'observed-hardware',
    stateAdapterReceipt: 'r', owner: {}, quietMember: {}, users: []};
  const plain = options([...base, '--users', '80']);
  assert.throws(() => validateManifest(manifest, plain), {code: 'manifest_state_or_user_gap'});
  // The paired-window gate: a valid 0600 file for this target/cohort passes, a mismatched one fails.
  const gatePath = join(directory, 'gate.json'), now = Date.now();
  const gate = {kind: 'p2-65-paired-window', version: 1, target: campaign, serverId: 'srv-x',
    directUsers: 80, hardwareUsers: 2, startedAt: new Date(now - 60_000).toISOString(), endsAt: new Date(now + 600_000).toISOString()};
  await writeFile(gatePath, JSON.stringify(gate), {mode: 0o600});
  await observedHardwareGate(gatePath, {target: campaign, serverId: 'srv-x'}, {users: 80, hardwareUsers: 2, rehearsal: false});
  await writeFile(gatePath, JSON.stringify({...gate, directUsers: 100}), {mode: 0o600});
  await assert.rejects(observedHardwareGate(gatePath, {target: campaign, serverId: 'srv-x'}, {users: 80, hardwareUsers: 2, rehearsal: false}),
    {code: 'hardware_receipt_gate_mismatch'});
  await unlink(gatePath);
  await writeFile(gatePath, JSON.stringify(gate), {mode: 0o644});
  await assert.rejects(observedHardwareGate(gatePath, {target: campaign, serverId: 'srv-x'}, {users: 80, hardwareUsers: 2, rehearsal: false}),
    {code: 'hardware_receipt_requires_owned_0600_file'});
  return {ingestBounds: 20, modeBoundaries: 6, pairedGateChecks: 3, ingestTrendChecks: 5};
}

async function requestFile(directory, request) {
  const path = join(directory, 'selfcheck-request-' + randomBytes(6).toString('hex') + '.json');
  await writeFile(path, JSON.stringify(request), {mode: 0o600});
  return path;
}
