import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

/**
 * The app's own Metro rule for `react-native-reanimated`, exercised through the real
 * `metro.config.js` resolver rather than a copy of it: shared sources (the `src-next` shell) must
 * reach the installed Amazon SDK, and the React Native shim's own import must reach the SDK
 * instead of itself.
 *
 * Only a missing direct `@react-native/metro-config` dependency skips these: that is the one
 * thing this file cannot supply for itself. Once it resolves, the config is required with no
 * guard, so a broken config, a transitive load failure or a resolver that is not a function is
 * a failure rather than a quiet skip.
 */
const app = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require_ = createRequire(import.meta.url);

const present = (() => {
  try {
    require_.resolve('@react-native/metro-config');
    return true;
  } catch (error) {
    // Only "this package is not installed" is a reason to skip. A package that is present but
    // unexportable, mangled, unreadable or otherwise broken is a failure of this run.
    if ((error as {code?: unknown})?.code === 'MODULE_NOT_FOUND') return false;
    throw error;
  }
})();
const skip = present ? false : 'the app dependencies are not installed';

type ResolveRequest = (context: unknown, requested: string, platform?: string) => unknown;

/** The app's real resolver. Required outside any guard: its failures belong to the run. */
function appResolver(): ResolveRequest {
  const config = require_(path.join(app, 'metro.config.js')) as {resolver?: {resolveRequest?: ResolveRequest}};
  const resolveRequest = config.resolver?.resolveRequest;
  assert.equal(typeof resolveRequest, 'function', 'the app metro config has no resolver.resolveRequest');
  return resolveRequest!;
}

/** The resolver's own delegation, recorded: what it asks for and where it came from. */
function resolver() {
  const calls: {requested: string; origin: string}[] = [];
  const context = {
    originModulePath: '',
    resolveRequest: (inner: {originModulePath: string}, requested: string) => {
      calls.push({requested, origin: inner.originModulePath});
      return {type: 'sourceFile', filePath: path.join(app, 'node_modules', requested, 'index.js')};
    },
  };
  return {context, calls};
}

test('shared current sources reach the Amazon reanimated package', {skip}, () => {
  const resolveRequest = appResolver();
  const {context, calls} = resolver();
  context.originModulePath = path.join(app, '../native/src-next/kit/overlays/BottomSheet.tsx');
  resolveRequest(context, 'react-native-reanimated', 'tv');
  assert.deepEqual(calls.map(call => call.requested), ['@amazon-devices/react-native-reanimated']);
});

test("the shim's own react-native import reaches the SDK, not itself", {skip}, () => {
  const resolveRequest = appResolver();
  const {context, calls} = resolver();
  context.originModulePath = path.join(app, 'src/shims/react-native.ts');
  resolveRequest(context, 'react-native', 'tv');
  assert.deepEqual(calls.map(call => call.requested), ['@amazon-devices/react-native-kepler']);
});


test('monorepo shared design resolves directly and retains the Vega SDK shim', {skip}, () => {
  const config = require_(path.join(app, 'metro.config.js'));
  const root = path.resolve(app, '../..');
  assert.ok(config.watchFolders.includes(root));
  assert.deepEqual(config.resolver.nodeModulesPaths, [path.join(app, 'node_modules'), path.join(root, 'node_modules')]);
  const {context, calls} = resolver();
  context.originModulePath = path.join(app, '../native/App.tsx');
  appResolver()(context, '@portico/design', 'tv');
  assert.deepEqual(calls.map(call => call.requested), [path.join(root, 'packages/design/src/index.ts')]);
  assert.deepEqual(appResolver()(context, 'react-native', 'tv'), {type: 'sourceFile', filePath: path.join(app, 'src/shims/react-native.ts')});
});
