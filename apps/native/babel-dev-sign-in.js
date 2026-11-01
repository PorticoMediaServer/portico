// The development test-owner sign-in exists only in a bundle built with PORTICO_DEV_SIGN_IN=1.
// Everywhere else __PORTICO_DEV_SIGN_IN__ is the literal false, and Metro's production constant
// folding removes the code and the module it requires before the bundle is written. Both apps
// that bundle the shared screens (apps/native and apps/vega) use this plugin. An Xcode Release
// build refuses the flag here; android/app/build.gradle refuses it for release variants.
const enabled = process.env.PORTICO_DEV_SIGN_IN === '1';
if (enabled && /release/i.test(process.env.CONFIGURATION || '')) throw new Error('PORTICO_DEV_SIGN_IN is for development builds only; unset it for a Release build.');

module.exports = ({types}) => ({
  name: 'portico-dev-sign-in',
  visitor: {Identifier(path) {
    if (path.node.name === '__PORTICO_DEV_SIGN_IN__' && path.isReferencedIdentifier()) path.replaceWith(types.booleanLiteral(enabled));
  }},
});
// Metro's cacheVersion: a bundle with the sign-in never shares cached transforms with one without.
module.exports.cacheVersion = enabled ? 'portico-dev-sign-in' : 'portico';
