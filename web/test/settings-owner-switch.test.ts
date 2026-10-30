import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {RouteError} from '@core/route-identity.ts';
import {SettingsOwnerSwitch} from '../src/screens/settings/owner-switch.ts';
import {SignInSettingsSource} from '../src/screens/settings/signin-source.ts';

const move = {id: 'move', from: 'local', to: 'portico', startOver: false, stage: 'preview', revision: 4, people: 2, profiles: 3, invitations: 1, waiting: 0, serverName: 'Actual server', ownAddressRequired: false, devices: 4};
function fixture() {
  let current = true, secure = true, blockedStorage = false, failPreparation = false;
  let prepareGate: Promise<void> | undefined, releasePrepare!: () => void;
  const calls: {path: string; body?: unknown}[] = [], order: string[] = [];
  let factories = 0, preparations = 0, cancellations = 0, serverCancellations = 0;
  let preview = move;
  const api = new HttpLocalApi('https://server.example', '');
  api.request = async <T>(path: string, _method?: string, body?: unknown): Promise<T> => {
    calls.push({path, body});order.push(path);
    if (path.endsWith('/preview')) {const startOver = (body as {startOver: boolean}).startOver;preview = {...move, startOver};if (startOver) {const {devices: _devices, ...rest} = preview;return rest as T;}return preview as T;}
    if (path.endsWith('/confirm')) {const {devices, ...rest} = preview;return {move: preview.startOver ? rest : preview} as T;}
    if (path.endsWith('/cancel')) return undefined as T;
    throw new Error('Unexpected move route');
  };
  const controller = new SettingsOwnerSwitch(api, {serverId: 'server', hostedOrigin: 'https://web.example', isCurrent: () => current,
    secureContextAllowed: () => secure, retireLateGrant: async () => {throw new Error('Unexpected grant');},
    createClaim: () => {
      factories++;order.push('factory');if (blockedStorage) throw new RouteError('secure_context_required', 'Secure pending unavailable');
      return {
        prepare: async (captured: HttpLocalApi, hostedOrigin: string, _purpose?: string, _role?: string, _caller?: unknown, attempt?: {lease?: {attemptId: string; origin: string; cancel: () => Promise<void>}}) => {
          preparations++;order.push('prepare');assert.equal(captured, api);assert.equal(hostedOrigin, 'https://web.example');
          const lease = {attemptId: 'attempt', origin: 'https://server.example', cancel: async () => {cancellations++;}};
          if (attempt) attempt.lease = lease;
          await prepareGate;if (failPreparation) throw new Error('preparation offline');return 'https://web.example/owner-claim#one-use-proof';
        },
        cancel: async () => {cancellations++;}, cancelOperation: async captured => {assert.equal(captured, api);serverCancellations++;},
      };
    },
  });
  return {controller, api, calls, order, counts: () => ({factories, preparations, cancellations, serverCancellations}),
    insecure: () => {secure = false;}, unavailableStorage: () => {blockedStorage = true;}, retire: () => {current = false;},
    offline: (value: boolean) => {failPreparation = value;}, holdPreparation: () => {prepareGate = new Promise(resolve => {releasePrepare = resolve;});}, releasePreparation: () => releasePrepare(),
  };
}

