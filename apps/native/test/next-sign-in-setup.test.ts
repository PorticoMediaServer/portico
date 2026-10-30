import assert from 'node:assert/strict';
import test from 'node:test';
import type {
  LocalSession,
  Viewer,
} from '../../../packages/client-core/src/index.ts';
import type { ServerPin } from '../../../packages/client-core/src/route-identity.ts';
import { standardHomeApp } from './helpers/standard-home-app-harness.mjs';

type NativeRequest = {
  path: string;
  method: string;
  body?: Record<string, unknown>;
  headers: Record<string, string>;
};
type Fixture = {
  pin: ServerPin;
  viewer: Viewer;
  session: LocalSession;
  installationId: string;
};

test('separate setup uses genuine React form, core auth, verified route and native persistence boundaries', async t => {
  let mode:
    | 'ordinary'
    | 'single-profile'
    | 'device-pending'
    | 'challenge'
    | 'password-change'
    | 'wrong-identity'
    | 'wrong-viewer'
    | 'hold-verification'
    | 'hold-account-logout' = 'ordinary';
  let issued = 0;
  const held = { release: () => {}, waiting: false };
  const app = await standardHomeApp({
    setup: true,
    setupResponse: async (request: NativeRequest, fixture: Fixture) => {
      const session = (account = false) => ({
        ...fixture.session,
        sessionFamilyId: account ? 'setup_account' : 'setup_viewer',
        accessToken:
          (account ? 'synthetic_account_' : 'synthetic_viewer_') + ++issued,
        viewer: { ...fixture.viewer, role: account ? 'account' : 'member' },
      });
      if (request.path === '/v1/direct/sign-in') {
        if (mode === 'device-pending')
          return {
            status: 200,
            body: {
              deviceApprovalPending: true,
              deviceId: 'pending-device',
              installationId: fixture.installationId,
            },
          };
        if (request.body?.password !== 'synthetic_password')
          return {
            status: 401,
            body: { error: { code: 'invalid_credentials' } },
          };
        if (mode === 'challenge')
          return {
            status: 200,
            body: {
              challenge: {
                token: 'synthetic_challenge',
                expiresAt: new Date(Date.now() + 60000).toISOString(),
              },
            },
          };
        return {
          status: 200,
          body: {
            authority: 'local',
            serverId: fixture.pin.serverId,
            canManage: false,
            capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false},
            account: {
              id: 'account',
              username: 'synthetic_user',
              primaryProfileId: 'profile',
              role: 'member',
              revision: 1,
              disabled: false,
              allowedLibraries: [],
            },
            profiles: [
              {
                id: 'profile',
                name: 'Actual returned profile',
                primary: true,
                position: 0,
                art: 'mint',
                revision: 1,
                pinRevision: 1,
                pinRequired: mode !== 'single-profile',
              },
            ],
            session: session(mode !== 'single-profile'),
            passwordChangeRequired: mode === 'password-change',
          },
        };
      }
      if (request.path === '/v1/direct/profiles/profile/select') {
        if (request.body?.pin !== '1234')
          return {
            status: 401,
            body: { error: { code: 'profile_pin_invalid' } },
          };
        assert.equal(request.body.installationId, fixture.installationId);
        const selected = session();
        return {
          status: 200,
          body: {
            session:
              mode === 'wrong-identity'
                ? {
                    ...selected,
                    serverIdentity: {
                      ...selected.serverIdentity,
                      fingerprint: 'A'.repeat(43),
                    },
                  }
                : selected,
          },
        };
      }
      if (request.path === '/v1/me') {
        if (mode === 'hold-verification')
          await new Promise<void>(resolve => {
            held.release = resolve;
            held.waiting = true;
          });
        return {
          status: 200,
          body: {
            viewer:
              mode === 'wrong-viewer'
                ? { ...fixture.viewer, profileId: 'another-profile' }
                : fixture.viewer,
          },
        };
      }
      if (
        request.method === 'DELETE' &&
        request.path === '/v1/sessions/current'
      ) {
        if (
          mode === 'hold-account-logout' &&
          request.headers.authorization.startsWith('Bearer synthetic_account_')
        )
          await new Promise<void>(resolve => {
            held.release = resolve;
            held.waiting = true;
          });
        return { status: 204 };
      }
      return undefined;
    },
  });
  const input = (label: string) =>
    app
      .hosts()
      .find(
        host =>
          host.name === 'TextInput' && host.props.accessibilityLabel === label,
      );
  const saved = () => app.syntheticStorage();
  try {
    await t.test(
      'empty fields, explicit fingerprint confirmation and retryable credential/PIN failures',
      async () => {
        assert.equal(input('Server address')?.props.value, '');
        assert.equal(saved().selected, null);
        await app.fill('Server address', 'https://standard-home.test');
        await app.tapLabel('Verify server');
        assert.ok(
          app
            .hosts()
            .some(
              host =>
                host.props.accessibilityLabel === 'Server fingerprint' &&
                typeof host.children === 'string',
            ),
        );
        assert.equal(
          app.receipts.some(receipt => receipt.path === '/v1/direct/sign-in'),
          false,
        );
        await app.tapLabel('Trust this server');
        assert.equal(input('Username')?.props.value, '');
        assert.equal(input('Password')?.props.value, '');
        assert.equal(input('Password')?.props.secureTextEntry, true);
        await app.fill('Username', 'synthetic_user');
        await app.fill('Password', 'incorrect');
        await app.tapLabel('Sign in');
        assert.equal(input('Password')?.props.value, '');
        assert.equal(saved().selected, null);
        await app.fill('Password', 'synthetic_password');
        await app.tapLabel('Sign in');
        assert.equal(
          saved().selected,
          null,
          'account proof is never published as selected viewer',
        );
        await app.tapLabel('Actual returned profile');
        assert.equal(input('Profile PIN')?.props.secureTextEntry, true);
        await app.fill('Profile PIN', '0000');
        await app.tapLabel('Continue with profile');
        assert.equal(input('Profile PIN')?.props.value, '');
        assert.equal(saved().selected, null);
      },
    );
    await t.test(
      'profile selection commits verified viewer, keeps refresh only in connection store and ends account family',
      async () => {
        await app.fill('Profile PIN', '1234');
        await app.tapLabel('Continue with profile');
        assert.ok(
          app
            .hosts()
            .some(
              host =>
                host.props.accessibilityLabel === 'Sign-in setup complete',
            ),
        );
        assert.equal(saved().selected.session.viewer.role, 'member');
        assert.equal(saved().selected.session.sessionFamilyId, 'setup_viewer');
        assert.equal(saved().selected.session.refreshToken, undefined);
        assert.equal(
          saved().connection.session.sessionFamilyId,
          'setup_viewer',
        );
        assert.equal(
          saved().connection.session.refreshToken,
          'fixture_refresh_only',
        );
        assert.ok(
          app.receipts.some(
            receipt => receipt.path === '/v1/me' && receipt.status === 200,
          ),
        );
        const logouts = app.requests.filter(
          request => request.path === '/v1/sessions/current',
        );
        assert.equal(logouts.length, 1);
        assert.match(
          logouts[0].headers.authorization,
          /^Bearer synthetic_account_/,
        );
        assert.equal(app.commands.length, 0);
        assert.equal(app.subscriptions().listeners, 0);
        assert.equal(app.subscriptions().appState, 0);
        await app.unmount();
        assert.equal(saved().selected.session.sessionFamilyId, 'setup_viewer');
      },
    );
    const { NativeSignInSetup } = await import('../setup/native-session.ts');
    await t.test(
      'an already saved sign-in is refused without replacing its real product records',
      async () => {
        const before = saved(),
          setup = new NativeSignInSetup();
        try {
          await assert.rejects(setup.prepare());
          assert.deepEqual(saved(), before);
        } finally {
          setup.dispose();
          await app.settle();
        }
      },
    );
    await t.test(
      'single unprotected actual profile uses the direct viewer session and canonical persistence',
      async () => {
        app.resetSyntheticStorage();
        mode = 'single-profile';
        const setup = new NativeSignInSetup();
        try {
          await setup.prepare();
          await setup.inspect('https://standard-home.test');
          await setup.confirm();
          assert.equal(
            await setup.signIn('synthetic_user', 'synthetic_password'),
            undefined,
          );
          assert.equal(saved().selected.session.viewer.role, 'member');
          assert.equal(
            saved().connection.session.sessionFamilyId,
            'setup_viewer',
          );
        } finally {
          setup.dispose();
          await app.settle();
        }
      },
    );
    for (const failure of [
      'challenge',
      'device-pending',
      'password-change',
      'wrong-identity',
      'wrong-viewer',
    ] as const) {
      await t.test(
        `${failure} fails closed without durable viewer or connection publication`,
        async () => {
          app.resetSyntheticStorage();
          mode = failure;
          const setup = new NativeSignInSetup();
          try {
            await setup.prepare();
            await setup.inspect('https://standard-home.test');
            await setup.confirm();
            if (
              failure === 'challenge' ||
              failure === 'device-pending' ||
              failure === 'password-change'
            )
              await assert.rejects(
                setup.signIn('synthetic_user', 'synthetic_password'),
              );
            else {
              await setup.signIn('synthetic_user', 'synthetic_password');
              await assert.rejects(setup.choose('profile', '1234'));
            }
            await app.settle();
            assert.equal(saved().selected, null);
            assert.equal(saved().connection, null);
          } finally {
            setup.dispose();
            await app.settle();
          }
        },
      );
    }
    await t.test(
      'cancellation during real viewer verification rolls back, retires unused families once and ignores late reply',
      async () => {
        app.resetSyntheticStorage();
        mode = 'hold-verification';
        held.waiting = false;
        const setup = new NativeSignInSetup(),
          before = app.requests.length;
        await setup.prepare();
        await setup.inspect('https://standard-home.test');
        await setup.confirm();
        await setup.signIn('synthetic_user', 'synthetic_password');
        const selection = setup.choose('profile', '1234'),
          rejected = assert.rejects(selection);
        await app.settle();
        assert.ok(held.waiting, 'real /me verification is pending');
        setup.dispose();
        held.release();
        await rejected;
        await app.settle();
        assert.equal(saved().selected, null);
        assert.equal(saved().connection, null);
        const logouts = app.requests
          .slice(before)
          .filter(request => request.path === '/v1/sessions/current');
        assert.equal(
          logouts.length,
          2,
          'each unused issued family is retired once',
        );
        assert.equal(app.subscriptions().pending, 0);
      },
    );
    await t.test(
      'stopping after committed viewer publication preserves it while temporary account retirement finishes',
      async () => {
        app.resetSyntheticStorage();
        mode = 'hold-account-logout';
        held.waiting = false;
        const setup = new NativeSignInSetup(),
          before = app.requests.length;
        await setup.prepare();
        await setup.inspect('https://standard-home.test');
        await setup.confirm();
        await setup.signIn('synthetic_user', 'synthetic_password');
        const selection = setup.choose('profile', '1234'),
          rejected = assert.rejects(selection);
        await app.settle();
        assert.ok(held.waiting);
        assert.equal(saved().selected.session.sessionFamilyId, 'setup_viewer');
        setup.dispose();
        held.release();
        await rejected;
        await app.settle();
        assert.equal(saved().selected.session.sessionFamilyId, 'setup_viewer');
        assert.equal(
          saved().connection.session.sessionFamilyId,
          'setup_viewer',
        );
        assert.equal(
          app.requests
            .slice(before)
            .filter(request => request.path === '/v1/sessions/current').length,
          1,
        );
      },
    );
  } finally {
    try {
      held.release();
    } finally {
      try {
        await app.unmount();
      } finally {
        app.disposeLoader();
      }
    }
  }
});
