import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp, mkdir, readFile, writeFile, rm, link} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {patchAndroidReader} from '../scripts/patch-android-reader.mjs';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const patch = JSON.parse(await readFile(path.join(root, 'scripts/patches/react-native-webview-16.0.0-android-reader.json'), 'utf8'));
async function fixture(t) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'reader-patch-'));
  t.after(() => rm(directory, {recursive: true, force: true}));
  const target = path.join(directory, 'node_modules/react-native-webview', patch.path);
  await mkdir(path.dirname(target), {recursive: true}); await mkdir(path.join(directory, 'Native'));
  await writeFile(path.join(directory, 'package.json'), JSON.stringify({dependencies: {'react-native-webview': '16.0.0'}}));
  await writeFile(path.join(directory, 'package-lock.json'), JSON.stringify({packages: {'node_modules/react-native-webview': {version: '16.0.0'}}}));
  await writeFile(path.join(directory, 'node_modules/react-native-webview/package.json'), JSON.stringify({name: 'react-native-webview', version: '16.0.0'}));
  let source = await readFile(path.join(root, 'node_modules/react-native-webview', patch.path), 'utf8');
  if (createHash('sha256').update(source).digest('hex') === patch.afterSHA256) source = source.replace(patch.after, patch.before);
  assert.equal(createHash('sha256').update(source).digest('hex'), patch.beforeSHA256);
  await writeFile(target, source);
  await writeFile(path.join(directory, 'Native/PorticoReaderAndroidAssets.java'), await readFile(path.join(root, 'Native/PorticoReaderAndroidAssets.java')));
  return {directory, target, source};
}
test('exact pinned Android hook is repeatable and never mutates another dependency hardlink', async t => {
  const f = await fixture(t), peer = path.join(f.directory, 'immutable-peer.java'); await link(f.target, peer);
  assert.equal(patchAndroidReader(f.directory), true); assert.equal(patchAndroidReader(f.directory), false);
  const changed = await readFile(f.target, 'utf8');
  assert.equal(createHash('sha256').update(changed).digest('hex'), patch.afterSHA256);
  assert.equal(await readFile(peer, 'utf8'), f.source);
  assert.equal(await readFile(path.join(path.dirname(f.target), 'PorticoReaderAndroidAssets.java'), 'utf8'), await readFile(path.join(root, 'Native/PorticoReaderAndroidAssets.java'), 'utf8'));
});
test('changed pin, upstream bytes or conflicting helper fail closed', async t => {
  const f = await fixture(t);
  await writeFile(f.target, f.source + '\n'); assert.throws(() => patchAndroidReader(f.directory), /reader_webview_source_changed/);
  await writeFile(f.target, f.source); await writeFile(path.join(path.dirname(f.target), 'PorticoReaderAndroidAssets.java'), 'unrelated');
  assert.throws(() => patchAndroidReader(f.directory), /reader_asset_helper_changed/);
  await writeFile(path.join(f.directory, 'package.json'), JSON.stringify({dependencies: {'react-native-webview': '17.0.0'}}));
  assert.throws(() => patchAndroidReader(f.directory), /reader_webview_pin_changed/);
});
