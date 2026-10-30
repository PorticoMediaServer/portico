import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {cpSync, mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {patchAppleBlur} from '../scripts/patch-apple-blur.mjs';

const app = fileURLToPath(new URL('../', import.meta.url));
const require = createRequire(import.meta.url);
const source = process.env.PORTICO_BLUR_SOURCE ?? dirname(require.resolve('@react-native-community/blur/package.json'));
const patch = JSON.parse(readFileSync(new URL('../scripts/patches/react-native-blur-4.4.1-public-api.json', import.meta.url), 'utf8'));
const hash = data => createHash('sha256').update(data).digest('hex');
const snapshot = dir => Object.fromEntries(readdirSync(dir, {recursive: true}).sort()
  .filter(path => statSync(join(dir, path)).isFile())
  .map(path => [path, hash(readFileSync(join(dir, path)))]));

function fixture(t, installed = true) {
  const root = mkdtempSync(join(tmpdir(), 'portico-apple-blur-'));
  t.after(() => rmSync(root, {recursive: true, force: true}));
  for (const path of ['package.json', 'package-lock.json']) {
    cpSync(join(app, path), join(root, path));
  }
  const dependency = join(root, 'node_modules', patch.package);
  if (installed) {
    mkdirSync(dirname(dependency), {recursive: true});
    cpSync(source, dependency, {recursive: true});
  }
  return {root, dependency};
}

test('controlled-copy patch uses public style; preserves all other package bytes and reruns without writes', t => {
  const {root, dependency} = fixture(t);
  const before = snapshot(dependency);
  const count = patch.files.filter(file => before[file.path] === file.beforeSHA256).length;
  assert.equal(patchAppleBlur(root), count);
  const after = snapshot(dependency);
  for (const [path, sha] of Object.entries(before)) {
    assert.equal(after[path], patch.files.find(file => file.path === path)?.afterSHA256 ?? sha, path);
  }
  assert.deepEqual(Object.keys(after), Object.keys(before));
  const times = patch.files.map(file => statSync(join(dependency, file.path)).mtimeMs);
  assert.equal(patchAppleBlur(root), 0);
  assert.deepEqual(patch.files.map(file => statSync(join(dependency, file.path)).mtimeMs), times);

  const apple = readdirSync(join(dependency, 'ios')).filter(path => /\.(h|m|mm)$/.test(path))
    .map(path => readFileSync(join(dependency, 'ios', path), 'utf8')).join('\n');
  assert.doesNotMatch(apple, /effectSettings|blurRadius|object_setClass|objc_(set|get)AssociatedObject|UIBlurEffect\s*\(Protected\)/);
  const view = readFileSync(join(dependency, 'ios/BlurView.mm'), 'utf8');
  assert.match(view, /self\.blurEffect = \[UIBlurEffect effectWithStyle:style\];/);
  const effectUpdate = view.slice(view.indexOf('- (void)updateBlurEffect'), view.indexOf('- (void)updateFallbackView'));
  assert.doesNotMatch(effectUpdate, /self\.blurAmount|andBlurAmount/);
  assert.match(view, /_blurAmount = blurAmount;/);
  assert.match(view, /@"dark"\]\) return UIBlurEffectStyleDark;/);
  const originalView = readFileSync(join(source, 'ios/BlurView.mm'), 'utf8');
  for (const [start, end] of [
    ['- (UIBlurEffectStyle)blurEffectStyle', '- (void)updateBlurEffect'],
    ['- (void)updateFallbackView', '#ifdef RCT_NEW_ARCH_ENABLED\nClass'],
    ['- (void)dealloc', '- (void)setBlurType'],
    ['- (instancetype)init', '#ifdef RCT_NEW_ARCH_ENABLED\n#pragma mark'],
  ]) {
    assert.equal(view.slice(view.indexOf(start), view.indexOf(end)),
      originalView.slice(originalView.indexOf(start), originalView.indexOf(end)), start);
  }
  assert.match(view, /UIAccessibilityIsReduceTransparencyEnabled\(\) == YES && self\.reducedTransparencyFallbackColor != NULL/);
  assert.match(view, /removeObserver:self/);
});

for (const target of ['package.json', 'package-lock.json', 'installed']) {
  test(`rejects unknown version in ${target} without changing any source`, t => {
    const {root, dependency} = fixture(t);
    const path = target === 'installed' ? join(dependency, 'package.json') : join(root, target);
    const data = JSON.parse(readFileSync(path, 'utf8'));
    if (target === 'package.json') {
      data.dependencies[patch.package] = '4.4.2';
    } else if (target === 'package-lock.json') {
      data.packages[`node_modules/${patch.package}`].version = '4.4.2';
    } else {
      data.version = '4.4.2';
    }
    writeFileSync(path, JSON.stringify(data));
    const before = snapshot(dependency);
    assert.throws(() => patchAppleBlur(root), /must pin|unsupported installed package/);
    assert.deepEqual(snapshot(dependency), before);
  });
}

test('unknown source in the last file aborts before any file changes', t => {
  const {root, dependency} = fixture(t);
  const path = join(dependency, patch.files.at(-1).path);
  writeFileSync(path, `${readFileSync(path, 'utf8')}\n// unexpected edit\n`);
  const before = snapshot(dependency);
  assert.throws(() => patchAppleBlur(root), /unexpected source SHA-256/);
  assert.deepEqual(snapshot(dependency), before);
});

test('missing installed dependency fails closed', t => {
  const {root} = fixture(t, false);
  assert.throws(() => patchAppleBlur(root), /Cannot find module/);
});

test('npm hook and both Apple Podfile entry paths run the same cwd-independent setup', t => {
  const {root, dependency} = fixture(t);
  const scripts = join(root, 'scripts');
  mkdirSync(scripts);
  cpSync(join(app, 'scripts/patch-apple-blur.mjs'), join(scripts, 'patch-apple-blur.mjs'));
  cpSync(join(app, 'scripts/patches'), join(scripts, 'patches'), {recursive: true});
  const manifest = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
  assert.equal(manifest.scripts.postinstall.split(' && ')[0], 'node scripts/patch-apple-blur.mjs');
  for (const platform of ['ios', 'tvos']) {
    const podfile = readFileSync(join(app, platform, 'Podfile'), 'utf8');
    const invocation = "Pod::Executable.execute_command('node', [File.expand_path('../scripts/patch-apple-blur.mjs', __dir__)])";
    assert.equal(podfile.split(invocation).length, 2);
    assert.ok(podfile.indexOf(invocation) < podfile.indexOf('require Pod::Executable'));
    assert.ok(podfile.indexOf(invocation) < podfile.indexOf('use_native_modules!'));
    const cwd = join(root, platform);
    mkdirSync(cwd);
    const result = spawnSync(process.execPath, [join(cwd, '../scripts/patch-apple-blur.mjs')], {cwd, encoding: 'utf8'});
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /public-API patch verified/);
  }
  assert.equal(patchAppleBlur(root), 0);
  for (const file of patch.files) {
    assert.equal(hash(readFileSync(join(dependency, file.path))), file.afterSHA256);
  }
});
