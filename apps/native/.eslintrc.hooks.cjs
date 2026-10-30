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
};
