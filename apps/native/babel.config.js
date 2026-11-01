const {execFileSync} = require('child_process');

// O-159: every bundle names the source it was built from (Settings › About, diagnostics).
// PORTICO_BUILD_ID wins (CI can pass its own); otherwise the checkout's short commit, else "dev".
function buildId() {
  const given = process.env.PORTICO_BUILD_ID;
  if (given && /^[A-Za-z0-9._+-]{1,64}$/.test(given)) return given;
  try {
    const commit = execFileSync('git', ['rev-parse', '--short=9', 'HEAD'], {cwd: __dirname, stdio: ['ignore', 'pipe', 'ignore']}).toString().trim();
    return /^[0-9a-f]{7,40}$/.test(commit) ? commit : 'dev';
  } catch {
    return 'dev';
  }
}
const stamp = buildId();
// The development test-owner sign-in exists only in a bundle built with PORTICO_DEV_SIGN_IN=1.
// Everywhere else __PORTICO_DEV_SIGN_IN__ is the literal false, and Metro's production constant
// folding removes the code and the module it requires before the bundle is written. An Xcode
// Release build refuses the flag here; android/app/build.gradle refuses it for release variants.
const devSignIn = process.env.PORTICO_DEV_SIGN_IN === '1';
if (devSignIn && /release/i.test(process.env.CONFIGURATION || '')) throw new Error('PORTICO_DEV_SIGN_IN is for development builds only; unset it for a Release build.');
const portico = ({types}) => ({
  name: 'portico-build-id',
  visitor: {Identifier(path) {
    if (!path.isReferencedIdentifier()) return;
    if (path.node.name === '__PORTICO_BUILD_ID__') path.replaceWith(types.stringLiteral(stamp));
    else if (path.node.name === '__PORTICO_DEV_SIGN_IN__') path.replaceWith(types.booleanLiteral(devSignIn));
  }},
});

module.exports = {
  presets: ['module:@react-native/babel-preset'],
  plugins: ['react-native-worklets/plugin', portico],
};
