const manifest = require('./package.json');

// The React Native Apple CLI represents both iOS and tvOS as `ios`.
// Pod installation uses the explicit flag; Xcode reruns Fabric codegen in a
// separate process with PLATFORM_NAME and no Podfile environment. Both must
// exclude components that tvOS cannot link.
const tvOS = process.env.PORTICO_APPLE_TARGET === 'tvos' ||
  ['appletvos', 'appletvsimulator'].includes(process.env.PLATFORM_NAME);
// Touch-only native packages: tvOS links neither (WebView; the touch keyboard controller).
const tvOSUnlinked = ['react-native-webview', 'react-native-keyboard-controller'].filter(name =>
  [manifest.dependencies, manifest.devDependencies, manifest.optionalDependencies]
    .some(dependencies => Object.hasOwn(dependencies ?? {}, name)));

module.exports = tvOS ? {
  project: {ios: {sourceDir: 'tvos'}},
  ...(tvOSUnlinked.length ? {
    dependencies: Object.fromEntries(tvOSUnlinked.map(name => [name, {platforms: {ios: null}}])),
  } : {}),
} : {};
