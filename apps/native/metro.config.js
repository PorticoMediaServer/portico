const path = require('path');
const {getDefaultConfig, mergeConfig} = require('@react-native/metro-config');
const repoRoot = path.resolve(__dirname, '../..');
const svgLite = path.resolve(__dirname, 'metro/svg-lite.js');
const svgCssStub = path.resolve(__dirname, 'metro/svg-css-stub.js');
const defaults = getDefaultConfig(__dirname);
module.exports = mergeConfig(defaults, {
  // babel.config.js substitutes PORTICO_DEV_SIGN_IN at transform time, so a bundle with the
  // development sign-in never shares cached transforms with one without it.
  cacheVersion: process.env.PORTICO_DEV_SIGN_IN === '1' ? 'portico-dev-sign-in' : 'portico',
  watchFolders: [repoRoot],
  transformer: {unstable_allowRequireContext: true},
  resolver: {
    // CMake creates/removes temporary directories while Metro watches node_modules.
    blockList: [defaults.resolver.blockList].flat().filter(Boolean).concat(/[\\/]android[\\/](?:build|\.cxx)[\\/]/),
    nodeModulesPaths: [path.resolve(__dirname, 'node_modules'), path.join(repoRoot, 'node_modules')],
    disableHierarchicalLookup: true,
    extraNodeModules: {
      '@portico/design': path.resolve(__dirname, '../../packages/design'),
      react: path.resolve(__dirname, 'node_modules/react'),
      'react-native': path.resolve(__dirname, 'node_modules/react-native'),
    },
    // PERF-15: keep react-native-svg's CSS/XML parsers out of the bundle (see metro/svg-lite.js).
    resolveRequest: (context, moduleName, platform) => {
      if (moduleName.startsWith('@/')) return context.resolveRequest(context, path.resolve(__dirname, 'src-next', moduleName.slice(2)), platform);
      if (moduleName === 'react-native-svg' && !context.originModulePath.includes(`${path.sep}node_modules${path.sep}react-native-svg${path.sep}`)) return {type: 'sourceFile', filePath: svgLite};
      if (moduleName === 'react-native-svg/css') return {type: 'sourceFile', filePath: svgCssStub};
      return context.resolveRequest(context, moduleName, platform);
    },
  },
});
