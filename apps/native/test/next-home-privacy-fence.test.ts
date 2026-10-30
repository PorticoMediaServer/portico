import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

const homeHost = (app: any) => app.hosts().find((host: any) => host.props.testID === 'native-home')?.host;
const playerHost = (app: any) => app.hosts().find((host: any) => host.name === 'PorticoPlayer')?.host;

// H-11: a library added or removed changes only the viewer fence. The shell, Home and the
// player host stay mounted and Home follows the new fence; a privacy change remounts.
test('H-11: a library added then removed keeps the shell and player host; Home follows the new fence', async () => {
  const app = await standardHomeApp({privacyFence: 'privacy-fence'});
  try {
    const home = homeHost(app), surface = playerHost(app);
    assert.ok(home); assert.ok(surface);
    for (const viewerFence of ['home-fence-with-music', 'home-fence']) {
      await app.changeHome({viewerFence});
      assert.equal(homeHost(app), home, `Home stays mounted (${viewerFence})`);
      assert.equal(playerHost(app), surface, `the player host is kept (${viewerFence})`);
      assert.ok(app.card(), `Home still shows the new fence's rows (${viewerFence})`);
    }
    assert.equal(app.errors.length, 0);
  } finally {
    try { await app.unmount(); } finally { app.disposeLoader(); }
  }
});

for (const server of ['privacy', 'older'] as const) test(`H-11: ${server === 'privacy' ? 'a privacy change' : 'an older server\'s viewer-fence change'} still retires the shell`, async () => {
  const app = await standardHomeApp(server === 'privacy' ? {privacyFence: 'privacy-fence'} : {});
  try {
    const home = homeHost(app);
    assert.ok(home);
    await app.changeHome(server === 'privacy' ? {privacyFence: 'another-viewer'} : {viewerFence: 'home-fence-with-music'});
    assert.notEqual(homeHost(app), home, 'the old Home is gone');
  } finally {
    try { await app.unmount(); } finally { app.disposeLoader(); }
  }
});
