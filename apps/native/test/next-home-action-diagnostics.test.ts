import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createHomeActionDiagnostics,
  HOME_DIAGNOSTIC_LIMIT,
  HOME_DIAGNOSTIC_ORIGIN_LIMIT,
  HOME_DIAGNOSTIC_PREFIX,
} from '../src-next/platform/home-action-diagnostics.ts';

const records = () => {
  const lines: string[] = [];
  const logger = createHomeActionDiagnostics(line => lines.push(line));
  return {
    logger,
    lines,
    read: () =>
      lines.map(line => JSON.parse(line.slice(HOME_DIAGNOSTIC_PREFIX.length))),
  };
};
test('library menu receipts compare only local reference numbers and fixed descriptor flags', () => {
  const f = records();
  let called = 0;
  const method = () => {called++; throw Error('private-function-body');};
  const bindings = {libraryMenu: method, account: 'private-account', token: 'private-token'};
  const runtime = {...bindings};
  f.logger.libraryMenu('bindings', bindings);
  f.logger.libraryMenu('attached', runtime);
  f.logger.libraryMenu('screen', runtime);
  const [before, attached, screen] = f.read();
  assert.notEqual(before.runtimeRef, attached.runtimeRef);
  assert.equal(before.menuRef, attached.menuRef);
  assert.equal(attached.runtimeRef, screen.runtimeRef);
  assert.deepEqual([screen.own, screen.enumerable, screen.callable], [true, true, true]);
  assert.equal(called, 0);
  const getter = Object.defineProperty({}, 'libraryMenu', {get() {called++; throw Error('private-getter');}});
  assert.doesNotThrow(() => f.logger.libraryMenu('screen', getter));
  assert.equal(f.read()[3].callable, false);
  assert.equal(called, 0);
  const hostile = new Proxy({}, {getOwnPropertyDescriptor() {throw Error('private-descriptor');}});
  assert.doesNotThrow(() => f.logger.libraryMenu('screen', hostile));
  f.logger.error('app-render', new ReferenceError('private-reference'));
  assert.equal(f.read()[4].class, 'ReferenceError');
  assert.equal(f.lines.join('').includes('private'), false);
});
test('passive receipts classify canonical routes and correlate only local numeric request IDs', () => {
  const f = records();
  const ticket = f.logger.request(
    'GET',
    'https://private-user:private-password@private-host/v1/items/private-item/playback-options?token=private-token',
  );
  f.logger.response(ticket, 200);
  const receipts = f.read();
  assert.equal(receipts.length, 2, 'one dispatch and its single response are the entire receipt stream');
  const [dispatch, response] = receipts;
  assert.deepEqual(dispatch, {
    v: 1,
    sequence: 1,
    origin: 'native-http',
    operation: 'dispatch',
    request: 1,
    route: 'get_playback_options',
  });
  const {elapsedMs, ...classified} = response;
  assert.deepEqual(classified, {
    v: 1,
    sequence: 2,
    origin: 'native-http',
    operation: 'response',
    request: 1,
    route: 'get_playback_options',
    status: 200,
  });
  assert.ok(
    Number.isSafeInteger(elapsedMs) && elapsedMs >= 0 && elapsedMs <= 60_000,
    'elapsed timing is a bounded local number',
  );
  assert.equal(f.lines.join('').includes('private-'), false);
  assert.equal(
    f.logger.request('POST', 'https://private/v1/direct/sign-in'),
    undefined,
  );
  assert.equal(
    f.logger.request('GET', 'https://private/v1/media/private-token/file'),
    undefined,
  );
  assert.equal(f.logger.request('GET', 'invalid URL private-token'), undefined);
  assert.equal(
    f.logger.request(
      'DELETE',
      'https://private/v1/items/private-item/playback-options',
    ),
    undefined,
  );
  assert.equal(f.lines.length, 2);
});
test('known safe errors retain class/code/status and stripped frame coordinates without raw messages, paths or identifiers', () => {
  const f = records();
  const failure = {
    name: 'PlaybackApiError',
    code: 'request_failed',
    status: 500,
    get message() {
      throw Error('raw message must never be read');
    },
    stack:
      'Error: secret-message\n at secretFunction (https://private-host/private-path?secret-query:1:456)\nsecretName@1:789\n at token-name (/private/account.ts:23:7)',
    headers: {Authorization: 'Bearer secret-header'},
    body: {account: 'secret-account'},
  };
  f.logger.error('assembly-error', failure);
  assert.deepEqual(f.read()[0], {
    v: 1,
    sequence: 1,
    origin: 'assembly-error',
    operation: 'report-error',
    code: 'request_failed',
    class: 'PlaybackApiError',
    status: 500,
    frames: [
      [1, 456],
      [1, 789],
      [23, 7],
    ],
  });
  f.logger.error('assembly-error', {
    name: 'secret-class',
    code: 'secret-code',
    status: 'secret-status',
    stack: 'secret-message',
  });
  assert.equal(f.read()[1].code, 'other');
  assert.equal(f.read()[1].class, 'unknown');
  assert.equal(f.lines.join('').includes('secret'), false);
});
test('throwing sink and hostile error accessors cannot change return or throw behavior', async () => {
  const f = createHomeActionDiagnostics(() => {
    throw Error('sink failure');
  });
  const original = Object.assign(new TypeError('must be preserved'), {
    code: 'request_failed',
  });
  const callback = () => {
    f.action('home-card', 'primary-play');
    return original;
  };
  assert.equal(callback(), original);
  const ticket = f.request('POST', 'https://private/v1/playback/sessions');
  assert.ok(ticket);
  assert.doesNotThrow(() => f.response(ticket, 201));
  assert.doesNotThrow(() => f.rejected(ticket, original));
  assert.doesNotThrow(() =>
    f.error('assembly-error', {
      get code() {
        throw original;
      },
    }),
  );
  await assert.rejects(
    (async () => {
      try {
        throw original;
      } catch (error) {
        f.rejected(ticket, error);
        throw error;
      }
    })(),
    error => error === original,
  );
});
test('all receipts are bounded, including a noisy origin and repeated sink failures', () => {
  const f = records();
  for (let n = 0; n < 1000; n++)
    f.logger.error('artwork-error', {code: 'artwork_pending'});
  assert.equal(f.lines.length, HOME_DIAGNOSTIC_ORIGIN_LIMIT);
  f.logger.action('home-card', 'primary-play');
  assert.equal(
    f.read().at(-1).origin,
    'home-card',
    'artwork cannot consume the Home action origin budget',
  );
  for (let n = 0; n < 1000; n++) {
    f.logger.action('home-card', 'primary-play');
    f.logger.action('home-assembly', 'primary-play');
    f.logger.error('assembly-error', {});
    f.logger.error('home-registration', {});
  }
  assert.equal(f.lines.length, HOME_DIAGNOSTIC_LIMIT);
  let calls = 0;
  const failed = createHomeActionDiagnostics(() => {
    calls++;
    throw Error('sink');
  });
  for (let n = 0; n < 1000; n++) failed.action('home-card', 'primary-play');
  assert.equal(calls, HOME_DIAGNOSTIC_ORIGIN_LIMIT);
});

