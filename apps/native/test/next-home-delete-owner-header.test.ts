import assert from 'node:assert/strict';
import test from 'node:test';
import type {ServerPin} from '../../../packages/client-core/src/route-identity.ts';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

test('production Home deletion composes real phone mark/badge through genuine controller states and mutations', async t => {
  const title = 'Native boundary test movie';
  const held = {
    preview: () => {},
    mutation: () => {},
    previewWaiting: false,
    mutationWaiting: false,
  };
  let previewError = false,
    blockLibrary = false;
  const app = await standardHomeApp({
    accountRole: 'owner',
    deleteCapability: true,
    nativeResponse: async (
      request: {path: string; body?: Record<string, unknown>},
      fixture: {pin: ServerPin},
    ) => {
      if (request.path === '/v1/admin/media/delete/preview') {
        assert.deepEqual(request.body, {itemIds: ['movie']});
        if (previewError)
          return {status: 503, body: {error: {code: 'unavailable'}}};
        await new Promise<void>(resolve => {
          held.preview = resolve;
          held.previewWaiting = true;
        });
        return {
          status: 200,
          body: {
            protocolVersion: '1.0',
            serverId: fixture.pin.serverId,
            result: {
              revision: 7,
              confirmation: title,
              confirmationKind: 'title',
              totalBytes: 1234,
              totalFiles: 1,
              totalBytesText: '1.2 kB',
              blocked: [],
              targets: [
                {
                  itemId: 'movie',
                  title,
                  kind: 'movie',
                  libraryId: 'movies',
                  libraryName: 'Movies',
                  allowMediaDeletion: !blockLibrary,
                  titles: 1,
                  bytes: 1234,
                  files: [
                    {
                      assetId: 'asset',
                      path: '/synthetic/movie.mp4',
                      bytes: 1234,
                      present: true,
                      shared: false,
                    },
                  ],
                  dependents: [
                    {
                      kind: 'saved',
                      count: 2,
                      description: 'Synthetic native boundary saved references',
                    },
                  ],
                },
              ],
            },
          },
        };
      }
      if (request.path === '/v1/admin/media/delete') {
        await new Promise<void>(resolve => {
          held.mutation = resolve;
          held.mutationWaiting = true;
        });
        return {
          status: 200,
          body: {
            protocolVersion: '1.0',
            serverId: fixture.pin.serverId,
            result: {
              revision: 8,
              removed: 1,
              failed: 0,
              bytesDeleted: 0,
              filesRetained: true,
              receipts: [
                {
                  itemId: 'movie',
                  title,
                  removed: true,
                  filesDeleted: 0,
                  filesKept: 1,
                  bytes: 1234,
                  code: '',
                },
              ],
            },
          },
        };
      }
      return undefined;
    },
  });
  const {ServerMark, OwnerTag} = await import(
    '../src-next/kit/shell/ServerIdentity.tsx'
  );
  const {StyleSheet} = await import('react-native');
  const texts = () =>
    app
      .hosts()
      .filter(host => host.name === 'Text' && typeof host.children === 'string')
      .map(host => host.children);
  const mutationRequests = () =>
    app.requests.filter(request => request.path === '/v1/admin/media/delete');
  const button = () =>
    app
      .hosts()
      .find(
        host => host.name === 'Pressable' && app.nativeLabel(host) === 'Delete',
      );
  // The authored phone footer hides its press handler exactly while the gate
  // blocks deletion, so a gated attempt is only truthful as the native host's
  // real disabled state; a live host is pressed through that same host.
  const pressDelete = async () => {
    const target = button();
    assert.ok(target, 'actual native Delete footer host');
    if (typeof target.props.onPress !== 'function') {
      assert.equal(
        target.props.disabled,
        true,
        'Delete may only lack a press handler while natively disabled',
      );
      return false;
    }
    target.props.onPress({nativeEvent: {}, stopPropagation() {}});
    await app.settle();
    return true;
  };
  const assertHeader = () => {
    const header = app
      .hosts()
      .find(
        host =>
          host.name === 'View' &&
          Array.isArray(host.children) &&
          host.children[0]?.type === ServerMark &&
          host.children[1]?.type === OwnerTag,
      );
    assert.ok(
      header,
      'actual native header must have separate existing server mark and policy badge',
    );
    assert.equal(header.children[0].props.variant, 'editorPhone');
    assert.equal(header.children[1].props.variant, 'badge');
    assert.equal(header.children[1].props.audience, 'owner');
    assert.equal(header.children[1].props.identity, undefined);
    assert.deepEqual(StyleSheet.flatten(header.props.style), {
      paddingHorizontal: 20,
      paddingTop: 6,
      paddingBottom: 0,
      flexDirection: 'row',
      alignItems: 'center',
      gap: 8,
    });
    assert.ok(
      app
        .hosts()
        .some(
          host =>
            host.name === 'View' &&
            host.props.accessible === false &&
            host.props.pointerEvents === 'none' &&
            StyleSheet.flatten(host.props.style).width === 24 &&
            StyleSheet.flatten(host.props.style).height === 24,
        ),
    );
    assert.ok(texts().includes('Owner only'));
  };
  const open = async () => {
    held.previewWaiting = false;
    await app.more();
    await app.tapLabel('Delete…');
  };
  try {
    await open();
    await t.test(
      'phone native header uses current real ServerMark and OwnerTag with the authored separation',
      assertHeader,
    );
    await t.test(
      'loading and actual preview retain factual summary and typed confirmation gate',
      async () => {
        assert.ok(held.previewWaiting);
        assert.ok(texts().includes('Checking what would be removed'));
        assert.equal(button()?.props.accessibilityState.disabled, true);
        held.preview();
        await app.settle();
        assertHeader();
        assert.ok(texts().some(text => text.includes('1.2 kB')));
        assert.equal(button()?.props.accessibilityState.disabled, true);
        assert.equal(await pressDelete(), false);
        assert.equal(mutationRequests().length, 0);
        await app.fill('Type the title to confirm', 'incorrect');
        assert.equal(await pressDelete(), false);
        assert.equal(mutationRequests().length, 0);
        await app.fill('Type the title to confirm', title);
        assert.equal(!!button()?.props.accessibilityState.disabled, false);
      },
    );
    await t.test(
      'real remove-files/confirmation callbacks preserve actual mutation body, pending gate and receipt',
      async () => {
        const toggle = app
          .hosts()
          .find(
            host =>
              host.name === 'Pressable' &&
              host.props.accessibilityRole === 'switch',
          );
        assert.ok(toggle);
        toggle.props.onPress();
        await app.settle();
        assert.equal(await pressDelete(), true);
        assert.ok(held.mutationWaiting);
        assertHeader();
        assert.equal(mutationRequests().length, 1);
        const body = mutationRequests()[0].body;
        assert.deepEqual(body.itemIds, ['movie']);
        assert.equal(body.expectedRevision, 7);
        assert.equal(body.confirmation, title);
        assert.equal(body.deleteFiles, false);
        assert.match(body.operationId, /^[a-f0-9-]{36}$/);
        assert.equal(button()?.props.accessibilityState.disabled, true);
        assert.equal(
          app
            .hosts()
            .find(
              host =>
                host.name === 'TextInput' &&
                host.props.accessibilityLabel === 'Type the title to confirm',
            )?.props.editable,
          false,
        );
        assert.equal(await pressDelete(), false);
        assert.equal(mutationRequests().length, 1);
        held.mutation();
        await app.settle();
        assert.ok(texts().includes('Done'));
        assertHeader();
        await app.tapLabel('Close');
      },
    );
    await t.test(
      'real preview error and library-disabled states preserve denial, header and dismissal',
      async () => {
        previewError = true;
        await open();
        assertHeader();
        assert.equal(button()?.props.accessibilityState.disabled, true);
        assert.equal(await pressDelete(), false);
        assert.equal(mutationRequests().length, 1);
        assert.equal(
          app
            .hosts()
            .some(
              host =>
                host.name === 'TextInput' &&
                host.props.accessibilityLabel === 'Type the title to confirm',
            ),
          false,
        );
        await app.tapLabel('Cancel');
        previewError = false;
        blockLibrary = true;
        await open();
        held.preview();
        await app.settle();
        assertHeader();
        assert.equal(button()?.props.accessibilityState.disabled, true);
        assert.equal(await pressDelete(), false);
        assert.equal(mutationRequests().length, 1);
        await app.tapLabel('Close');
        blockLibrary = false;
      },
    );
    await t.test(
      'missing genuine capability removes delete and a retired Home authority refuses retained native callback',
      async () => {
        app.setDeleteCapability(false);
        await app.refresh();
        await app.more();
        assert.equal(
          app
            .hosts()
            .some(
              host =>
                host.name === 'Pressable' &&
                host.props.accessibilityLabel === 'Delete…',
            ),
          false,
        );
        await app.back();
        app.setDeleteCapability(true);
        await app.refresh();
        await open();
        held.preview();
        await app.settle();
        await app.fill('Type the title to confirm', title);
        const retained = button()?.props.onPress;
        assert.ok(retained);
        await app.retireHome();
        assert.equal(
          app.hosts().some(host => host.props.testID === 'native-home'),
          false,
        );
        retained();
        await app.settle();
        assert.equal(mutationRequests().length, 1);
      },
    );
  } finally {
    try {
      held.preview();
    } finally {
      try {
        held.mutation();
      } finally {
        try {
          await app.unmount();
        } finally {
          app.disposeLoader();
        }
      }
    }
  }
});
