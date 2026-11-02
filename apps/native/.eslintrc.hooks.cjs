// React's hook rules alone (B-16.23), for the phone, tablet and television app and the Vega app:
// hooks are called unconditionally and their dependency lists are complete. `npm run lint:hooks`
// runs it; CI fails on any finding. A deliberate exception says why on the line it excuses.
module.exports = {
  root: true,
  parser: '@typescript-eslint/parser',
  parserOptions: {ecmaVersion: 'latest', sourceType: 'module', ecmaFeatures: {jsx: true}},
  plugins: ['react-hooks'],
  rules: {
    'react-hooks/rules-of-hooks': 'error',
    'react-hooks/exhaustive-deps': 'error',
  },
  overrides: [{
    // TV focus review step 0: a screen declares a region `entry` instead. tvOS re-applies an
    // always-true preferred view on every later layout change and takes focus back (§4.4.2).
    // A warning until step 4 migrates the existing uses; test/tv-preferred-focus-ratchet.test.ts
    // keeps the count from growing meanwhile.
    files: ['src-next/screens/**/*.ts', 'src-next/screens/**/*.tsx'],
    rules: {
      'no-restricted-syntax': ['warn',
        {selector: "JSXAttribute[name.name='hasTVPreferredFocus']", message: 'No new hasTVPreferredFocus in screens/: give the FocusRegion an entry instead (TV focus review §4.4.2).'},
        {selector: "Property[key.name='hasTVPreferredFocus']", message: 'No new hasTVPreferredFocus in screens/: give the FocusRegion an entry instead (TV focus review §4.4.2).'}],
    },
  }],
};