test('original native HTTP boundary preserves response, rejection and call arguments when the production log sink throws', async () => {
  const {NativeModules} = await import('./react-native-stub.mjs');
  const {nativeNetworkFetch} = await import(
    '../src-next/platform/server-connections.ts'
  );
  const runtime = NativeModules.PorticoRuntime,
    previous = runtime.networkRequest,
    warning = console.warn;
  const calls: unknown[][] = [];
  try {
    console.warn = () => {
      throw Error('throwing production sink');
    };
    runtime.networkRequest = async (...args: unknown[]) => {
      calls.push(args);
      return {
        status: 201,
        headers: {'Content-Type': 'text/plain'},
        body: 'unchanged response',
      };
    };
    const response = await nativeNetworkFetch(
      'https://private-host/v1/playback/sessions',
      {method: 'POST', body: 'unchanged request'},
    );
    assert.equal(response.status, 201);
    assert.equal(await response.text(), 'unchanged response');
    assert.equal(calls[0]![1], 'https://private-host/v1/playback/sessions');
    assert.equal(calls[0]![2], 'POST');
    assert.equal(calls[0]![4], 'unchanged request');
    const original = Object.assign(
      new TypeError('never log this private message'),
      {code: 'network_error'},
    );
    runtime.networkRequest = async () => {
      throw original;
    };
    await assert.rejects(
      nativeNetworkFetch(
        'https://private-host/v1/items/private-item/playback-options',
      ),
      error => error === original,
    );
  } finally {
    runtime.networkRequest = previous;
    console.warn = warning;
  }
});


