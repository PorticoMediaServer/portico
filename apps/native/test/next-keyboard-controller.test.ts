import test from 'node:test';
import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const app = fileURLToPath(new URL('..', import.meta.url));
const read = (relative: string) => readFileSync(path.join(app, relative), 'utf8');
const sources = (dir: string): string[] => readdirSync(path.join(app, dir), {withFileTypes: true}).flatMap(entry => {
  const relative = path.join(dir, entry.name);
  return entry.isDirectory() ? sources(relative) : /\.tsx?$/.test(entry.name) ? [relative] : [];
});

test('one wrapper owns the touch keyboard controller, and no screen keeps its own keyboard workaround', () => {
  const importers = sources('src-next').filter(file => /from 'react-native-keyboard-controller'/.test(read(file)));
  assert.deepEqual(importers, [path.join('src-next', 'platform', 'keyboard.tsx')]);
  for (const file of sources('src-next').filter(file => !file.includes(`${path.sep}dev${path.sep}`) && file !== importers[0])) {
    const source = read(file);
    assert.doesNotMatch(source, /automaticallyAdjustKeyboardInsets|<KeyboardAvoidingView|keyboardWillShow'|keyboardDidShow'/, file);
  }
  assert.match(read('src-next/App.tsx'), /<SafeAreaProvider><KeyboardProvider>/);
  assert.match(read('src-next/kit/overlays/BottomSheet.tsx'), /useKeyboardHeight\(avoidKeyboard && open\)/);
});

test('Vega keeps React Native views under the same names and never bundles the package', () => {
  const names = (source: string) => [...source.matchAll(/^export (?:const|function) (\w+)/gm)].map(match => match[1]).sort();
  const vega = readFileSync(path.join(app, '../vega/src/overrides/native-next/platform/keyboard.tsx'), 'utf8');
  assert.deepEqual(names(vega), names(read('src-next/platform/keyboard.tsx')));
  assert.doesNotMatch(vega, /react-native-keyboard-controller/);
  assert.doesNotMatch(read('licences/js-vega.txt'), /^react-native-keyboard-controller@/m);
  for (const client of ['ios', 'android']) assert.match(read(`licences/js-${client}.txt`), /^react-native-keyboard-controller@1\.22\.9\t/m);
});

test('the controller is native on iPhone, iPad and Android, and absent from Apple TV', () => {
  assert.equal(JSON.parse(read('package.json')).dependencies['react-native-keyboard-controller'], '1.22.9');
  assert.match(read('ios/Podfile.lock'), /^  - react-native-keyboard-controller \(1\.22\.9\):$/m);
  assert.doesNotMatch(read('tvos/Podfile.lock'), /react-native-keyboard-controller/);
  const registration = (JSON.parse(read('licences/native-npm.json')) as {name: string; platforms: string[]; licence: string}[])
    .find(entry => entry.name === 'react-native-keyboard-controller');
  assert.deepEqual(registration?.platforms, ['android', 'apple']);
  assert.equal(registration?.licence, 'MIT');
});
