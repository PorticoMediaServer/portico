import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

test('genuine member Home authority offers no owner deletion or owner header, including selected-item More', async () => {
  const app = await standardHomeApp({
    accountRole: 'member',
    deleteCapability: true,
  });
  const deletion = () =>
    app
      .hosts()
      .filter(
        host =>
          host.name === 'Pressable' &&
          ['Delete', 'Delete…'].includes(host.props.accessibilityLabel),
      );
  try {
    await app.more();
    assert.equal(deletion().length, 0);
    await app.back();
    await app.select();
    await app.tapLabel('More');
    assert.equal(deletion().length, 0);
    assert.equal(
      app
        .hosts()
        .some(host => host.name === 'Text' && host.children === 'Owner only'),
      false,
    );
    assert.equal(
      app.requests.some(request =>
        request.path.startsWith('/v1/admin/media/delete'),
      ),
      false,
    );
    await app.retireHome();
    assert.equal(
      app.hosts().some(host => host.props.testID === 'native-home'),
      false,
    );
    assert.equal(
      app.requests.some(request =>
        request.path.startsWith('/v1/admin/media/delete'),
      ),
      false,
    );
  } finally {
    try {
      await app.unmount();
    } finally {
      app.disposeLoader();
    }
  }
});
