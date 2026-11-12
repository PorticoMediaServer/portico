import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {patchTVOSFocusOcclusion} from '../scripts/patch-tvos-focus-occlusion.mjs';

const app = fileURLToPath(new URL('../', import.meta.url));
const installed = dirname(createRequire(import.meta.url).resolve('react-native/package.json'));
const patch = JSON.parse(readFileSync(new URL('../scripts/patches/react-native-tvos-0.83.10-1-transparent-focus.json', import.meta.url), 'utf8'));
const [file] = patch.files;
const hash = text => createHash('sha256').update(text).digest('hex');

// A controlled copy: the app's manifest and lock, react-native's package.json and the one source file, as released.
function fixture(t, edit = text => text) {
  const root = mkdtempSync(join(tmpdir(), 'portico-tvos-focus-'));
  t.after(() => rmSync(root, {recursive: true, force: true}));
  for (const name of ['package.json', 'package-lock.json']) cpSync(join(app, name), join(root, name));
  const dependency = join(root, 'node_modules', 'react-native'), target = join(dependency, file.path);
  mkdirSync(dirname(target), {recursive: true});
  cpSync(join(installed, 'package.json'), join(dependency, 'package.json'));
  const released = file.replacements.reduceRight((text, {before, after}) => text.replace(after, before), readFileSync(join(installed, file.path), 'utf8'));
  writeFileSync(target, edit(released));
  return {root, target};
}

test('the reviewed react-native-tvos source gains the tvOS focus fixes, and a rerun writes nothing', t => {
  const {root, target} = fixture(t);
  assert.equal(hash(readFileSync(target, 'utf8')), file.beforeSHA256);
  assert.equal(patchTVOSFocusOcclusion(root), 1);
  const patched = readFileSync(target, 'utf8');
  assert.equal(hash(patched), file.afterSHA256);
  assert.match(patched, /#if TARGET_OS_TV\n[^]*- \(BOOL\)isTransparentFocusItem[^]*#endif/);
  assert.equal(patched.split('kPorticoFocusGuideGap').length - 1, 5, 'every nextFocus guide sits outside its item');
  assert.equal(patchTVOSFocusOcclusion(root), 0);
});

test('any other source is refused and left as it was', t => {
  const {root, target} = fixture(t, text => text + '\n// changed upstream\n'), original = readFileSync(target, 'utf8');
  assert.throws(() => patchTVOSFocusOcclusion(root), /unexpected source bytes/);
  assert.equal(readFileSync(target, 'utf8'), original);
});
