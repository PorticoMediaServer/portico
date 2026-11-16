/**
 * `@amazon-devices/react-native-kepler` as the Fire TV bridge (`apps/vega/src/bridge`) uses it,
 * in a TV browser: React Native (react-native-web, through the app's shim), plus the one
 * Vega-only call the player view makes. The bridge's files are used unchanged.
 */
export * from '../shims/react-native';

const component = Object.freeze({});
/** The player hands this to the media element as the owner of the remote's transport keys. */
export function useKeplerAppStateManager() {
  return {getComponentInstance: () => component};
}