test('Switch and Start over refuse HTTP before factory, preview, storage or handoff', async () => {
  for (const startOver of [false, true]) {
    const f = fixture();f.insecure();
    await assert.rejects(f.controller.preview(startOver), {code: 'secure_context_required'});
    assert.deepEqual(f.calls, []);assert.equal(f.counts().factories, 0);assert.equal(f.counts().preparations, 0);f.controller.dispose();
  }
});
test('the real lazy factory prerequisites run before either move preview', async () => {
  for (const startOver of [false, true]) {
    const f = fixture();assert.deepEqual(f.calls, []);assert.equal(f.counts().factories, 0);f.unavailableStorage();
    await assert.rejects(f.controller.preview(startOver), {code: 'secure_context_required'});
    assert.deepEqual(f.calls, []);assert.equal(f.counts().factories, 1);f.controller.dispose();
  }
});
test('actual preview revision is confirmed once before claim preparation, without local credentials or URL snapshots', async () => {
  for (const startOver of [false, true]) {
    const f = fixture();
    try {
      const preview = await f.controller.preview(startOver);
      assert.equal(preview.stage, 'preview');assert.equal(preview.startOver, startOver);
      const url = await f.controller.confirmAndPrepare(startOver ? 'Actual server' : '');
      assert.deepEqual(f.order, ['factory', '/v1/admin/sign-in/move/preview', '/v1/admin/sign-in/move/confirm', 'prepare']);
      assert.deepEqual(f.calls[1].body, {moveId: 'move', expectedRevision: 4, confirmation: startOver ? 'Actual server' : '', username: '', password: ''});
      assert.equal(url, 'https://web.example/owner-claim#one-use-proof');
      assert.doesNotMatch(JSON.stringify(f.controller.getSnapshot()), /one-use-proof|password|verifier|accessToken/);
      f.controller.dispose();assert.equal(f.counts().cancellations, 0);
    } finally {f.controller.dispose();}
  }
});
test('a failed preparation retries its immutable helper attempt without confirming the move again', async () => {
  const f = fixture();
  try {
    await f.controller.preview(false);f.offline(true);
    await assert.rejects(f.controller.confirmAndPrepare(''), /preparation offline/);
    assert.equal(f.controller.getSnapshot().confirmed, true);f.offline(false);
    await f.controller.retryPreparation();
    assert.equal(f.counts().factories, 1);assert.equal(f.counts().preparations, 2);
    assert.equal(f.calls.filter(call => call.path.endsWith('/confirm')).length, 1);
  } finally {f.controller.dispose();}
});
test('retired or canceled preparation cannot deliver its URL; explicit Cancel forgets only the captured private attempt', async () => {
  for (const end of ['retire', 'cancel', 'dispose']) {
    const f = fixture();await f.controller.preview(false);f.holdPreparation();
    const pending = f.controller.confirmAndPrepare('');await new Promise(resolve => setImmediate(resolve));
    if (end === 'retire') f.retire();else if (end === 'cancel') await f.controller.cancel();else f.controller.dispose();
    f.releasePreparation();await assert.rejects(pending, {name: 'AbortError'});
    assert.ok(f.counts().cancellations > 0);assert.equal(f.counts().serverCancellations, 0);
    assert.equal(f.calls.some(call => call.path.endsWith('/cancel')), false);f.controller.dispose();
  }
});
test('preview cancellation and guarded Server cancellation are explicit commands, never disposal side effects', async () => {
  const preview = fixture();await preview.controller.preview(false);await preview.controller.cancelPreview();
  assert.deepEqual(preview.calls.at(-1), {path: '/v1/admin/sign-in/move/move/cancel', body: {expectedRevision: 4}});
  assert.equal(preview.controller.getSnapshot().preview, undefined);assert.equal(preview.counts().serverCancellations, 0);preview.controller.dispose();
  const server = fixture();await server.controller.preview(false);await server.controller.confirmAndPrepare('');await server.controller.cancelOperation();
  assert.equal(server.counts().serverCancellations, 1);assert.equal(server.counts().cancellations, 1);server.controller.dispose();
});

test('the mounted Sign-in source is inert and exposes only actual kind/owner facts', async () => {
  let calls = 0, current = true;
  const source = new SignInSettingsSource({request: async <T>() => {
    calls++;return {signIn: 'local', owner: {accountId: 'owner', username: 'actual_owner', displayName: 'Actual owner'}, move: null, webPath: '/settings?scope=server&section=signin'} as T;
  }}, {serverId: 'server', signIn: 'local', isCurrent: () => current, capabilities: () => ({manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true}), onAuthorityChanged: () => {throw new Error('Unexpected authority change');}});
  try {
    assert.equal(calls, 0);await source.load();assert.equal(source.rowState('signin.ownerLocal')?.displayValue, 'actual_owner');
    assert.equal(source.rowState('signin.owner')?.present, false);await source.load();assert.equal(calls, 1);
    current = false;assert.equal(source.rowState('signin.ownerLocal')?.present, false);
  } finally {source.dispose();}
});
