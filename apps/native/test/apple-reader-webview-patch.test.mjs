import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, readFileSync, writeFileSync, cpSync, rmSync, statSync, readdirSync} from 'node:fs';
import {createRequire} from 'node:module';
import {dirname, join} from 'node:path';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {patchAppleReaderWebView} from '../scripts/patch-apple-reader-webview.mjs';

const app = fileURLToPath(new URL('../', import.meta.url));
const require = createRequire(import.meta.url);
const source = dirname(require.resolve('react-native-webview/package.json'));
const patch = JSON.parse(readFileSync(new URL('../scripts/patches/react-native-webview-16.0.0-reader-origin.json', import.meta.url)));
const hash = data => createHash('sha256').update(data).digest('hex');
function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'portico-reader-patch-'));
  t.after(() => rmSync(root, {recursive: true, force: true}));
  for (const path of ['package.json', 'package-lock.json']) cpSync(join(app, path), join(root, path));
  const dependency = join(root, 'node_modules/react-native-webview');
  mkdirSync(join(dependency, 'apple'), {recursive: true});
  cpSync(join(source, 'package.json'), join(dependency, 'package.json'));
  for (const file of patch.files) cpSync(join(source, file.path), join(dependency, file.path));
  writeFileSync(join(dependency, 'apple/unrelated.h'), 'unchanged');
  mkdirSync(join(dependency, 'android'));writeFileSync(join(dependency, 'android/unrelated.java'), 'unchanged');
  return {root, dependency};
}
const snapshot = dir => Object.fromEntries(readdirSync(dir, {recursive: true}).sort().filter(path => statSync(join(dir, path)).isFile()).map(path => [path, hash(readFileSync(join(dir, path)))]));

test('exact WebView Apple hook preserves all unrelated bytes and is idempotent without rewrites', t => {
  const {root, dependency} = fixture(t);const before = snapshot(dependency);
  assert.ok([0, 1].includes(patchAppleReaderWebView(root)));
  const after = snapshot(dependency);
  assert.deepEqual(Object.keys(after), Object.keys(before));
  for (const [path, sha] of Object.entries(before)) assert.equal(after[path], patch.files.find(file => file.path === path)?.afterSHA256 ?? sha, path);
  const times = patch.files.map(file => statSync(join(dependency, file.path)).mtimeMs);
  assert.equal(patchAppleReaderWebView(root), 0);
  assert.deepEqual(patch.files.map(file => statSync(join(dependency, file.path)).mtimeMs), times);
});

test('modified upstream source is rejected without a write', t => {
  const {root, dependency} = fixture(t);const path = join(dependency, patch.files[0].path);
  writeFileSync(path, readFileSync(path, 'utf8') + '\n');const before = snapshot(dependency);
  assert.throws(() => patchAppleReaderWebView(root), /unexpected source bytes/);
  assert.deepEqual(snapshot(dependency), before);
});

test('unreviewed manifest, lock or installed versions are rejected before patching', t => {
  for (const target of ['manifest', 'root-lock', 'package-lock', 'installed']) {
    const {root, dependency} = fixture(t);
    const path = target === 'manifest' ? join(root, 'package.json') : target === 'installed' ? join(dependency, 'package.json') : join(root, 'package-lock.json');
    const value = JSON.parse(readFileSync(path, 'utf8'));
    if (target === 'manifest') value.dependencies['react-native-webview'] = '16.0.1';
    if (target === 'root-lock') value.packages[''].dependencies['react-native-webview'] = '16.0.1';
    if (target === 'package-lock') value.packages['node_modules/react-native-webview'].version = '16.0.1';
    if (target === 'installed') value.version = '16.0.1';
    writeFileSync(path, JSON.stringify(value));const before = snapshot(dependency);
    assert.throws(() => patchAppleReaderWebView(root), /review|reviewed/);
    assert.deepEqual(snapshot(dependency), before);
  }
});

test('iOS installs the reviewed hook before autolinking; tvOS has no WebView hook or Read native source', () => {
  const pod = readFileSync(join(app, 'ios/Podfile'), 'utf8');
  assert.ok(pod.indexOf('patch-apple-reader-webview.mjs') >= 0 && pod.indexOf('patch-apple-reader-webview.mjs') < pod.indexOf('use_native_modules!'));
  assert.ok(!readFileSync(join(app, 'tvos/Podfile'), 'utf8').includes('patch-apple-reader-webview'));
  const ios = readFileSync(join(app, 'ios/Portico.xcodeproj/project.pbxproj'), 'utf8');
  const tvos = readFileSync(join(app, 'tvos/Portico.xcodeproj/project.pbxproj'), 'utf8');
  for (const name of ['PorticoReaderAssets.m', 'PorticoReaderAssetOrigin.m']) {
    assert.ok(ios.includes(`${name} in Sources`));assert.ok(!tvos.includes(name));
  }
});