test('shell lifetime receipts preserve original owner linkage using ordinals without reading private fields', async () => {
  const f = records();let inspected = 0;
  const owner = Object.defineProperty({}, 'accessToken', {get() {inspected++;throw Error('private-token');}});
  const installation = {privateId: 'private-installation'}, successor = {};
  f.logger.lifetime('root-begin', owner, installation);
  f.logger.lifetime('bootstrap-begin', successor, installation);
  f.logger.lifetime('root-retire', owner, installation);
  assert.equal(f.lines.length, 0, 'no sink or journal callback runs inside the observed operation');
  await Promise.resolve();
  const [root, bootstrap, retired] = f.read();
  assert.equal(root.ownerRef, retired.ownerRef);assert.notEqual(root.ownerRef, bootstrap.ownerRef);
  assert.equal(root.parentRef, bootstrap.parentRef);assert.equal(inspected, 0);
  assert.deepEqual([root.lifetimeOrder, bootstrap.lifetimeOrder, retired.lifetimeOrder], [1, 2, 3]);
  assert.equal(root.origin, 'shell-lifetime');assert.equal(root.operation, 'root-begin');
  assert.equal(JSON.stringify(f.read()).includes('private-'), false);
  for (const frame of root.frames) assert.ok(frame.length === 2 && frame.every(Number.isSafeInteger));
});

test('shell lifetime events reject arbitrary values, remain bounded and cannot alter original recovery through a sink', async () => {
  const f = records(), owner = {};
  f.logger.lifetime('private-cause' as never, owner);f.logger.lifetime('root-begin', null as never);
  f.logger.lifetime('root-begin', owner, 'private-parent' as never);assert.equal(f.lines.length, 0);
  for (let i = 0; i < HOME_DIAGNOSTIC_ORIGIN_LIMIT + 10; i++) f.logger.lifetime('root-retire', owner);
  await Promise.resolve();assert.equal(f.lines.length, HOME_DIAGNOSTIC_ORIGIN_LIMIT);
  const broken = createHomeActionDiagnostics(() => {throw Error('broken-sink');});
  assert.doesNotThrow(() => broken.lifetime('boundary-retry', owner));await Promise.resolve();
});


test('an accepted first lifetime receipt reserves shared capacity against intervening synchronous origins', async () => {
  const f = records(), origins = ['assembly-error', 'restore-error', 'route-memory', 'home-registration', 'artwork-error'] as const;
  for (let i = 0; i < HOME_DIAGNOSTIC_LIMIT - 1; i++) f.logger.error(origins[i % origins.length], {});
  assert.equal(f.lines.length, HOME_DIAGNOSTIC_LIMIT - 1);
  f.logger.lifetime('bootstrap-current-invalid', {});
  // The one accepted pending slot plus written receipts fills the EXISTING global budget.
  assert.equal(f.lines.length + 1, HOME_DIAGNOSTIC_LIMIT);
  f.logger.error('app-render', {});f.logger.error('restore-error', {});
  assert.equal(f.lines.length, HOME_DIAGNOSTIC_LIMIT - 1, 'synchronous writes cannot displace a reserved first-cause receipt');
  await Promise.resolve();
  assert.equal(f.lines.length, HOME_DIAGNOSTIC_LIMIT);
  assert.equal(f.read().at(-1).operation, 'bootstrap-current-invalid');
  assert.equal(f.read().at(-1).origin, 'shell-lifetime');
  assert.equal(f.read().filter(record => record.operation === 'bootstrap-current-invalid').length, 1);
  f.logger.error('app-render', {});assert.equal(f.lines.length, HOME_DIAGNOSTIC_LIMIT);
});
test('a swallowed cancellation leaves a line naming the top component, even after the session budget is spent', () => {
  const f = records();
  for (let i = 0; i < HOME_DIAGNOSTIC_LIMIT; i++) f.logger.error('restore-error', {});
  const error = Object.assign(new Error('private-message'), {name: 'AbortError', stack: 'AbortError: private-message\n    at load (index.android.bundle:1:4242)'});
  f.logger.cancellationRemount(error, '\n    at TVSearchScreen (index.android.bundle:1:777)\n    at NativeShell (index.android.bundle:1:99)');
  const line = f.read().at(-1);
  assert.deepEqual({origin: line.origin, operation: line.operation, class: line.class, component: line.component, componentFrames: line.componentFrames, frames: line.frames},
    {origin: 'boundary-cancelled', operation: 'quiet-remount', class: 'AbortError', component: 'TVSearchScreen', componentFrames: [[1, 777]], frames: [[1, 4242]]});
  f.logger.cancellationRemount(error, '\n    at <anonymous> (index.android.bundle:1:5)');
  assert.equal(f.read().at(-1).component, undefined, 'only an identifier is kept as a name');
  assert.equal(f.lines.join('').includes('private'), false);
  for (let i = 0; i < HOME_DIAGNOSTIC_ORIGIN_LIMIT; i++) f.logger.cancellationRemount(error, '');
  assert.equal(f.read().filter(record => record.origin === 'boundary-cancelled').length, HOME_DIAGNOSTIC_ORIGIN_LIMIT, 'its own cap still holds');
});
