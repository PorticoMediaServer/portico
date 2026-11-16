import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsSupportSource} from '../src/screens/settings/server/troubleshooting/support-source.ts';
import {DEFAULT_SUPPORT_COMPONENTS} from '../src/screens/settings/server/troubleshooting/support-options.ts';

// Troubleshooting › Support on the rebuilt server (W2 F2, 16 Nov walk: the page failed on the old
// server's console routes). Definition 12 rules 74 to 76.
const capabilities = {items: [
  {area: 'ffmpeg', name: 'ffmpeg', state: 'works', detail: 'FFmpeg 7.1', path: '/usr/bin/ffmpeg'},
  {area: 'graphics', name: 'graphics', state: 'does_not_work', reason: 'no_graphics_device', detail: 'No graphics device works for conversions.'},
]};
const warned = {path: '/config', owner: 'portico', account: 'portico', readableByOthers: true, fixable: true, checked: true};

function fixture(options: {refuse?: number} = {}) {
  const calls: {path: string; method: string}[] = [], exports: unknown[] = [], saved: {blob: Blob; name: string}[] = [], errors: unknown[] = [];
  let permissions = warned;
  const api = {
    request: async <T>(path: string, method = 'GET') => {
      calls.push({path, method});
      if (options.refuse) throw Object.assign(new Error('refused'), {status: options.refuse});
      if (path === '/v1/admin/capabilities') return capabilities as T;
      if (path === '/v1/admin/data-folder/permissions/fix') {permissions = {...warned, readableByOthers: false};return permissions as T;}
      if (path === '/v1/admin/data-folder/permissions') return permissions as T;
      throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
    },
    downloadSupportExport: async (body: Readonly<Record<string, unknown>>) => {exports.push(body);return new Blob(['PK'], {type: 'application/zip'});},
  };
  const source = new SettingsSupportSource(api, () => true, error => errors.push(error), (blob, name) => saved.push({blob, name}));
  return {source, calls, exports, saved, errors};
}

test('Support reads what the server can do and the data folder permissions from the rebuilt routes', async () => {
  const f = fixture();
  try {
    await f.source.load();
    const state = f.source.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.deepEqual(state.facts?.capabilities.map(c => [c.name, c.state]), [['ffmpeg', 'works'], ['graphics', 'does_not_work']]);
    assert.equal(state.facts?.permissions.readableByOthers, true);
    assert.deepEqual(f.calls.map(call => call.path).sort(), ['/v1/admin/capabilities', '/v1/admin/data-folder/permissions']);
  } finally {f.source.dispose();}
});

test('Prepare export makes the ZIP with the chosen window, parts and paths, and downloads it once', async () => {
  const f = fixture();
  try {
    await f.source.load();
    await f.source.prepare({windowMinutes: 1440, components: DEFAULT_SUPPORT_COMPONENTS, includePaths: true});
    assert.deepEqual(f.exports, [{windowMinutes: 1440, components: [...DEFAULT_SUPPORT_COMPONENTS], includePaths: true}]);
    assert.equal(f.saved.length, 1);
    assert.match(f.saved[0]!.name, /^portico-support-\d{4}-\d{2}-\d{2}\.zip$/);
    assert.equal(f.source.getSnapshot().downloaded, f.saved[0]!.name);
    assert.equal(f.source.getSnapshot().prepareError, false);
  } finally {f.source.dispose();}
});

test('Fix permissions clears the warning once the server has fixed it', async () => {
  const f = fixture();
  try {
    await f.source.load();
    await f.source.fixPermissions();
    assert.ok(f.calls.some(call => call.path === '/v1/admin/data-folder/permissions/fix' && call.method === 'POST'));
    assert.equal(f.source.getSnapshot().facts?.permissions.readableByOthers, false);
    assert.equal(f.source.getSnapshot().fixed, true);
  } finally {f.source.dispose();}
});

test('a refused read is the page\'s (signed out or no longer the owner); another failure stays in the part with Try again', async () => {
  const refused = fixture({refuse: 403});
  await refused.source.load();
  assert.equal(refused.errors.length, 1);
  const failed = fixture({refuse: 500});
  try {
    await failed.source.load();
    assert.equal(failed.errors.length, 0);
    assert.equal(failed.source.getSnapshot().phase, 'error');
  } finally {failed.source.dispose();}
});
