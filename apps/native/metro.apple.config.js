const path = require('path');
const {realpathSync} = require('fs');
const {mergeConfig} = require('@react-native/metro-config');
const common = require('./metro.config');

// Apple build checkouts use the shared lock-keyed dependency tree. Metro must
// watch the symlink target as well as the app before it can resolve its modules.
module.exports = mergeConfig(common, {
  watchFolders: [...new Set([...common.watchFolders, realpathSync(path.join(__dirname, 'node_modules'))])],
  maxWorkers: 1,
});
