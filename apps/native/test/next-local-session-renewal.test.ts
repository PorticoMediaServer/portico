import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

test('H-11: a genuine Local refresh advances its inactivity horizon in place, tearing nothing down', async t => {
  t.mock.timers.enable({apis: ['Date'], now: Date.now()});
  let refreshes = 0;
  const app = await standardHomeApp({nativeResponse: async (request: any, fixture: any) => {
    if (request.path !== '/v1/auth/refresh') return undefined;
    refreshes++;
    assert.equal(request.body.installationId, fixture.installationId);
    return {status: 200, body: {...fixture.session,
      accessToken: 'synthetic_refreshed_access', refreshToken: 'synthetic_refreshed_credential', tokenGeneration: '2',
      expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(), authorizationHorizon: new Date(Date.now() + 90 * 86_400_000).toISOString()}};
  }});
  try {
    const home = app.hosts().find(host => host.props.testID === 'native-home')?.host;
    const surface = app.hosts().find(host => host.name === 'PorticoPlayer')?.host;
    assert.ok(home);
    assert.ok(surface);
    t.mock.timers.tick(59 * 60_000);
    await app.refresh();
    assert.equal(refreshes, 1, 'one real refresh request is shared by the foreground readers');
    assert.ok(app.hosts().some(host => host.props.testID === 'native-home'), 'the renewed Local family stays on Home');
    assert.equal(app.hosts().find(host => host.name === 'PorticoPlayer')?.host, surface, 'the player host is kept: a token renewal is not a tear-down');
    assert.equal(app.hosts().find(host => host.props.testID === 'native-home')?.host, home, 'the Home screen stays mounted: nothing remounted');
    const count = app.requests.length;
    await app.refresh();
    const later = app.requests.slice(count).filter((request: any) => request.path.startsWith('/v1/'));
    assert.ok(later.length > 0 && later.every((request: any) => Object.entries(request.headers).some(([name, value]) => name.toLowerCase() === 'authorization' && String(value).includes('synthetic_refreshed_access'))),
      'later reads use the renewed session');
    assert.equal(app.errors.length, 0);
  } finally {
    try {
      await app.unmount();
    } finally {
      app.disposeLoader();
    }
  }
});

for (const changed of ['installation', 'backwards-horizon'] as const) test(`Local renewal still retires private Home for ${changed}`, async t => {
  t.mock.timers.enable({apis: ['Date'], now: Date.now()});
  const app = await standardHomeApp({nativeResponse: async (request: any, fixture: any) => {
    if (request.path !== '/v1/auth/refresh') return undefined;
    return {status: 200, body: {...fixture.session,
      accessToken: 'synthetic_refreshed_access', refreshToken: 'synthetic_refreshed_credential', tokenGeneration: '2',
      ...(changed === 'installation' ? {installationId: fixture.installationId + '_changed'} : {}),
      expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
      authorizationHorizon: new Date(Date.now() + (changed === 'backwards-horizon' ? 60 * 60_000 : 90 * 86_400_000)).toISOString()}};
  }});
  try {
    t.mock.timers.tick(59 * 60_000);
    await app.refresh();
    assert.equal(app.hosts().some(host => host.props.testID === 'native-home'), false, 'the permitted Local advance does not admit a changed binding or shorter horizon');
    assert.equal(app.hosts().some(host => host.name === 'PorticoPlayer'), false, 'the retired private runtime releases its persistent host');
  } finally {
    try {
      await app.unmount();
    } finally {
      app.disposeLoader();
    }
  }
});
