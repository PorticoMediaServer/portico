import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {readFileSync, realpathSync} from 'node:fs';
import {fileURLToPath} from 'node:url';

const require = createRequire(import.meta.url);
const common = require('../metro.config.js');
const apple = require('../metro.apple.config.js');

test('Apple Metro watches the actual dependency tree with one worker and preserves the common resolver', () => {
  const actual = realpathSync(fileURLToPath(new URL('../node_modules', import.meta.url)));
  assert.ok(apple.watchFolders.includes(actual));
  for (const folder of common.watchFolders) assert.ok(apple.watchFolders.includes(folder));
  assert.equal(apple.maxWorkers, 1);
  for (const key of Object.keys(common)) if (key !== 'watchFolders' && key !== 'maxWorkers') assert.deepEqual(apple[key], common[key], key);
  assert.equal(require.resolve('@babel/runtime/helpers/interopRequireDefault').startsWith(actual + '/'), true);
});

test('both Apple bundle phases select the platform config before invoking the standard React Native script', () => {
  for (const platform of ['ios', 'tvos']) {
    const source = readFileSync(new URL(`../${platform}/Portico.xcodeproj/project.pbxproj`, import.meta.url), 'utf8');
    const phases = [...source.matchAll(/shellScript = ("(?:\\.|[^"\\])*");/g)].map(match => JSON.parse(match[1]));
    const bundle = phases.filter(value => value.includes('REACT_NATIVE_XCODE='));
    assert.equal(bundle.length, 1, platform);
    assert.ok(bundle[0].includes('export BUNDLE_CONFIG="$SRCROOT/../metro.apple.config.js"'));
    assert.ok(bundle[0].indexOf('export BUNDLE_CONFIG=') < bundle[0].indexOf('/bin/sh -c'));
    assert.ok(bundle[0].includes('$REACT_NATIVE_PATH/scripts/xcode/with-environment.sh'));
    assert.ok(bundle[0].includes('$REACT_NATIVE_PATH/scripts/react-native-xcode.sh'));
  }
});
