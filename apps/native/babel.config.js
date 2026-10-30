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
const portico = ({types}) => ({
  name: 'portico-build-id',
  visitor: {Identifier(path) {if (path.node.name === '__PORTICO_BUILD_ID__' && path.isReferencedIdentifier()) path.replaceWith(types.stringLiteral(stamp));}},
});

module.exports = {
  presets: ['module:@react-native/babel-preset'],
  plugins: ['react-native-worklets/plugin', portico],
};
