const fs = require('fs');
const path = require('path');
const {getDefaultConfig, mergeConfig} = require('@react-native/metro-config');

// The screens are the Apple app's (`../native`), read in place and never copied. Two rules make
// them run on Vega without a change:
//
// 1. `react-native`, for the app and the shared sources, is `src/shims/react-native.ts`: the Vega
//    SDK's React Native plus Portico's modules and the few television exports whose names or
//    behavior differ. Libraries in node_modules keep the SDK's package.
// 2. A shared file that is Apple-specific is replaced by the file at the same path under
//    `src/overrides/native-next` (the `.vega.ts` sibling, kept out of `apps/native`); a file of the
//    shared packages, by the one under `src/overrides/packages`.
const app = __dirname;
const repoRoot = path.resolve(app, '../..');
const shared = path.resolve(app, '../native');
const packages = path.resolve(app, '../../packages');
const modules = path.join(app, 'node_modules');
const shims = path.join(app, 'src/shims');
const bridge = path.join(app, 'src/bridge');
const debugFile = path.join(app, 'src/debug.tsx');
const overrides = path.join(app, 'src/overrides');
const reactNativeShim = path.join(shims, 'react-native.ts');
const nextSrc = path.join(shared, 'src-next');
const sdkReactNative = '@amazon-devices/react-native-kepler';

// The community names the shared sources import, and the SDK's builds of them.
const aliases = {
  '@react-native-masked-view/masked-view': path.join(shims, 'masked-view.tsx'),
  '@react-native-community/blur': path.join(shims, 'blur.tsx'),
  '@portico/design': path.join(packages, 'design/src/index.ts'),
  'react-native-gesture-handler': '@amazon-devices/react-native-gesture-handler',
  '@react-navigation/native': '@amazon-devices/react-navigation__native',
  '@react-navigation/native-stack': '@amazon-devices/react-navigation__native-stack',
  'react-native-safe-area-context': '@amazon-devices/react-native-safe-area-context',
  'react-native-screens': '@amazon-devices/react-native-screens',
  'react-native-svg': '@amazon-devices/react-native-svg',
};

// Community libraries that are plain JavaScript and not built for Vega: they get the shim too
// (one of them reads `NativeModules` as it loads).
const plainLibraries = ['react-native-url-polyfill', 'react-native-qrcode-svg'].map(name => path.join(modules, name) + path.sep);
const inNodeModules = file => file.includes(`${path.sep}node_modules${path.sep}`) && !plainLibraries.some(library => file.startsWith(library));
const within = (dir, file) => file === dir || file.startsWith(dir + path.sep);

const overrideRoots = [[nextSrc, path.join(overrides, 'native-next')], [packages, path.join(overrides, 'packages')]];

function overrideFor(file) {
  for (const [root, replacements] of overrideRoots) {
    if (!within(root, file)) continue;
    const relative = path.relative(root, file).replace(/\.(tsx?|jsx?)$/, '');
    for (const extension of ['.ts', '.tsx']) {
      const candidate = path.join(replacements, relative + extension);
      if (fs.existsSync(candidate)) return candidate;
    }
  }
  return undefined;
}

/** `react-native` is the SDK's package on this platform. (The SDK's own renaming is replaced, not
 * wrapped, by a project resolver, so it is done here.) */
function sdkName(moduleName) {
  if (moduleName === 'react-native') return sdkReactNative;
  if (moduleName.startsWith('react-native/')) return sdkReactNative + moduleName.slice('react-native'.length);
  return moduleName;
}

function alias(moduleName) {
  for (const [from, to] of Object.entries(aliases)) {
    if (moduleName === from) return to;
    if (moduleName.startsWith(from + '/')) return to + moduleName.slice(from.length);
  }
  return moduleName;
}

module.exports = mergeConfig(getDefaultConfig(app), {
  cacheVersion: require('../native/babel-dev-sign-in').cacheVersion,
  watchFolders: [repoRoot],
  resolver: {
    nodeModulesPaths: [modules, path.join(repoRoot, 'node_modules')],
    disableHierarchicalLookup: true,
    extraNodeModules: {react: path.join(modules, 'react')},
    // Only the shared sources and assets are read from the Apple app.
    blockList: [new RegExp(`^${shared.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}/(ios|tvos|android|Native|test|node_modules)/.*`)],
    resolveRequest: (context, requested, platform) => {
      const origin = context.originModulePath;
      const moduleName = alias(sdkName(requested));
      // The shim and the bridge it serves are built on the SDK's package itself.
      if (moduleName === sdkReactNative && !inNodeModules(origin) && !within(shims, origin) && !within(bridge, origin) && origin !== debugFile) {
        return {type: 'sourceFile', filePath: reactNativeShim};
      }
      // The app uses the SDK's full layout/scroll/worklet API.
      if (requested === 'react-native-reanimated' && !inNodeModules(origin)) {
        return context.resolveRequest(context, '@amazon-devices/react-native-reanimated', platform);
      }
      const resolved = context.resolveRequest(context, moduleName, platform);
      if (resolved.type === 'sourceFile') {
        const override = overrideFor(resolved.filePath);
        // An override may import the file it replaces (to wrap it).
        if (override && origin !== override) return {type: 'sourceFile', filePath: override};
      }
      return resolved;
    },
  },
});
