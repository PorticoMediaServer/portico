import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {realpathSync, readFileSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const app = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const root = path.resolve(app, '../..');
const require = createRequire(import.meta.url);
const config = require('../metro.config.js');
test('native Metro uses the single shared source tree and local tvOS dependencies', () => {
  assert.ok(config.watchFolders.includes(root));
  assert.deepEqual(config.resolver.nodeModulesPaths, [path.join(app, 'node_modules'), path.join(root, 'node_modules')]);
  assert.equal(config.resolver.disableHierarchicalLookup, true);
  const design = config.resolver.extraNodeModules['@portico/design'];
  assert.equal(realpathSync(path.resolve(design, JSON.parse(readFileSync(path.join(design, 'package.json'), 'utf8')).exports)), realpathSync(path.join(root, 'packages/design/src/index.ts')));
  assert.equal(require('react-native/package.json').name, 'react-native-tvos');
  assert.equal(config.resolver.extraNodeModules['react-native'], path.join(app, 'node_modules/react-native'));
});
